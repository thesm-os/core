// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"context"
	"errors"
	"testing"

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
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			got = append(got, e)
		}
		if len(got) != len(entries) {
			t.Fatalf("len: got %d, want %d", len(got), len(entries))
		}
		for i, e := range got {
			if e != entries[i] {
				t.Fatalf("entry %d: got %+v, want %+v", i, e, entries[i])
			}
		}
	})

	t.Run("empty entries yield nothing without error", func(t *testing.T) {
		t.Parallel()
		c := page.NewMapCursor[string, int](nil, "")
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
		final := page.NewMapCursor[string, int](nil, "")
		if got := final.NextPage(); got != "" {
			t.Fatalf("NextPage on final: got %q, want empty", got)
		}
		more := page.NewMapCursor([]page.Entry[string, int]{{Key: "x"}}, "next")
		if got := more.NextPage(); got != "next" {
			t.Fatalf("NextPage: got %q, want next", got)
		}
	})

	t.Run("Close returns nil and is idempotent", func(t *testing.T) {
		t.Parallel()
		c := page.NewMapCursor([]page.Entry[string, int]{{Key: "a", Value: 1}}, "")
		if err := c.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
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
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			count++
			if e.Value == 2 {
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
		if !errors.Is(sawErr, context.Canceled) {
			t.Fatalf("err: got %v, want Canceled", sawErr)
		}
		if count != 1 {
			t.Fatalf("count: got %d, want 1 (single yield with err)", count)
		}
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
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			seen++
			_ = e.Key
			_ = e.Value
		}
		if seen != 2 {
			t.Fatalf("seen: got %d, want 2", seen)
		}
	})
}
