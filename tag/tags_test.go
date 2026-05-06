// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"slices"
	"testing"

	"go.thesmos.sh/core/tag"
)

const sampleRegion = "eu-west-1"

func sample() tag.Tags {
	return tag.Tags{
		{Key: "region", Value: sampleRegion},
		{Key: "service", Value: "ledger"},
		{Key: "tier", Value: "prod"},
	}
}

func TestTagsFind(t *testing.T) {
	t.Parallel()

	ts := sample()

	t.Run("returns existing tag", func(t *testing.T) {
		t.Parallel()
		got, ok := ts.Find("service")
		if !ok {
			t.Fatal("Find(service): got !ok, want ok")
		}
		want := tag.Tag{Key: "service", Value: "ledger"}
		if got != want {
			t.Fatalf("Find(service): got %+v, want %+v", got, want)
		}
	})

	t.Run("returns zero on missing key", func(t *testing.T) {
		t.Parallel()
		got, ok := ts.Find("missing")
		if ok {
			t.Fatal("Find(missing): got ok, want !ok")
		}
		if got != (tag.Tag{}) {
			t.Fatalf("Find(missing): got %+v, want zero", got)
		}
	})

	t.Run("first match wins on duplicate key", func(t *testing.T) {
		t.Parallel()
		dup := tag.Tags{
			{Key: "k", Value: "first"},
			{Key: "k", Value: "second"},
		}
		got, _ := dup.Find("k")
		if got.Value != "first" {
			t.Fatalf("Find on duplicates: got %q, want first", got.Value)
		}
	})

	t.Run("empty Tags returns !ok", func(t *testing.T) {
		t.Parallel()
		_, ok := tag.Tags(nil).Find("k")
		if ok {
			t.Fatal("Find on nil Tags: got ok, want !ok")
		}
	})
}

func TestTagsHas(t *testing.T) {
	t.Parallel()

	ts := sample()

	if !ts.Has("region") {
		t.Fatal("Has(region): got false, want true")
	}
	if ts.Has("missing") {
		t.Fatal("Has(missing): got true, want false")
	}
}

func TestTagsGet(t *testing.T) {
	t.Parallel()

	ts := sample()

	if got := ts.Get("region"); got != sampleRegion {
		t.Fatalf("Get(region): got %q, want %q", got, sampleRegion)
	}
	if got := ts.Get("missing"); got != "" {
		t.Fatalf("Get(missing): got %q, want empty", got)
	}
}

func TestTagsWith(t *testing.T) {
	t.Parallel()

	t.Run("appends new key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.With(tag.Tag{Key: "new", Value: "v"})
		if len(out) != 4 {
			t.Fatalf("len(With): got %d, want 4", len(out))
		}
		if got, _ := out.Find("new"); got.Value != "v" {
			t.Fatalf("With: new tag missing or wrong value, got %+v", got)
		}
		if ts.Has("new") {
			t.Fatal("With mutated original Tags")
		}
	})

	t.Run("replaces existing key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.With(tag.Tag{Key: "region", Value: "us-east-1"})
		if len(out) != len(ts) {
			t.Fatalf("len(With) on existing key: got %d, want %d",
				len(out), len(ts))
		}
		if got := out.Get("region"); got != "us-east-1" {
			t.Fatalf("With(region=us-east-1): got %q", got)
		}
		if got := ts.Get("region"); got != sampleRegion {
			t.Fatalf("original mutated: got %q, want %q", got, sampleRegion)
		}
	})
}

func TestTagsWithout(t *testing.T) {
	t.Parallel()

	t.Run("removes matching key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.Without("service")
		if out.Has("service") {
			t.Fatal("Without(service): result still has key")
		}
		if !ts.Has("service") {
			t.Fatal("Without mutated original")
		}
		if len(out) != len(ts)-1 {
			t.Fatalf("len(Without): got %d, want %d", len(out), len(ts)-1)
		}
	})

	t.Run("missing key returns slice of equal length", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.Without("missing")
		if len(out) != len(ts) {
			t.Fatalf("len(Without missing): got %d, want %d",
				len(out), len(ts))
		}
		if !slices.Equal(out, ts) {
			t.Fatalf("Without missing: got %+v, want %+v", out, ts)
		}
	})

	t.Run("removes every duplicate of the key", func(t *testing.T) {
		t.Parallel()
		dup := tag.Tags{
			{Key: "k", Value: "1"},
			{Key: "k", Value: "2"},
			{Key: "other", Value: "x"},
		}
		out := dup.Without("k")
		if len(out) != 1 {
			t.Fatalf("len: got %d, want 1", len(out))
		}
		if out[0].Key != "other" {
			t.Fatalf("remaining tag: got %+v, want other=x", out[0])
		}
	})
}

// TestTagsZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestTagsZeroAlloc(t *testing.T) {
	ts := sample()

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _, _ = ts.Find("service") }, "Find/hit"},
		{func() { _, _ = ts.Find("missing") }, "Find/miss"},
		{func() { _ = ts.Has("service") }, "Has/hit"},
		{func() { _ = ts.Has("missing") }, "Has/miss"},
		{func() { _ = ts.Get("service") }, "Get/hit"},
		{func() { _ = ts.Get("missing") }, "Get/miss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}
