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
	"math/bits"
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
	// placedVersion is the version of the layout of a table with a block of
	// places, which places the filter, the block of fragments of range
	// tombstones and the filter of prefixes. The filter fields of the footer
	// state the place of the block of places.
	placedVersion = 3
	// partitionedFlag is the flag of the block of places of a table whose
	// index and filters are cut into partitions.
	partitionedFlag = 1
	// fragmentPartsFlag is the flag of the block of places of a table in
	// partitions whose fragments are cut into partitions. The place of the
	// fragments then states the top-level index of the fragment partitions.
	fragmentPartsFlag = 2
	// suffixFlag is the flag of the block of places of a table whose keys
	// end with versions. The width of the versions follows the name of the
	// extractor as a varint.
	suffixFlag = 4
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

	// maxFilterTopOverhead bounds an entry of the top-level index of a
	// filter beside its key: the sequence 0 after the key, and the bytes of
	// an entry of a top-level index.
	maxFilterTopOverhead = seqLen + maxTopOverhead
)

// maxSizeCount is the largest count that [WriterOptions.MaxSize] bounds: 2^48
// on a platform whose int has 64 bits, and the largest int otherwise. The
// bound of smaller counts fits an int64.
const maxSizeCount = min(1<<48, math.MaxInt)

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

// PrefixExtractor returns the prefix of a key for the prefix filter of a
// table. The Writer records the Name of the extraction in each table, and a
// [Reader] uses the prefix filter only for a [ReaderOptions] whose extractor
// has that name. A reader that assumed another extraction would skip tables
// that contain keys of a prefix.
//
// # Concurrency
//
// The methods of a PrefixExtractor must be safe for concurrent use, because
// the Writers and the Readers of many tables can share one.
type PrefixExtractor interface {
	// Name returns the name of the extraction. It must not be empty.
	Name() string

	// Prefix returns the prefix of key, and reports false for a key without
	// a prefix. The prefix must be a byte prefix of key, and the prefixes of
	// keys in the order of bytes.Compare must not fall, so the keys of one
	// prefix are consecutive in a table. The Writer copies the prefix.
	Prefix(key []byte) ([]byte, bool)
}

// Counts are the counts of the parts of a table that
// [WriterOptions.MaxSize] bounds. A negative count counts as 0.
type Counts struct {
	// Entries is the most entries of the table.
	Entries int64

	// KeyBytes is the most bytes of the keys of the entries, in all.
	KeyBytes int64

	// ValueBytes is the most bytes of the values of the entries, in all.
	ValueBytes int64

	// Longest is the most bytes of one key.
	Longest int

	// RangeDeletes is the most calls of [Writer.AddRangeDelete], one for each
	// sequence of each fragment.
	RangeDeletes int64

	// RangeDeleteBytes is the most bytes of the starts and the ends of those
	// calls, in all.
	RangeDeleteBytes int64
}

// WriterOptions configure a [Writer]. The zero value writes data blocks of
// 4,096 bytes with a restart point every 16 entries, without compression and
// without a filter.
type WriterOptions struct {
	// Prefix extracts the prefixes of the prefix filter of the table: a
	// [filter.Fuse] over the [xxh3.Hash] of each distinct prefix, with the
	// width of FilterBits and the partitions of PartitionSize. With nil the
	// Writer does not write a prefix filter. A table with a prefix filter
	// has format version 3.
	Prefix PrefixExtractor

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
	// filter partition. The Writer ends an index partition with the entry
	// that brings its entries to PartitionSize bytes or more. It ends a
	// filter partition at the most keys or prefixes up to which the encoding
	// of every count takes at most PartitionSize bytes, so every filter
	// partition of more than one key fits PartitionSize. A table in
	// partitions has format version 2, or 3 with fragments or a Prefix. With
	// 0 the Writer writes one index block and one filter block of each kind.
	// The largest PartitionSize is 2^32 - 1. The Writer also cuts the
	// fragments of a table in partitions into fragment partitions: it ends a
	// fragment partition at the first fragment after the entries of the
	// partition take PartitionSize bytes or more.
	PartitionSize int

	// SuffixBytes is the width in bytes of the version at the end of each
	// key. The Writer orders the entries by [CompareWithSuffix] of the width.
	// It adds the hash of each key without its version to the filter of the
	// keys, and passes each key without its version to Prefix. The start of
	// a fragment of [Writer.AddRangeDelete] ends with the version of the
	// fragment, and its end has no version. A table with versions has format
	// version 3, and records the width. 0 orders the entries by [Compare].
	SuffixBytes int
}

// MaxSize returns the most bytes of a table that a [Writer] under o writes
// for the counts of c. A store that reserves the space of a table before a
// flush passes the counts of the entries and the fragments that the flush
// writes.
//
// The bound takes every entry at a restart point with no shared prefix, and
// every block without compression. It counts the most data blocks and
// partitions that the bytes of their entries allow. The key of an entry of an
// index or of a top-level index counts at most Longest bytes, and the keys of
// one kind of index count at most KeyBytes bytes together. A filter takes the
// bytes of the largest encoding of at most its count of keys.
// [filter.MaxFuseSize] returns that length. A filter of prefixes has at most
// a prefix for each entry, and takes the bound of the filter of the keys.
// Each call of [Writer.AddRangeDelete] takes an entry of the block of
// fragments with no shared prefix. A table in partitions counts the most
// fragment partitions that the bytes of the entries of the fragments allow,
// and an entry of the top-level index of the fragments for each. The ends of
// those entries count at most RangeDeleteBytes bytes together. A SuffixBytes
// above 0 gives the table a block of places with the varint of the width. A
// count above 2^48 makes MaxSize return math.MaxInt64.
//
// Error modes:
//
//   - Options that [NewWriter] refuses return [ErrOptions].
//   - Entries whose filter block of a table without partitions needs more
//     than 2^32 fingerprints return the error of [filter.MaxFuseSize].
func (o WriterOptions) MaxSize(c Counts) (int64, error) {
	if err := o.check(); err != nil {
		return 0, err
	}
	n, keys, values, l := uint64(max(c.Entries, 0)), uint64(max(c.KeyBytes, 0)), uint64(max(c.ValueBytes, 0)),
		uint64(max(c.Longest, 0))
	deletes, deleteBytes := uint64(max(c.RangeDeletes, 0)), uint64(max(c.RangeDeleteBytes, 0))
	if max(n, keys, values, l, deletes, deleteBytes) > maxSizeCount {
		return math.MaxInt64, nil
	}

	// keysOf returns the most bytes that the keys of c distinct entries
	// take: c keys of the longest length, and at most the bytes of every key.
	keysOf := func(c uint64) uint64 {
		if l != 0 && c > keys/l {
			return keys
		}

		return c * l
	}
	keys = keysOf(n)
	// A table of no entries has no values.
	if n == 0 {
		values = 0
	}

	// An entry has three varints, its internal key and its value. A varint
	// of a length takes one byte, and at most one more for each 128 bytes of
	// the length. A block ends after the entry that brings its entries to
	// BlockSize bytes. It then has a uint32 for each restart point and one
	// for their count, and a trailer.
	ikeys := keys + seqLen*n
	data := ikeys + values + 3*n + ikeys/128 + values/128
	//nolint:gosec // G115: check refuses a negative BlockSize
	blocks := min(n, data/uint64(cmp.Or(o.BlockSize, defaultBlockSize))+1)
	//nolint:gosec // G115: check refuses a negative RestartInterval
	data += 4*min(n, n/uint64(cmp.Or(o.RestartInterval, defaultRestartInterval))+blocks) + (4+trailerLen)*blocks

	// An entry of the index has the internal key of the last entry of its
	// block, and the offset and the length of the block. The first block is
	// at the offset 0, whose varint takes one byte. Each other offset and
	// each length takes at most the bytes of the varint of the data blocks.
	// A table of no entries has no data, so its varint takes one byte too.
	placeLen := uint64(bits.Len64(data|1)+6) / 7 //nolint:gosec // G115: Len64 returns at most 64
	last := keysOf(blocks) + seqLen*blocks
	index := last + last/128 + (3+crcLen+2*placeLen)*blocks - (placeLen - 1)

	// The filter of a table without partitions is one block of the hashes of
	// every key. A table in partitions cuts it into partitions of at most the
	// keys of partitionKeys. The filter of prefixes has at most a prefix for
	// each entry, so it takes the same bound.
	filterKinds := uint64(0)
	if o.FilterBits != 0 {
		filterKinds = 1
	}
	if o.Prefix != nil {
		filterKinds = 2
	}
	fparts, fkeys := uint64(1), n
	filters, fpartLen := uint64(0), uint64(0)
	if o.FilterBits != 0 {
		if o.PartitionSize != 0 {
			//nolint:gosec // G115: partitionKeys returns at least 1
			fkeys = min(uint64(partitionKeys(o.PartitionSize, o.FilterBits)), max(n, 1))
			fparts = (n + fkeys - 1) / fkeys
		}
		//nolint:gosec // G115: fkeys is at most maxSizeCount, an int
		f, err := filter.MaxFuseSize(int(fkeys), o.FilterBits)
		if err != nil {
			return 0, err
		}
		largest := uint64(f) //nolint:gosec // G115: the length of an encoding is not negative
		filters = filterKinds * fparts * (largest + trailerLen)
		fpartLen = uint64(bits.Len64(largest|1)+6) / 7 //nolint:gosec // G115: Len64 returns at most 64
	}

	// The block of fragments has an entry at a restart point for each call
	// of AddRangeDelete: three varints, the internal key of the start and the
	// sequence, and the end as its value. It has a uint32 for each restart
	// point and one for their count, and a trailer.
	//
	// A table in partitions ends a fragment partition at the first fragment
	// after the entries of the partition take PartitionSize bytes, so every
	// partition but the last has PartitionSize bytes of entries or more. Each
	// partition has a uint32 for the count of its restart points and a
	// trailer. The top-level index of the fragments has an entry for each
	// partition: three varints, the end of the last fragment of the partition
	// and the sequence 0, and a value of the offset, the length and the count
	// of the entries up to the end of the partition as varints and the CRC32C.
	// The entry has a uint32 for its restart point. The top-level index has a
	// uint32 for the count of its entries and a trailer. The offset and the
	// length of each partition count among the tops.
	fragments, fragTops := uint64(0), uint64(0)
	if deletes != 0 {
		ikeys := deleteBytes + seqLen*deletes
		entries := ikeys + ikeys/128 + 3*deletes
		fragments = entries + 4*deletes + 4 + trailerLen
		if o.PartitionSize != 0 {
			//nolint:gosec // G115: check refuses a negative PartitionSize
			parts := min(deletes, entries/uint64(o.PartitionSize)+1)
			ends := deleteBytes + seqLen*parts
			countLen := uint64(bits.Len64(deletes|1)+6) / 7 //nolint:gosec // G115: Len64 returns at most 64
			fragments += (4+trailerLen)*(parts-1) + ends + ends/128 + (3+crcLen+4+countLen)*parts + 4 + trailerLen
			fragTops = 2 * parts
		}
	}

	// The entries of the index have a uint32 each for their restart points.
	// An index block has a uint32 for the count of its entries and a trailer.
	size := data + filters + fragments + index + 4*blocks + footerLen
	tops := uint64(0)
	if o.PartitionSize == 0 {
		size += 4 + trailerLen
	} else {
		// An index partition ends at the entry that brings its entries to
		// PartitionSize bytes. It has a uint32 for the count of its entries
		// and a trailer.
		//nolint:gosec // G115: check refuses a negative PartitionSize
		parts := min(blocks, index/uint64(o.PartitionSize)+1)
		size += (4 + trailerLen) * parts

		// An entry of a top-level index has the internal key of the last
		// entry of its partition, and the offset and the length of the
		// partition. An entry of the index also has the number of data blocks
		// up to the end of its partition. A top-level index has a uint32 for
		// the restart point of each entry and one for their count, and a
		// trailer. The top-level index of the prefixes has the bound of the
		// top-level index of the keys.
		partKeys := keysOf(parts) + seqLen*parts
		blocksLen := uint64(bits.Len64(blocks|1)+6) / 7 //nolint:gosec // G115: Len64 returns at most 64
		size += partKeys + partKeys/128 + (3+crcLen+4+blocksLen)*parts + 4 + trailerLen
		filterKeys := keysOf(fparts) + seqLen*fparts
		size += filterKinds * (filterKeys + filterKeys/128 + (3+crcLen+4+fpartLen)*fparts + 4 + trailerLen)
		tops = 2*parts + filterKinds*fparts + fragTops
	}

	// The block of places of format version 3 has the flags, the CRC32C of
	// three parts, the name of the extractor with the varint of its length,
	// and a trailer. The offset of each part is below the size of the table,
	// and so is the length of a top-level index. The length of a filter block
	// takes the bytes of the varint of the largest filter, and the length of
	// the block of fragments the bytes of the varint of its bound. A missing
	// part has an offset and a length of one byte each. The width of the
	// versions follows the name as a varint.
	if o.Prefix != nil || deletes != 0 || o.SuffixBytes != 0 {
		name := uint64(0)
		if o.Prefix != nil {
			name = uint64(len(o.Prefix.Name()))
		}
		//nolint:gosec // G115: Len64 returns at most 64
		size += 1 + 3*crcLen + uint64(bits.Len64(name|1)+6)/7 + name + trailerLen + 2*(2-filterKinds)
		tops += filterKinds
		if o.PartitionSize == 0 {
			size += filterKinds * fpartLen
		} else {
			tops += filterKinds
		}
		switch {
		case deletes == 0:
			size += 2
		case o.PartitionSize == 0:
			//nolint:gosec // G115: Len64 returns at most 64
			size += uint64(bits.Len64((fragments-trailerLen)|1)+6) / 7
			tops++
		default:
			// The place states the top-level index of the fragment partitions.
			tops += 2
		}
		//nolint:gosec // G115: check refuses a negative SuffixBytes, and Len64 returns at most 64
		size += uint64(bits.Len64(uint64(o.SuffixBytes))+6) / 7
	}

	// The offset of each partition and of each part of the block of places,
	// and the length of each index partition and of each part, are below the
	// size of the table, so the varint of a bound of the size bounds the
	// varint of each of them. The bound with varints of 10 bytes bounds the
	// size, and so does each bound with the varints of the bound before it.
	offLen := uint64(binary.MaxVarintLen64)
	for {
		total := size + offLen*tops
		next := uint64(bits.Len64(total|1)+6) / 7 //nolint:gosec // G115: Len64 returns at most 64
		if next == offLen {
			//nolint:gosec // G115: the bound of counts up to maxSizeCount fits an int64
			return int64(total), nil
		}
		offLen = next
	}
}

// check returns [ErrOptions] for options that a Writer does not accept: a
// negative BlockSize, RestartInterval, MaxRetained, PartitionSize or
// SuffixBytes, a BlockSize or a PartitionSize above 2^32 - 1, a Compression
// that is not valid, a FilterBits that is neither 0 nor a width of
// fingerprints that [filter.FuseSize] accepts, and a Prefix without
// FilterBits or with an empty name.
func (o WriterOptions) check() error {
	if o.BlockSize < 0 || int64(o.BlockSize) > math.MaxUint32 || o.RestartInterval < 0 ||
		!o.Compression.Valid() || o.MaxRetained < 0 ||
		o.PartitionSize < 0 || int64(o.PartitionSize) > math.MaxUint32 || o.SuffixBytes < 0 {

		return ErrOptions
	}
	if o.FilterBits != 0 {
		if _, err := filter.FuseSize(0, o.FilterBits); err != nil {
			return ErrOptions
		}
	}
	if o.Prefix != nil && (o.FilterBits == 0 || o.Prefix.Name() == "") {
		return ErrOptions
	}

	return nil
}

// Summary describes the entries, the fragments of range tombstones, the size
// and the range of keys and sequences of a table. For a table of no entries
// and no fragments, every field except Size is zero or nil. Summary keeps the
// bounds of the entries apart from the bounds of the fragments, and an engine
// places a table by both pairs.
type Summary struct {
	SmallestKey      []byte // the key of the first entry
	LargestKey       []byte // the key of the last entry
	RangeDeleteStart []byte // the start of the first fragment
	RangeDeleteEnd   []byte // the end of the last fragment, which the fragment excludes
	Entries          int64  // the number of entries
	RangeDeletes     int64  // the number of the entries of the block of fragments, one for each sequence of a fragment
	Size             int64  // the bytes of the table
	MinSeq           uint64 // the lowest sequence of any entry or fragment
	MaxSeq           uint64 // the highest sequence of any entry or fragment
	SmallestSeq      uint64 // the sequence of the first entry
	LargestSeq       uint64 // the sequence of the last entry
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

// places is the block of places of a table of format version 3: whether the
// index and the filters are partitioned, whether the fragments are, the
// places of the filter, of the fragments and of the filter of prefixes, the
// name of the extractor of the prefixes, and the width of the versions of
// the keys. A part that the table does not have has the zero place, and a
// table without a filter of prefixes has an empty name. Every block has at
// least one byte, so a Reader takes a length of 0 for a missing part. The
// place of the fragments states the block of fragments, or the top-level
// index of the fragment partitions when fragmentParts is true. A table
// without versions has the width 0.
type places struct {
	name                      string
	filter, fragments, prefix blockPlace
	suffix                    uint64
	partitioned               bool
	fragmentParts             bool
}

// append appends the encoding of p to dst and returns the extended slice:
// the flags as a varint, each place as its offset and its length as varints
// and its CRC32C as a uint32, then the length of the name as a varint and the
// name. The flags have bit 0 for a table in partitions, bit 1 for fragment
// partitions and bit 2 for versions. A width of versions above 0 follows the
// name as a varint.
func (p *places) append(dst []byte) []byte {
	flags := uint64(0)
	if p.partitioned {
		flags |= partitionedFlag
	}
	if p.fragmentParts {
		flags |= fragmentPartsFlag
	}
	if p.suffix != 0 {
		flags |= suffixFlag
	}
	dst = binary.AppendUvarint(dst, flags)
	for _, b := range [...]blockPlace{p.filter, p.fragments, p.prefix} {
		dst = binary.BigEndian.AppendUint32(binary.AppendUvarint(binary.AppendUvarint(dst, b.off), b.n), b.crc)
	}
	dst = binary.AppendUvarint(dst, uint64(len(p.name)))
	dst = append(dst, p.name...)
	if p.suffix != 0 {
		dst = binary.AppendUvarint(dst, p.suffix)
	}

	return dst
}

// decode decodes the block of places b into p. It reports false for a block
// whose fields do not decode, or with a flag other than partitionedFlag,
// fragmentPartsFlag and suffixFlag. It also reports false for
// fragmentPartsFlag without partitionedFlag or without a place of the
// fragments, for a width of versions of 0 under suffixFlag, and for a byte
// after the last field. decode allocates the name.
func (p *places) decode(b []byte) bool {
	// When the varint of the flags fails, Uvarint returns the flags 0, and
	// the place of the filter decodes the same bytes and fails too.
	flags, n := binary.Uvarint(b)
	if flags&^(partitionedFlag|fragmentPartsFlag|suffixFlag) != 0 {
		return false
	}
	p.partitioned, p.fragmentParts = flags&partitionedFlag != 0, flags&fragmentPartsFlag != 0
	if p.fragmentParts && !p.partitioned {
		return false
	}
	b = b[max(n, 0):]
	for _, dst := range [...]*blockPlace{&p.filter, &p.fragments, &p.prefix} {
		off, n1 := binary.Uvarint(b)
		// When the first varint fails, Uvarint decodes the same bytes for the
		// second and fails again, so n2 reports either failure.
		size, n2 := binary.Uvarint(b[max(n1, 0):])
		at := max(n1, 0) + max(n2, 0)
		//dokimi:mutate-skip lcr-right,ror-boundary: a varint that fails leaves a length of 0 and a CRC32C of continuation bytes, which tail refuses for a missing part, and a block that ends at a CRC32C does not have the length of the name
		if n2 <= 0 || len(b) < at+crcLen {
			return false
		}
		*dst, b = blockPlace{off: off, n: size, crc: binary.BigEndian.Uint32(b[at:])}, b[at+crcLen:]
	}
	if p.fragmentParts && p.fragments.n == 0 {
		return false
	}
	length, n := binary.Uvarint(b)
	//nolint:gosec // G115: n is at most len(b)
	if n <= 0 || length > uint64(len(b)-n) {
		return false
	}
	p.name, b = string(b[n:n+int(length)]), b[n+int(length):] //nolint:gosec // G115: length is at most len(b)-n
	if flags&suffixFlag != 0 {
		p.suffix, n = binary.Uvarint(b)
		if n <= 0 || p.suffix == 0 {
			return false
		}
		b = b[n:]
	}

	return len(b) == 0
}

// filterBuild is a filter that a [Writer] builds over the hashes of the
// distinct keys of a table, or of their prefixes. In partitions, it keeps
// the finished partitions until Close.
type filterBuild struct {
	// hashes are the hashes of the filter, or of its current partition.
	hashes []uint64

	// parts contains the finished partitions with their trailers, in their
	// order.
	parts []byte

	// meta contains a record of each finished partition: the length of its
	// last key or prefix as a varint, that key or prefix, the stored length
	// of the partition as a varint and its CRC32C as a uint32.
	meta []byte

	// top bounds the bytes of the entries of the top-level index of the
	// partitions.
	top uint64
}

// Writer writes a table to a [Sink] from entries in the order of [Compare].
// It writes each data block when the block ends, and the last data block,
// the filter, the index and the footer at [Writer.Close]. Each write takes
// the context of the call that makes it. With the PartitionSize of
// [WriterOptions], the Writer cuts the index and the filter into partitions
// as the entries arrive, and keeps the partitions until Close. With the
// Prefix of WriterOptions it also builds a filter of the prefixes of the
// keys, and [Writer.AddRangeDelete] adds the fragments of range tombstones,
// which it keeps until Close in a block of their own.
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
// of its key, and the first [Writer.AddRangeDelete] a copy of its start. Add
// and AddRangeDelete otherwise allocate only to grow the buffers of the
// current block, the index, the hashes of the filters and the fragments, so
// they do not allocate once those have room. Close allocates the encoding of
// the filter, the scratch of its builder, the growth of the buffer of the
// table for the filter and the index, and the largest key and the end of the
// last fragment of the summary. [Writer.Reset] keeps every buffer for the
// next table while [Writer.Footprint] is within the MaxRetained of
// [WriterOptions], so a Writer that writes many tables of one size allocates
// the keys of each summary once its buffers have grown. A table after a
// release of the buffers allocates their growth again.
type Writer struct {
	// w receives the bytes of the table.
	w Sink

	// err is the error of w, or ErrClosed once Close wrote the table. Every
	// later call returns it.
	err error

	// prefix is the Prefix of the options, or nil.
	prefix PrefixExtractor

	// builder builds the filters.
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

	// indexParts contains the finished index partitions with their trailers,
	// in their order.
	indexParts []byte

	// indexMeta contains a record of each finished index partition: the
	// length of the internal key of its last entry as a varint, that key, the
	// stored length of the partition as a varint, its CRC32C as a uint32, and
	// the number of data blocks up to the end of the partition as a varint.
	indexMeta []byte

	// prefixLast is the last distinct prefix of the keys, which prefixed
	// reports.
	prefixLast []byte

	// frags contains the entries of the block of fragments, or of the current
	// fragment partition, and fragRestarts their offsets. Every entry is a
	// restart point. fragLast is the internal key of the last entry, the
	// start of the last fragment and its sequence, and fragEnd the end of the
	// last fragment.
	frags             []byte
	fragRestarts      []uint32
	fragLast, fragEnd []byte

	// fragParts contains the finished fragment partitions with their
	// trailers, in their order.
	fragParts []byte

	// fragMeta contains a record of each finished fragment partition: the
	// length of the end of its last fragment with the sequence 0 as a
	// varint, that end and sequence, the stored length of the partition as a
	// varint, its CRC32C as a uint32, and the number of the entries of the
	// fragments up to the end of the partition as a varint.
	fragMeta []byte

	// smallest is the key of the first entry, and fragStart the start of the
	// first fragment. Both are copies for the summary.
	smallest, fragStart []byte

	// keys is the filter of the keys of the table, and prefixes the filter of
	// their prefixes.
	keys, prefixes filterBuild

	// blockSize, restartInterval, filterBits, maxRetained, partitionSize and
	// suffix are options of the Writer after NewWriter applied the defaults.
	// suffix is the SuffixBytes of the options.
	blockSize       int
	restartInterval int
	filterBits      int
	maxRetained     int
	partitionSize   int
	suffix          int

	// filterKeys is the number of distinct keys at which a filter partition
	// ends: the most keys whose encoding takes at most partitionSize bytes,
	// and at least 1.
	filterKeys int

	// blockEntries is the number of entries of the current block.
	blockEntries int

	// blocks is the number of finished data blocks.
	blocks uint64

	// indexTop bounds the bytes of the entries of the top-level index of the
	// index, and fragTop of the top-level index of the fragments.
	indexTop, fragTop uint64

	// size is the number of bytes that w accepted.
	size int64

	// entries is the number of entries that Add added, and fragCount the
	// number of fragments that AddRangeDelete added. minSeq and maxSeq are
	// the lowest and the highest sequence of both, and smallestSeq is the
	// sequence of the first entry.
	entries, fragCount          int64
	minSeq, maxSeq, smallestSeq uint64

	// compression is the codec of the data blocks.
	compression Compression

	// prefixed reports whether a key had a prefix, which prefixLast then
	// contains.
	prefixed bool
}

var _ RangeDeleter = (*Writer)(nil)

// NewWriter returns a [Writer] that writes a table to w under opts. It
// checks FilterBits with [filter.FuseSize], so the Writer accepts every
// width of fingerprints that the filter package builds.
//
// Error modes:
//
//   - A negative BlockSize, a BlockSize above 2^32 - 1, a negative
//     RestartInterval, an invalid Compression, a FilterBits that is neither
//     0 nor a width of fingerprints, a negative MaxRetained, a negative
//     PartitionSize or a PartitionSize above 2^32 - 1 returns [ErrOptions].
//   - A Prefix without FilterBits, or whose Name is empty, returns
//     ErrOptions.
//
// # Allocation contract
//
// Allocates the Writer.
func NewWriter(w Sink, opts WriterOptions) (*Writer, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}

	wr := &Writer{
		w:               w,
		prefix:          opts.Prefix,
		blockSize:       cmp.Or(opts.BlockSize, defaultBlockSize),
		restartInterval: cmp.Or(opts.RestartInterval, defaultRestartInterval),
		compression:     opts.Compression,
		filterBits:      opts.FilterBits,
		maxRetained:     opts.MaxRetained,
		partitionSize:   opts.PartitionSize,
		suffix:          opts.SuffixBytes,
	}
	if opts.FilterBits != 0 {
		wr.filterKeys = partitionKeys(opts.PartitionSize, opts.FilterBits)
	}

	return wr, nil
}

// Add appends the entry of key, seq and value to the table. The entry must
// follow the previous entry in the order of [CompareWithSuffix] of the
// SuffixBytes of the options, which is the order of [Compare] without
// versions. Add copies key and value. It writes the current data block to
// the Sink under ctx when the entry ends the block. A key is distinct from
// the key before it when their bytes before their versions differ. With a
// PartitionSize, a distinct key after the most keys of a filter partition
// ends the partition, and a data block that brings the current index
// partition to PartitionSize bytes ends that partition. With a Prefix, a
// distinct key whose prefix differs from the prefix before it adds a hash to
// the filter of prefixes, and ends a full partition of it. Add passes key
// without its version to Prefix.
//
// Error modes:
//
//   - A key whose entry in the index, in an index partition or in a
//     top-level index could bring it past 2^32 - 1 bytes returns
//     [ErrTooLarge], and the Writer is unchanged.
//   - An entry at or before the previous entry returns [ErrOrder], and the
//     Writer is unchanged.
//   - A prefix that is not a byte prefix of key without its version, or
//     that is before the prefix of an earlier key, returns [ErrPrefix], and
//     the Writer is unchanged.
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
// of the filters and the partitions have room, apart from what Prefix
// allocates. Otherwise Add allocates their growth, and an end of a filter
// partition can grow the scratch of the builder of the filter. The first Add
// also allocates a copy of its key for the summary.
func (w *Writer) Add(ctx context.Context, key []byte, seq uint64, value []byte) error {
	if w.err != nil {
		return w.err
	}
	//dokimi:mutate-skip lcr-left,lcr-right,ror-boundary,ror-false,sbr-delete: a key of 4 GiB passes both bounds, and Add keeps three copies of a key that passes the check, which no test allocates
	if uint64(len(w.index))+uint64(len(key)) > math.MaxUint32-maxIndexOverhead ||
		//dokimi:mutate-skip ror-boundary,ror-false: the bound of a top-level index differs from the bound of the index only for an index near 4 GiB, which no test writes
		max(w.indexTop, w.keys.top, w.prefixes.top)+uint64(len(key)) > math.MaxUint32-seqLen-maxTopOverhead {

		return ErrTooLarge
	}

	// The filters and the prefixes take the key without its version.
	user := key[:max(len(key)-w.suffix, 0)]
	distinct := true
	if w.entries != 0 {
		n := len(w.last) - seqLen
		last, lastSeq := w.last[:n], binary.BigEndian.Uint64(w.last[n:])
		// Compare at the width 0 saves the call of CompareWithSuffix on each
		// Add.
		var order int
		//dokimi:mutate-skip ror-false: CompareWithSuffix of the width 0 orders as Compare, so the short path changes only the time
		if w.suffix == 0 {
			order = Compare(key, seq, last, lastSeq)
		} else {
			order = CompareWithSuffix(key, seq, last, lastSeq, w.suffix)
		}
		if order <= 0 {
			return ErrOrder
		}
		distinct = !bytes.Equal(user, last[:max(n-w.suffix, 0)])
	}

	// A key of the last key has its prefix. A new prefix follows the last
	// one, so the keys of a prefix are consecutive.
	var prefix []byte
	fresh := false
	if w.prefix != nil && distinct {
		if p, ok := w.prefix.Prefix(user); ok {
			if !bytes.HasPrefix(user, p) {
				return ErrPrefix
			}
			order := 1
			if w.prefixed {
				order = bytes.Compare(p, w.prefixLast)
			}
			if order < 0 {
				return ErrPrefix
			}
			prefix, fresh = p, order > 0
		}
	}

	if w.entries == 0 {
		w.smallest, w.smallestSeq = append([]byte{}, key...), seq
	}
	if w.entries == 0 && w.fragCount == 0 {
		w.minSeq, w.maxSeq = seq, seq
	}
	if w.filterBits != 0 && distinct {
		// A filter partition ends at the previous key without its version,
		// which is distinct from key.
		if w.partitionSize != 0 && len(w.keys.hashes) == w.filterKeys {
			n := len(w.last) - seqLen
			w.err = w.finishFilterPartition(&w.keys, w.last[:max(n-w.suffix, 0)])
		}
		w.keys.hashes = append(w.keys.hashes, xxh3.Hash(user))
	}
	if fresh {
		// A partition of prefixes ends at the prefix before prefix.
		if w.partitionSize != 0 && len(w.prefixes.hashes) == w.filterKeys {
			w.err = cmp.Or(w.err, w.finishFilterPartition(&w.prefixes, w.prefixLast))
		}
		w.prefixes.hashes = append(w.prefixes.hashes, xxh3.Hash(prefix))
		w.prefixLast, w.prefixed = append(w.prefixLast[:0], prefix...), true
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

// AddRangeDelete adds the fragment of a range tombstone of the keys from
// start up to end, which the fragment excludes, at the sequence seq. The
// fragment must follow the fragment before it, as [RangeDeleter] states: the
// same start and the same end with a lower sequence, or a start at or after
// the end before it. A [Fragmenter] turns tombstones that overlap into such
// fragments. AddRangeDelete copies start and end, and keeps the fragments in
// the block of fragments, which Close writes. A table with a fragment has
// format version 3. The sequence counts toward the lowest and the highest
// sequence of the table.
//
// With the SuffixBytes of the options, start ends with the version of the
// fragment, and end has no version. The positions of one fragment have one
// start without its version and one end. They follow one another by version
// and then by sequence, from the highest to the lowest, as
// [CompareWithSuffix] orders the start and the sequence.
//
// With a PartitionSize, a fragment after a fragment partition of
// PartitionSize bytes or more ends that partition, so the positions of one
// fragment are in one partition.
//
// Error modes:
//
//   - A start without its version at or after its end returns [ErrRange],
//     and the Writer is unchanged.
//   - A fragment that could bring the block of fragments or a fragment
//     partition past 2^32 - 1 bytes returns [ErrTooLarge], and so does an
//     end that could bring the top-level index of the fragments past that
//     size. The Writer is then unchanged.
//   - A fragment that does not follow the fragment before it returns
//     [ErrOrder], and the Writer is unchanged.
//   - The error of an earlier write of the Writer, and [ErrClosed] after
//     Close.
//
// # Allocation contract
//
// Zero-alloc when the buffers of the fragments and of the fragment
// partitions have room. The first AddRangeDelete also allocates a copy of
// its start for the summary.
func (w *Writer) AddRangeDelete(_ context.Context, start, end []byte, seq uint64) error {
	if w.err != nil {
		return w.err
	}
	user := start[:max(len(start)-w.suffix, 0)]
	if bytes.Compare(user, end) >= 0 {
		return ErrRange
	}
	size := uint64(len(w.frags)) + uint64(len(start)) + uint64(len(end))
	//dokimi:mutate-skip lcr-left,lcr-right,ror-boundary,ror-false,sbr-delete: a fragment at either bound has 4 GiB, and AddRangeDelete keeps three copies of a start that passes the check, which no test allocates
	if size > math.MaxUint32-maxIndexOverhead || w.fragTop+uint64(len(end)) > math.MaxUint32-seqLen-maxTopOverhead {
		return ErrTooLarge
	}
	same := false
	if w.fragCount != 0 {
		n := len(w.fragLast) - seqLen
		last := w.fragLast[:n]
		same = bytes.Equal(user, last[:max(n-w.suffix, 0)]) && bytes.Equal(end, w.fragEnd)
		if same && CompareWithSuffix(start, seq, last, binary.BigEndian.Uint64(w.fragLast[n:]), w.suffix) <= 0 {
			return ErrOrder
		}
		if !same && bytes.Compare(user, w.fragEnd) < 0 {
			return ErrOrder
		}
	} else {
		w.fragStart = append([]byte{}, start...)
	}
	if w.partitionSize != 0 && !same && len(w.frags) >= w.partitionSize {
		w.finishFragmentPartition()
	}
	if w.entries == 0 && w.fragCount == 0 {
		w.minSeq, w.maxSeq = seq, seq
	}
	w.minSeq, w.maxSeq = min(w.minSeq, seq), max(w.maxSeq, seq)

	w.fragLast = binary.BigEndian.AppendUint64(append(w.fragLast[:0], start...), seq)
	//nolint:gosec // G115: AddRangeDelete keeps the block of fragments below 2^32 - 1 bytes
	w.fragRestarts = append(w.fragRestarts, uint32(len(w.frags)))
	w.frags = appendEntry(w.frags, w.fragLast, end, 0)
	w.fragEnd = append(w.fragEnd[:0], end...)
	w.fragCount++

	return nil
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
// With a PartitionSize, Close cuts the index and the filter into partitions,
// in format version 2. The data blocks are followed by the filter
// partitions, the index partitions, the top-level index of the filter and
// the top-level index of the index, each with a trailer.
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
// A table with a Prefix or with a fragment of [Writer.AddRangeDelete] has
// format version 3. It has the index and the filter of version 1, or of
// version 2 with a PartitionSize, and the parts of version 3 among them.
//
//   - Without partitions, the data blocks are followed by the block of
//     fragments, the filter block, the prefix filter block, the index block
//     and the block of places.
//   - With partitions, the data blocks are followed by the filter
//     partitions, the prefix filter partitions, the index partitions, the
//     block of fragments, the top-level index of the filter, the top-level
//     index of the prefix filter, the top-level index of the index and the
//     block of places.
//   - The block of fragments has the layout of the index block. Its entry
//     for a fragment and one of its sequences has the internal key of the
//     start of the fragment and the sequence, and the end as its value.
//   - The filter of prefixes has the layout of the filter of the keys over
//     the distinct prefixes. Its top-level index has the last prefix of each
//     partition with the sequence 0.
//   - The block of places has the flags as a varint, with bit 0 set for a
//     table in partitions. Then the places of the filter, the block of
//     fragments and the filter of prefixes follow, each as the offset and
//     the length as varints and the CRC32C as a uint32, or three zeros. Then
//     the length of the name of the Prefix as a varint and the name follow.
//   - The footer has the layout of version 1 with the version 3. Its index
//     fields state the index block or the top-level index of the index, and
//     its filter fields the block of places.
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
// partitions, and the largest key and the end of the last fragment of the
// summary.
func (w *Writer) Close(ctx context.Context) (Summary, error) {
	if w.err != nil {
		return Summary{}, w.err
	}
	if w.blockEntries != 0 {
		w.finishBlock()
	}

	//nolint:gosec // G115: the size is not negative
	size := uint64(w.size)
	var index blockPlace
	p := places{partitioned: w.partitionSize != 0}
	var err error
	if w.partitionSize == 0 {
		p.fragments = w.appendFragments(size)
		var prefixErr error
		if w.filterBits != 0 {
			p.filter, err = w.appendFilter(size, w.keys.hashes)
		}
		if w.prefix != nil {
			p.prefix, prefixErr = w.appendFilter(size, w.prefixes.hashes)
		}
		//dokimi:mutate-skip sbr-delete: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
		err = cmp.Or(err, prefixErr)
		start := len(w.out)
		w.out = appendIndex(w.out, w.index, w.indexRestarts)
		index = w.sealPlace(size, start)
	} else {
		index, err = w.appendPartitions(size, &p)
	}
	version, filter := uint32(formatVersion), p.filter
	if w.partitionSize != 0 {
		version = partitionedVersion
	}
	if w.prefix != nil || w.fragCount != 0 || w.suffix != 0 {
		if w.prefix != nil {
			p.name = w.prefix.Name()
		}
		//nolint:gosec // G115: NewWriter refuses a negative SuffixBytes
		p.suffix = uint64(w.suffix)
		start := len(w.out)
		w.out = p.append(w.out)
		version, filter = placedVersion, w.sealPlace(size, start)
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
		RangeDeletes: w.fragCount, RangeDeleteStart: w.fragStart,
	}
	if w.entries != 0 {
		n := len(w.last) - seqLen
		sum.LargestKey, sum.LargestSeq = append([]byte{}, w.last[:n]...), binary.BigEndian.Uint64(w.last[n:])
	}
	if w.fragCount != 0 {
		sum.RangeDeleteEnd = append([]byte{}, w.fragEnd...)
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
// buffers after a table writes a table of no more entries and fragments and
// no longer keys, values and fragments without growing them. The [Summary]
// that Close returned before remains valid, because its keys are copies.
//
// # Allocation contract
//
// Zero-alloc. A table that a Writer writes after Reset allocates the copies
// of its smallest key, its largest key and the bounds of its fragments, and
// the growth of a buffer that the table needs.
func (w *Writer) Reset(dst Sink) {
	w.retain()
	*w = Writer{
		w:             dst,
		prefix:        w.prefix,
		builder:       w.builder,
		out:           w.out[:0],
		restarts:      w.restarts[:0],
		last:          w.last[:0],
		cur:           w.cur[:0],
		scratch:       w.scratch[:0],
		index:         w.index[:0],
		indexRestarts: w.indexRestarts[:0],
		keys:          filterBuild{hashes: w.keys.hashes[:0], parts: w.keys.parts[:0], meta: w.keys.meta[:0]},
		prefixes: filterBuild{
			hashes: w.prefixes.hashes[:0], parts: w.prefixes.parts[:0], meta: w.prefixes.meta[:0],
		},
		indexParts:      w.indexParts[:0],
		indexMeta:       w.indexMeta[:0],
		prefixLast:      w.prefixLast[:0],
		frags:           w.frags[:0],
		fragRestarts:    w.fragRestarts[:0],
		fragLast:        w.fragLast[:0],
		fragEnd:         w.fragEnd[:0],
		fragParts:       w.fragParts[:0],
		fragMeta:        w.fragMeta[:0],
		blockSize:       w.blockSize,
		restartInterval: w.restartInterval,
		filterBits:      w.filterBits,
		maxRetained:     w.maxRetained,
		partitionSize:   w.partitionSize,
		suffix:          w.suffix,
		filterKeys:      w.filterKeys,
		compression:     w.compression,
	}
}

// Footprint returns the bytes of memory that the buffers of w take: the
// capacity of the buffers of the current block, of its keys, of the
// compressed block, of the index, of the hashes and the partitions of the
// filters, of the last prefix and of the fragments and their partitions, and
// the scratch of the filter builder that [filter.FuseBuilder.Footprint]
// returns. It does not count the smallest key and the start of the first
// fragment, because Close returns them in the [Summary]. A new Writer and a
// Writer that released its buffers return 0.
//
// # Allocation contract
//
// Zero-alloc.
func (w *Writer) Footprint() int {
	n := cap(w.out) + cap(w.last) + cap(w.cur) + cap(w.scratch) + cap(w.index) + cap(w.prefixLast)
	n += cap(w.keys.parts) + cap(w.keys.meta) + cap(w.prefixes.parts) + cap(w.prefixes.meta)
	n += cap(w.indexParts) + cap(w.indexMeta) + cap(w.frags) + cap(w.fragLast) + cap(w.fragEnd)
	n += cap(w.fragParts) + cap(w.fragMeta)
	n += 4 * (cap(w.restarts) + cap(w.indexRestarts) + cap(w.fragRestarts))

	return n + 8*(cap(w.keys.hashes)+cap(w.prefixes.hashes)) + w.builder.Footprint()
}

// retain releases the buffers of w and the scratch of its filter builder
// when Footprint exceeds maxRetained, and keeps them for a maxRetained of 0.
// The next table grows the buffers again.
func (w *Writer) retain() {
	if w.maxRetained == 0 || w.Footprint() <= w.maxRetained {
		return
	}
	w.out, w.restarts, w.last, w.cur, w.scratch = nil, nil, nil, nil, nil
	w.index, w.indexRestarts, w.indexParts, w.indexMeta = nil, nil, nil, nil
	w.keys, w.prefixes, w.prefixLast = filterBuild{}, filterBuild{}, nil
	w.frags, w.fragRestarts, w.fragLast, w.fragEnd = nil, nil, nil, nil
	w.fragParts, w.fragMeta = nil, nil
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

// finishFragmentPartition ends the current fragment partition. It appends
// the partition with its trailer to fragParts, and its record to fragMeta.
// The key of the record is the end of the last fragment of the partition
// with the sequence 0. The count of the record is the number of the entries
// of the fragments up to the end of the partition. The fragments start the
// next partition empty.
func (w *Writer) finishFragmentPartition() {
	start := len(w.fragParts)
	w.fragParts = appendIndex(w.fragParts, w.frags, w.fragRestarts)
	n, sum := w.seal(&w.fragParts, start, NoCompression)
	w.cur = binary.BigEndian.AppendUint64(append(w.cur[:0], w.fragEnd...), 0)
	//nolint:gosec // G115: the count of the fragments is not negative
	w.fragMeta = binary.AppendUvarint(appendRecord(w.fragMeta, w.cur, n, sum), uint64(w.fragCount))
	w.fragTop += uint64(len(w.fragEnd)) + seqLen + maxTopOverhead
	w.frags, w.fragRestarts = w.frags[:0], w.fragRestarts[:0]
}

// finishFilterPartition ends the current partition of the filter f, whose
// last key or prefix is last. It appends the encoding of a filter of the
// hashes of the partition with its trailer to the partitions of f, and its
// record to the records of f, and empties the hashes. It returns the error of
// a builder that refuses the hashes, and then appends nothing.
func (w *Writer) finishFilterPartition(f *filterBuild, last []byte) error {
	start := len(f.parts)
	parts, err := w.builder.Append(f.parts, f.hashes, w.filterBits)
	f.parts = parts
	//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
	if err == nil {
		n, sum := w.seal(&f.parts, start, NoCompression)
		f.meta = appendRecord(f.meta, last, n, sum)
		//dokimi:mutate-skip sbr-delete: the bound of a top-level index of a filter refuses only a key next to a top-level index near 4 GiB, which no test writes
		f.top += uint64(len(last)) + maxFilterTopOverhead
	}
	f.hashes = f.hashes[:0]

	return err
}

// appendPartitions ends the last partitions at Close of a table in
// partitions. It appends the filter partitions, the partitions of the filter
// of prefixes, the index partitions, the fragment partitions and the
// top-level indexes of the fragments, of the filter, of the filter of
// prefixes and of the index to out, where size bytes of the table precede
// out. It sets the places of the filter, of the top-level index of the
// fragments and of the filter of prefixes in p, and the flag of the fragment
// partitions of a table with fragments. It returns the place of the
// top-level index of the index, and the first error of a filter partition
// that the builder refuses.
func (w *Writer) appendPartitions(size uint64, p *places) (blockPlace, error) {
	if len(w.indexRestarts) != 0 {
		w.finishIndexPartition()
	}
	if len(w.fragRestarts) != 0 {
		w.finishFragmentPartition()
	}
	var err, prefixErr error
	if len(w.keys.hashes) != 0 {
		n := len(w.last) - seqLen
		err = w.finishFilterPartition(&w.keys, w.last[:max(n-w.suffix, 0)])
	}
	if len(w.prefixes.hashes) != 0 {
		prefixErr = w.finishFilterPartition(&w.prefixes, w.prefixLast)
	}

	// The partitions follow the last data block.
	off := size + uint64(len(w.out))
	w.out = append(w.out, w.keys.parts...)
	w.out = append(w.out, w.prefixes.parts...)
	w.out = append(w.out, w.indexParts...)
	fragOff := size + uint64(len(w.out))
	w.out = append(w.out, w.fragParts...)
	if w.fragCount != 0 {
		start := len(w.out)
		w.out = w.appendTop(w.out, w.fragMeta, fragOff, true)
		p.fragments, p.fragmentParts = w.sealPlace(size, start), true
	}
	if w.filterBits != 0 {
		start := len(w.out)
		w.out = w.appendTop(w.out, w.keys.meta, off, false)
		p.filter = w.sealPlace(size, start)
	}
	off += uint64(len(w.keys.parts))
	if w.prefix != nil {
		start := len(w.out)
		w.out = w.appendTop(w.out, w.prefixes.meta, off, false)
		p.prefix = w.sealPlace(size, start)
	}
	start := len(w.out)
	w.out = w.appendTop(w.out, w.indexMeta, off+uint64(len(w.prefixes.parts)), true)

	return w.sealPlace(size, start), cmp.Or(err, prefixErr)
}

// appendFilter appends the filter block of hashes with its trailer to out,
// where size bytes of the table precede out, and returns its place. It
// returns the error of a builder that refuses the hashes, and then appends
// nothing.
func (w *Writer) appendFilter(size uint64, hashes []uint64) (place blockPlace, err error) {
	start := len(w.out)
	//dokimi:mutate-skip ror-true: the builder refuses only more hashes than fit 2^32 fingerprints and a set that no seed peels, which no test writes
	if w.out, err = w.builder.Append(w.out, hashes, w.filterBits); err == nil {
		place = w.sealPlace(size, start)
	}

	return place, err
}

// appendFragments appends the block of fragments with its trailer to out,
// where size bytes of the table precede out, and returns its place. The
// block has the layout of an index block. A table without fragments has no
// block of fragments, and appendFragments returns the zero place.
func (w *Writer) appendFragments(size uint64) blockPlace {
	if w.fragCount == 0 {
		return blockPlace{}
	}
	start := len(w.out)
	w.out = appendIndex(w.out, w.frags, w.fragRestarts)

	return w.sealPlace(size, start)
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
// a filter partition of size bytes, for fingerprints of the width bits: the
// most keys n for which the encoding of every count up to n takes at most
// size bytes. The last partition of a table can have fewer keys than n, and
// it fits size bytes too. One key that takes more than size bytes alone
// makes the count 1. [filter.MaxFuseSize] never falls as its count grows, so
// partitionKeys searches it in halves. A filter has at least one fingerprint
// for each key, so size*8/bits + 1 keys do not fit.
func partitionKeys(size, bits int) int {
	lo, hi := 1, int(min(int64(size)*8/int64(bits)+1, math.MaxInt32))
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		//dokimi:mutate-skip lcr-right,ror-false: the search stays below 2^31 keys, and MaxFuseSize refuses only more keys than fit 2^32 fingerprints
		if n, err := filter.MaxFuseSize(mid, bits); err != nil ||
			n > size {

			hi = mid
		} else {
			lo = mid
		}
	}

	return lo
}
