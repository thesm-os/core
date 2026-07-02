// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

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
			testkit.Equal(t, tc.v.IsZero(), tc.isZero,
				"IsZero must match expected predicate result")
			testkit.Equal(t, tc.v.IsWildcard(), tc.isWildcard,
				"IsWildcard must match expected predicate result")
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
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkIsZero(b *testing.B) {
	v := version.Version("opaque-token")
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = v.IsZero()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkIsWildcard(b *testing.B) {
	v := version.Wildcard
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = v.IsWildcard()
	}
	runtime.KeepAlive(sink)
}
