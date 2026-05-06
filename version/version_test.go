// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

	"go.thesmos.sh/core/version"
)

func TestVersionPredicates(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		v          version.Version
		isZero     bool
		isWildcard bool
	}{
		"unspecified":      {version.Unspecified, true, false},
		"empty literal":    {"", true, false},
		"wildcard":         {version.Wildcard, false, true},
		"asterisk literal": {"*", false, true},
		"opaque token":     {"abc123", false, false},
		"counter-shaped":   {"42", false, false},
		"hex-shaped":       {"deadbeef", false, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.v.IsZero(); got != tc.isZero {
				t.Fatalf("IsZero(%q): got %v, want %v", tc.v, got, tc.isZero)
			}
			if got := tc.v.IsWildcard(); got != tc.isWildcard {
				t.Fatalf("IsWildcard(%q): got %v, want %v",
					tc.v, got, tc.isWildcard)
			}
		})
	}
}

// TestVersionZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestVersionZeroAlloc(t *testing.T) {
	v := version.Version("opaque-token")

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _ = v.IsZero() }, "IsZero"},
		{func() { _ = v.IsWildcard() }, "IsWildcard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkIsZero(b *testing.B) {
	v := version.Version("opaque-token")
	b.ReportAllocs()
	for b.Loop() {
		_ = v.IsZero()
	}
}

func BenchmarkIsWildcard(b *testing.B) {
	v := version.Wildcard
	b.ReportAllocs()
	for b.Loop() {
		_ = v.IsWildcard()
	}
}
