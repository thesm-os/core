// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/tag"
)

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("zero value reports IsZero", func(t *testing.T) {
		t.Parallel()
		testkit.True(t, (tag.Tag{}).IsZero(), "zero Tag.IsZero must return true")
	})

	t.Run("non-zero value does not report IsZero", func(t *testing.T) {
		t.Parallel()
		cases := []tag.Tag{
			{Key: "k"},
			{Value: "v"},
			{Key: "k", Value: "v"},
		}
		for _, tc := range cases {
			testkit.False(t, tc.IsZero(), "non-zero Tag.IsZero must return false")
		}
	})
}

// TestTagZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestTagZeroAlloc(t *testing.T) {
	tt := tag.Tag{Key: "k", Value: "v"}
	testkit.Equal(t, testing.AllocsPerRun(100, func() { _ = tt.IsZero() }),
		float64(0), "IsZero must be zero-alloc")
}

func BenchmarkIsZero(b *testing.B) {
	tt := tag.Tag{Key: "k", Value: "v"}
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = tt.IsZero()
	}
	runtime.KeepAlive(sink)
}
