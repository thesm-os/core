// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package constant_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
)

// value is the configured value of the Rands that the tests do not
// draw.
const value uint64 = 0x0102030405060708

// justBelowOne is the largest float64 below 1, the value at which
// FromFloat64 clamps an input of 1 or more.
const justBelowOne = 1.0 - 1.0/(1<<53)

// TestConstantRandContract runs the contract suite of rand.Rand.
// constant.Rand returns the same value forever, so the suite runs
// without its distinctness assertion.
func TestConstantRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, func() rand.Rand { return constant.New(value) },
		randtest.RandContractAssertions()...)
}

func TestRand(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Rand whose Uint64 returns the value", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(v uint64) uint64 { return constant.New(v).Uint64() },
				func(v uint64) uint64 { return v }, "Uint64 must return the value that New received",
				prop.Using(prop.Integer[uint64](0, math.MaxUint64)), prop.Example(uint64(0xDEADBEEFCAFEBABE)))
		})
	})

	t.Run("FromFloat64", func(t *testing.T) {
		t.Parallel()

		// FromFloat64 inverts the (uint64 >> 11) / 2^53 construction of
		// rand.Float64, so every multiple of 2^-53 in [0, 1) returns.
		t.Run("returns a Rand whose Float64 returns v for a multiple of 2^-53", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(k uint64) float64 { return rand.Float64(constant.FromFloat64(float64(k) / (1 << 53))) },
				func(k uint64) float64 { return float64(k) / (1 << 53) }, "Float64 must return v exactly",
				prop.Using(prop.Integer[uint64](0, 1<<53-1)), prop.Example(uint64(0)), prop.Example(uint64(1<<52)))
		})

		t.Run("returns a Rand whose Float64 returns 0 for a negative v", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(v float64) float64 { return rand.Float64(constant.FromFloat64(v)) },
				func(float64) float64 { return 0 }, "FromFloat64 must clamp a negative v to 0",
				prop.Using(prop.Float(math.Inf(-1), -math.SmallestNonzeroFloat64)), prop.Example(-1.0))
		})

		t.Run("returns a Rand at the largest float below 1 for a v of at least 1", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(v float64) float64 { return rand.Float64(constant.FromFloat64(v)) },
				func(float64) float64 { return justBelowOne }, "FromFloat64 must clamp v to a value below 1",
				prop.Using(prop.Float(1, math.Inf(1))), prop.Example(1.0), prop.Example(1.5))
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the same value on every call", func(t *testing.T) {
			t.Parallel()
			r := constant.New(value)
			assert.Deterministic(t, func(struct{}) (uint64, error) { return r.Uint64(), nil }, struct{}{},
				"Uint64 must return the configured value on every call")
		})

		t.Run("returns 0 for the zero Rand", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, constant.Rand{}.Uint64(), uint64(0), "the zero Rand must return 0")
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("fills p with the value in little-endian order repeated", func(t *testing.T) {
			t.Parallel()
			chunk := binary.LittleEndian.AppendUint64(nil, value)
			prop.Equal(t, func(n int) []byte {
				p := make([]byte, n)
				_, _ = constant.New(value).Read(p)

				return p
			}, func(n int) []byte {
				return bytes.Repeat(chunk, n/len(chunk)+1)[:n]
			}, "Read must repeat the encoding of the value", prop.Using(prop.Integer(0, 100)),
				prop.Example(0), prop.Example(1), prop.Example(8), prop.Example(9), prop.Example(16))
		})

		t.Run("returns the length of p", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int {
				got, _ := constant.New(value).Read(make([]byte, n))

				return got
			}, func(n int) int { return n }, "Read must report every byte of p", prop.Using(prop.Integer(0, 100)))
		})

		t.Run("returns no error", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := constant.New(value).Read(make([]byte, n))

				return err
			}, "Read must not fail", prop.Using(prop.Integer(0, 100)))
		})
	})
}

// TestRandAllocs checks that Uint64 and Read allocate nothing. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
func TestRandAllocs(t *testing.T) {
	r := constant.New(value)
	p := make([]byte, 100)

	t.Run("Uint64", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = r.Uint64() }, 0, "Uint64 must not allocate")
		assert.Equal(t, got, value, "the test must measure the configured value")
	})

	t.Run("Read", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n, _ = r.Read(p) }, 0, "Read must not allocate")
		assert.Equal(t, n, len(p), "the test must measure a read of p")
	})
}

// BenchmarkConstantRand runs the benchmarks of the contract suite of
// rand.Rand.
func BenchmarkConstantRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, func() rand.Rand { return constant.New(value) },
		randtest.RandBenchOnUint64(testkitbench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// BenchmarkRand reports the cost of Uint64 and of a Read of 100 bytes,
// and fails when either allocates.
func BenchmarkRand(b *testing.B) {
	r := constant.New(value)

	b.Run("Uint64", func(b *testing.B) {
		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Uint64()
		}

		assert.Equal(b, got, value, "the benchmark must measure the configured value")
	})

	b.Run("Read", func(b *testing.B) {
		p := make([]byte, 100)
		var n int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			n, _ = r.Read(p)
		}

		assert.Equal(b, n, len(p), "the benchmark must measure a read of p")
	})
}
