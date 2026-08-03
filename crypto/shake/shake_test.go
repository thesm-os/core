// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package shake_test

import (
	stdsha3 "crypto/sha3"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/shake"
)

var (
	shake128ID = crypto.ID{'s', 'h', 'a', 'k', 'e', '1', '2', '8', '/', 'v', '1'}
	shake256ID = crypto.ID{'s', 'h', 'a', 'k', 'e', '2', '5', '6', '/', 'v', '1'}
)

// --- testkit-driven contract layer ---

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

// --- impl-specific ---

func TestConstructionsDiffer(t *testing.T) {
	t.Parallel()

	t.Run("SHAKE128 and SHAKE256 have distinct IDs", func(t *testing.T) {
		t.Parallel()
		testkit.NotEqual(t, shake.New128().ID(), shake.New256().ID(),
			"the two constructions must not share an ID")
	})

	t.Run("SHAKE128 and SHAKE256 produce distinct output", func(t *testing.T) {
		t.Parallel()
		// Same input, same length, different security strength: the
		// outputs must not coincide, or the Algorithm recorded with
		// a squeeze would be meaningless.
		a := squeeze(t, shake.New128(), []byte("absorb me"), 32)
		b := squeeze(t, shake.New256(), []byte("absorb me"), 32)
		testkit.NotEqual(t, a, b, "the two constructions must not agree on any output")
	})
}

func TestNISTVectors(t *testing.T) {
	t.Parallel()

	// SHAKE of the empty message, from the NIST FIPS 202 examples.
	// Without a known-answer test the contract suite would pass
	// against any self-consistent sponge; this pins the bytes to
	// SHAKE.
	cases := []struct {
		name string
		xof  crypto.XOF
		want string
	}{
		{
			name: "SHAKE128 of the empty message",
			xof:  shake.New128(),
			want: "7f9c2ba4e88f827d616045507605853ed73b8093f6efbc88eb1a6eacfa66ef26",
		},
		{
			name: "SHAKE256 of the empty message",
			xof:  shake.New256(),
			want: "46b9dd2b0ba88d13233b3feb743eeb243fcd52ea62b81b82b50c27646ed5762f",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, squeeze(t, tc.xof, nil, 32), testkit.MustDecodeHex(t, tc.want),
				"output must match the FIPS 202 example")
		})
	}
}

func TestWriteAfterReadDoesNotPanic(t *testing.T) {
	t.Parallel()

	// The standard library panics here. This package exists in part
	// to convert that into an error, so the conversion is asserted
	// directly as well as through the contract suite.
	for _, tc := range []struct {
		name string
		xof  crypto.XOF
	}{
		{"SHAKE128", shake.New128()},
		{"SHAKE256", shake.New256()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tc.xof.NewXOFStream()

			_, err := s.Write([]byte("absorb"))
			testkit.NoError(t, err, "the first Write must succeed")

			_, err = s.Read(make([]byte, 8))
			testkit.NoError(t, err, "Read must succeed")

			n, err := s.Write([]byte("too late"))
			testkit.ErrorIs(t, err, crypto.ErrXOFSqueezing, "a write after a read must be an error")
			testkit.Equal(t, n, 0, "a rejected write must absorb nothing")

			// The stream stays usable for reading afterwards — a
			// rejected write must not corrupt the sponge.
			_, err = s.Read(make([]byte, 8))
			testkit.NoError(t, err, "a rejected write must leave the stream readable")
		})
	}
}

func BenchmarkSHAKE(b *testing.B) {
	for _, tc := range []struct {
		name string
		xof  crypto.XOF
	}{
		{"SHAKE128", shake.New128()},
		{"SHAKE256", shake.New256()},
	} {
		for _, n := range []int{32, 1024} {
			b.Run(tc.name+"/"+strconv.Itoa(n), func(b *testing.B) {
				input := []byte("benchmark input")
				out := make([]byte, n)
				b.ReportAllocs()

				for b.Loop() {
					s := tc.xof.NewXOFStream()
					_, _ = s.Write(input)
					_, _ = s.Read(out)
				}
			})
		}
	}
}

// squeeze absorbs data into a fresh stream and reads n bytes.
func squeeze(t *testing.T, x crypto.XOF, data []byte, n int) []byte {
	t.Helper()

	s := x.NewXOFStream()
	_, err := s.Write(data)
	testkit.NoError(t, err, "Write must succeed")

	out := make([]byte, n)
	_, err = s.Read(out)
	testkit.NoError(t, err, "Read must succeed")

	return out
}
