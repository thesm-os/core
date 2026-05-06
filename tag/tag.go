// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag

// Tag is a string key/value pair.
//
// # Allocation contract
//
// Value type; pass by value. Comparable.
type Tag struct {
	Key   string
	Value string
}

// IsZero reports whether t is the zero [Tag] (both fields empty).
func (t Tag) IsZero() bool {
	return t.Key == "" && t.Value == ""
}
