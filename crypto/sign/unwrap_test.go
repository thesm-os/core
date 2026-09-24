// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// signerDecorator wraps a Signer and returns it from Unwrap, as a
// tracing decorator does.
type signerDecorator struct{ sign.Signer }

func (d signerDecorator) Unwrap() sign.Signer { return d.Signer }

// verifierDecorator wraps a Verifier and returns it from Unwrap.
type verifierDecorator struct{ sign.Verifier }

func (d verifierDecorator) Unwrap() sign.Verifier { return d.Verifier }

// opaqueSigner wraps a Signer and has no Unwrap, so the As functions
// cannot see past it.
type opaqueSigner struct{ sign.Signer }

func newECDSA(t *testing.T) *ecdsap384.Signer {
	t.Helper()

	s, err := ecdsap384.Generate()
	testkit.NoError(t, err, "Generate must succeed")

	return s
}

// TestUnwrapZeroAlloc enforces the allocation contract of the As
// functions through two decorators. testing.AllocsPerRun reads a
// process-global malloc counter, so this test does not call
// t.Parallel.
//
//nolint:paralleltest // see comment above
func TestUnwrapZeroAlloc(t *testing.T) {
	var s sign.Signer = signerDecorator{signerDecorator{newECDSA(t)}}

	for name, fn := range map[string]func(){
		"AsStreamingSigner":   func() { _, _ = sign.AsStreamingSigner(s) },
		"AsStreamingVerifier": func() { _, _ = sign.AsStreamingVerifier(s) },
		"AsContextSigner":     func() { _, _ = sign.AsContextSigner(s) },
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, fn), float64(0), name+" must not allocate")
		})
	}
}

func BenchmarkAsContextSigner(b *testing.B) {
	edS, err := ed25519.Generate(randcrypto.New())
	testkit.NoError(b, err, "Generate must succeed")

	var s sign.Signer = signerDecorator{signerDecorator{edS}}
	b.ReportAllocs()

	for b.Loop() {
		_, _ = sign.AsContextSigner(s)
	}
}

func TestAsStreamingSigner(t *testing.T) {
	t.Parallel()

	t.Run("returns the StreamingSigner behind two decorators", func(t *testing.T) {
		t.Parallel()
		s := newECDSA(t)
		got, ok := sign.AsStreamingSigner(signerDecorator{signerDecorator{s}})
		testkit.True(t, ok, "a StreamingSigner behind decorators must be found")
		testkit.True(t, got == sign.StreamingSigner(s), "the wrapped signer must be returned")
	})

	t.Run("reports false for a signer that cannot stream", func(t *testing.T) {
		t.Parallel()
		_, ok := sign.AsStreamingSigner(signerDecorator{newEd25519(t)})
		testkit.False(t, ok, "Ed25519 must not report a streaming capability")
	})

	t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
		t.Parallel()
		_, ok := sign.AsStreamingSigner(opaqueSigner{newECDSA(t)})
		testkit.False(t, ok, "a decorator without Unwrap must end the chain")
	})
}

func TestAsStreamingVerifier(t *testing.T) {
	t.Parallel()

	t.Run("returns the StreamingVerifier behind a Verifier decorator", func(t *testing.T) {
		t.Parallel()
		v := newECDSA(t).Verifier
		got, ok := sign.AsStreamingVerifier(verifierDecorator{v})
		testkit.True(t, ok, "a StreamingVerifier behind a decorator must be found")
		testkit.True(t, got == sign.StreamingVerifier(v), "the wrapped verifier must be returned")
	})

	t.Run("returns the StreamingVerifier behind a Signer decorator", func(t *testing.T) {
		t.Parallel()
		s := newECDSA(t)
		got, ok := sign.AsStreamingVerifier(signerDecorator{s})
		testkit.True(t, ok, "a signer that verifies by stream must be found through its decorator")
		testkit.True(t, got == sign.StreamingVerifier(s), "the wrapped signer must be returned")
	})
}

func TestAsContextSigner(t *testing.T) {
	t.Parallel()

	t.Run("returns the ContextSigner behind a decorator", func(t *testing.T) {
		t.Parallel()
		cs := &contextual{counting{Signer: newEd25519(t)}}
		got, ok := sign.AsContextSigner(signerDecorator{cs})
		testkit.True(t, ok, "a ContextSigner behind a decorator must be found")
		testkit.True(t, got == sign.ContextSigner(cs), "the wrapped signer must be returned")
	})

	t.Run("reports false for a nil signer and a decorator of nil", func(t *testing.T) {
		t.Parallel()
		_, ok := sign.AsContextSigner(nil)
		testkit.False(t, ok, "a nil signer must not report a capability")

		_, ok = sign.AsContextSigner(signerDecorator{})
		testkit.False(t, ok, "a decorator of nil must not report a capability")
	})
}
