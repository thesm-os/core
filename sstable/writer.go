// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strconv"

	"go.thesmos.sh/core/compress/snappy"
	"go.thesmos.sh/core/filter"
	"go.thesmos.sh/core/hash/xxh3"
)

// The layout of a table. Its integers are big-endian.
const (
	// seqLen is the size of the sequence at the end of an internal key. An
	// internal key is the key of an entry followed by its sequence.
	seqLen = 8
	// crcLen is the size of a CRC32C.
	crcLen = 4
	// trailerLen is the size of the trailer of a block. The trailer has the
	// compression byte and the CRC32C of the block and the compression byte.
	trailerLen = 5
	// footerLen is the size of the footer at the end of a table.
	footerLen = 80
	// footerChecked is the number of bytes at the start of the footer that
	// the CRC32C of the footer covers.
	footerChecked = 68
	// formatVersion is the version of the layout of a table with one index
	// block and one filter block. The footer contains the version.
	formatVersion = 1
	// partitionedVersion is the version of the layout of a table whose index
	// and filter are cut into partitions.
	partitionedVersion = 2
	// magic is the number at the end of a table. It is the first 8 bytes of
	// the SHA-256 of go.thesmos.sh/core/sstable.
	magic = 0xf65ba2ce515d9bd3
)

// The defaults of [WriterOptions].
const (
	// defaultBlockSize is the BlockSize of the zero options.
	defaultBlockSize = 4096
	// defaultRestartInterval is the RestartInterval of the zero options.
	defaultRestartInterval = 16
)

// The largest number of bytes beside the key that an entry of an index
// takes. A varint has at most 10 bytes.
const (
	// maxIndexOverhead bounds an entry of the index block or of an index
	// partition: the three varints of its header, the 8 bytes of the
	// sequence, the two varints of the place of the block and the 4 bytes of
	// its CRC32C.
	maxIndexOverhead = 62

	// maxTopOverhead bounds an entry of a top-level index, whose value has a
	// varint more: the number of data blocks up to the end of its partition.
	maxTopOverhead = maxIndexOverhead + binary.MaxVarintLen64
)

// castagnoli is the table of CRC32C. The blocks and the footer of a table
// have CRC32C checksums.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Compression is the codec of the data blocks of a table. The trailer of a
// block contains it as a byte, so its values do not change between releases.
type Compression uint8

const (
	// NoCompression is the setting under which the Writer stores every data
	// block as it builds the block.
	NoCompression Compression = 0 // none
	// SnappyCompression is the setting under which the Writer stores a data
	// block in the Snappy block format when the encoding shrinks the block by
	// more than an eighth.
	SnappyCompression Compression = 1 // snappy
)

// Valid reports whether c is [NoCompression] or [SnappyCompression].
func (c Compression) Valid() bool {
	return c <= SnappyCompression
}

// String returns "none" or "snappy", and "Compression(N)" for a value that
// is not valid.
//
// # Allocation contract
//
// Zero-alloc for a valid Compression. Another value allocates its text.
func (c Compression) String() string {
	switch c {
	case NoCompression:
		return "none"
	case SnappyCompression:
		return "snappy"
	default:
		return "Compression(" + strconv.Itoa(int(c)) + ")"
	}
}

// WriterOptions configure a [Writer]. The zero value writes data blocks of
// 4,096 bytes with a restart point every 16 entries, without compression and
// without a filter.
type WriterOptions struct {
	// BlockSize is the size of the entries after which a data block ends.
	// The Writer ends a block after the first entry that brings the entries
	// of the block to BlockSize bytes or more. 0 means 4,096, and the largest
	// BlockSize is 2^32 - 1.
	BlockSize int

	// RestartInterval is the number of entries from one restart point of a
	// data block to the next. At a restart point the Writer stores the whole
	// key. For every other entry it stores only the bytes of the key after
	// the prefix that the key shares with the key before it. 0 means 16.
	RestartInterval int

	// Compression is the codec of the data blocks.
	Compression Compression

	// FilterBits is the width of the fingerprints of the filter of the
	// table in bits: 4, 8, 10, 12 or 16. With 0 the Writer does not write a
	// filter.
	FilterBits int

	// MaxRetained bounds the memory that a Writer keeps for its next table,
	// in bytes. When [Writer.Footprint] exceeds MaxRetained after Close
	// writes a table, or at Reset, the Writer releases its buffers and the
	// scratch of its filter builder, and the next table grows them again. 0
	// keeps every buffer.
	MaxRetained int

	// PartitionSize is the size in bytes of an index partition and of a
	// filter partition, in format version 2. The Writer ends an index
	// partition with the entry that brings its entries to PartitionSize
	// bytes or more. It ends a filter partition before the key that would
	// take the encoding of the partition past PartitionSize bytes. With 0 the
	// Writer writes one index block and one filter block, in format version
	// 1. The largest PartitionSize is 2^32 - 1.
	PartitionSize int
}

// Summary describes the entries, the size and the range of keys and
// sequences of a table. For a table of no entries, every field except Size
// is zero or nil.
type Summary struct {
	SmallestKey []byte // the key of the first entry
	LargestKey  []byte // the key of the last entry
	Entries     int64  // the number of entries
	Size        int64  // the bytes of the table
	MinSeq      uint64 // the lowest sequence of any entry
	MaxSeq      uint64 // the highest sequence of any entry
	SmallestSeq uint64 // the sequence of the first entry
	LargestSeq  uint64 // the sequence of the last entry
}

// Sink receives the bytes of the table that a [Writer] writes. Write has the
// contract of io.Writer. A Write that waits, such as for a budget of disk
// I/O, ends its wait with the error of ctx. A bytes.Buffer or an *os.File
// becomes a Sink through a type whose Write drops ctx.
type Sink interface {
	// Write writes the bytes of p, as io.Writer writes them.
	Write(ctx context.Context, p []byte) (int, error)
}

// blockPlace is the place of a block in a table: the offset of the block, its
// length without its trailer and the CRC32C in its trailer. The zero
// blockPlace is the place of no block, which the footer states for a table
// without a filter.
type blockPlace struct {
	off, n uint64
	crc    uint32
}

// Writer writes a table to a [Sink] from entries in the order of [Compare].
// It writes each data block when the block ends, and the last data block,
// the filter, the index and the footer at [Writer.Close]. Each write takes
// the context of the call that makes it. With the PartitionSize of
// [WriterOptions], the Writer cuts the index and the filter into partitions
// as the entries arrive, and keeps the partitions until Close.
//
// The data blocks follow one another from the start of the table. A block
// contains its entries, then the offset of each restart point as a uint32,
// then their count as a uint32. An entry has five fields:
//
//   - the number of bytes that its internal key shares with the internal key
//     before it, as a varint
//   - the number of the other bytes of its internal key, as a varint
//   - the length of its value, as a varint
//   - the other bytes of its internal key
//   - its value
//
// The internal key of an entry is its key followed by its sequence in 8
// bytes.
//
// # Concurrency
//
// A Writer is not safe for concurrent use.
//
// # Allocation contract
//
// [NewWriter] allocates the Writer. The first [Writer.Add] allocates a copy
// of its key. Add otherwise allocates only to grow the buffers of the current
// block, the index and the hashes of the filter, so it does not allocate once
// they have room. Close allocates the encoding of the filter, the scratch of
// its builder, the growth of the buffer of the table for the filter and the
// index, and the largest key of the summary. [Writer.Reset] keeps every
// buffer for the next table while [Writer.Footprint] is within the
// MaxRetained of [WriterOptions], so a Writer that writes many tables of one
// size allocates the two keys of each summary once its buffers have grown. A
// table after a release of the buffers allocates their growth again.
type Writer struct {
	// w receives the bytes of the table.
	w Sink

	// err is the error of w, or ErrClosed once Close wrote the table. Every
	// later call returns it.
	err error

	// builder builds the filter at Close.
	builder filter.FuseBuilder

	// out contains the current data block, and at Close the rest of the
	// table.
	out []byte

	// restarts are the offsets of the restart points of the current block.
	restarts []uint32

	// last is the internal key of the previous entry. cur is the buffer in
	// which Add builds the internal key of the next entry.
	last, cur []byte

	// scratch receives the encoding of a compressed block.
	scratch []byte

	// index contains the entries of the index block, or of the current index
	// partition, and indexRestarts their offsets. Every entry of the index is
	// a restart point.
	index         []byte
	indexRestarts []uint32

	// hashes are the hashes of the distinct keys of the filter, or of the
	// current filter partition.
	hashes []uint64

	// filterParts and indexParts contain the finished filter partitions and
	// index partitions with their trailers, each kind in its order.
	filterParts, indexParts []byte

	// filterMeta and indexMeta contain a record of each finished partition:
	// the length of its key as a varint, the key, the stored length of the
	// partition as a varint and its CRC32C as a uint32. The key of a filter
	// partition is its last key. The key of an index partition is the
	// internal key of its last entry, and its record ends with the number
	// of data blocks up to the end of the partition as a varint.
	filterMeta, indexMeta []byte

	// smallest is the key of the first entry, and smallestSeq its sequence.
	smallest    []byte
	smallestSeq uint64

	// blockSize, restartInterval, filterBits, maxRetained and partitionSize
	// are options of the Writer after NewWriter applied the defaults.
	blockSize       int
	restartInterval int
	filterBits      int
	maxRetained     int
	partitionSize   int

	// filterKeys is the number of distinct keys at which a filter partition
	// ends: the most keys whose encoding takes at most partitionSize bytes,
	// and at least 1.
	filterKeys int

	// blockEntries is the number of entries of the current block.
	blockEntries int

	// blocks is the number of finished data blocks.
	blocks uint64

	// indexTop and filterTop bound the bytes of the entries of the top-level
	// index of the index and of the filter.
	indexTop, filterTop uint64

	// size is the number of bytes that w accepted.
	size int64

	// entries, minSeq and maxSeq describe the entries that Add added.
	entries        int64
	minSeq, maxSeq uint64

	// compression is the codec of the data blocks.
	compression Compression
}

// NewWriter returns a [Writer] that writes a table to w under opts. It
// checks FilterBits with a [filter.FuseBuilder], so the Writer accepts every
// width of fingerprints that the filter package builds.
//
// Error modes:
//
//   - A negative BlockSize, a BlockSize above 2^32 - 1, a negative
//     RestartInterval, an invalid Compression, a FilterBits that is neither
//     0 nor a width of fingerprints, a negative MaxRetained, a negative
//     PartitionSize or a PartitionSize above 2^32 - 1 returns [ErrOptions].
//
// # Allocation contract
//
// Allocates the Writer, and with a filter the encoding of an empty filter
// that checks the width.
func NewWriter(w Sink, opts WriterOptions) (*Writer, error) {
	if opts.BlockSize < 0 || int64(opts.BlockSize) > math.MaxUint32 || opts.RestartInterval < 0 ||
		!opts.Compression.Valid() || opts.MaxRetained < 0 ||
		opts.PartitionSize < 0 || int64(opts.PartitionSize) > math.MaxUint32 {

		return nil, ErrOptions
	}

	wr := &Writer{
		w:               w,
		blockSize:       cmp.Or(opts.BlockSize, defaultBlockSize),
		restartInterval: cmp.Or(opts.RestartInterval, defaultRestartInterval),
		compression:     opts.Compression,
		filterBits:      opts.FilterBits,
		maxRetained:     opts.MaxRetained,
		partitionSize:   opts.PartitionSize,
	}
	if opts.FilterBits != 0 {
		if _, err := wr.builder.Append(nil, nil, opts.FilterBits); err != nil {
			return nil, ErrOptions
		}
		wr.filterKeys = partitionKeys(opts.PartitionSize, opts.FilterBits)
	}

	return wr, nil
}

// Add appends the entry of key, seq and value to the table. The entry must
// follow the previous entry in the order of [Compare]. Add copies key and
// value. It writes the current data block to the Sink under ctx when the
// entry ends the block. With a PartitionSize, a key that would take the
// current filter partition past PartitionSize bytes ends the partition, and
// a data block that brings the current index partition to PartitionSize
// bytes ends that partition.
//
// Error modes:
//
//   - A key whose entry in the index, in an index partition or in a
//     top-level index could bring it past 2^32 - 1 bytes returns
//     [ErrTooLarge], and the Writer is unchanged.
//   - An entry at or before the previous entry returns [ErrOrder], and the
//     Writer is unchanged.
//   - An error of the Sink, such as the error of ctx at the end of a wait,
//     returns an error that wraps it, and every later call returns the same
//     error. A write of fewer bytes without an error returns an error that
//     wraps io.ErrShortWrite.
//   - A filter partition that the builder of the filter refuses returns the
//     error of the builder, and every later call returns it too.
//   - A call after Close returns [ErrClosed].
//
// # Allocation contract
//
// Zero-alloc when the buffers of the current block, the index, the hashes
// of the filter and the partitions have room. Otherwise Add allocates their
// growth, and an end of a filter partition can grow the scratch of the
// builder of the filter. The first Add also allocates a copy of its key for
// the summary.
func (w *Writer) Add(ctx context.Context, key []byte, seq uint64, value []byte) error {
	if w.err != nil {
		return w.err
	}
	//dokimi:mutate-skip ror-boundary: a key at the bound has 4 GiB, and Add keeps three copies of it, which no test allocates
	if uint64(len(w.index))+uint64(len(key)) > math.MaxUint32-maxIndexOverhead ||
		max(w.indexTop, w.filterTop)+uint64(len(key)) > math.MaxUint32-seqLen-maxTopOverhead {

		return ErrTooLarge
	}

	distinct := true
	if w.entries != 0 {
		n := len(w.last) - seqLen
		if Compare(key, seq, w.last[:n], binary.BigEndian.Uint64(w.last[n:])) <= 0 {
			return ErrOrder
		}
		distinct = !bytes.Equal(key, w.last[:n])
	} else {
		w.smallest, w.smallestSeq, w.minSeq, w.maxSeq = append([]byte{}, key...), seq, seq, seq
	}
	if w.filterBits != 0 && distinct {
		// A filter partition ends at the previous key, which is distinct from
		// key.
		if w.partitionSize != 0 && len(w.hashes) == w.filterKeys {
			w.err = w.finishFilterPartition(w.last[:len(w.last)-seqLen])
		}
		w.hashes = append(w.hashes, xxh3.Hash(key))
	}
	w.minSeq, w.maxSeq = min(w.minSeq, seq), max(w.maxSeq, seq)

	w.cur = binary.BigEndian.AppendUint64(append(w.cur[:0], key...), seq)
	shared := 0
	if w.blockEntries%w.restartInterval == 0 {
		//nolint:gosec // G115: a block ends once its entries have BlockSize bytes, at most 2^32 - 1
		w.restarts = append(w.restarts, uint32(len(w.out)))
	} else {
		n := min(len(w.cur), len(w.last))
		for shared < n && w.cur[shared] == w.last[shared] {
			shared++
		}
	}
	w.out = appendEntry(w.out, w.cur, value, shared)
	w.last, w.cur = w.cur, w.last
	w.entries++
	//dokimi:mutate-skip uoi-incdec: a count of -k has a remainder of 0 and a value of 0 exactly when k has
	w.blockEntries++

	if len(w.out) < w.blockSize {
		return w.err
	}
	w.finishBlock()
	w.err = cmp.Or(w.err, w.flush(ctx))

	return w.err
}

// Close writes the last data block, the filter, the index and the footer
// with one write under ctx, and returns the summary of the table. After the
// write it releases the buffers of w when [Writer.Footprint] exceeds the
// MaxRetained of [WriterOptions]. A Close that fails keeps them.
//
// The filter block is the encoding of a [filter.Fuse] over the [xxh3.Hash]
// of each distinct key. The index block has the layout of a data block with
// a restart point at every entry. Its entry for a data block has the
// internal key of the last entry of the block. The value of the entry is the
// offset and the length of the stored block without its trailer as two
// varints, followed by the CRC32C in the trailer of the block as a uint32.
// Close stores both blocks without compression.
//
// A trailer of 5 bytes follows every block. It has the compression byte of
// the block, and the CRC32C of the stored block and the compression byte.
//
// The footer has 80 bytes:
//
//   - the offset and the length of the index block as uint64, and the CRC32C
//     in its trailer as uint32
//   - the offset and the length of the filter block as uint64, and the
//     CRC32C in its trailer as uint32, or three zeros
//   - the number of entries, the lowest and the highest sequence, as uint64
//   - the version 1 and the CRC32C of the first 68 bytes, as uint32
//   - the magic number 0xf65ba2ce515d9bd3
//
// With a PartitionSize, Close writes format version 2. The data blocks are
// followed by the filter partitions, the index partitions, the top-level
// index of the filter and the top-level index of the index, each with a
// trailer.
//
//   - An index partition has the layout of the index block, with the entries
//     of consecutive data blocks.
//   - A filter partition is the encoding of a [filter.Fuse] over the hashes
//     of the distinct keys of a range of the table.
//   - A top-level index has the layout of the index block, with an entry for
//     each partition of its kind. The entry of an index partition has the
//     internal key of the last entry of the partition. Its value is the
//     offset, the length and the number of data blocks of the table up to
//     the end of the partition as varints, followed by the CRC32C of the
//     partition as a uint32. The entry of a filter partition has the last
//     key of the partition with the sequence 0, and a value of the offset
//     and the length as varints and the CRC32C as a uint32.
//   - The footer has the layout of version 1 with the version 2. Its index
//     fields state the top-level index of the index, and its filter fields
//     the top-level index of the filter.
//
// Error modes:
//
//   - An error of the Sink, such as the error of ctx at the end of a wait,
//     returns an error that wraps it, and every later call returns the same
//     error.
//   - A set of hashes that the builder of the filter refuses returns the
//     error of the builder, and every later call returns it too.
//   - A call after Close returned a summary returns [ErrClosed].
//
// # Allocation contract
//
// Allocates the encoding of the filter, the scratch of its builder, the
// growth of the buffer of the table for the filter, the index and the
// partitions, and the largest key of the summary.
func (w *Writer) Close(ctx context.Context) (Summary, error) {
	if w.err != nil {
		return Summary{}, w.err
	}
	if w.blockEntries != 0 {
		w.finishBlock()
	}

	//nolint:gosec // G115: the size is not negative
	size := uint64(w.size)
	var index, filter blockPlace
	var err error
	version := uint32(formatVersion)
	if w.partitionSize == 0 {
		if w.filterBits != 0 {
			start := len(w.out)
			//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
			if w.out, err = w.builder.Append(w.out, w.hashes, w.filterBits); err == nil {
				filter = w.sealPlace(size, start)
			}
		}
		start := len(w.out)
		w.out = appendIndex(w.out, w.index, w.indexRestarts)
		index = w.sealPlace(size, start)
	} else {
		version = partitionedVersion
		index, filter, err = w.appendPartitions(size)
	}

	be := binary.BigEndian
	footer := len(w.out)
	w.out = be.AppendUint64(w.out, index.off)
	w.out = be.AppendUint64(w.out, index.n)
	w.out = be.AppendUint32(w.out, index.crc)
	w.out = be.AppendUint64(w.out, filter.off)
	w.out = be.AppendUint64(w.out, filter.n)
	w.out = be.AppendUint32(w.out, filter.crc)
	//nolint:gosec // G115: the number of entries is not negative
	w.out = be.AppendUint64(w.out, uint64(w.entries))
	w.out = be.AppendUint64(w.out, w.minSeq)
	w.out = be.AppendUint64(w.out, w.maxSeq)
	w.out = be.AppendUint32(w.out, version)
	w.out = be.AppendUint32(w.out, crc32.Checksum(w.out[footer:], castagnoli))
	w.out = be.AppendUint64(w.out, magic)

	//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
	if err == nil {
		err = w.flush(ctx)
	}
	w.err = cmp.Or(err, ErrClosed)
	if err != nil {
		return Summary{}, err
	}

	sum := Summary{
		Entries: w.entries, Size: w.size, MinSeq: w.minSeq, MaxSeq: w.maxSeq,
		SmallestKey: w.smallest, SmallestSeq: w.smallestSeq,
	}
	if w.entries != 0 {
		n := len(w.last) - seqLen
		sum.LargestKey, sum.LargestSeq = append([]byte{}, w.last[:n]...), binary.BigEndian.Uint64(w.last[n:])
	}
	w.retain()

	return sum, nil
}

// Reset discards the table that w writes, and starts a new table on dst
// under the options of [NewWriter]. It clears the error of an earlier write
// and [ErrClosed]. A Writer whose write failed or that Close closed then
// writes the next table.
//
// Reset keeps the buffers of w and the scratch of its filter builder while
// [Writer.Footprint] is within MaxRetained of [WriterOptions], or for a
// MaxRetained of 0, and releases them otherwise. A Writer that kept its
// buffers after a table writes a table of no more entries and no longer keys
// and values without growing them. The [Summary] that Close returned before
// remains valid, because its keys are copies.
//
// # Allocation contract
//
// Zero-alloc. A table that a Writer writes after Reset allocates the copies
// of its smallest key and its largest key, and the growth of a buffer that
// the table needs.
func (w *Writer) Reset(dst Sink) {
	w.retain()
	*w = Writer{
		w:               dst,
		builder:         w.builder,
		out:             w.out[:0],
		restarts:        w.restarts[:0],
		last:            w.last[:0],
		cur:             w.cur[:0],
		scratch:         w.scratch[:0],
		index:           w.index[:0],
		indexRestarts:   w.indexRestarts[:0],
		hashes:          w.hashes[:0],
		filterParts:     w.filterParts[:0],
		indexParts:      w.indexParts[:0],
		filterMeta:      w.filterMeta[:0],
		indexMeta:       w.indexMeta[:0],
		blockSize:       w.blockSize,
		restartInterval: w.restartInterval,
		filterBits:      w.filterBits,
		maxRetained:     w.maxRetained,
		partitionSize:   w.partitionSize,
		filterKeys:      w.filterKeys,
		compression:     w.compression,
	}
}

// Footprint returns the bytes of memory that the buffers of w take: the
// capacity of the buffers of the current block, of its keys, of the
// compressed block, of the index, of the hashes of the filter and of the
// partitions, and the scratch of the filter builder that
// [filter.FuseBuilder.Footprint] returns. It does not count the keys of a
// [Summary], because Close returns copies of them. A new Writer and a Writer
// that released its buffers return 0.
//
// # Allocation contract
//
// Zero-alloc.
func (w *Writer) Footprint() int {
	n := cap(w.out) + cap(w.last) + cap(w.cur) + cap(w.scratch) + cap(w.index)
	n += cap(w.filterParts) + cap(w.indexParts) + cap(w.filterMeta) + cap(w.indexMeta)
	n += 4 * (cap(w.restarts) + cap(w.indexRestarts))

	return n + 8*cap(w.hashes) + w.builder.Footprint()
}

// retain releases the buffers of w and the scratch of its filter builder
// when Footprint exceeds maxRetained, and keeps them for a maxRetained of 0.
// The next table grows the buffers again.
func (w *Writer) retain() {
	if w.maxRetained == 0 || w.Footprint() <= w.maxRetained {
		return
	}
	w.out, w.restarts, w.last, w.cur, w.scratch = nil, nil, nil, nil, nil
	w.index, w.indexRestarts, w.hashes = nil, nil, nil
	w.filterParts, w.indexParts, w.filterMeta, w.indexMeta = nil, nil, nil, nil
	w.builder = filter.FuseBuilder{}
}

// finishBlock ends the current data block, which out contains from its first
// byte. It appends the restart offsets and their count, seals the block and
// adds the entry of the block to the index: the place of the block and its
// CRC32C. With a partitionSize, a block whose entry brings the entries of
// the index to partitionSize bytes or more ends the index partition.
func (w *Writer) finishBlock() {
	for _, r := range w.restarts {
		w.out = binary.BigEndian.AppendUint32(w.out, r)
	}
	//nolint:gosec // G115: a block has fewer restart points than bytes
	w.out = binary.BigEndian.AppendUint32(w.out, uint32(len(w.restarts)))
	n, sum := w.seal(&w.out, 0, w.compression)

	var place [2*binary.MaxVarintLen64 + crcLen]byte
	//nolint:gosec // G115: the size is not negative
	value := binary.AppendUvarint(binary.AppendUvarint(place[:0], uint64(w.size)), n)
	value = binary.BigEndian.AppendUint32(value, sum)
	//nolint:gosec // G115: Add keeps the index below 2^32 - 1 bytes
	w.indexRestarts = append(w.indexRestarts, uint32(len(w.index)))
	w.index = appendEntry(w.index, w.last, value, 0)
	w.restarts, w.blockEntries = w.restarts[:0], 0
	w.blocks++
	if w.partitionSize != 0 && len(w.index) >= w.partitionSize {
		w.finishIndexPartition()
	}
}

// finishIndexPartition ends the current index partition. It appends the
// partition with its trailer to indexParts, and its record to indexMeta. The
// key of the record is the internal key of the last entry of the partition,
// the entry that ended the last data block. The index starts the next
// partition empty.
func (w *Writer) finishIndexPartition() {
	start := len(w.indexParts)
	w.indexParts = appendIndex(w.indexParts, w.index, w.indexRestarts)
	n, sum := w.seal(&w.indexParts, start, NoCompression)
	w.indexMeta = binary.AppendUvarint(appendRecord(w.indexMeta, w.last, n, sum), w.blocks)
	w.indexTop += uint64(len(w.last)) + maxTopOverhead
	w.index, w.indexRestarts = w.index[:0], w.indexRestarts[:0]
}

// finishFilterPartition ends the current filter partition, whose last key is
// last. It appends the encoding of a filter of the hashes of the partition
// with its trailer to filterParts, and its record to filterMeta, and empties
// the hashes. It returns the error of a builder that refuses the hashes, and
// then appends nothing.
func (w *Writer) finishFilterPartition(last []byte) error {
	start := len(w.filterParts)
	parts, err := w.builder.Append(w.filterParts, w.hashes, w.filterBits)
	w.filterParts = parts
	//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
	if err == nil {
		n, sum := w.seal(&w.filterParts, start, NoCompression)
		w.filterMeta = appendRecord(w.filterMeta, last, n, sum)
		w.filterTop += uint64(len(last)) + seqLen + maxTopOverhead
	}
	w.hashes = w.hashes[:0]

	return err
}

// appendPartitions ends the last partitions at Close of a table of format
// version 2. It appends the filter partitions, the index partitions and the
// two top-level indexes to out, where size bytes of the table precede out.
// It returns the places of the top-level index of the index and of the
// filter, and the error of a filter partition that the builder refuses.
func (w *Writer) appendPartitions(size uint64) (index, filter blockPlace, err error) {
	if len(w.indexRestarts) != 0 {
		w.finishIndexPartition()
	}
	if w.filterBits != 0 && len(w.hashes) != 0 {
		err = w.finishFilterPartition(w.last[:len(w.last)-seqLen])
	}

	// The partitions follow the last data block.
	off := size + uint64(len(w.out))
	w.out = append(w.out, w.filterParts...)
	w.out = append(w.out, w.indexParts...)
	if w.filterBits != 0 {
		start := len(w.out)
		w.out = w.appendTop(w.out, w.filterMeta, off, false)
		filter = w.sealPlace(size, start)
	}
	start := len(w.out)
	w.out = w.appendTop(w.out, w.indexMeta, off+uint64(len(w.filterParts)), true)

	return w.sealPlace(size, start), filter, err
}

// appendTop appends a top-level index over the partitions whose records meta
// contains to dst, and returns the extended slice. The first partition is
// at the offset off of the table, and each partition follows the one before
// it. Each entry has the key of the record of its partition and a value of
// the offset, the length and, for counted records, the number of data blocks
// of the partition as varints, then its CRC32C. The key of a record that is
// not counted is the last key of a filter partition, and its entry has that
// key with the sequence 0.
func (w *Writer) appendTop(dst, meta []byte, off uint64, counted bool) []byte {
	start := len(dst)
	w.indexRestarts = w.indexRestarts[:0]
	for len(meta) != 0 {
		keyLen, k := binary.Uvarint(meta)
		//nolint:gosec // G115: a key of a record is shorter than the record
		key, rest := meta[k:k+int(keyLen)], meta[k+int(keyLen):]
		n, k := binary.Uvarint(rest)
		sum, rest := binary.BigEndian.Uint32(rest[k:]), rest[k+crcLen:]

		var value [3*binary.MaxVarintLen64 + crcLen]byte
		v := binary.AppendUvarint(binary.AppendUvarint(value[:0], off), n)
		if counted {
			blocks, k := binary.Uvarint(rest)
			v, rest = binary.AppendUvarint(v, blocks), rest[k:]
		} else {
			w.cur = binary.BigEndian.AppendUint64(append(w.cur[:0], key...), 0)
			key = w.cur
		}
		//nolint:gosec // G115: Add keeps a top-level index below 2^32 - 1 bytes
		w.indexRestarts = append(w.indexRestarts, uint32(len(dst)-start))
		dst = appendEntry(dst, key, binary.BigEndian.AppendUint32(v, sum), 0)
		off += n + trailerLen
		meta = rest
	}
	for _, r := range w.indexRestarts {
		dst = binary.BigEndian.AppendUint32(dst, r)
	}

	//nolint:gosec // G115: a top-level index has at most 2^32 - 1 bytes, so it has fewer entries
	return binary.BigEndian.AppendUint32(dst, uint32(len(w.indexRestarts)))
}

// sealPlace seals the block at out[start:] without compression, where size
// bytes of the table precede out, and returns its place.
func (w *Writer) sealPlace(size uint64, start int) blockPlace {
	n, sum := w.seal(&w.out, start, NoCompression)

	//nolint:gosec // G115: start is an offset in out, which is not negative
	return blockPlace{off: size + uint64(start), n: n, crc: sum}
}

// seal ends the block at (*buf)[start:]. Under SnappyCompression it replaces
// the block with its encoding when the encoding is shorter than seven
// eighths of the block, as LevelDB does. The codec refuses a block above
// 2^32 - 1 bytes, and seal stores such a block without compression. seal
// then appends the trailer. It returns the length of the stored block
// without the trailer, and the CRC32C in the trailer.
func (w *Writer) seal(buf *[]byte, start int, c Compression) (uint64, uint32) {
	kind := NoCompression
	if c == SnappyCompression {
		raw := (*buf)[start:]
		enc, err := snappy.AppendEncode(w.scratch[:0], raw)
		w.scratch = enc
		//dokimi:mutate-skip lcr-right,ror-true: the codec refuses only a block above 2^32 - 1 bytes, which no test writes
		if err == nil && len(enc) < len(raw)-len(raw)/8 {
			*buf = append((*buf)[:start], enc...)
			kind = SnappyCompression
		}
	}
	n := uint64(len((*buf)[start:]))
	*buf = append(*buf, byte(kind))
	sum := crc32.Checksum((*buf)[start:], castagnoli)
	*buf = binary.BigEndian.AppendUint32(*buf, sum)

	return n, sum
}

// flush writes out to the Sink under ctx and empties out. It returns an
// error that wraps the error of the Sink, or io.ErrShortWrite for a write of
// fewer bytes without an error.
func (w *Writer) flush(ctx context.Context) error {
	off := w.size
	n, err := w.w.Write(ctx, w.out)
	w.size += int64(n)
	if err == nil && n < len(w.out) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("sstable: write %d bytes at %d: %w", len(w.out), off, err)
	}
	w.out = w.out[:0]

	return nil
}

// appendEntry appends the entry of the internal key ikey and value to dst
// and returns the extended slice. The entry leaves out the first shared
// bytes of ikey.
func appendEntry(dst, ikey, value []byte, shared int) []byte {
	prefix, rest := ikey[:shared], ikey[shared:]
	dst = binary.AppendUvarint(dst, uint64(len(prefix)))
	dst = binary.AppendUvarint(dst, uint64(len(rest)))
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	dst = append(dst, rest...)

	return append(dst, value...)
}

// appendIndex appends an index block or an index partition to dst and
// returns the extended slice: the entries, the offset of each restart point
// as a uint32 and their count as a uint32.
func appendIndex(dst, entries []byte, restarts []uint32) []byte {
	dst = append(dst, entries...)
	for _, r := range restarts {
		dst = binary.BigEndian.AppendUint32(dst, r)
	}

	//nolint:gosec // G115: the index has at most 2^32 - 1 bytes, so it has fewer entries
	return binary.BigEndian.AppendUint32(dst, uint32(len(restarts)))
}

// appendRecord appends the record of a partition to dst and returns the
// extended slice: the length of key as a varint, key, the stored length n of
// the partition as a varint and its CRC32C sum as a uint32.
func appendRecord(dst, key []byte, n uint64, sum uint32) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(key)))
	dst = binary.AppendUvarint(append(dst, key...), n)

	return binary.BigEndian.AppendUint32(dst, sum)
}

// partitionKeys returns the number of distinct keys at which the Writer ends
// a filter partition of size bytes, for fingerprints of the width bits. The
// encoding of that count n takes at most size bytes, and n+1 keys take more.
// One key that takes more than size bytes alone makes the count 1. The
// length of an encoding grows in steps that can fall back between two
// counts, so partitionKeys searches in halves for a count at the bound. A
// filter has at least one fingerprint for each key, so size*8/bits + 1 keys
// do not fit.
func partitionKeys(size, bits int) int {
	lo, hi := 1, int(min(int64(size)*8/int64(bits)+1, math.MaxInt32))
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if n, err := filter.FuseSize(mid, bits); err != nil || n > size {
			hi = mid
		} else {
			lo = mid
		}
	}

	return lo
}
