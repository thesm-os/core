// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"encoding/binary"
	"math/bits"
	"slices"
)

// The limits of a build.
const (
	// maxSeeds is the number of seeds that Append tries before it returns
	// ErrBuild.
	maxSeeds = 1024
	// maxFingerprints is the largest number of fingerprints of a filter, so a
	// position fits 32 bits in the scratch of a builder.
	maxFingerprints = 1 << 32
	// minHalving and maxHalving bound the number of hashes of a set whose
	// build halves the segment length on every fourth attempt.
	minHalving = 5
	maxHalving = 999_999
	// minCapacityFactor is 1,024 times the size factor of a set of 2^20
	// hashes or more.
	minCapacityFactor = 1152
)

// exponentThresholds[k-2] is the smallest number of hashes whose segment
// exponent is k, for k from 2 to 18: the smallest n with
// ⌊ln n / ln 3.33 + 2.25⌋ ≥ k. A table of integers gives every platform the
// same exponents, because math.Log runs assembly on amd64 and Go code
// elsewhere.
var exponentThresholds = [...]int{
	1, 3, 9, 28, 92, 304, 1010, 3362, 11193, 37273, 124118, 413310, 1376322, 4583150, 15261887, 50822082,
	169237530,
}

// capacityFactors[b-2] is 1,024 times the size factor of a set of hashes
// whose number has the bit length b, for b from 2 to 20: 1,024 times
// max(1.125, 0.875 + 0.25 * ln 10^6 / ln m) for the smallest m of that bit
// length, rounded up. A larger set takes minCapacityFactor.
var capacityFactors = [...]int{
	5999, 3448, 2597, 2172, 1917, 1747, 1625, 1534, 1463, 1407, 1360, 1322, 1289, 1261, 1237, 1215, 1197, 1180, 1165,
}

// FuseBuilder builds binary fuse filters. It keeps its scratch memory between
// builds, so a builder that built a set of n hashes builds another set of up
// to n hashes without an allocation. The zero FuseBuilder is ready to use.
//
// A build mixes each hash with a seed and takes three positions in three
// consecutive segments of fingerprints for it. It then peels the set: it
// repeatedly removes a hash that is the only one at one of its positions, and
// assigns the fingerprints in the reverse order of the removals. When a seed
// leaves a hash without a removable position, the build sorts the hashes,
// removes the repeated ones and tries the next seed of a splitmix64 sequence
// that starts at 1.
//
// # Concurrency
//
// A FuseBuilder is not safe for concurrent use.
//
// # Allocation contract
//
// Append allocates nothing when dst has room for the encoding and the builder
// built at least as many hashes before. Otherwise it allocates the growth of
// dst and of its scratch, about 34 bytes per hash.
type FuseBuilder struct {
	keys   []uint64 // a copy of the hashes of the set
	order  []uint64 // the mixed hashes by their first segment, then in the order of their removal
	which  []uint8  // for each removed hash, which of its positions it was alone at: 0, 1 or 2
	count  []uint8  // per position, the number of its hashes << 2 | the xor of their indexes 0, 1 and 2
	xors   []uint64 // per position, the exclusive or of its mixed hashes
	queue  []uint32 // the positions that one hash occupies alone
	fps    []uint16 // per position, its fingerprint
	starts []uint32 // per segment, the start of its hashes in order
}

// Append builds a binary fuse filter of hashes with fingerprints of bits
// bits, appends its encoding to dst and returns the extended slice. Append
// does not modify hashes, and a hash that hashes contains more than once
// counts once. A build produces the same bytes for the same hashes and the
// same width on every platform.
//
// Error modes:
//
//   - A bits other than 4, 8, 10, 12 or 16 returns [ErrFingerprintBits].
//   - More hashes than fit 2^32 fingerprints returns [ErrTooLarge].
//   - A set that none of 1,024 seeds peels returns [ErrBuild].
//
// Append returns dst unchanged with an error.
func (b *FuseBuilder) Append(dst []byte, hashes []uint64, bits int) ([]byte, error) {
	g, err := newGeometry(len(hashes), bits)
	if err != nil {
		return dst, err
	}
	if len(hashes) == 0 {
		return appendEncoding(dst, bits, g, 0, nil), nil
	}
	b.keys = append(b.keys[:0], hashes...)
	// A halving attempt has twice the segments and two more.
	b.starts = slices.Grow(b.starts[:0], 2*int(g.segments)+3)
	state := uint64(1)
	for attempt := 1; attempt <= maxSeeds; attempt++ {
		seed := splitmix64(&state)
		a := g.forAttempt(attempt, len(hashes))
		stack, removed := b.peel(a, seed)
		if stack+removed == len(b.keys) {
			return appendEncoding(dst, bits, a, seed, b.assign(a, bits, stack)), nil
		}
		// No seed peels two equal hashes, so a failed attempt removes the
		// repeated hashes that its peel did not leave out.
		slices.Sort(b.keys)
		b.keys = slices.Compact(b.keys)
	}

	return dst, ErrBuild
}

// peel orders the hashes of b.keys by their first segment under the geometry
// g and the seed, counts the hashes of each position, and removes the hashes
// one position at a time. It returns the number of removed hashes, which are
// in b.order with their positions in b.which, and the number of repeated
// hashes that it left out. It does not allocate when the scratch has room.
//
// A hash that repeats a hash which has its three positions alone with it is
// left out. A position of more than 63 hashes ends the peel with no hash
// removed and none left out, because its count has 6 bits.
func (b *FuseBuilder) peel(g geometry, seed uint64) (stack, removed int) {
	segments, span, segment := int(g.segments), uint64(g.segments)<<g.exponent, uint64(1)<<g.exponent
	total := (segments + 2) << g.exponent

	starts := resize(b.starts, segments+1)
	for _, k := range b.keys {
		p0, _ := bits.Mul64(murmur64(k+seed), span)
		starts[p0>>g.exponent+1]++
	}
	for s := 1; s < len(starts); s++ {
		starts[s] += starts[s-1]
	}
	order := resize(b.order, len(b.keys))
	for _, k := range b.keys {
		x := murmur64(k + seed)
		p0, _ := bits.Mul64(x, span)
		s := p0 >> g.exponent
		order[starts[s]] = x
		starts[s]++
	}

	count, xors := resize(b.count, total), resize(b.xors, total)
	b.starts, b.order, b.count, b.xors = starts, order, count, xors
	for _, x := range order {
		p0, p1, p2 := positions(x, segment, span)
		count[p0] += 4
		xors[p0] ^= x
		count[p1] += 4
		count[p1] ^= 1
		xors[p1] ^= x
		count[p2] += 4
		count[p2] ^= 2
		xors[p2] ^= x
		if xors[p0] == 0 && count[p0] == 8 || xors[p1] == 0 && count[p1] == 8 || xors[p2] == 0 && count[p2] == 8 {
			count[p0] -= 4
			xors[p0] ^= x
			count[p1] -= 4
			count[p1] ^= 1
			xors[p1] ^= x
			count[p2] -= 4
			count[p2] ^= 2
			xors[p2] ^= x
			removed++
		}
		if count[p0] < 4 ||
			//dokimi:mutate-skip ror-boundary: a count of 4 is one hash of the index 0, and x has the index 1 at p1
			count[p1] < 4 ||
			//dokimi:mutate-skip ror-boundary: a count of 4 is one hash of the index 0, and x has the index 2 at p2
			count[p2] < 4 {

			return 0, 0
		}
	}

	queue := resize(b.queue, total)[:0]
	for p, c := range count {
		if c>>2 == 1 {
			queue = append(queue, uint32(p))
		}
	}
	which := resize(b.which, len(b.keys))
	b.which = which
	for len(queue) > 0 {
		p := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if count[p]>>2 != 1 {
			continue
		}
		x, w := xors[p], count[p]&3
		order[stack], which[stack] = x, w
		stack++
		var ps [3]uint64
		ps[0], ps[1], ps[2] = positions(x, segment, span)
		for j := range uint8(3) {
			q := ps[j]
			//dokimi:mutate-skip sbr-delete,ror-false: x was alone at the position w, so no later pop finds a hash there
			if j == w {
				continue
			}
			count[q] -= 4
			count[q] ^= j
			xors[q] ^= x
			if count[q]>>2 == 1 {
				queue = append(queue, uint32(q)) //nolint:gosec // G115: a position is below 2^32 fingerprints
			}
		}
	}
	b.queue = queue

	return stack, removed
}

// assign sets the fingerprints of width bits of the stack hashes that peel
// removed under the geometry g, in the reverse order of the removals, and
// returns them. A hash sets the fingerprint at the position where it was
// alone, so the fingerprints at its three positions combine to its own by
// exclusive or. It does not allocate when the scratch has room.
func (b *FuseBuilder) assign(g geometry, width, stack int) []uint16 {
	span, segment := uint64(g.segments)<<g.exponent, uint64(1)<<g.exponent
	fps := resize(b.fps, (int(g.segments)+2)<<g.exponent)
	b.fps = fps
	mask := uint64(1)<<width - 1
	for i := stack - 1; i >= 0; i-- {
		x, w := b.order[i], b.which[i]
		var ps [3]uint64
		ps[0], ps[1], ps[2] = positions(x, segment, span)
		fp := uint16((x ^ x>>32) & mask) //nolint:gosec // G115: the mask keeps at most 16 bits
		fps[ps[w]] = fp ^ fps[ps[(w+1)%3]] ^ fps[ps[(w+2)%3]]
	}

	return fps
}

// geometry sets the size of a fuse filter: segments + 2 segments of
// 2^exponent fingerprints. A filter of no hashes has no segments.
type geometry struct {
	exponent uint8
	segments uint32
}

// newGeometry returns the geometry of a filter of n hashes with fingerprints
// of width bits, from the tables of segment exponents and capacities. It does
// not allocate.
//
// Error modes:
//
//   - A width other than 4, 8, 10, 12 or 16 returns ErrFingerprintBits.
//   - More hashes than fit 2^32 fingerprints returns ErrTooLarge.
func newGeometry(n, width int) (geometry, error) {
	switch width {
	case 4, 8, 10, 12, 16:
	default:
		return geometry{}, ErrFingerprintBits
	}
	//dokimi:mutate-skip ror-boundary: the capacity of 2^32 hashes is more than 2^32 fingerprints, which the check below refuses
	if uint64(n) > maxFingerprints { //nolint:gosec // G115: n is a length, so it is not negative
		return geometry{}, ErrTooLarge
	}
	if n == 0 {
		return geometry{exponent: minExponent}, nil
	}
	e := uint8(minExponent)
	for e < maxExponent && exponentThresholds[e+1-minExponent] <= n {
		e++
	}
	capacity := uint64(0)
	if n > 1 {
		q := minCapacityFactor
		if b := bits.Len64(uint64(n)); b-2 < len(capacityFactors) {
			q = capacityFactors[b-2]
		}
		capacity = (uint64(n)*uint64(q) + 1023) / 1024
	}
	total := max((capacity+1<<e-1)>>e, 3)
	if total<<e > maxFingerprints {
		return geometry{}, ErrTooLarge
	}

	return geometry{exponent: e, segments: uint32(total - 2)}, nil //nolint:gosec // G115: total<<e fits 32 bits
}

// forAttempt returns the geometry of the attempt numbered attempt, from 1, of
// a build of n hashes. From the second attempt on, every fourth attempt for 5
// to 999,999 hashes halves the segment length and doubles the segments plus
// two, as the authors' implementation does, which keeps the number of
// fingerprints.
func (g geometry) forAttempt(attempt, n int) geometry {
	if n >= minHalving && n <= maxHalving && attempt%4 == 2 {
		return geometry{exponent: g.exponent - 1, segments: 2*g.segments + 2}
	}

	return g
}

// appendEncoding appends the encoding of a filter of the geometry g, the seed
// and the fingerprints fps of width bits to dst. It packs the fingerprints
// from the most significant bit of the first byte, and grows dst once.
func appendEncoding(dst []byte, width int, g geometry, seed uint64, fps []uint16) []byte {
	dst = slices.Grow(dst, headerLen+len(fps)*width/8+padLen)
	dst = append(dst, fuseVersion, byte(width), g.exponent) //nolint:gosec // G115: a width is at most 16
	dst = binary.BigEndian.AppendUint32(dst, g.segments)
	dst = binary.BigEndian.AppendUint64(dst, seed)
	var acc uint64
	pending := 0
	for _, fp := range fps {
		acc = acc<<width | uint64(fp)
		pending += width
		for pending >= 8 {
			pending -= 8
			dst = append(dst, byte(acc>>pending)) //nolint:gosec // G115: the conversion keeps the low eight bits
		}
	}

	return append(dst, 0, 0, 0)
}

// resize returns s with length n and every element zero. It reuses the array
// of s when the array has room, and does not allocate then.
func resize[T uint8 | uint16 | uint32 | uint64](s []T, n int) []T {
	s = slices.Grow(s[:0], n)[:n]
	clear(s)

	return s
}

// splitmix64 advances the state of a splitmix64 sequence and returns its next
// value.
func splitmix64(state *uint64) uint64 {
	*state += 0x9E3779B97F4A7C15
	z := *state
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB

	return z ^ z>>31
}
