// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

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
		testkit.True(t, ok, "Find(service) must return ok=true")
		testkit.Equal(t, got, tag.Tag{Key: "service", Value: "ledger"},
			"Find must return the matched tag")
	})

	t.Run("returns zero on missing key", func(t *testing.T) {
		t.Parallel()
		got, ok := ts.Find("missing")
		testkit.False(t, ok, "Find(missing) must return ok=false")
		testkit.Equal(t, got, tag.Tag{}, "Find(missing) must return the zero tag")
	})

	t.Run("first match wins on duplicate key", func(t *testing.T) {
		t.Parallel()
		dup := tag.Tags{
			{Key: "k", Value: "first"},
			{Key: "k", Value: "second"},
		}
		got, _ := dup.Find("k")
		testkit.Equal(t, got.Value, "first", "Find on duplicates must return the first match")
	})

	t.Run("empty Tags returns !ok", func(t *testing.T) {
		t.Parallel()
		_, ok := tag.Tags(nil).Find("k")
		testkit.False(t, ok, "Find on nil Tags must return ok=false")
	})
}

func TestTagsHas(t *testing.T) {
	t.Parallel()

	ts := sample()

	testkit.True(t, ts.Has("region"), "Has(region) must return true")
	testkit.False(t, ts.Has("missing"), "Has(missing) must return false")
}

func TestTagsGet(t *testing.T) {
	t.Parallel()

	ts := sample()

	testkit.Equal(t, ts.Get("region"), sampleRegion, "Get(region) must match")
	testkit.Equal(t, ts.Get("missing"), "", "Get(missing) must return empty string")
}

func TestTagsWith(t *testing.T) {
	t.Parallel()

	t.Run("appends new key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.With(tag.Tag{Key: "new", Value: "v"})
		testkit.Equal(t, len(out), 4, "With must append a new entry")
		got, _ := out.Find("new")
		testkit.Equal(t, got.Value, "v", "With(new) must add the tag with value v")
		testkit.False(t, ts.Has("new"), "With must not mutate the original Tags")
	})

	t.Run("replaces existing key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.With(tag.Tag{Key: "region", Value: "us-east-1"})
		testkit.Equal(t, len(out), len(ts), "With on existing key must not change length")
		testkit.Equal(t, out.Get("region"), "us-east-1", "With must replace the value")
		testkit.Equal(t, ts.Get("region"), sampleRegion, "With must not mutate the original")
	})
}

func TestTagsWithout(t *testing.T) {
	t.Parallel()

	t.Run("removes matching key without mutating original", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.Without("service")
		testkit.False(t, out.Has("service"), "Without must remove the matching key")
		testkit.True(t, ts.Has("service"), "Without must not mutate the original")
		testkit.Equal(t, len(out), len(ts)-1, "Without must shrink the length by 1")
	})

	t.Run("missing key returns slice of equal length", func(t *testing.T) {
		t.Parallel()
		ts := sample()
		out := ts.Without("missing")
		testkit.Equal(t, len(out), len(ts), "Without(missing) must not change length")
		testkit.True(t, slices.Equal(out, ts),
			"Without(missing) must return a slice equal to the original")
	})

	t.Run("removes every duplicate of the key", func(t *testing.T) {
		t.Parallel()
		dup := tag.Tags{
			{Key: "k", Value: "1"},
			{Key: "k", Value: "2"},
			{Key: "other", Value: "x"},
		}
		out := dup.Without("k")
		testkit.Equal(t, len(out), 1, "Without must remove every duplicate")
		testkit.Equal(t, out[0].Key, "other", "remaining tag must be the non-matched one")
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
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkFind(b *testing.B) {
	ts := sample()
	b.Run("hit", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = ts.Find("service")
		}
	})
	b.Run("miss", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = ts.Find("missing")
		}
	})
}

func BenchmarkHas(b *testing.B) {
	ts := sample()
	b.ReportAllocs()
	for b.Loop() {
		_ = ts.Has("service")
	}
}

func BenchmarkGet(b *testing.B) {
	ts := sample()
	b.ReportAllocs()
	for b.Loop() {
		_ = ts.Get("service")
	}
}

func BenchmarkWith(b *testing.B) {
	ts := sample()
	b.Run("replace", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ts.With(tag.Tag{Key: "service", Value: "new"})
		}
	})
	b.Run("append", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = ts.With(tag.Tag{Key: "fresh", Value: "added"})
		}
	})
}

func BenchmarkWithout(b *testing.B) {
	ts := sample()
	b.ReportAllocs()
	for b.Loop() {
		_ = ts.Without("service")
	}
}
