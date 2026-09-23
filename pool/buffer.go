// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool

import "bytes"

// Buffer is a [bytes.Buffer] whose Reset also zeroes the bytes the
// buffer held. [bytes.Buffer.Reset] only truncates, and
// [bytes.Buffer.AvailableBuffer] hands the next user the previous
// user's bytes as spare capacity. A pooled Buffer passes nothing on.
type Buffer struct {
	bytes.Buffer
}

// Reset empties the buffer and zeroes its whole capacity.
//
// # Allocation contract
//
// Zero-alloc. Runs in time proportional to the capacity.
func (b *Buffer) Reset() {
	b.Buffer.Reset()
	avail := b.AvailableBuffer()
	clear(avail[:cap(avail)])
}

// NewBufferPool returns a [ResetPool] of [Buffer] pointers, which
// zero their bytes when put back.
//
// Equivalent to:
//
//	pool.NewResetPool(func() *pool.Buffer { return new(pool.Buffer) })
//
// Construct one pool per "shape" of allocation: separate pools for
// small (~256 B) and large (~64 KiB) buffers, etc., to avoid retaining
// oversized buffers in the small-allocation pool.
func NewBufferPool() *ResetPool[*Buffer] {
	return NewResetPool(func() *Buffer { return new(Buffer) })
}
