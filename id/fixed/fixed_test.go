// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/fixed"
)

// fixedSampleID is the canonical configured value used by the
// SUT factory. Reused across the contract suite + impl-specific
// subtests so failure messages reference one well-known fixture.
var fixedSampleID = id.New128([id.Size128]byte{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
})

func newFixed() id.Generator { return fixed.New(fixedSampleID) }

// --- testkit-driven contract layer ---

// fixed.Generator deliberately returns the same value on every
// call — opt out of the distinctness assertion, opt INTO the
// reproducibility assertion.
func TestFixedGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newFixed,
		append(idtest.GeneratorAllowZeroAndDuplicates(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func BenchmarkFixedGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newFixed,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// --- fixed-specific tests ---

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Generator returns Zero", func(t *testing.T) {
		t.Parallel()
		var g fixed.Generator
		testkit.True(t, g.Generate().IsZero(),
			"zero-value Generator must return id.Zero")
	})

	t.Run("Generate returns the configured value", func(t *testing.T) {
		t.Parallel()
		g := fixed.New(fixedSampleID)
		testkit.Equal(t, g.Generate(), fixedSampleID,
			"Generate must return the configured value")
	})

	t.Run("works with all three sizes", func(t *testing.T) {
		t.Parallel()
		w128 := id.New128([id.Size128]byte{1})
		w160 := id.New160([id.Size160]byte{2})
		w256 := id.New256([id.Size256]byte{3})

		testkit.Equal(t, fixed.New(w128).Generate().Size(), id.Size128,
			"fixed.New on a 128-bit ID must produce a 128-bit Generator")
		testkit.Equal(t, fixed.New(w160).Generate().Size(), id.Size160,
			"fixed.New on a 160-bit ID must produce a 160-bit Generator")
		testkit.Equal(t, fixed.New(w256).Generate().Size(), id.Size256,
			"fixed.New on a 256-bit ID must produce a 256-bit Generator")
	})
}
