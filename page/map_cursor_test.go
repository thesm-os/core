// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"context"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/page"
)

func TestMapCursor(t *testing.T) {
	t.Parallel()

	t.Run("yields every entry exactly once with no error", func(t *testing.T) {
		t.Parallel()
		entries := []page.Entry[string, int]{
			{Key: "a", Value: 1},
			{Key: "b", Value: 2},
			{Key: "c", Value: 3},
		}
		c := page.NewMapCursor(entries, "")
		var got []page.Entry[string, int]
		for e, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "iteration must not error")
			got = append(got, e)
		}
		testkit.Equal(t, got, entries, "Seq must yield every entry in order")
	})

	t.Run("empty entries yield nothing without error", func(t *testing.T) {
		t.Parallel()
		c := page.NewMapCursor[string, int](nil, "")
		count := 0
		for _, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "empty Seq must not error")
			count++
		}
		testkit.Equal(t, count, 0, "empty cursor must yield nothing")
	})

	t.Run("NextPage returns the configured token", func(t *testing.T) {
		t.Parallel()
		final := page.NewMapCursor[string, int](nil, "")
		testkit.Equal(t, final.NextPage(), "", "NextPage on final cursor must be empty")
		more := page.NewMapCursor([]page.Entry[string, int]{{Key: "x"}}, "next")
		testkit.Equal(t, more.NextPage(), "next", "NextPage must return the configured token")
	})

	t.Run("Close returns nil and is idempotent", func(t *testing.T) {
		t.Parallel()
		c := page.NewMapCursor([]page.Entry[string, int]{{Key: "a", Value: 1}}, "")
		testkit.NoError(t, c.Close(), "first Close must succeed")
		testkit.NoError(t, c.Close(), "second Close must succeed (idempotent)")
	})

	t.Run("early break stops iteration", func(t *testing.T) {
		t.Parallel()
		entries := []page.Entry[string, int]{
			{Key: "a", Value: 1},
			{Key: "b", Value: 2},
			{Key: "c", Value: 3},
			{Key: "d", Value: 4},
			{Key: "e", Value: 5},
		}
		c := page.NewMapCursor(entries, "")
		count := 0
		for e, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "iteration must not error")
			count++
			if e.Value == 2 {
				break
			}
		}
		testkit.Equal(t, count, 2, "early break must stop iteration")
	})

	t.Run("cancelled context yields ctx.Err and stops", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		c := page.NewMapCursor([]page.Entry[string, int]{
			{Key: "a", Value: 1},
			{Key: "b", Value: 2},
		}, "")
		var sawErr error
		count := 0
		for e, err := range c.Seq(ctx) {
			count++
			if err != nil {
				sawErr = err
				_ = e
				break
			}
		}
		testkit.ErrorIs(t, sawErr, context.Canceled,
			"cancelled context must surface context.Canceled")
		testkit.Equal(t, count, 1,
			"cancelled context must yield exactly one (entry, err) pair before stopping")
	})

	t.Run("works with non-string K and V types", func(t *testing.T) {
		t.Parallel()
		entries := []page.Entry[int, []byte]{
			{Key: 1, Value: []byte("hello")},
			{Key: 2, Value: []byte("world")},
		}
		c := page.NewMapCursor(entries, "")
		var seen int
		for e, err := range c.Seq(t.Context()) {
			testkit.NoError(t, err, "iteration must not error")
			seen++
			_ = e.Key
			_ = e.Value
		}
		testkit.Equal(t, seen, 2, "must yield every entry regardless of K/V types")
	})
}

func BenchmarkMapCursor(b *testing.B) {
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"16", 16},
		{"256", 256},
		{"4K", 4096},
	} {
		b.Run(sz.name, func(b *testing.B) {
			entries := make([]page.Entry[string, int], sz.n)
			for i := range entries {
				entries[i] = page.Entry[string, int]{Key: "k", Value: i}
			}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				c := page.NewMapCursor(entries, "")
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
