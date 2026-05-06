// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"testing"

	"go.thesmos.sh/core/pool"
)

func TestNewBufferPool(t *testing.T) {
	t.Parallel()

	t.Run("Get returns a fresh empty buffer", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		b := p.Get()
		if b == nil {
			t.Fatal("Get returned nil")
		}
		if b.Len() != 0 {
			t.Fatalf("fresh buffer Len: got %d, want 0", b.Len())
		}
	})

	t.Run("Put auto-Resets buffer", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		b := p.Get()
		b.WriteString("tenant data")
		if b.Len() == 0 {
			t.Fatal("Write did not extend buffer")
		}
		p.Put(b)
		if b.Len() != 0 {
			t.Fatalf("buffer after Put: Len=%d, want 0", b.Len())
		}
	})

	t.Run("recycled buffer is empty on next Get", func(t *testing.T) {
		t.Parallel()
		p := pool.NewBufferPool()
		first := p.Get()
		first.WriteString("contaminated")
		p.Put(first)

		next := p.Get()
		// sync.Pool may evict; guard the same-pointer case.
		if next == first && next.Len() != 0 {
			t.Fatalf("recycled buffer not empty: Len=%d, want 0",
				next.Len())
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
