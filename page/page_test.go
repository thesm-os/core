// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package page_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/page"
)

// defaultLimit is the default that the WithDefault cases apply.
const defaultLimit = 50

func TestPage(t *testing.T) {
	t.Parallel()

	t.Run("IsFirst", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the token is empty", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, page.Page.IsFirst, func(p page.Page) bool { return p.Token == "" },
				"IsFirst must report a request without a token",
				prop.Example(page.Page{}), prop.Example(page.Page{Limit: 10}),
				prop.Example(page.Page{Token: "abc"}), prop.Example(page.Page{Token: "abc", Limit: 10}))
		})
	})

	t.Run("WithDefault", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces a Limit of zero or less with the default", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(p page.Page) page.Page { return p.WithDefault(defaultLimit) },
				func(p page.Page) page.Page {
					if p.Limit > 0 {
						return p
					}

					return page.Page{Token: p.Token, Limit: defaultLimit}
				}, "WithDefault must keep a positive Limit and the Token, and replace any other Limit",
				prop.Example(page.Page{}), prop.Example(page.Page{Limit: -1}), prop.Example(page.Page{Limit: 10}),
				prop.Example(page.Page{Token: "tok", Limit: 1000}))
		})

		t.Run("keeps a Limit of zero for a default of zero", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, page.Page{}.WithDefault(0), page.Page{}, "a zero default must leave the Limit at zero")
		})

		t.Run("leaves the receiver unchanged", func(t *testing.T) {
			t.Parallel()
			orig := page.Page{Token: "tok"}
			assert.Pure(t, func() page.Page { return orig }, func() { _ = orig.WithDefault(100) },
				"WithDefault must return a copy")
		})
	})
}

// TestPageAllocs checks the allocation contract of each method of Page.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestPageAllocs(t *testing.T) {
	p := page.Page{Token: "page-2"}

	t.Run("IsFirst", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = p.IsFirst() }, 0, "IsFirst must not allocate")
		assert.False(t, got, "the test must measure a request with a token")
	})

	t.Run("WithDefault", func(t *testing.T) {
		var got page.Page
		expect.MaxAllocs(t, func() { got = p.WithDefault(defaultLimit) }, 0, "WithDefault must not allocate")
		assert.Equal(t, got.Limit, defaultLimit, "the test must measure a request that takes the default")
	})
}

// BenchmarkPage reports the cost of each method of Page, and fails when a
// method allocates.
func BenchmarkPage(b *testing.B) {
	p := page.Page{Token: "page-2"}

	b.Run("IsFirst", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = p.IsFirst()
		}

		assert.False(b, got, "the benchmark must measure a request with a token")
	})

	b.Run("WithDefault", func(b *testing.B) {
		var got page.Page

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = p.WithDefault(defaultLimit)
		}

		assert.Equal(b, got.Limit, defaultLimit, "the benchmark must measure a request that takes the default")
	})
}
