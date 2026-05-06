// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

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
			if got := tc.in.IsConditional(); got != tc.want {
				t.Fatalf("IsConditional: got %v, want %v", got, tc.want)
			}
		})
	}
}
