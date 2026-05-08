// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"context"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/page"
)

func TestSliceCursor(t *testing.T) {
	t.Parallel()

	t.Run("yields every item exactly once with no error", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2, 3}, "")
		var got []int
		for item, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "iteration must not error")
			got = append(got, item)
		}
		testkit.Equal(t, got, []int{1, 2, 3}, "Seq must yield every item in order")
	})

	t.Run("empty slice yields nothing without error", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{}, "")
		count := 0
		for _, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "empty Seq must not error")
			count++
		}
		testkit.Equal(t, count, 0, "empty cursor must yield nothing")
	})

	t.Run("NextPage returns the configured token", func(t *testing.T) {
		t.Parallel()
		final := page.NewSliceCursor([]int{}, "")
		testkit.Equal(t, final.NextPage(), "", "NextPage on final cursor must be empty")
		more := page.NewSliceCursor([]int{1}, "next-batch")
		testkit.Equal(t, more.NextPage(), "next-batch", "NextPage must return the configured token")
	})

	t.Run("Close returns nil and is idempotent", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2}, "")
		testkit.NoError(t, c.Close(), "first Close must succeed")
		testkit.NoError(t, c.Close(), "second Close must succeed (idempotent)")
	})

	t.Run("early break stops iteration", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2, 3, 4, 5}, "")
		count := 0
		for item, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "iteration must not error")
			count++
			if item == 2 {
				break
			}
		}
		testkit.Equal(t, count, 2, "early break must stop iteration")
	})

	t.Run("cancelled context yields ctx.Err and stops", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		c := page.NewSliceCursor([]int{1, 2, 3}, "")
		var sawErr error
		count := 0
		for item, err := range c.Seq(ctx) {
			count++
			if err != nil {
				sawErr = err
				_ = item
				break
			}
		}
		testkit.ErrorIs(t, sawErr, context.Canceled,
			"cancelled context must surface context.Canceled")
		testkit.Equal(t, count, 1,
			"cancelled context must yield exactly one (item, err) pair before stopping")
	})
}

func BenchmarkSliceCursor(b *testing.B) {
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"16", 16},
		{"256", 256},
		{"4K", 4096},
	} {
		b.Run(sz.name, func(b *testing.B) {
			items := make([]int, sz.n)
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				c := page.NewSliceCursor(items, "")
				for _, err := range c.Seq(ctx) {
					if err != nil {
						b.Fatal(err)
					}
				}
				_ = c.Close()
			}
		})
	}
}
