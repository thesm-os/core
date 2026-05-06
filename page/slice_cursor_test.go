// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"context"
	"errors"
	"testing"

	"go.thesmos.sh/core/page"
)

func TestSliceCursor(t *testing.T) {
	t.Parallel()

	t.Run("yields every item exactly once with no error", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2, 3}, "")
		var got []int
		for item, err := range c.Seq(t.Context()) {
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			got = append(got, item)
		}
		want := []int{1, 2, 3}
		if len(got) != len(want) {
			t.Fatalf("len: got %d, want %d", len(got), len(want))
		}
		for i, v := range got {
			if v != want[i] {
				t.Fatalf("item %d: got %d, want %d", i, v, want[i])
			}
		}
	})

	t.Run("empty slice yields nothing without error", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{}, "")
		count := 0
		for _, err := range c.Seq(t.Context()) {
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			count++
		}
		if count != 0 {
			t.Fatalf("count: got %d, want 0", count)
		}
	})

	t.Run("NextPage returns the configured token", func(t *testing.T) {
		t.Parallel()
		final := page.NewSliceCursor([]int{}, "")
		if got := final.NextPage(); got != "" {
			t.Fatalf("NextPage on final: got %q, want empty", got)
		}
		more := page.NewSliceCursor([]int{1}, "next-batch")
		if got := more.NextPage(); got != "next-batch" {
			t.Fatalf("NextPage: got %q, want next-batch", got)
		}
	})

	t.Run("Close returns nil and is idempotent", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2}, "")
		if err := c.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})

	t.Run("early break stops iteration", func(t *testing.T) {
		t.Parallel()
		c := page.NewSliceCursor([]int{1, 2, 3, 4, 5}, "")
		count := 0
		for item, err := range c.Seq(t.Context()) {
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			count++
			if item == 2 {
				break
			}
		}
		if count != 2 {
			t.Fatalf("count: got %d, want 2", count)
		}
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
		if !errors.Is(sawErr, context.Canceled) {
			t.Fatalf("err: got %v, want Canceled", sawErr)
		}
		if count != 1 {
			t.Fatalf("count: got %d, want 1 (single yield with err)", count)
		}
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
