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
	// formatVersion is the version of the layout. The footer contains it.
	formatVersion = 1
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

// maxIndexOverhead is the largest number of bytes beside the key that an
// entry of the index takes. The entry has the three varints of its header,
// the 8 bytes of the sequence, the two varints of the place of the block and
// the 4 bytes of its CRC32C. A varint has at most 10 bytes.
const maxIndexOverhead = 62

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

// Writer writes a table to a [Sink] from entries in the order of [Compare].
// It writes each data block when the block ends, and the last data block,
// the filter, the index and the footer at [Writer.Close]. Each write takes
// the context of the call that makes it.
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
// buffer for the next table, so a Writer that writes many tables allocates
// the two keys of each summary once its buffers have grown.
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

	// index contains the entries of the index block, and indexRestarts their
	// offsets. Every entry of the index is a restart point.
	index         []byte
	indexRestarts []uint32

	// hashes are the hashes of the distinct keys. The filter contains them.
	hashes []uint64

	// smallest is the key of the first entry, and smallestSeq its sequence.
	smallest    []byte
	smallestSeq uint64

	// blockSize, restartInterval and filterBits are options of the Writer
	// after NewWriter applied the defaults.
	blockSize       int
	restartInterval int
	filterBits      int

	// blockEntries is the number of entries of the current block.
	blockEntries int

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
//     RestartInterval, an invalid Compression or a FilterBits that is
//     neither 0 nor a width of fingerprints returns [ErrOptions].
//
// # Allocation contract
//
// Allocates the Writer, and with a filter the encoding of an empty filter
// that checks the width.
func NewWriter(w Sink, opts WriterOptions) (*Writer, error) {
	if opts.BlockSize < 0 || int64(opts.BlockSize) > math.MaxUint32 || opts.RestartInterval < 0 ||
		!opts.Compression.Valid() {

		return nil, ErrOptions
	}

	wr := &Writer{
		w:               w,
		blockSize:       cmp.Or(opts.BlockSize, defaultBlockSize),
		restartInterval: cmp.Or(opts.RestartInterval, defaultRestartInterval),
		compression:     opts.Compression,
		filterBits:      opts.FilterBits,
	}
	if opts.FilterBits != 0 {
		if _, err := wr.builder.Append(nil, nil, opts.FilterBits); err != nil {
			return nil, ErrOptions
		}
	}

	return wr, nil
}

// Add appends the entry of key, seq and value to the table. The entry must
// follow the previous entry in the order of [Compare]. Add copies key and
// value. It writes the current data block to the Sink under ctx when the
// entry ends the block.
//
// Error modes:
//
//   - A key whose entry in the index could bring the index past 2^32 - 1
//     bytes returns [ErrTooLarge], and the Writer is unchanged.
//   - An entry at or before the previous entry returns [ErrOrder], and the
//     Writer is unchanged.
//   - An error of the Sink, such as the error of ctx at the end of a wait,
//     returns an error that wraps it, and every later call returns the same
//     error. A write of fewer bytes without an error returns an error that
//     wraps io.ErrShortWrite.
//   - A call after Close returns [ErrClosed].
//
// # Allocation contract
//
// Zero-alloc when the buffers of the current block, the index and the
// hashes of the filter have room. Otherwise Add allocates their growth. The
// first Add also allocates a copy of its key for the summary.
func (w *Writer) Add(ctx context.Context, key []byte, seq uint64, value []byte) error {
	if w.err != nil {
		return w.err
	}
	//dokimi:mutate-skip ror-boundary: a key at the bound has 4 GiB, and Add keeps three copies of it, which no test allocates
	if uint64(len(w.index))+uint64(len(key)) > math.MaxUint32-maxIndexOverhead {
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
		return nil
	}
	w.finishBlock()
	w.err = w.flush(ctx)

	return w.err
}

// Close writes the last data block, the filter, the index and the footer
// with one write under ctx, and returns the summary of the table.
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
// growth of the buffer of the table for the filter and the index, and the
// largest key of the summary.
func (w *Writer) Close(ctx context.Context) (Summary, error) {
	if w.err != nil {
		return Summary{}, w.err
	}
	if w.blockEntries != 0 {
		w.finishBlock()
	}

	//nolint:gosec // G115: the size is not negative
	size := uint64(w.size)
	var filterOff, filterLen uint64
	var filterCRC uint32
	var err error
	if w.filterBits != 0 {
		start, off := len(w.out), size+uint64(len(w.out))
		//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
		if w.out, err = w.builder.Append(w.out, w.hashes, w.filterBits); err == nil {
			filterOff = off
			filterLen, filterCRC = w.seal(start, NoCompression)
		}
	}

	be := binary.BigEndian
	start, indexOff := len(w.out), size+uint64(len(w.out))
	w.out = append(w.out, w.index...)
	for _, r := range w.indexRestarts {
		w.out = be.AppendUint32(w.out, r)
	}
	//nolint:gosec // G115: the index has at most 2^32 - 1 bytes, so it has fewer entries
	w.out = be.AppendUint32(w.out, uint32(len(w.indexRestarts)))
	indexLen, indexCRC := w.seal(start, NoCompression)

	footer := len(w.out)
	w.out = be.AppendUint64(w.out, indexOff)
	w.out = be.AppendUint64(w.out, indexLen)
	w.out = be.AppendUint32(w.out, indexCRC)
	w.out = be.AppendUint64(w.out, filterOff)
	w.out = be.AppendUint64(w.out, filterLen)
	w.out = be.AppendUint32(w.out, filterCRC)
	//nolint:gosec // G115: the number of entries is not negative
	w.out = be.AppendUint64(w.out, uint64(w.entries))
	w.out = be.AppendUint64(w.out, w.minSeq)
	w.out = be.AppendUint64(w.out, w.maxSeq)
	w.out = be.AppendUint32(w.out, formatVersion)
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

	return sum, nil
}

// Reset discards the table that w writes, and starts a new table on dst
// under the options of [NewWriter]. It clears the error of an earlier write
// and [ErrClosed]. A Writer whose write failed or that Close closed then
// writes the next table.
//
// Reset keeps the buffers of w and the scratch of its filter builder. A
// Writer that wrote a table then writes a table of no more entries and no
// longer keys and values without growing them. The [Summary] that Close
// returned before remains valid, because its keys are copies.
//
// # Allocation contract
//
// Zero-alloc. A table that a Writer writes after Reset allocates the copies
// of its smallest key and its largest key, and the growth of a buffer that
// the table needs.
func (w *Writer) Reset(dst Sink) {
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
		blockSize:       w.blockSize,
		restartInterval: w.restartInterval,
		filterBits:      w.filterBits,
		compression:     w.compression,
	}
}

// finishBlock ends the current data block, which out contains from its first
// byte. It appends the restart offsets and their count, seals the block and
// adds the entry of the block to the index: the place of the block and its
// CRC32C.
func (w *Writer) finishBlock() {
	for _, r := range w.restarts {
		w.out = binary.BigEndian.AppendUint32(w.out, r)
	}
	//nolint:gosec // G115: a block has fewer restart points than bytes
	w.out = binary.BigEndian.AppendUint32(w.out, uint32(len(w.restarts)))
	n, sum := w.seal(0, w.compression)

	var place [2*binary.MaxVarintLen64 + crcLen]byte
	//nolint:gosec // G115: the size is not negative
	value := binary.AppendUvarint(binary.AppendUvarint(place[:0], uint64(w.size)), n)
	value = binary.BigEndian.AppendUint32(value, sum)
	//nolint:gosec // G115: Add keeps the index below 2^32 - 1 bytes
	w.indexRestarts = append(w.indexRestarts, uint32(len(w.index)))
	w.index = appendEntry(w.index, w.last, value, 0)
	w.restarts, w.blockEntries = w.restarts[:0], 0
}

// seal ends the block at out[start:]. Under SnappyCompression it replaces
// the block with its encoding when the encoding is shorter than seven
// eighths of the block, as LevelDB does. The codec refuses a block above
// 2^32 - 1 bytes, and seal stores such a block without compression. seal
// then appends the trailer. It returns the length of the stored block
// without the trailer, and the CRC32C in the trailer.
func (w *Writer) seal(start int, c Compression) (uint64, uint32) {
	kind := NoCompression
	if c == SnappyCompression {
		raw := w.out[start:]
		enc, err := snappy.AppendEncode(w.scratch[:0], raw)
		w.scratch = enc
		//dokimi:mutate-skip lcr-right,ror-true: the codec refuses only a block above 2^32 - 1 bytes, which no test writes
		if err == nil && len(enc) < len(raw)-len(raw)/8 {
			w.out = append(w.out[:start], enc...)
			kind = SnappyCompression
		}
	}
	n := uint64(len(w.out[start:]))
	w.out = append(w.out, byte(kind))
	sum := crc32.Checksum(w.out[start:], castagnoli)
	w.out = binary.BigEndian.AppendUint32(w.out, sum)

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
