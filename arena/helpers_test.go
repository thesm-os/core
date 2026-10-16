// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"iter"

	"go.thesmos.sh/core/arena"
)

// fill is the byte that a case writes into the memory of an arena, a Slabs,
// a RefSlabs or a Bump, to tell a written byte from a zero one.
const fill = 0xa5

// slabSize is the size of the slabs of most cases of a Slabs and a
// RefSlabs: room for two allocations of the class 512.
const slabSize = 1024

// sizeClasses contains the size classes of a Slabs and a RefSlabs up to
// 2^30 bytes in ascending order. The function builds them from the
// definition of the classes. The first class is MinClass. Above each power
// of two from MinClass up, the classes are the power plus one to eight times
// an eighth of the power. The class of n is the first class that is at least
// n.
var sizeClasses = func() []int {
	c := []int{arena.MinClass}
	for p := arena.MinClass; p < 1<<30; p *= 2 {
		for k := 1; k <= 8; k++ {
			c = append(c, p+k*(p/8))
		}
	}

	return c
}()

// slabbed is the method that a Slabs and a RefSlabs share: All, which
// yields every slab in the order of allocation.
type slabbed interface {
	All() iter.Seq[[]byte]
}

// slabLens returns the length of every slab of s, in the order of All.
func slabLens(s slabbed) []int {
	var lens []int
	for slab := range s.All() {
		lens = append(lens, len(slab))
	}

	return lens
}

// slabsWith returns the positions, in the order of All, of the slabs of s
// that contain the byte v.
func slabsWith(s slabbed, v byte) []int {
	var at []int

	i := 0
	for slab := range s.All() {
		if bytes.IndexByte(slab, v) != -1 {
			at = append(at, i)
		}

		i++
	}

	return at
}
