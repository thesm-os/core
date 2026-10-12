// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap_test

import "go.thesmos.sh/core/idmap"

// The names of the actions of the machines, and of the calls that the
// histories record.
const (
	opGet    = "Get"
	opSet    = "Set"
	opDelete = "Delete"
	opClear  = "Clear"
	opClone  = "Clone"
	opAll    = "All"
	opLoad   = "Load"
	opStore  = "Store"
)

// keyOf returns the key number i of a test: i/4 in its first 8 bytes,
// little-endian, and i%4 in its last byte. Four consecutive keys differ in
// their last byte alone, which is past the first 16 bytes of an ID160 or an
// ID256 key.
func keyOf[K idmap.Key](i int) K {
	var k K
	for b := range 8 {
		k[b] = byte(i / 4 >> (8 * b))
	}

	k[len(k)-1] = byte(i % 4)

	return k
}
