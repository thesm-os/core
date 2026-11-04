// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"go.dokimi.dev/assert"
)

// The tables of the internal specs: refEntries entries in blocks of refBlock
// bytes, with an index partition for each data block and a fragment
// partition for each fragment of refDeletes.
const (
	refEntries = 200
	refBlock   = 64
)

// refDeletes are the fragments of the tables of the internal specs, as the
// start, the end and the sequence of each.
var refDeletes = []struct {
	start, end string
	seq        uint64
}{
	{start: "key 010", end: "key 015", seq: 9},
	{start: "key 015", end: "key 020", seq: 9},
	{start: "key 015", end: "key 020", seq: 7},
	{start: "key 020", end: "key 030", seq: 7},
	{start: "key 040", end: "key 041", seq: 3},
}

// memFile is a File over the bytes of a table.
type memFile []byte

var _ File = memFile(nil)

// ReadAt reads the bytes of f at off.
func (f memFile) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	return bytes.NewReader(f).ReadAt(p, off)
}

// memSink is a Sink that appends the bytes of a table to its buffer.
type memSink struct {
	bytes.Buffer
}

var _ Sink = (*memSink)(nil)

// Write appends p to the buffer.
func (s *memSink) Write(_ context.Context, p []byte) (int, error) {
	return s.Buffer.Write(p)
}

// refKey is the identity of a table and the offset of a block in it, the key
// of a block of a refCache.
type refKey struct {
	id, offset uint64
}

// refCache is a LendingCache over a map that keeps every block that a Reader
// adds, and evicts none. It keeps a reference of its own to each block, and
// lends a block with a reference for the caller. A block of the cache then
// has one reference more than the cursors that keep it. When two cursors add
// a block at one place, the cache keeps the first, and the second cursor
// keeps the only reference to its own block.
//
// # Concurrency
//
// Safe for concurrent use. A mutex guards the map.
type refCache struct {
	blocks map[refKey]*Block
	mu     sync.Mutex
}

var _ LendingCache = (*refCache)(nil)

// Get reports false. A Reader with a LendingCache calls Lend in its place.
func (*refCache) Get(context.Context, uint64, uint64) ([]byte, bool) {
	return nil, false
}

// Add drops the block. A Reader with a LendingCache calls AddBlock in its
// place.
func (*refCache) Add(context.Context, uint64, uint64, []byte) {}

// Lend returns the block of the table id at offset with a reference for the
// caller, and reports whether the cache contains it.
func (c *refCache) Lend(_ context.Context, id, offset uint64, _ BlockRole) (*Block, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, ok := c.blocks[refKey{id: id, offset: offset}]
	if ok {
		b.Retain()
	}

	return b, ok
}

// AddBlock keeps b as the block of the table id at offset with a reference
// of the cache, unless the cache contains a block at that place.
func (c *refCache) AddBlock(_ context.Context, id, offset uint64, _ BlockRole, b *Block) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := refKey{id: id, offset: offset}
	if _, ok := c.blocks[key]; ok {
		return
	}
	if c.blocks == nil {
		c.blocks = map[refKey]*Block{}
	}
	b.Retain()
	c.blocks[key] = b
}

// refs returns the count of references of each block of the cache.
func (c *refCache) refs() map[refKey]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	refs := make(map[refKey]int32, len(c.blocks))
	for key, b := range c.blocks {
		refs[key] = b.refs.Load()
	}

	return refs
}

// want returns the count of references that each block of the cache has
// while the cursors keep the blocks kept: the reference of the cache, and one
// for each time that kept lists the block. kept can list a nil block and a
// block that the cache does not contain, and want skips both.
func (c *refCache) want(kept ...*Block) map[refKey]int32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	refs := make(map[refKey]int32, len(c.blocks))
	for key, b := range c.blocks {
		refs[key] = 1
		for _, k := range kept {
			if k == b {
				refs[key]++
			}
		}
	}

	return refs
}

// refTable writes the entries "key 000" to "key 199" at the sequence 1 and
// the fragments of refDeletes into a table in partitions, and opens the table
// through cache under the identity id. Every data block, index partition and
// fragment partition that a cursor reads then goes through the cache.
func refTable(tb assert.TB, cache *refCache, id uint64) *Reader {
	tb.Helper()

	ctx := assert.Context(tb)
	var sink memSink
	w, err := NewWriter(&sink, WriterOptions{BlockSize: refBlock, PartitionSize: 1})
	assert.NoError(tb, err, "NewWriter accepts the options")
	for i := range refEntries {
		assert.NoError(tb, w.Add(ctx, fmt.Appendf(nil, "key %03d", i), 1, []byte("value")),
			"Add adds each entry in order")
	}
	for _, d := range refDeletes {
		assert.NoError(tb, w.AddRangeDelete(ctx, []byte(d.start), []byte(d.end), d.seq),
			"AddRangeDelete adds each fragment in order")
	}
	_, err = w.Close(ctx)
	assert.NoError(tb, err, "Close writes the table")
	r, err := Open(ctx, memFile(sink.Bytes()), int64(sink.Len()), ReaderOptions{Cache: cache, CacheID: id})
	assert.NoError(tb, err, "Open reads the table")

	return r
}
