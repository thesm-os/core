// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/version"
)

func TestWriteOptionsIsConditional(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   version.WriteOptions
		want bool
	}{
		"zero value is not conditional": {
			version.WriteOptions{}, false,
		},
		"IfMatch set is conditional": {
			version.WriteOptions{IfMatch: "abc"}, true,
		},
		"IfNoneMatch wildcard is conditional": {
			version.WriteOptions{IfNoneMatch: version.Wildcard}, true,
		},
		"both set is conditional": {
			version.WriteOptions{IfMatch: "abc", IfNoneMatch: "def"}, true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.in.IsConditional(), tc.want,
				"IsConditional must reflect IfMatch/IfNoneMatch presence")
		})
	}
}

func BenchmarkIsConditional(b *testing.B) {
	opts := version.WriteOptions{IfMatch: "v1"}
	b.ReportAllocs()
	for b.Loop() {
		_ = opts.IsConditional()
	}
}
