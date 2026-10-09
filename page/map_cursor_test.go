// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/page"
)

// MapCursor is an alias of a SliceCursor over Entry values, so TestSliceCursor
// covers its methods. TestMapCursor covers what NewMapCursor adds.
func TestMapCursor(t *testing.T) {
	t.Parallel()

	t.Run("NewMapCursor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a cursor over the entries in order", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(entries []page.Entry[string, int]) []page.Entry[string, int] {
				got := make([]page.Entry[string, int], 0, len(entries))
				for e, err := range page.NewMapCursor(entries, "").Seq(t.Context()) {
					if err != nil {
						return nil
					}
					got = append(got, e)
				}

				return got
			}, func(entries []page.Entry[string, int]) []page.Entry[string, int] { return entries },
				"the cursor must yield the entries in order and no error", assert.EquateEmpty(),
				prop.Example([]page.Entry[string, int]{{Key: "a", Value: 1}, {Key: "b", Value: 2}}))
		})

		t.Run("returns a cursor whose NextPage is the token", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(token string) string { return page.NewMapCursor[string, int](nil, token).NextPage() },
				func(token string) string { return token }, "NextPage must return the token it was given",
				prop.Example(""), prop.Example("next"))
		})

		t.Run("returns a cursor over entries of any key and value types", func(t *testing.T) {
			t.Parallel()
			entries := []page.Entry[int, []byte]{{Key: 1, Value: []byte("hello")}, {Key: 2, Value: []byte("world")}}
			var got []page.Entry[int, []byte]
			for e, err := range page.NewMapCursor(entries, "").Seq(t.Context()) {
				assert.NoError(t, err, "the iteration must not fail")
				got = append(got, e)
			}
			assert.Equal(t, got, entries, "the cursor must yield every entry")
		})
	})
}

// TestMapCursorAllocs checks that NewMapCursor allocates only the cursor.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestMapCursorAllocs(t *testing.T) {
	t.Run("NewMapCursor", func(t *testing.T) {
		entries := make([]page.Entry[string, int], 64)

		var got *page.MapCursor[string, int]
		expect.MaxAllocs(t, func() { got = page.NewMapCursor(entries, "") }, 1,
			"NewMapCursor must allocate only the cursor")
		assert.NotNil(t, got, "the test must measure a cursor")
	})
}

// BenchmarkMapCursor reports the cost of a whole pass over a cursor of
// entries, and fails when the pass allocates more than the cursor.
func BenchmarkMapCursor(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			entries := make([]page.Entry[string, int], size.n)
			for i := range entries {
				entries[i] = page.Entry[string, int]{Key: "k", Value: i}
			}
			ctx := b.Context()

			var (
				n   int
				err error
			)

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				cur := page.NewMapCursor(entries, "")
				n = 0
				for range cur.Seq(ctx) {
					n++
				}
				err = cur.Close()
			}

			assert.NoError(b, err, "the benchmark must measure a Close that succeeds")
			assert.Equal(b, n, size.n, "the benchmark must measure every entry")
		})
	}
}
