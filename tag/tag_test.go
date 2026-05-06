// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"testing"

	"go.thesmos.sh/core/tag"
)

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("zero value reports IsZero", func(t *testing.T) {
		t.Parallel()
		if !(tag.Tag{}).IsZero() {
			t.Fatal("zero Tag.IsZero(): got false, want true")
		}
	})

	t.Run("non-zero value does not report IsZero", func(t *testing.T) {
		t.Parallel()
		cases := []tag.Tag{
			{Key: "k"},
			{Value: "v"},
			{Key: "k", Value: "v"},
		}
		for _, tc := range cases {
			if tc.IsZero() {
				t.Fatalf("Tag%+v.IsZero(): got true, want false", tc)
			}
		}
	})
}

// TestTagZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestTagZeroAlloc(t *testing.T) {
	tt := tag.Tag{Key: "k", Value: "v"}
	if got := testing.AllocsPerRun(100, func() { _ = tt.IsZero() }); got != 0 {
		t.Fatalf("IsZero: %v allocs/op, want 0", got)
	}
}
