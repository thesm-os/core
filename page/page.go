// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package page

// Page describes a request for one page of results from a
// paginated API.
//
// Every list-returning interface accepts [Page] and returns a
// [Cursor], so unbounded scans are impossible to write
// accidentally.
//
// # Field semantics
//
//   - [Page.Limit]: maximum number of items to return.
//     Implementations may enforce their own ceiling regardless of
//     [Page.Limit] (typically to prevent unbounded scans).
//     [Page.Limit] <= 0 means "use the implementation's default."
//   - [Page.Token]: the opaque continuation cursor returned by a
//     previous response ([Cursor.NextPage]). Empty for the first
//     page.
//
// # Allocation contract
//
// Value type; pass by value.
type Page struct {
	Token string
	Limit int
}

// IsFirst reports whether p is a first-page request (Token empty).
func (p Page) IsFirst() bool {
	return p.Token == ""
}

// WithDefault returns a copy of p with [Page.Limit] replaced by
// def when the original Limit is unset (≤ 0). Implementations
// call this at the top of a paginated handler to apply a uniform
// default:
//
//	const defaultPageSize = 50
//	p = p.WithDefault(defaultPageSize)
//
// A positive Limit passes through unchanged; def is ignored when
// p.Limit > 0.
//
// # Allocation contract
//
// Zero alloc.
func (p Page) WithDefault(def int) Page {
	if p.Limit <= 0 {
		p.Limit = def
	}
	return p
}
