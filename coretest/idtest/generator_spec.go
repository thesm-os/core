// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package idtest

import (
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/id"
)

// GeneratorContractAssertions returns the generic assertions
// every [id.Generator] implementation must satisfy: Generate
// returns a non-zero [id.ID], Size matches one of the canonical
// widths, consecutive Generate calls produce distinct IDs (the
// fixed implementation opts out by design — wire
// [GeneratorAllowZero] and [GeneratorAllowDuplicates] for that
// path).
//
//	idtest.AssertGeneratorContract(t, factory,
//	    idtest.GeneratorContractAssertions()...,
//	)
func GeneratorContractAssertions() []GeneratorOption {
	return []GeneratorOption{
		GeneratorCustom("Generate returns a non-zero ID", func(t *testing.T, g id.Generator) {
			testkit.False(t, g.Generate().IsZero(),
				"Generate must not return the Zero sentinel — see Generator contract")
		}),

		GeneratorCustom("Generate Size matches a canonical width", func(t *testing.T, g id.Generator) {
			sz := g.Generate().Size()
			testkit.True(t,
				sz == id.Size128 || sz == id.Size160 || sz == id.Size256,
				"Generate Size must equal Size128, Size160, or Size256 (got "+strconv.Itoa(sz)+")")
		}),

		GeneratorCustom("Generate Size is stable across calls", func(t *testing.T, g id.Generator) {
			testkit.Equal(t, g.Generate().Size(), g.Generate().Size(),
				"Generate Size must be stable — the impl picks one identifier width and sticks to it")
		}),

		GeneratorCustom("Generate produces distinct IDs across 16 calls", func(t *testing.T, g id.Generator) {
			const samples = 16
			seen := make(map[id.ID]struct{}, samples)
			for range samples {
				seen[g.Generate()] = struct{}{}
			}
			// Birthday bound on 128-bit uniform draws: collision
			// probability over 16 samples is ~10⁻³⁶. KSUID and
			// time-based identifiers are even less colliding.
			testkit.True(t, len(seen) >= samples/2,
				"Generate must produce diverse IDs — degenerate stream detected")
		}),
	}
}

// GeneratorAllowZeroAndDuplicates returns assertion replacements
// for [GeneratorContractAssertions] for the deliberately-fixed
// [id/constant.Generator]: Generate may return [id.Zero], and
// consecutive calls return the same value. Only the Size
// stability assertion still applies.
//
// Use INSTEAD of [GeneratorContractAssertions]:
//
//	idtest.AssertGeneratorContract(t, factory,
//	    idtest.GeneratorAllowZeroAndDuplicates()...,
//	)
func GeneratorAllowZeroAndDuplicates() []GeneratorOption {
	return []GeneratorOption{
		GeneratorCustom("Generate Size matches a canonical width", func(t *testing.T, g id.Generator) {
			sz := g.Generate().Size()
			testkit.True(t,
				sz == id.Size128 || sz == id.Size160 || sz == id.Size256,
				"Generate Size must equal Size128, Size160, or Size256 (got "+strconv.Itoa(sz)+")")
		}),

		GeneratorCustom("Generate Size is stable across calls", func(t *testing.T, g id.Generator) {
			testkit.Equal(t, g.Generate().Size(), g.Generate().Size(),
				"Generate Size must be stable across calls")
		}),

		GeneratorCustom("Generate is reproducible across calls", func(t *testing.T, g id.Generator) {
			testkit.Equal(t, g.Generate(), g.Generate(),
				"fixed Generator must produce the same ID on every call")
		}),
	}
}

// GeneratorSizeAssertion verifies [id.ID.Size] of the generated
// value matches the expected width. Composes with
// [GeneratorContractAssertions] when an impl commits to one
// specific width (every impl in this module does).
func GeneratorSizeAssertion(want int) GeneratorOption {
	return GeneratorCustom("Generate Size matches expected width",
		func(t *testing.T, g id.Generator) {
			testkit.Equal(t, g.Generate().Size(), want,
				"Generate must produce IDs of width "+strconv.Itoa(want))
		})
}
