// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter_test

import (
	"math"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/filter"
)

// widths are the widths of fingerprints that a fuse filter supports.
var widths = []int{4, 8, 10, 12, 16}

// anyHash generates hashes over the whole 64-bit range.
var anyHash = prop.Integer[uint64](0, math.MaxUint64)

// sets generates sets of up to 3,000 hashes. One element in two comes from a
// domain of 65 hashes, so a set often contains a hash more than once.
var sets = prop.List(prop.OneOf(prop.Integer[uint64](0, 64), anyHash), prop.MaxSize(3000))

// hashes returns n hashes of the splitmix64 sequence that starts at seed.
// The sequence is the same on every platform, so a golden file can pin the
// encoding of a filter of them. hashes(1, 1) returns the seed of the first
// attempt of a FuseBuilder.
func hashes(n int, seed uint64) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		seed += 0x9E3779B97F4A7C15
		z := seed
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		out[i] = z ^ z>>31
	}

	return out
}

// build returns the encoding of a fuse filter of hs with fingerprints of
// width bits, from a new builder.
func build(tb assert.TB, hs []uint64, width int) []byte {
	tb.Helper()

	var b filter.FuseBuilder
	enc, err := b.Append(nil, hs, width)
	assert.NoError(tb, err, "Append builds a filter of the set")

	return enc
}

// parse returns the filter over enc.
func parse(tb assert.TB, enc []byte) filter.Fuse {
	tb.Helper()

	f, err := filter.ParseFuse(enc)
	assert.NoError(tb, err, "ParseFuse accepts the encoding that Append builds")

	return f
}
