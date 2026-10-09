// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/arena"
)

func TestLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets Len to zero", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("data"))
			a.Reset()
			assert.Equal(t, a.Len(), 0, "Len after Reset must be 0")
		})

		t.Run("keeps the backing buffer", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("data"))
			assert.Pure(t, a.Cap, a.Reset, "Reset must keep the capacity")
		})

		t.Run("lets the next Append write from offset 0", func(t *testing.T) {
			t.Parallel()
			a := arena.New()
			a.Append([]byte("first"))
			a.Reset()
			assert.Equal(t, a.Append([]byte("second")), []byte("second"), "Append after Reset must return its payload")
			assert.Equal(t, a.Bytes(), []byte("second"), "the arena must start again at offset 0")
		})

		t.Run("zeroes the bytes written", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Reset must zero every byte that the lifecycle wrote", func(c *prop.Case) {
				a := arena.NewWithCapacity(c.Draw(capacities, "initialCap"))
				for _, d := range c.Draw(payloads, "payloads") {
					a.Append(d)
				}
				a.Reset()
				assert.Equal(c, spare(c, a), make([]byte, a.Cap()), "no written byte may survive Reset",
					assert.EquateEmpty())
			})
		})

		t.Run("zeroes the bytes that a TruncateTo rewind left written", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("keep"))
			m := a.Mark()
			a.Append(bytes.Repeat([]byte{fill}, 16))
			assert.True(t, a.TruncateTo(m), "TruncateTo must return true for a current marker")
			a.Reset()
			assert.Equal(t, spare(t, a), make([]byte, 64), "bytes past the rewound length must be zeroed")
		})

		t.Run("zeroes the spare capacity that a failed AppendVia wrote into", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			_, err := a.AppendVia(func(dst []byte) ([]byte, error) {
				_ = append(dst, bytes.Repeat([]byte{fill}, 16)...)

				return nil, errEncode
			})
			assert.ErrorIs(t, err, errEncode, "AppendVia must return the appender's error")
			a.Reset()
			assert.Equal(t, spare(t, a), make([]byte, 64), "bytes that a failed appender wrote must be zeroed")
		})

		t.Run("keeps the smaller buffer that an appender returned", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			m := a.Mark()
			a.Append(make([]byte, 32))
			assert.True(t, a.TruncateTo(m), "TruncateTo must return true for a current marker")
			_, err := a.AppendVia(func([]byte) ([]byte, error) { return []byte{1}, nil })
			assert.NoError(t, err, "AppendVia must return the appender's success")
			a.Reset()
			assert.Equal(t, a.Cap(), 1, "Reset must keep the buffer that the appender returned")
		})
	})

	t.Run("CapExceeds", func(t *testing.T) {
		t.Parallel()

		// maxCap is the threshold of the cases, and capExceeds reports
		// CapExceeds(maxCap) of an arena of the capacity it receives.
		const maxCap = 1024
		capExceeds := func(capacity int) bool { return arena.NewWithCapacity(capacity).CapExceeds(maxCap) }

		t.Run("reports false for a capacity at or below maxCap", func(t *testing.T) {
			t.Parallel()
			prop.False(t, capExceeds, "CapExceeds must report false for a capacity up to maxCap",
				prop.Using(prop.Integer(0, maxCap)), prop.Example(64), prop.Example(maxCap))
		})

		t.Run("reports true for a capacity above maxCap", func(t *testing.T) {
			t.Parallel()
			prop.True(t, capExceeds, "CapExceeds must report true for a capacity above maxCap",
				prop.Using(prop.Integer(maxCap+1, 8*maxCap)), prop.Example(maxCap+1), prop.Example(2*maxCap))
		})
	})

	t.Run("Shrink", func(t *testing.T) {
		t.Parallel()

		t.Run("releases the backing buffer", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(1024)
			a.Append([]byte("data"))
			a.Shrink()
			expect.Equal(t, a.Len(), 0, "Len after Shrink must be 0")
			expect.Equal(t, a.Cap(), 0, "Cap after Shrink must be 0")
		})

		t.Run("lets the next Append allocate a backing buffer", func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(64)
			a.Append([]byte("first"))
			a.Shrink()
			assert.Equal(t, a.Append([]byte("second")), []byte("second"), "Append after Shrink must return its payload")
			assert.Equal(t, a.Bytes(), []byte("second"), "the arena must start again at offset 0")
		})
	})
}

// TestLifecycleAllocs checks the allocation contracts of Reset, CapExceeds
// and Shrink. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestLifecycleAllocs(t *testing.T) {
	t.Run("Reset", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		data := make([]byte, 64)
		expect.MaxAllocs(t, func() {
			a.Append(data)
			a.Reset()
		}, 0, "Reset must not allocate")
		assert.Equal(t, a.Len(), 0, "the test must measure a Reset")
	})

	t.Run("CapExceeds", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)

		var got bool
		expect.MaxAllocs(t, func() { got = a.CapExceeds(512) }, 0, "CapExceeds must not allocate")
		assert.True(t, got, "the test must measure a capacity above the threshold")
	})

	t.Run("Shrink", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		expect.MaxAllocs(t, a.Shrink, 0, "Shrink must not allocate")
		assert.Equal(t, a.Cap(), 0, "the test must measure a Shrink")
	})
}

// BenchmarkLifecycle reports the cost of Reset, CapExceeds and Shrink, and
// fails when any of them allocates.
func BenchmarkLifecycle(b *testing.B) {
	b.Run("Reset", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)
		data := make([]byte, 1024)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			a.Append(data)
			a.Reset()
		}

		assert.Equal(b, a.Len(), 0, "the benchmark must measure a Reset")
	})

	b.Run("CapExceeds", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = a.CapExceeds(8192)
		}

		assert.False(b, got, "the benchmark must measure a capacity below the threshold")
	})

	b.Run("Shrink", func(b *testing.B) {
		a := arena.NewWithCapacity(4096)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			a.Shrink()
		}

		assert.Equal(b, a.Cap(), 0, "the benchmark must measure a Shrink")
	})
}

// spare returns a copy of the spare capacity of a, read the way an
// appender passed to AppendVia can read it.
func spare(tb assert.TB, a *arena.Arena) []byte {
	tb.Helper()

	var seen []byte
	_, err := a.AppendVia(func(dst []byte) ([]byte, error) {
		seen = bytes.Clone(dst[len(dst):cap(dst)])

		return dst, nil
	})
	assert.NoError(tb, err, "a no-op appender must succeed")

	return seen
}
