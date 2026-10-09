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

func TestWriteOptions(t *testing.T) {
	t.Parallel()

	t.Run("IsConditional", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether either precondition is set", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, version.WriteOptions.IsConditional,
				func(o version.WriteOptions) bool { return o.IfMatch != "" || o.IfNoneMatch != "" },
				"IsConditional must report true when IfMatch or IfNoneMatch is set",
				prop.Example(version.WriteOptions{}),
				prop.Example(version.WriteOptions{IfMatch: "abc"}),
				prop.Example(version.WriteOptions{IfNoneMatch: version.Wildcard}),
				prop.Example(version.WriteOptions{IfMatch: "abc", IfNoneMatch: "def"}),
			)
		})
	})
}

// TestWriteOptionsAllocs checks that IsConditional does not allocate.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestWriteOptionsAllocs(t *testing.T) {
	t.Run("IsConditional", func(t *testing.T) {
		opts := version.WriteOptions{IfMatch: "v1"}

		var got bool
		expect.MaxAllocs(t, func() { got = opts.IsConditional() }, 0, "IsConditional must not allocate")
		assert.True(t, got, "the test must measure a conditional write")
	})
}

func BenchmarkWriteOptions(b *testing.B) {
	b.Run("IsConditional", func(b *testing.B) {
		opts := version.WriteOptions{IfMatch: "v1"}

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = opts.IsConditional()
		}

		assert.True(b, got, "the benchmark must measure a conditional write")
	})
}
