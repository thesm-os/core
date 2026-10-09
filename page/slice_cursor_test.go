// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/page"
)

// sizes are the numbers of items that the cursor benchmarks iterate.
var sizes = []struct {
	name string
	n    int
}{
	{"16", 16},
	{"256", 256},
	{"4K", 4096},
}

func TestSliceCursor(t *testing.T) {
	t.Parallel()

	t.Run("Seq", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every item once in order", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(items []int) []int {
				got := make([]int, 0, len(items))
				for item, err := range page.NewSliceCursor(items, "").Seq(t.Context()) {
					if err != nil {
						return nil
					}
					got = append(got, item)
				}

				return got
			}, func(items []int) []int { return items }, "Seq must yield the items in order and no error",
				assert.EquateEmpty(), prop.Example([]int{1, 2, 3}), prop.Example([]int{}))
		})

		t.Run("stops at a break", func(t *testing.T) {
			t.Parallel()
			c := page.NewSliceCursor([]int{1, 2, 3, 4, 5}, "")
			count := 0
			for item := range c.Seq(t.Context()) {
				count++
				if item == 2 {
					break
				}
			}
			assert.Equal(t, count, 2, "a break must stop the iteration")
		})

		t.Run("yields the error of a cancelled context once", func(t *testing.T) {
			t.Parallel()
			c := page.NewSliceCursor([]int{1, 2, 3}, "")
			pairs := 0
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				for _, err := range c.Seq(ctx) {
					pairs++
					if err != nil {
						return err
					}
				}

				return nil
			}, "Seq must yield the error of a cancelled context")
			assert.Equal(t, pairs, 1, "a cancelled context must end the iteration at its first pair")
		})
	})

	t.Run("NextPage", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the token of the construction", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(token string) string { return page.NewSliceCursor([]int{1}, token).NextPage() },
				func(token string) string { return token }, "NextPage must return the token it was given",
				prop.Example(""), prop.Example("next-batch"))
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil on every call", func(t *testing.T) {
			t.Parallel()
			c := page.NewSliceCursor([]int{1, 2}, "")
			assert.Idempotent(t, func(struct{}) error { return c.Close() }, struct{}{}, func() []int {
				var items []int
				for item := range c.Seq(t.Context()) {
					items = append(items, item)
				}

				return items
			}, "a second Close must change nothing")
		})
	})
}

// TestSliceCursorAllocs checks the allocation contract of SliceCursor.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestSliceCursorAllocs(t *testing.T) {
	ctx := t.Context()
	items := make([]int, 64)

	t.Run("NewSliceCursor", func(t *testing.T) {
		var got *page.SliceCursor[int]
		expect.MaxAllocs(t, func() { got = page.NewSliceCursor(items, "") }, 1,
			"NewSliceCursor must allocate only the cursor")
		assert.NotNil(t, got, "the test must measure a cursor")
	})

	t.Run("Seq", func(t *testing.T) {
		c := page.NewSliceCursor(items, "")

		var n int
		expect.MaxAllocs(t, func() {
			n = 0
			for range c.Seq(ctx) {
				n++
			}
		}, 0, "Seq must not allocate")
		assert.Equal(t, n, len(items), "the test must measure every item")
	})

	t.Run("NextPage", func(t *testing.T) {
		c := page.NewSliceCursor(items, "next")

		var got string
		expect.MaxAllocs(t, func() { got = c.NextPage() }, 0, "NextPage must not allocate")
		assert.Equal(t, got, "next", "the test must measure the token")
	})

	t.Run("Close", func(t *testing.T) {
		c := page.NewSliceCursor(items, "")

		var err error
		expect.MaxAllocs(t, func() { err = c.Close() }, 0, "Close must not allocate")
		assert.NoError(t, err, "the test must measure a Close that succeeds")
	})
}

// BenchmarkSliceCursor reports the cost of a whole pass over a cursor,
// and fails when the pass allocates more than the cursor.
func BenchmarkSliceCursor(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			items := make([]int, size.n)
			ctx := b.Context()

			var (
				n   int
				err error
			)

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				cur := page.NewSliceCursor(items, "")
				n = 0
				for range cur.Seq(ctx) {
					n++
				}
				err = cur.Close()
			}

			assert.NoError(b, err, "the benchmark must measure a Close that succeeds")
			assert.Equal(b, n, size.n, "the benchmark must measure every item")
		})
	}
}
