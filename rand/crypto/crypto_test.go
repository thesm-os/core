// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/iotest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/seeded"
)

// The goroutines of a concurrent case, and the values that each draws.
const (
	goroutines = 8
	rounds     = 20
)

// errSource is the error of the sources that fail in the tests.
var errSource = errors.New("entropy depleted")

// sink receives a Rand converted to the interface, so the conversion
// happens at run time and is measured.
var sink rand.Rand

// TestCryptoRandContract runs the contract suite of rand.Rand with the
// assertion of distinct draws. crypto.Rand reads the CSPRNG of the
// operating system, so the suite runs without its determinism
// assertion.
func TestCryptoRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, func() rand.Rand { return crypto.New() },
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
		)...,
	)
}

// TestCryptoRandModel runs the model test of the contract suite.
func TestCryptoRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, func() rand.Rand { return crypto.New() })
}

func TestRand(t *testing.T) {
	t.Parallel()

	t.Run("reads from crypto/rand for the zero Rand", func(t *testing.T) {
		t.Parallel()
		var r crypto.Rand
		n, err := r.Read(make([]byte, 8))
		assert.NoError(t, err, "the zero Rand must read the default source")
		assert.Equal(t, n, 8, "the zero Rand must fill p")
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the zero Rand", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, crypto.New(), crypto.Rand{}, "New must return the Rand of the default source")
		})
	})

	t.Run("NewWithReader", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Rand that reads the bytes of src", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(b []byte) []byte {
				p := make([]byte, len(b))
				_, _ = crypto.NewWithReader(bytes.NewReader(b)).Read(p)

				return p
			}, func(b []byte) []byte { return b }, "Read must return the bytes of src verbatim",
				prop.Using(prop.Bytes(prop.MaxSize(64))), assert.EquateEmpty())
		})

		t.Run("returns the zero Rand for a nil src", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, crypto.NewWithReader(nil), crypto.Rand{}, "a nil src must give the default source")
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first 8 bytes of the source in little-endian order", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(b []byte) uint64 { return crypto.NewWithReader(bytes.NewReader(b)).Uint64() },
				binary.LittleEndian.Uint64, "Uint64 must decode the bytes of the source",
				prop.Using(prop.Bytes(prop.MinSize(8), prop.MaxSize(8))))
		})

		t.Run("panics with the error of the source", func(t *testing.T) {
			t.Parallel()
			r := crypto.NewWithReader(iotest.ErrReader(errSource))
			recovered := assert.Panics(t, func() { _ = r.Uint64() }, "Uint64 must panic when the source fails")
			err, _ := recovered.(error)
			assert.ErrorIs(t, err, errSource, "the panic must wrap the error of the source")
		})

		t.Run("panics for a source of fewer than 8 bytes", func(t *testing.T) {
			t.Parallel()
			r := crypto.NewWithReader(bytes.NewReader([]byte{1, 2, 3}))
			recovered := assert.Panics(t, func() { _ = r.Uint64() }, "Uint64 must panic on a short source")
			err, _ := recovered.(error)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "the panic must report the short read")
		})

		// A seeded source fills each Read under its lock, so each call
		// takes 8 bytes of one stream whatever its pooled buffer.
		t.Run("returns each value of the source once to goroutines that draw at once", func(t *testing.T) {
			t.Parallel()
			r := crypto.NewWithReader(seeded.New(rand.Seed(1)))
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
				values := make([]uint64, 0, rounds)
				for range rounds {
					values = append(values, r.Uint64())
				}

				return values, nil
			})

			var got []uint64
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				values, _ := o.Output.([]uint64)
				got = append(got, values...)
			}

			src := crypto.NewWithReader(seeded.New(rand.Seed(1)))
			want := make([]uint64, 0, goroutines*rounds)
			for range goroutines * rounds {
				want = append(want, src.Uint64())
			}
			assert.Permutation(t, got, want, "the values must be the values of the source, each once")
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the source behind the prefix of the package", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.NewWithReader(iotest.ErrReader(errSource)).Read(make([]byte, 8))
			assert.ErrorIs(t, err, errSource, "Read must wrap the error of the source")
			assert.HasPrefix(t, err.Error(), "rand/crypto: ", "the error must name the package")
		})

		t.Run("returns the bytes that it read before the source failed", func(t *testing.T) {
			t.Parallel()
			r := crypto.NewWithReader(io.MultiReader(bytes.NewReader([]byte{1, 2, 3}), iotest.ErrReader(errSource)))
			n, _ := r.Read(make([]byte, 8))
			assert.Equal(t, n, 3, "Read must count the bytes that it filled")
		})

		t.Run("fills p from the default source", func(t *testing.T) {
			t.Parallel()
			n, err := crypto.New().Read(make([]byte, 64))
			assert.NoError(t, err, "the default source must not fail")
			assert.Equal(t, n, 64, "Read must fill p")
		})
	})
}

// TestRandAllocs checks that Read and a warm Uint64 allocate nothing, and
// that a Rand converts to rand.Rand without boxing. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestRandAllocs(t *testing.T) {
	r := crypto.New()
	custom := crypto.NewWithReader(bytes.NewReader(nil))
	p := make([]byte, 64)

	t.Run("Uint64", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = r.Uint64() }, 0, "Uint64 must not allocate on the warm path")
		assert.NotEqual(t, got, 0, "the test must measure a draw")
	})

	t.Run("Read", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n, _ = r.Read(p) }, 0, "Read must not allocate")
		assert.Equal(t, n, len(p), "the test must measure a read of p")
	})

	conversions := []struct {
		name string
		give crypto.Rand
	}{
		{name: "converts the default Rand to rand.Rand without boxing", give: r},
		{name: "converts a Rand of a custom reader to rand.Rand without boxing", give: custom},
	}
	for _, tt := range conversions {
		t.Run(tt.name, func(t *testing.T) {
			expect.MaxAllocs(t, func() { sink = tt.give }, 0, "a Rand must be pointer-shaped")
			assert.Equal(t, sink, rand.Rand(tt.give), "the test must measure the conversion")
		})
	}
}

// BenchmarkCryptoRand runs the benchmarks of the contract suite of
// rand.Rand.
func BenchmarkCryptoRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, func() rand.Rand { return crypto.New() },
		randtest.RandBenchOnUint64(testkitbench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// BenchmarkRand reports the cost of Uint64 and of a Read of 64 bytes from
// the default source, and fails when either allocates.
func BenchmarkRand(b *testing.B) {
	r := crypto.New()

	b.Run("Uint64", func(b *testing.B) {
		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Uint64()
		}

		assert.NotEqual(b, got, 0, "the benchmark must measure a draw")
	})

	b.Run("Read", func(b *testing.B) {
		p := make([]byte, 64)
		var n int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			n, _ = r.Read(p)
		}

		assert.Equal(b, n, len(p), "the benchmark must measure a read of p")
	})
}

// FuzzCryptoRandModel runs the model of the contract suite on the inputs
// that a fuzzer finds.
func FuzzCryptoRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, func() rand.Rand { return crypto.New() })
}
