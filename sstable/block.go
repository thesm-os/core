// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"math/bits"
	"slices"
	"sync/atomic"

	"go.thesmos.sh/core/pool"
)

// The size classes of the buffers of a [Block]. A buffer has the capacity
// of its class, and a block takes the smallest class of at least its size.
const (
	// minClassShift is the log2 of the capacity of the smallest class. A
	// block of at most 1 KiB takes a buffer of that class.
	minClassShift = 10

	// maxClassShift is the log2 of the capacity of the largest class. A
	// block of more bytes takes a buffer of its own size, outside the
	// pools.
	maxClassShift = 24

	// classSteps is the number of classes from one power of two to the
	// next, so a buffer exceeds its block by at most an eighth of its
	// capacity.
	classSteps = 8

	// blockClasses is the number of classes.
	blockClasses = 1 + (maxClassShift-minClassShift)*classSteps

	// unpooled is the class of a buffer outside the pools.
	unpooled = -1
)

// blocks are the pools of the buffers of each class. The pool of a class
// allocates a buffer of the capacity of the class when it is empty.
var blocks = func() (pools [blockClasses]*pool.Pool[*Block]) {
	for c := range pools {
		size := classSize(c)
		pools[c] = pool.NewPool(func() *Block { return &Block{buf: make([]byte, 0, size), class: int16(c)} })
	}

	return pools
}()

// Block is the bytes of a decoded data block in a buffer from a pool, which
// a [LendingCache] shares with the readers of its table. A Block counts its
// references. The cache keeps one while the block is in the cache, and each
// cursor or [Reader.Get] keeps one while it reads the block. The last
// [Block.Release] returns the buffer to its pool, which lends it again for
// another block.
//
// The bytes of a Block do not change while a reference remains, so a holder
// reads them without a lock. The holder of a new Block writes its bytes
// before it shares the Block.
//
// # Concurrency
//
// Bytes, Footprint, Retain and Release are safe for concurrent use. A second
// Release of one reference is a programmer error, because the buffer then
// returns to its pool while another holder reads it.
//
// # Allocation contract
//
// [NewBlock] takes a buffer from the pool of its class, and allocates one
// when the pool is empty, such as after a garbage collection. Bytes,
// Footprint, Retain and Release do not allocate.
type Block struct {
	// buf contains the bytes of the block, in the capacity of its class.
	buf []byte

	// refs counts the references of the block.
	refs atomic.Int32

	// class is the size class of buf, or unpooled.
	class int16
}

// NewBlock returns a Block of n bytes with one reference for the caller.
// The buffer of a block of at most 1 KiB has the 1 KiB of the smallest
// class. The buffer of a larger block has the capacity of the smallest class
// of at least n bytes, which exceeds n by at most an eighth of that
// capacity. A block of more than 16 MiB takes a buffer of exactly n bytes
// outside the pools. A negative n counts as 0.
//
// The bytes of the Block are the bytes that an earlier block left in the
// buffer, so the caller writes all n of them before it shares the Block.
//
// # Allocation contract
//
// Zero alloc when the pool of the class has a buffer. Otherwise NewBlock
// allocates the Block and its buffer.
func NewBlock(n int) *Block {
	n = max(n, 0)
	var b *Block
	if c, ok := classOf(n); ok {
		b = blocks[c].Get()
		b.buf = b.buf[:n]
	} else {
		b = &Block{buf: make([]byte, n), class: unpooled}
	}
	b.refs.Store(1)

	return b
}

// Bytes returns the bytes of the block, with a capacity of their length.
// They do not change until the last Release.
//
// # Allocation contract
//
// Zero alloc.
func (b *Block) Bytes() []byte {
	return slices.Clip(b.buf)
}

// Footprint returns the bytes of memory that the buffer of the block takes:
// the capacity of its class, or the bytes of a block larger than the
// largest class. A [LendingCache] costs a block by its footprint.
//
// # Allocation contract
//
// Zero alloc.
func (b *Block) Footprint() int {
	return cap(b.buf)
}

// Retain adds a reference, which the caller ends with a Release.
//
// # Allocation contract
//
// Zero alloc.
func (b *Block) Retain() {
	b.refs.Add(1)
}

// Release ends a reference. The last Release returns the buffer to the pool
// of its class, which can lend it for another block at once, so a holder
// does not read the bytes after its Release. The buffer of a block larger
// than the largest class goes to the garbage collector.
//
// # Allocation contract
//
// Zero alloc.
func (b *Block) Release() {
	if b.refs.Add(-1) == 0 && b.class != unpooled {
		blocks[b.class].Put(b)
	}
}

// classOf returns the size class of a block of n bytes, and reports false
// for a block larger than the largest class. n is not negative.
func classOf(n int) (int, bool) {
	//dokimi:mutate-skip ror-boundary: the computation of the steps also gives class 0 for a block of 1 KiB
	if n <= 1<<minClassShift {
		return 0, true
	}

	// 1<<k <= n < 1<<(k+1), and the classes split that span in classSteps
	// steps.
	k := bits.Len(uint(n)) - 1
	step := 1 << k / classSteps
	c := (k-minClassShift)*classSteps + (n-1<<k+step-1)/step

	return c, c < blockClasses
}

// classSize returns the capacity of the buffers of the size class c. The
// capacity is the power of two 1<<(c/classSteps+minClassShift) plus
// c%classSteps steps, and a step is an eighth of that power.
func classSize(c int) int {
	k, steps := c/classSteps+minClassShift, c%classSteps

	return 1<<k + steps*(1<<k/classSteps)
}
