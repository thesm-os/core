// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"slices"
	"strings"
	"sync"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// The layout of the tables of most cases: small blocks with a restart point
// every 4 entries, so a table of a few hundred entries has many blocks and
// many restart points.
const (
	smallBlocks   = 256
	everyFourth   = 4
	goldenEntries = 120
)

// The layout of a table, which the cases that change a table and seal it
// again rely on.
const (
	// seqLen is the size of the sequence at the end of an internal key.
	seqLen = 8
	// trailerLen is the size of the trailer of a block: its compression byte
	// and its checksum.
	trailerLen = 5
	// footerLen is the size of the footer.
	footerLen = 72
	// footerChecked is the number of bytes of the footer that its checksum
	// covers. The checksum follows them.
	footerChecked = 60
	// The offsets of the fields of the footer.
	footerIndexOff  = 0
	footerIndexLen  = 8
	footerFilterOff = 16
	footerFilterLen = 24
	footerVersion   = 56
)

// errRead is the error of a reader that fails.
var errRead = errors.New("read failed")

// castagnoli is the table of CRC32C, the checksum of the blocks and the
// footer of a table.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// settings are the options of the cases that run for every setting of
// compression and filter.
var settings = []struct {
	name string
	opts sstable.WriterOptions
}{
	{
		name: "uncompressed without a filter",
		opts: sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: everyFourth},
	},
	{
		name: "uncompressed with a filter",
		opts: sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: everyFourth, FilterBits: 8},
	},
	{
		name: "compressed without a filter",
		opts: sstable.WriterOptions{
			BlockSize: smallBlocks, RestartInterval: everyFourth, Compression: sstable.SnappyCompression,
		},
	},
	{
		name: "compressed with a filter",
		opts: sstable.WriterOptions{
			BlockSize: smallBlocks, RestartInterval: everyFourth, Compression: sstable.SnappyCompression,
			FilterBits: 8,
		},
	},
}

// keys are the keys that the generators draw most often. They share
// prefixes, and the empty key comes first.
var keys = [][]byte{
	{},
	[]byte("a"), []byte("aa"), []byte("aab"), []byte("ab"), []byte("b"), []byte("ba"), []byte("bab"),
	[]byte("c"), []byte("cab"), []byte("cb"), []byte("d"),
}

// goldenKeys are the keys of the golden tables, in their order.
var goldenKeys = func() [][]byte {
	ks := make([][]byte, 0, goldenEntries/2)
	for i := range goldenEntries / 2 {
		ks = append(ks, fmt.Appendf(nil, "key %03d", i))
	}

	return ks
}()

// entries generates runs of up to 300 entries in the order of Compare,
// without two entries of one key and one sequence. Most keys come from keys,
// so they repeat with other sequences and share prefixes. The others are
// random bytes.
var entries = prop.Composite(func(c *prop.Case) []entry {
	key := prop.OneOf(prop.SampledFrom(keys...), prop.Bytes(prop.MaxSize(20)))
	n := c.Draw(prop.Integer(0, 300), "entries")
	es := make([]entry, 0, n)
	for range n {
		es = append(es, entry{
			key:   c.Draw(key, "key"),
			seq:   c.Draw(prop.OneOf(prop.Integer[uint64](0, 5), prop.Integer[uint64](0, 1<<63)), "seq"),
			value: c.Draw(prop.Bytes(prop.MaxSize(40)), "value"),
		})
	}
	slices.SortFunc(es, func(a, b entry) int { return sstable.Compare(a.key, a.seq, b.key, b.seq) })

	return slices.CompactFunc(es, func(a, b entry) bool { return bytes.Equal(a.key, b.key) && a.seq == b.seq })
})

// entry is an entry of a table: a key, a sequence and a value.
type entry struct {
	key   []byte
	seq   uint64
	value []byte
}

// mapCache is a BlockCache over a map. It counts the blocks that Add
// received.
type mapCache struct {
	mu     sync.Mutex
	blocks map[[2]uint64][]byte
	adds   int
}

// Get returns the block of the table id at offset.
func (c *mapCache) Get(id, offset uint64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, ok := c.blocks[[2]uint64{id, offset}]

	return b, ok
}

// Add keeps the block of the table id at offset.
func (c *mapCache) Add(id, offset uint64, block []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.blocks == nil {
		c.blocks = map[[2]uint64][]byte{}
	}
	c.blocks[[2]uint64{id, offset}] = block
	c.adds++
}

// place is the offset and the stored length of a block of a table, without
// its trailer.
type place struct {
	off, n int
}

// layout is a table with the places of its blocks. A case changes the bytes
// of the table and seals it again, so that its checksums pass and a read
// runs the checks of the layout.
type layout struct {
	data   []byte
	blocks []place // the data blocks, in their order
	filter place   // the filter block, or the zero place
	index  place   // the index block
}

// newLayout writes a table of the entries under opts, and reads the places
// of its blocks from its footer and its index. Every entry of the index is a
// restart point, and its value is the offset and the length of its block as
// two varints.
func newLayout(tb assert.TB, opts sstable.WriterOptions, es []entry) *layout {
	tb.Helper()

	data := writeTable(tb, opts, es)
	f := data[len(data)-footerLen:]
	be := binary.BigEndian
	l := &layout{
		data:   data,
		filter: place{off: int(be.Uint64(f[footerFilterOff:])), n: int(be.Uint64(f[footerFilterLen:]))},
		index:  place{off: int(be.Uint64(f[footerIndexOff:])), n: int(be.Uint64(f[footerIndexLen:]))},
	}
	index := data[l.index.off : l.index.off+l.index.n]
	for _, off := range restarts(index) {
		_, value := restartEntry(index[off:])
		blockOff, n := binary.Uvarint(value)
		blockLen, _ := binary.Uvarint(value[n:])
		l.blocks = append(l.blocks, place{off: int(blockOff), n: int(blockLen)})
	}

	return l
}

// seal writes the CRC32C of each block into its trailer and the CRC32C of
// the footer into the footer, at the places that newLayout read before the
// case changed the table.
func (l *layout) seal() {
	for _, b := range append([]place{l.filter, l.index}, l.blocks...) {
		if b.n != 0 {
			binary.BigEndian.PutUint32(l.data[b.off+b.n+1:], crc32.Checksum(l.data[b.off:b.off+b.n+1], castagnoli))
		}
	}
	f := l.data[len(l.data)-footerLen:]
	binary.BigEndian.PutUint32(f[footerChecked:], crc32.Checksum(f[:footerChecked], castagnoli))
}

// countingReader is an io.ReaderAt over data that counts its reads.
type countingReader struct {
	data  []byte
	reads int
}

var _ io.ReaderAt = (*countingReader)(nil)

// ReadAt reads data at off and counts the read.
func (r *countingReader) ReadAt(p []byte, off int64) (int, error) {
	r.reads++

	return bytes.NewReader(r.data).ReadAt(p, off)
}

// failingReader is an io.ReaderAt over data that fails every read after
// its first ok reads with errRead.
type failingReader struct {
	data  []byte
	ok    int
	reads int
}

var _ io.ReaderAt = (*failingReader)(nil)

// ReadAt reads data at off, or fails once the reader has made ok reads.
func (r *failingReader) ReadAt(p []byte, off int64) (int, error) {
	r.reads++
	if r.reads > r.ok {
		return 0, errRead
	}

	return bytes.NewReader(r.data).ReadAt(p, off)
}

// goldenTable returns the entries of the golden tables: goldenEntries
// entries of two versions per key, with values that repeat their text.
func goldenTable() []entry {
	es := make([]entry, 0, goldenEntries)
	for i := range goldenEntries {
		key := fmt.Sprintf("key %03d", i/2)
		seq := uint64(2 - i%2)
		es = append(es, entry{
			key:   []byte(key),
			seq:   seq,
			value: []byte(strings.Repeat(fmt.Sprintf("value of %s at %d. ", key, seq), 1+i%3)),
		})
	}

	return es
}

// writeTable writes the entries into a table under opts and returns the
// bytes of the table.
func writeTable(tb assert.TB, opts sstable.WriterOptions, es []entry) []byte {
	tb.Helper()

	var buf bytes.Buffer
	w, err := sstable.NewWriter(&buf, opts)
	assert.NoError(tb, err, "NewWriter accepts the options")
	for _, e := range es {
		assert.NoError(tb, w.Add(e.key, e.seq, e.value), "Add adds each entry in order")
	}
	_, err = w.Close()
	assert.NoError(tb, err, "Close writes the table")

	return buf.Bytes()
}

// openTable opens the table of data.
func openTable(tb assert.TB, data []byte, opts sstable.ReaderOptions) *sstable.Reader {
	tb.Helper()

	r, err := sstable.Open(bytes.NewReader(data), int64(len(data)), opts)
	assert.NoError(tb, err, "Open reads the table")

	return r
}

// scanAll returns a copy of every entry that s yields from its first entry,
// and the error of s.
func scanAll(s sstable.Source) ([]entry, error) {
	var got []entry
	for ok := s.First(); ok; ok = s.Next() {
		got = append(got, entry{key: bytes.Clone(s.Key()), seq: s.Seq(), value: bytes.Clone(s.Value())})
	}

	return got, s.Err()
}

// restarts returns the offsets of the restart points of a block without
// compression, from the uint32 offsets and their count at its end.
func restarts(block []byte) []int {
	count := int(binary.BigEndian.Uint32(block[len(block)-4:]))
	offsets := make([]int, 0, count)
	for i := range count {
		offsets = append(offsets, int(binary.BigEndian.Uint32(block[len(block)-4-4*count+4*i:])))
	}

	return offsets
}

// restartEntry returns the internal key and the value of the entry at the
// start of p, an entry at a restart point: three varints, then its internal
// key and its value. Both are parts of p.
func restartEntry(p []byte) (ikey, value []byte) {
	var header [3]uint64
	for i := range header {
		v, n := binary.Uvarint(p)
		header[i], p = v, p[n:]
	}

	return p[:header[1]], p[header[1] : header[1]+header[2]]
}

// readAll reads every block of a table through a cursor and every key
// through Get, and returns the first error.
func readAll(r *sstable.Reader, es []entry) error {
	it := r.NewIter()
	defer it.Close()
	if _, err := scanAll(it); err != nil {
		return err
	}
	for _, e := range es {
		if _, _, _, err := r.Get(e.key, e.seq, nil); err != nil {
			return err
		}
	}

	return nil
}
