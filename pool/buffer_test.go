// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/pool"
)

func TestBuffer(t *testing.T) {
	t.Parallel()

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("empties the buffer", func(t *testing.T) {
			t.Parallel()
			var b pool.Buffer
			b.WriteString("tenant data")
			b.Reset()
			assert.Equal(t, b.Len(), 0, "Reset must empty the buffer")
		})

		t.Run("zeroes the bytes that the buffer held", func(t *testing.T) {
			t.Parallel()
			var b pool.Buffer
			b.WriteString("tenant data")
			b.Reset()
			avail := b.AvailableBuffer()
			assert.Equal(t, avail[:cap(avail)], make([]byte, cap(avail)),
				"no byte of the previous user may survive Reset")
		})
	})

	t.Run("NewBufferPool", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a pool whose Get returns an empty buffer", func(t *testing.T) {
			t.Parallel()
			b := pool.NewBufferPool().Get()
			assert.NotNil(t, b, "Get must return a buffer")
			assert.Equal(t, b.Len(), 0, "a new buffer must be empty")
		})

		t.Run("returns a pool whose Put empties the buffer", func(t *testing.T) {
			t.Parallel()
			p := pool.NewBufferPool()
			b := p.Get()
			b.WriteString("tenant data")
			p.Put(b)
			assert.Equal(t, b.Len(), 0, "Put must Reset the buffer")
		})
	})
}

// TestBufferAllocs checks that a Get of a pooled Buffer, a short write and
// the Put that zeroes it do not allocate. MaxAllocs counts the allocations
// of the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestBufferAllocs(t *testing.T) {
	p := pool.NewBufferPool()
	warm := p.Get()
	warm.WriteString("hello, world")
	p.Put(warm)

	t.Run("NewBufferPool", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() {
			b := p.Get()
			b.WriteString("hello, world")
			n = b.Len()
			p.Put(b)
		}, 0, "a Get, a write that fits and the Put must not allocate")
		assert.Equal(t, n, 12, "the test must measure a write of the whole text")
	})
}

// BenchmarkBuffer reports the cost of a Get of a pooled Buffer, a short
// write and the Put that zeroes it, on one goroutine and on goroutines in
// parallel, and fails when the cycle allocates.
func BenchmarkBuffer(b *testing.B) {
	p := pool.NewBufferPool()
	p.Put(p.Get())

	b.Run("NewBufferPool", func(b *testing.B) {
		b.Run("of a Get, a write and its Put", func(b *testing.B) {
			var n int

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				buf := p.Get()
				buf.WriteString("hello, world")
				n = buf.Len()
				p.Put(buf)
			}

			assert.Equal(b, n, 12, "the benchmark must measure a write of the whole text")
		})

		b.Run("of goroutines in parallel", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				for pb.Next() {
					buf := p.Get()
					buf.WriteString("payload")
					p.Put(buf)
				}
			})
		})
	})
}
