// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package constant_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/constant"
)

// constantSampleID is the configured value of the Generator of the
// contract suite.
var constantSampleID = id.New128([id.Size128]byte{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
})

// validBytes generates byte strings of the three lengths of an ID.
var validBytes = prop.SampledFrom(id.Size128, id.Size160, id.Size256).Bind(func(n int) prop.Generator[[]byte] {
	return prop.Bytes(prop.MinSize(n), prop.MaxSize(n))
})

// TestConstantGeneratorContract runs the contract suite of id.Generator. A
// constant Generator returns the same value on every call, so it opts out
// of the distinctness assertion.
func TestConstantGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, func() id.Generator { return constant.New(constantSampleID) },
		append(idtest.GeneratorAllowZeroAndDuplicates(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("Generate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns Zero for the zero Generator", func(t *testing.T) {
			t.Parallel()
			var g constant.Generator
			assert.True(t, g.Generate().IsZero(), "the zero Generator must return id.Zero")
		})

		t.Run("returns the value of New", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(b []byte) id.ID {
				v, _ := id.FromBytes(b)

				return constant.New(v).Generate()
			}, func(b []byte) id.ID {
				v, _ := id.FromBytes(b)

				return v
			}, "Generate must return the value of New", prop.Using(validBytes))
		})
	})
}

// TestGeneratorAllocs checks the allocation contract of Generate.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestGeneratorAllocs(t *testing.T) {
	g := constant.New(constantSampleID)

	t.Run("Generate", func(t *testing.T) {
		var got id.ID
		expect.MaxAllocs(t, func() { got = g.Generate() }, 0, "Generate must not allocate")
		assert.Equal(t, got, constantSampleID, "the test must measure the configured value")
	})
}

// BenchmarkConstantGenerator runs the benchmarks of the contract suite of
// id.Generator.
func BenchmarkConstantGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, func() id.Generator { return constant.New(constantSampleID) },
		idtest.GeneratorBenchOnGenerate(testkitbench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// BenchmarkGenerator reports the cost of Generate, and fails when it
// allocates.
func BenchmarkGenerator(b *testing.B) {
	b.Run("Generate", func(b *testing.B) {
		g := constant.New(constantSampleID)
		var got id.ID

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = g.Generate()
		}

		assert.Equal(b, got, constantSampleID, "the benchmark must measure the configured value")
	})
}
