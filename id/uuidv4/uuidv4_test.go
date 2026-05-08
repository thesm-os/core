// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// idFromBytes wraps a 16-byte literal as a 128-bit [id.ID].
// Lives in this file so both uuidv4_test.go and parse_test.go
// can use it (same _test package).
func idFromBytes(b ...byte) id.ID {
	var raw [id.Size128]byte
	copy(raw[:], b)
	return id.New128(raw)
}

// newUUIDv4 is the SUT factory for the testkit-driven contract
// suite.
func newUUIDv4() id.Generator {
	return uuidv4.New(seeded.New(rand.Seed(1)))
}

// --- testkit-driven contract layer ---

func TestUUIDv4GeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newUUIDv4,
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func TestUUIDv4GeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, newUUIDv4)
}

func FuzzUUIDv4GeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, newUUIDv4)
}

func BenchmarkUUIDv4Generator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newUUIDv4,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// --- uuidv4-specific tests ---

func TestGeneratorGenerate(t *testing.T) {
	t.Parallel()

	rng := seeded.New(rand.Seed(1))
	g := uuidv4.New(rng)

	t.Run("stamps version-4 in byte 6 high nibble", func(t *testing.T) {
		t.Parallel()
		b := g.Generate().Bytes()
		testkit.Equal(t, b[6]&0xF0, byte(0x40),
			"byte 6 high nibble must encode UUID version 4")
	})

	t.Run("stamps variant-10 in byte 8 high bits", func(t *testing.T) {
		t.Parallel()
		b := g.Generate().Bytes()
		testkit.Equal(t, b[8]&0xC0, byte(0x80),
			"byte 8 high bits must encode RFC 4122 variant (10xx)")
	})
}

func TestGeneratorDeterministic(t *testing.T) {
	t.Parallel()

	a := uuidv4.New(seeded.New(rand.Seed(42)))
	b := uuidv4.New(seeded.New(rand.Seed(42)))

	for range 8 {
		testkit.Equal(t, a.Generate(), b.Generate(),
			"identical seeds must produce identical UUIDs")
	}
}
