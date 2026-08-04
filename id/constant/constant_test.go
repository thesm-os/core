// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package constant_test

import (
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/constant"
)

// constantSampleID is the canonical configured value used by the
// SUT factory. Reused across the contract suite + impl-specific
// subtests so failure messages reference one well-known fixture.
var constantSampleID = id.New128([id.Size128]byte{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
})

func newConstant() id.Generator { return constant.New(constantSampleID) }

// --- testkit-driven contract layer ---

// constant.Generator deliberately returns the same value on every
// call — opt out of the distinctness assertion, opt INTO the
// reproducibility assertion.
func TestConstantGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newConstant,
		append(idtest.GeneratorAllowZeroAndDuplicates(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func BenchmarkConstantGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newConstant,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// --- constant-specific tests ---

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Generator returns Zero", func(t *testing.T) {
		t.Parallel()
		var g constant.Generator
		testkit.True(t, g.Generate().IsZero(),
			"zero-value Generator must return id.Zero")
	})

	t.Run("Generate returns the configured value", func(t *testing.T) {
		t.Parallel()
		g := constant.New(constantSampleID)
		testkit.Equal(t, g.Generate(), constantSampleID,
			"Generate must return the configured value")
	})

	t.Run("works with all three sizes", func(t *testing.T) {
		t.Parallel()
		w128 := id.New128([id.Size128]byte{1})
		w160 := id.New160([id.Size160]byte{2})
		w256 := id.New256([id.Size256]byte{3})

		testkit.Equal(t, constant.New(w128).Generate().Size(), id.Size128,
			"constant.New on a 128-bit ID must produce a 128-bit Generator")
		testkit.Equal(t, constant.New(w160).Generate().Size(), id.Size160,
			"constant.New on a 160-bit ID must produce a 160-bit Generator")
		testkit.Equal(t, constant.New(w256).Generate().Size(), id.Size256,
			"constant.New on a 256-bit ID must produce a 256-bit Generator")
	})
}
