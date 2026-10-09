// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/version"
)

func TestVersion(t *testing.T) {
	t.Parallel()

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the version is empty", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, version.Version.IsZero, func(v version.Version) bool { return len(v) == 0 },
				"IsZero must report true for the empty version alone",
				prop.Example(version.Unspecified),
				prop.Example(version.Wildcard),
				prop.Example(version.Version("abc123")),
				prop.Example(version.Version("42")),
			)
		})
	})

	t.Run("IsWildcard", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the version is an asterisk", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, version.Version.IsWildcard, func(v version.Version) bool { return string(v) == "*" },
				"IsWildcard must report true for the asterisk alone",
				prop.Example(version.Wildcard),
				prop.Example(version.Unspecified),
				prop.Example(version.Version("**")),
				prop.Example(version.Version("deadbeef")),
			)
		})
	})
}

// TestVersionAllocs checks that the predicates of a Version do not
// allocate. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestVersionAllocs(t *testing.T) {
	v := version.Version("opaque-token")

	t.Run("IsZero", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = v.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a version that is set")
	})

	t.Run("IsWildcard", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = v.IsWildcard() }, 0, "IsWildcard must not allocate")
		assert.False(t, got, "the test must measure a version that is no wildcard")
	})
}

func BenchmarkVersion(b *testing.B) {
	v := version.Version("opaque-token")

	b.Run("IsZero", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = v.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a version that is set")
	})

	b.Run("IsWildcard", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = v.IsWildcard()
		}

		assert.False(b, got, "the benchmark must measure a version that is no wildcard")
	})
}
