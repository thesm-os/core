// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package shake_test

import (
	stdsha3 "crypto/sha3"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/shake"
	"go.thesmos.sh/core/errs"
)

// The goroutines of a concurrent case, and the streams that each
// squeezes.
const (
	goroutines = 8
	rounds     = 20
)

// outputSize is the length of the outputs of the NIST FIPS 202 examples,
// and of every output that squeeze reads.
const outputSize = 32

// The canonical build-local IDs of the two constructions.
var (
	shake128ID = crypto.ID{'s', 'h', 'a', 'k', 'e', '1', '2', '8', '/', 'v', '1'}
	shake256ID = crypto.ID{'s', 'h', 'a', 'k', 'e', '2', '5', '6', '/', 'v', '1'}
)

// constructions are the two constructors of the package, with the
// output of 32 bytes for the empty message from the NIST FIPS 202
// examples.
var constructions = []struct {
	name  string
	xof   crypto.XOF
	empty string
}{
	{
		name:  "New128",
		xof:   shake.New128(),
		empty: "7f9c2ba4e88f827d616045507605853ed73b8093f6efbc88eb1a6eacfa66ef26",
	},
	{
		name:  "New256",
		xof:   shake.New256(),
		empty: "46b9dd2b0ba88d13233b3feb743eeb243fcd52ea62b81b82b50c27646ed5762f",
	},
}

// TestSHAKE128Contract runs the contract suite of crypto.XOF on
// SHAKE128 with its ID, its Algorithm and the output of the standard
// library.
func TestSHAKE128Contract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertXOFContract(t, shake.New128,
		append(cryptotest.XOFContractAssertions(),
			cryptotest.XOFIDAssertion(shake128ID),
			cryptotest.XOFAlgorithmAssertion(crypto.AlgSHAKE128),
			cryptotest.XOFCrossStdlibAssertion(stdsha3.SumSHAKE128),
		)...,
	)
}

// TestSHAKE256Contract runs the contract suite of crypto.XOF on
// SHAKE256 with its ID, its Algorithm and the output of the standard
// library.
func TestSHAKE256Contract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertXOFContract(t, shake.New256,
		append(cryptotest.XOFContractAssertions(),
			cryptotest.XOFIDAssertion(shake256ID),
			cryptotest.XOFAlgorithmAssertion(crypto.AlgSHAKE256),
			cryptotest.XOFCrossStdlibAssertion(stdsha3.SumSHAKE256),
		)...,
	)
}

func TestSHAKE(t *testing.T) {
	t.Parallel()

	for _, c := range constructions {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// Without a known-answer test the contract suite would pass
			// against any self-consistent sponge.
			t.Run("returns an XOF whose output for the empty message matches the FIPS 202 example", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, hex.EncodeToString(squeeze(t, c.xof, nil)), c.empty,
					"the output must match the FIPS 202 example")
			})

			t.Run("returns an XOF whose stream reads every byte of p", func(t *testing.T) {
				t.Parallel()
				n, err := c.xof.NewXOFStream().Read(make([]byte, 64))
				assert.NoError(t, err, "Read must succeed")
				assert.Equal(t, n, 64, "Read must fill p")
			})

			// The standard library panics on a write after a read, and
			// the stream turns the panic into an error.
			t.Run("returns an XOF whose stream returns ErrXOFSqueezing for a Write after a Read", func(t *testing.T) {
				t.Parallel()
				s := squeezing(t, c.xof)
				_, err := s.Write([]byte("too late"))
				assert.ErrorIs(t, err, crypto.ErrXOFSqueezing, "a write after a read must be an error")
			})

			t.Run("returns an XOF whose stream returns an error of class Invalid for a Write after a Read",
				func(t *testing.T) {
					t.Parallel()
					s := squeezing(t, c.xof)
					_, err := s.Write([]byte("too late"))
					assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrXOFSqueezing must classify as Invalid")
				})

			t.Run("returns an XOF whose stream absorbs nothing for a Write after a Read", func(t *testing.T) {
				t.Parallel()
				s := squeezing(t, c.xof)
				n, _ := s.Write([]byte("too late"))
				assert.Equal(t, n, 0, "a refused write must absorb nothing")
			})

			t.Run("returns an XOF whose stream reads after a refused Write", func(t *testing.T) {
				t.Parallel()
				s := squeezing(t, c.xof)
				_, _ = s.Write([]byte("too late"))
				_, err := s.Read(make([]byte, 8))
				assert.NoError(t, err, "a refused write must leave the stream readable")
			})

			t.Run("returns an XOF whose streams return the output of one stream to goroutines that squeeze at once",
				func(t *testing.T) {
					t.Parallel()
					outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
						data := []byte("payload " + strconv.Itoa(client))
						outputs := make([][]byte, 0, rounds)
						for range rounds {
							s := c.xof.NewXOFStream()
							out := make([]byte, outputSize)
							_, errWrite := s.Write(data)
							_, errRead := s.Read(out)
							if err := errors.Join(errWrite, errRead); err != nil {
								return outputs, err
							}
							outputs = append(outputs, out)
						}

						return outputs, nil
					})
					for _, o := range outcomes {
						assert.True(t, o.Finished, "every goroutine must finish")
						assert.NoError(t, o.Error, "every stream must absorb and squeeze")
						outputs, _ := o.Output.([][]byte)
						want := squeeze(t, c.xof, []byte("payload "+strconv.Itoa(o.Client)))
						assert.Equal(t, outputs, slices.Repeat([][]byte{want}, rounds),
							"every stream must squeeze the output of one stream")
					}
				})
		})
	}

	t.Run("New128", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an XOF whose ID differs from the ID of New256", func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, shake.New128().ID(), shake.New256().ID(), "the two constructions must not share an ID")
		})

		// Two constructions that agreed on an output would make the
		// Algorithm recorded with a squeeze meaningless.
		t.Run("returns an XOF whose output differs from the output of New256", func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, squeeze(t, shake.New128(), []byte("absorb me")),
				squeeze(t, shake.New256(), []byte("absorb me")), "the two constructions must not agree")
		})
	})
}

// TestSHAKEAllocs checks that a stream allocates once, at construction,
// and that Write, Read and Reset allocate nothing after it. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
//
//nolint:paralleltest // see above
func TestSHAKEAllocs(t *testing.T) {
	input := []byte("benchmark input")
	out := make([]byte, 64)

	for _, c := range constructions {
		t.Run(c.name, func(t *testing.T) {
			t.Run("NewXOFStream", func(t *testing.T) {
				var s crypto.XOFStream
				expect.MaxAllocs(t, func() { s = c.xof.NewXOFStream() }, 1, "NewXOFStream must allocate once")
				assert.NotNil(t, s, "the test must measure a stream")
			})

			t.Run("a stream that absorbs and squeezes", func(t *testing.T) {
				s := c.xof.NewXOFStream()
				var err error
				expect.MaxAllocs(t, func() {
					s.Reset()
					_, _ = s.Write(input)
					_, err = s.Read(out)
				}, 0, "Reset, Write and Read must not allocate")
				assert.NoError(t, err, "the test must measure a read that succeeds")
			})
		})
	}
}

// BenchmarkSHAKE reports the cost of a new stream that absorbs a short
// input and squeezes 32 or 1024 bytes, for each construction.
func BenchmarkSHAKE(b *testing.B) {
	input := []byte("benchmark input")

	for _, c := range constructions {
		for _, n := range []int{32, 1024} {
			b.Run(c.name+"/"+strconv.Itoa(n), func(b *testing.B) {
				out := make([]byte, n)
				var err error

				bc := bench.Start(b).MaxAllocs(1)
				defer bc.End()

				for bc.Loop() {
					s := c.xof.NewXOFStream()
					_, _ = s.Write(input)
					_, err = s.Read(out)
				}

				assert.NoError(b, err, "the benchmark must measure a read that succeeds")
			})
		}
	}
}

// squeeze absorbs data into a fresh stream of x and reads outputSize
// bytes. It fails t when Write or Read fails.
func squeeze(t *testing.T, x crypto.XOF, data []byte) []byte {
	t.Helper()

	s := x.NewXOFStream()
	_, err := s.Write(data)
	assert.NoError(t, err, "Write must succeed")

	out := make([]byte, outputSize)
	_, err = s.Read(out)
	assert.NoError(t, err, "Read must succeed")

	return out
}

// squeezing returns a stream of x that absorbed a short input and then
// squeezed 8 bytes. It fails t when Write or Read fails.
func squeezing(t *testing.T, x crypto.XOF) crypto.XOFStream {
	t.Helper()

	s := x.NewXOFStream()
	_, err := s.Write([]byte("absorb"))
	assert.NoError(t, err, "the first Write must succeed")
	_, err = s.Read(make([]byte, 8))
	assert.NoError(t, err, "Read must succeed")

	return s
}
