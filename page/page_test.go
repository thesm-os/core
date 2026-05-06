// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"testing"

	"go.thesmos.sh/core/page"
)

func TestPageIsFirst(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   page.Page
		want bool
	}{
		"empty token is first":    {page.Page{}, true},
		"limit-only is first":     {page.Page{Limit: 10}, true},
		"with token is not first": {page.Page{Token: "abc"}, false},
		"with token and limit":    {page.Page{Token: "abc", Limit: 10}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsFirst(); got != tc.want {
				t.Fatalf("IsFirst(%+v): got %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPageWithDefault(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   page.Page
		def  int
		want int
	}{
		"unset Limit picks up the default":         {page.Page{}, 50, 50},
		"negative Limit picks up the default":      {page.Page{Limit: -1}, 50, 50},
		"positive Limit passes through":            {page.Page{Limit: 10}, 50, 10},
		"large positive passes through":            {page.Page{Limit: 1000}, 50, 1000},
		"unset Limit with zero default stays at 0": {page.Page{}, 0, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := tc.in.WithDefault(tc.def)
			if got.Limit != tc.want {
				t.Fatalf("WithDefault: got Limit=%d, want %d",
					got.Limit, tc.want)
			}
			if got.Token != tc.in.Token {
				t.Fatalf("Token mutated: got %q, want %q",
					got.Token, tc.in.Token)
			}
		})
	}

	t.Run("does not mutate the receiver", func(t *testing.T) {
		t.Parallel()
		orig := page.Page{Token: "tok"}
		_ = orig.WithDefault(100)
		if orig.Limit != 0 {
			t.Fatalf("receiver mutated: got Limit=%d, want 0", orig.Limit)
		}
	})
}
