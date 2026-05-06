// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool

import "bytes"

// NewBufferPool returns a [ResetPool] of [bytes.Buffer]
// pointers — the canonical [Resettable] pooled value type.
//
// Equivalent to:
//
//	pool.NewResetPool(func() *bytes.Buffer { return new(bytes.Buffer) })
//
// Construct one [BufferPool] per "shape" of allocation:
// separate pools for small (~256 B) and large (~64 KiB)
// buffers, etc., to avoid retaining oversized buffers in the
// small-allocation pool.
func NewBufferPool() *ResetPool[*bytes.Buffer] {
	return NewResetPool(func() *bytes.Buffer { return new(bytes.Buffer) })
}
