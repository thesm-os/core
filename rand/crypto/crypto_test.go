// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"io"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/crypto"
)

// failingReader returns a fixed error on every Read. Used to
// drive [crypto.Rand]'s failure paths via the public
// [crypto.NewWithReader] constructor — testkit's RandStub doesn't
// fit because the failure-path consumer takes an io.Reader, not
// a rand.Rand.
type failingReader struct{ err error }

func (f failingReader) Read(_ []byte) (int, error) { return 0, f.err }

// failingReader implements io.Reader; the compile-time check
// keeps the test fixture in sync if the interface changes.
var _ io.Reader = failingReader{}

// newCrypto is the SUT factory for the testkit-driven contract
// suite — uses the default [crypto/rand.Reader] source.
func newCrypto() rand.Rand { return crypto.New() }

// --- testkit-driven contract layer ---

// crypto.Rand draws from the OS CSPRNG; the determinism assertion
// does not apply (different bytes per call by design). Skip
// RandSeedDeterminismAssertion. RandUint64DistinctnessAssertion
// applies — a healthy CSPRNG produces diverse output.
func TestCryptoRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, newCrypto,
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
		)...,
	)
}

func TestCryptoRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, newCrypto)
}

func FuzzCryptoRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, newCrypto)
}

func BenchmarkCryptoRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, newCrypto,
		randtest.RandBenchOnUint64(bench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// --- crypto-specific tests ---

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("source error is wrapped with package context", func(t *testing.T) {
		t.Parallel()
		sentinel := testkit.TestError("entropy depleted")
		r := crypto.NewWithReader(failingReader{err: sentinel})
		_, err := r.Read(make([]byte, 8))
		testkit.ErrorIs(t, err, sentinel,
			"Read must wrap the source error preserving the cause via errors.Is")
	})
}

func TestUint64(t *testing.T) {
	t.Parallel()

	t.Run("panics with wrapped error on source failure", func(t *testing.T) {
		t.Parallel()
		sentinel := testkit.TestError("entropy depleted")
		r := crypto.NewWithReader(failingReader{err: sentinel})

		recovered := testkit.Panics(t, func() { _ = r.Uint64() },
			"Uint64 must panic when the entropy source fails")
		err, ok := recovered.(error)
		testkit.True(t, ok, "panic value must be an error")
		testkit.ErrorIs(t, err, sentinel,
			"panicked error must wrap the source error via errors.Is")
	})
}

func TestZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	// Documented contract: zero-value Rand uses crypto/rand.Reader.
	var r crypto.Rand
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	testkit.NoError(t, err, "zero-value Read")
	testkit.Equal(t, n, 8, "zero-value Read must fill the supplied buffer")
}

func TestNewWithReaderUsesProvidedSource(t *testing.T) {
	t.Parallel()

	// A non-failing custom reader must produce its bytes via Read.
	const want = "deterministic-bytes-for-test"
	r := crypto.NewWithReader(bytes.NewReader([]byte(want)))
	got := make([]byte, len(want))
	n, err := r.Read(got)
	testkit.NoError(t, err, "Read")
	testkit.Equal(t, n, len(want), "Read must fill the supplied buffer")
	testkit.Equal(t, string(got), want, "Read must produce the supplied source's bytes verbatim")
}

func TestNewWithReaderOfNil(t *testing.T) {
	t.Parallel()

	t.Run("reads from the default source", func(t *testing.T) {
		t.Parallel()
		r := crypto.NewWithReader(nil)
		buf := make([]byte, 8)
		n, err := r.Read(buf)
		testkit.NoError(t, err, "a nil reader must fall back to crypto/rand")
		testkit.Equal(t, n, 8, "Read must fill the supplied buffer")
	})
}

// sink receives a Rand converted to the interface, so the conversion
// happens at run time and is measured.
var sink rand.Rand

// TestZeroAlloc enforces the allocation contract of Rand: Read is
// zero-alloc, Uint64 is zero-alloc once its buffer pool is warm, and
// converting a Rand to rand.Rand stores it without boxing.
// testing.AllocsPerRun reads a process-global malloc counter, so this
// test does not call t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := crypto.New()
	custom := crypto.NewWithReader(bytes.NewReader(nil))
	buf := make([]byte, 64)

	// Fills the pool, so the first measured Uint64 does not count the
	// buffer it creates.
	_ = r.Uint64()

	tests := []struct {
		name string
		fn   func()
	}{
		{"Read", func() { _, _ = r.Read(buf) }},
		{"Uint64", func() { _ = r.Uint64() }},
		{"conversion of the default Rand", func() { sink = r }},
		{"conversion of a Rand with a custom reader", func() { sink = custom }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tt.fn), float64(0), tt.name+" must not allocate")
		})
	}
}
