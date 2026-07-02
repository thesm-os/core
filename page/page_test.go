// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

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
			testkit.Equal(t, tc.in.IsFirst(), tc.want, "IsFirst must reflect token presence")
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
			testkit.Equal(t, got.Limit, tc.want, "WithDefault.Limit must match expected")
			testkit.Equal(t, got.Token, tc.in.Token, "WithDefault must not mutate Token")
		})
	}

	t.Run("does not mutate the receiver", func(t *testing.T) {
		t.Parallel()
		orig := page.Page{Token: "tok"}
		_ = orig.WithDefault(100)
		testkit.Equal(t, orig.Limit, 0, "WithDefault must not mutate the receiver")
	})
}

func BenchmarkIsFirst(b *testing.B) {
	p := page.Page{Token: "page-2"}
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = p.IsFirst()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkWithDefault(b *testing.B) {
	p := page.Page{}
	b.ReportAllocs()
	var sink page.Page
	for b.Loop() {
		sink = p.WithDefault(50)
	}
	runtime.KeepAlive(sink)
}
