// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/pool"
)

func TestNewBufferPool(t *testing.T) {
	t.Parallel()

	t.Run("Get returns a fresh empty buffer", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		b := p.Get()
		testkit.True(t, b != nil, "Get must not return nil")
		testkit.Equal(t, b.Len(), 0, "fresh buffer Len must be 0")
	})

	t.Run("Put auto-Resets buffer", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		b := p.Get()
		b.WriteString("tenant data")
		testkit.True(t, b.Len() > 0, "Write must extend buffer")
		p.Put(b)
		testkit.Equal(t, b.Len(), 0, "Put must auto-Reset the buffer")
	})

	t.Run("recycled buffer is empty on next Get", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		first := p.Get()
		first.WriteString("contaminated")
		p.Put(first)

		next := p.Get()
		// sync.Pool may evict; guard the same-pointer case.
		if next == first {
			testkit.Equal(t, next.Len(), 0,
				"recycled buffer must be empty on next Get")
		}
	})

	t.Run("returned type is *bytes.Buffer", func(t *testing.T) {
		t.Parallel()
		// Compile-time check: NewBufferPool returns
		// *ResetPool[*bytes.Buffer] whose Get returns
		// *bytes.Buffer. Calling a *bytes.Buffer-only method
		// without conversion would fail if the signature
		// drifted.
		p := pool.NewBufferPool()
		b := p.Get()
		// (*bytes.Buffer).Cap is unique to *bytes.Buffer; calling
		// it without an interface assertion proves the static
		// type.
		_ = b.Cap()
	})
}

func BenchmarkBufferPool(b *testing.B) {
	p := pool.NewBufferPool()
	p.Put(p.Get()) // warm
	b.ReportAllocs()
	for b.Loop() {
		buf := p.Get()
		buf.WriteString("hello, world")
		p.Put(buf)
	}
}

func BenchmarkBufferPoolParallel(b *testing.B) {
	p := pool.NewBufferPool()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := p.Get()
			buf.WriteString("payload")
			p.Put(buf)
		}
	})
}
