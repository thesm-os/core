// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/crypto"
)

// errEncode is the error of an appender that fails.
var errEncode = errors.New("arena_test: encode failed")

func TestAppendVia(t *testing.T) {
	t.Parallel()

	t.Run("AppendVia", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that the appender appended", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendVia must adopt what the appender appended", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				head := c.Draw(payload, "head")
				a.Append(head)
				data := c.Draw(payload, "data")

				got, err := a.AppendVia(func(dst []byte) ([]byte, error) { return append(dst, data...), nil })
				assert.NoError(c, err, "AppendVia must return the appender's success")
				assert.Equal(c, got, data, "the region must be what the appender appended", assert.EquateEmpty())
				assert.Equal(c, cap(got), len(got), "the capacity of the region must be its length")
				assert.Equal(c, a.Bytes(), slices.Concat(head, data), "Bytes must return the head and then the data",
					assert.EquateEmpty())
			})
		})

		t.Run("returns the encoding of an encoding.BinaryAppender", func(t *testing.T) {
			t.Parallel()
			d := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})
			a := arena.NewWithCapacity(64)
			got, err := a.AppendVia(d.AppendBinary)
			assert.NoError(t, err, "AppendVia must return the success of AppendBinary")
			assert.Equal(t, got, d.Bytes(), "the region must be the encoded digest")
		})

		t.Run("returns the appender's error", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			got, err := a.AppendVia(func(dst []byte) ([]byte, error) { return append(dst, "partial"...), errEncode })
			assert.ErrorIs(t, err, errEncode, "the appender's error must reach the caller")
			assert.Nil(t, got, "no region may be returned with an error")
		})

		t.Run("leaves the bytes unchanged when the appender fails", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a failed AppendVia must leave the bytes of the arena unchanged", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				a.Append(c.Draw(payload, "head"))
				partial := c.Draw(payload, "partial")

				assert.Pure(c, func() []byte { return bytes.Clone(a.Bytes()) }, func() {
					_, _ = a.AppendVia(func(dst []byte) ([]byte, error) { return append(dst, partial...), errEncode })
				}, "the bytes that the appender wrote must be discarded")
			})
		})

		t.Run("lets a later Append follow the bytes before a failed call", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("keep"))
			_, err := a.AppendVia(func(dst []byte) ([]byte, error) { return append(dst, "discard"...), errEncode })
			assert.ErrorIs(t, err, errEncode, "the appender's error must reach the caller")
			assert.Equal(t, a.Append([]byte("after")), []byte("after"), "a later Append must return its payload")
			assert.Equal(t, a.Bytes(), []byte("keepafter"), "the discarded bytes must not reappear")
		})
	})

	t.Run("TruncateTo", func(t *testing.T) {
		t.Parallel()

		t.Run("rewinds the arena to the marker", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "TruncateTo must discard every byte appended after the marker", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				before := c.Draw(payloads, "before")
				for _, d := range before {
					a.Append(d)
				}
				m := a.Mark()
				for _, d := range c.Draw(payloads, "after") {
					a.Append(d)
				}

				assert.True(c, a.TruncateTo(m), "TruncateTo must return true for a current marker")
				assert.Equal(c, a.Bytes(), slices.Concat(before...), "Bytes must return the bytes before the marker",
					assert.EquateEmpty())
			})
		})

		t.Run("leaves the bytes unchanged for a marker at the end", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("keep"))
			m := a.Mark()

			var ok bool
			assert.Pure(t, func() []byte { return bytes.Clone(a.Bytes()) }, func() { ok = a.TruncateTo(m) },
				"a marker at the end must discard nothing")
			assert.True(t, ok, "TruncateTo must return true for a marker at the end")
		})

		t.Run("keeps the backing buffer", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			m := a.Mark()
			a.Append(make([]byte, 32))

			var ok bool
			assert.Pure(t, a.Cap, func() { ok = a.TruncateTo(m) }, "TruncateTo must not release the backing buffer")
			assert.True(t, ok, "TruncateTo must return true for a current marker")
		})

		t.Run("keeps an earlier marker valid", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			start := a.Mark()
			a.Append([]byte("keep"))
			mid := a.Mark()
			a.Append([]byte("discard"))
			assert.True(t, a.TruncateTo(mid), "TruncateTo must return true for a current marker")
			assert.Equal(t, a.SliceSince(start), []byte("keep"), "a marker before the rewind must still slice")
		})

		t.Run("returns false for a marker past the end", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			start := a.Mark()
			a.Append([]byte("discard"))
			end := a.Mark()
			assert.True(t, a.TruncateTo(start), "TruncateTo must return true for a current marker")

			var ok bool
			assert.Pure(t, func() []byte { return bytes.Clone(a.Bytes()) }, func() { ok = a.TruncateTo(end) },
				"a marker past the end must not expose the discarded bytes")
			assert.False(t, ok, "TruncateTo must return false for a marker past the end")
		})

		for _, tt := range ends {
			t.Run("returns false for a marker from before "+tt.name, func(t *testing.T) {
				t.Parallel()
				a := arena.NewWithCapacity(64)
				a.Append([]byte("first"))
				stale := a.Mark()
				tt.end(a)
				a.Append([]byte("second"))

				var ok bool
				assert.Pure(t, func() []byte { return bytes.Clone(a.Bytes()) }, func() { ok = a.TruncateTo(stale) },
					"a marker of an ended lifecycle must leave the arena unchanged")
				assert.False(t, ok, "TruncateTo must return false for a marker of an ended lifecycle")
			})
		}
	})
}

// TestAppendViaAllocs checks the allocation contracts of AppendVia and
// TruncateTo. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestAppendViaAllocs(t *testing.T) {
	t.Run("AppendVia", func(t *testing.T) {
		a := arena.NewWithCapacity(4096)
		d := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})

		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() {
			a.Reset()
			got, err = a.AppendVia(d.AppendBinary)
		}, 0, "AppendVia into a backing buffer with room must not allocate")
		assert.NoError(t, err, "the test must measure an encode that succeeds")
		assert.Equal(t, got, d.Bytes(), "the test must measure the encoded digest")
	})

	t.Run("TruncateTo", func(t *testing.T) {
		a := arena.NewWithCapacity(4096)
		m := a.Mark()
		a.Append(make([]byte, 128))

		var ok bool
		expect.MaxAllocs(t, func() { ok = a.TruncateTo(m) }, 0, "TruncateTo must not allocate")
		assert.True(t, ok, "the test must measure a current marker")
	})
}

// BenchmarkAppendVia reports the cost of AppendVia and TruncateTo, and fails
// when either allocates.
func BenchmarkAppendVia(b *testing.B) {
	b.Run("AppendVia", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		d := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			a.Reset()
			got, err = a.AppendVia(d.AppendBinary)
		}

		assert.NoError(b, err, "the benchmark must measure an encode that succeeds")
		assert.Equal(b, got, d.Bytes(), "the benchmark must measure the encoded digest")
	})

	b.Run("TruncateTo", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		m := a.Mark()
		a.Append(make([]byte, 128))

		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = a.TruncateTo(m)
		}

		assert.True(b, ok, "the benchmark must measure a current marker")
	})
}
