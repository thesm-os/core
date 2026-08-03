// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"errors"
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/crypto"
)

var errEncode = errors.New("arena_test: encode failed")

func TestAppendVia(t *testing.T) {
	t.Parallel()

	t.Run("adopts what the appender wrote", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)

		got, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "payload"...), nil
		})

		testkit.NoError(t, err, "AppendVia must succeed")
		testkit.Equal(t, got, []byte("payload"), "the region must hold what was appended")
		testkit.Equal(t, a.Len(), len("payload"), "the arena must have grown by the written bytes")
	})

	t.Run("composes with encoding.BinaryAppender", func(t *testing.T) {
		t.Parallel()
		// The reason this method exists: AppendBinary's signature is
		// exactly AppendVia's callback, so a type that implements the
		// stdlib interface writes into arena capacity unchanged.
		d := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})
		a := arena.NewWithCapacity(64)

		got, err := a.AppendVia(d.AppendBinary)

		testkit.NoError(t, err, "AppendVia must accept an encoding.BinaryAppender")
		testkit.Equal(t, got, d.Bytes(), "the region must hold the encoded digest")
	})

	t.Run("appends after existing content without disturbing it", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		first := a.Append([]byte("first"))

		second, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "second"...), nil
		})

		testkit.NoError(t, err, "AppendVia must succeed")
		testkit.Equal(t, first, []byte("first"), "earlier content must be untouched")
		testkit.Equal(t, second, []byte("second"), "the region must cover only the new bytes")
		testkit.Equal(t, a.Bytes(), []byte("firstsecond"), "the arena must hold both, in order")
	})

	t.Run("grows past capacity", func(t *testing.T) {
		t.Parallel()
		// The appender may exceed the arena's spare capacity, in
		// which case append reallocates and the arena adopts the new
		// backing array, exactly as Append does when it grows.
		a := arena.NewWithCapacity(8)
		big := make([]byte, 1024)
		for i := range big {
			big[i] = byte(i)
		}

		got, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, big...), nil
		})

		testkit.NoError(t, err, "AppendVia must succeed past capacity")
		testkit.Equal(t, got, big, "the region must hold every written byte")
		testkit.Equal(t, a.Len(), len(big), "the arena must have grown")
	})

	t.Run("an appender writing nothing yields an empty region", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)

		got, err := a.AppendVia(func(dst []byte) ([]byte, error) { return dst, nil })

		testkit.NoError(t, err, "AppendVia must succeed")
		testkit.Equal(t, len(got), 0, "writing nothing must yield an empty region")
		testkit.Equal(t, a.Len(), 0, "the arena must not have grown")
	})

	t.Run("the region is three-index capped", func(t *testing.T) {
		t.Parallel()
		// Appending to the returned region must not reach into a
		// neighbour's bytes, which is the invariant every other
		// arena accessor holds.
		a := arena.NewWithCapacity(64)
		got, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "abc"...), nil
		})
		testkit.NoError(t, err, "AppendVia must succeed")
		testkit.Equal(t, cap(got), len(got), "the region's capacity must equal its length")
	})
}

func TestAppendViaError(t *testing.T) {
	t.Parallel()

	t.Run("returns the appender's error", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)

		got, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "partial"...), errEncode
		})

		testkit.ErrorIs(t, err, errEncode, "the appender's error must reach the caller")
		testkit.Equal(t, got, []byte(nil), "no region may be returned alongside an error")
	})

	t.Run("leaves no partial region behind", func(t *testing.T) {
		t.Parallel()
		// A failed encode must not leave half a record in the arena
		// for the next append to run into.
		a := arena.NewWithCapacity(64)
		a.Append([]byte("keep"))

		_, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "discard"...), errEncode
		})

		testkit.ErrorIs(t, err, errEncode, "the error must propagate")
		testkit.Equal(t, a.Len(), len("keep"), "the arena must be back at its pre-call extent")
		testkit.Equal(t, a.Bytes(), []byte("keep"), "earlier content must survive")
	})

	t.Run("the arena stays usable after a failure", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		_, err := a.AppendVia(func(dst []byte) ([]byte, error) {
			return append(dst, "discard"...), errEncode
		})
		testkit.ErrorIs(t, err, errEncode, "the error must propagate")

		got := a.Append([]byte("after"))
		testkit.Equal(t, got, []byte("after"), "a later append must succeed")
		testkit.Equal(t, a.Bytes(), []byte("after"), "the discarded bytes must not reappear")
	})
}

func TestTruncateTo(t *testing.T) {
	t.Parallel()

	t.Run("rewinds to the marked extent", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("keep"))
		m := a.Mark()
		a.Append([]byte("discard"))

		testkit.True(t, a.TruncateTo(m), "TruncateTo must accept a live marker")
		testkit.Equal(t, a.Bytes(), []byte("keep"), "bytes after the mark must be discarded")
	})

	t.Run("truncating to the current end is a no-op", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("keep"))
		m := a.Mark()

		testkit.True(t, a.TruncateTo(m), "a marker at the end must be accepted")
		testkit.Equal(t, a.Bytes(), []byte("keep"), "nothing must be discarded")
	})

	t.Run("rejects a marker from a previous lifecycle", func(t *testing.T) {
		t.Parallel()
		// The epoch check is what stops a stale marker rewinding into
		// the current cycle's bytes — the same guard SliceSince has.
		a := arena.NewWithCapacity(64)
		a.Append([]byte("first"))
		stale := a.Mark()

		a.Reset()
		a.Append([]byte("second"))

		testkit.False(t, a.TruncateTo(stale), "a stale marker must be rejected")
		testkit.Equal(t, a.Bytes(), []byte("second"), "the arena must be unchanged")
	})

	t.Run("capacity is preserved", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		m := a.Mark()
		a.Append(make([]byte, 32))

		before := a.Cap()
		testkit.True(t, a.TruncateTo(m), "TruncateTo must accept a live marker")
		testkit.Equal(t, a.Cap(), before, "truncation must not release the backing buffer")
	})

	t.Run("markers stay valid across a truncation", func(t *testing.T) {
		t.Parallel()
		// TruncateTo does not advance the epoch: it rewinds within
		// the same lifecycle, so earlier markers still describe
		// positions that are still meaningful.
		a := arena.NewWithCapacity(64)
		start := a.Mark()
		a.Append([]byte("keep"))
		mid := a.Mark()
		a.Append([]byte("discard"))

		testkit.True(t, a.TruncateTo(mid), "TruncateTo must accept a live marker")
		testkit.Equal(t, a.SliceSince(start), []byte("keep"), "an earlier marker must still slice")
	})
}

func BenchmarkAppendVia(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	d := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})
	b.ReportAllocs()

	var sink []byte
	for b.Loop() {
		a.Reset()
		sink, _ = a.AppendVia(d.AppendBinary)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkTruncateTo(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	m := a.Mark()
	a.Append(make([]byte, 128))
	b.ReportAllocs()

	var sink bool
	for b.Loop() {
		sink = a.TruncateTo(m)
	}
	runtime.KeepAlive(sink)
}
