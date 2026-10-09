// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ecdsap384"
)

// signerDecorator wraps a Signer and returns it from Unwrap, as a
// tracing decorator does.
type signerDecorator struct{ sign.Signer }

// Unwrap returns the Signer that d wraps.
func (d signerDecorator) Unwrap() sign.Signer { return d.Signer }

// verifierDecorator wraps a Verifier and returns it from Unwrap.
type verifierDecorator struct{ sign.Verifier }

// Unwrap returns the Verifier that d wraps.
func (d verifierDecorator) Unwrap() sign.Verifier { return d.Verifier }

// opaqueSigner wraps a Signer and has no Unwrap, so the As functions
// cannot see past it.
type opaqueSigner struct{ sign.Signer }

func TestUnwrap(t *testing.T) {
	t.Parallel()

	t.Run("AsStreamingSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the StreamingSigner behind two decorators", func(t *testing.T) {
			t.Parallel()
			s := newECDSA(t)
			got, ok := sign.AsStreamingSigner(signerDecorator{signerDecorator{s}})
			assert.True(t, ok, "a StreamingSigner behind decorators must be found")
			assert.Equal(t, got, sign.StreamingSigner(s), "the wrapped signer must be returned", assert.ByIdentity())
		})

		t.Run("reports false for a signer that cannot stream", func(t *testing.T) {
			t.Parallel()
			_, ok := sign.AsStreamingSigner(signerDecorator{newEd25519(t)})
			assert.False(t, ok, "Ed25519 must not report a streaming capability")
		})

		t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
			t.Parallel()
			_, ok := sign.AsStreamingSigner(opaqueSigner{newECDSA(t)})
			assert.False(t, ok, "a decorator without Unwrap must end the chain")
		})
	})

	t.Run("AsStreamingVerifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the StreamingVerifier behind a Verifier decorator", func(t *testing.T) {
			t.Parallel()
			v := newECDSA(t).Verifier
			got, ok := sign.AsStreamingVerifier(verifierDecorator{v})
			assert.True(t, ok, "a StreamingVerifier behind a decorator must be found")
			assert.Equal(t, got, sign.StreamingVerifier(v), "the wrapped verifier must be returned",
				assert.ByIdentity())
		})

		t.Run("returns the StreamingVerifier behind a Signer decorator", func(t *testing.T) {
			t.Parallel()
			s := newECDSA(t)
			got, ok := sign.AsStreamingVerifier(signerDecorator{s})
			assert.True(t, ok, "a signer that verifies by stream must be found through its decorator")
			assert.Equal(t, got, sign.StreamingVerifier(s), "the wrapped signer must be returned", assert.ByIdentity())
		})
	})

	t.Run("AsContextSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ContextSigner behind a decorator", func(t *testing.T) {
			t.Parallel()
			cs := &contextual{counting{Signer: newEd25519(t)}}
			got, ok := sign.AsContextSigner(signerDecorator{cs})
			assert.True(t, ok, "a ContextSigner behind a decorator must be found")
			assert.Equal(t, got, sign.ContextSigner(cs), "the wrapped signer must be returned", assert.ByIdentity())
		})

		t.Run("reports false for a nil signer", func(t *testing.T) {
			t.Parallel()
			_, ok := sign.AsContextSigner(nil)
			assert.False(t, ok, "a nil signer must not report a capability")
		})

		t.Run("reports false for a decorator of nil", func(t *testing.T) {
			t.Parallel()
			_, ok := sign.AsContextSigner(signerDecorator{})
			assert.False(t, ok, "a decorator of nil must not report a capability")
		})
	})

	t.Run("AsAppendSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the AppendSigner behind two decorators", func(t *testing.T) {
			t.Parallel()
			s := newEd25519(t)
			got, ok := sign.AsAppendSigner(signerDecorator{signerDecorator{s}})
			assert.True(t, ok, "an AppendSigner behind decorators must be found")
			assert.Equal(t, got, s.(sign.AppendSigner), "the wrapped signer must be returned", assert.ByIdentity())
		})

		t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
			t.Parallel()
			_, ok := sign.AsAppendSigner(opaqueSigner{newEd25519(t)})
			assert.False(t, ok, "a decorator without Unwrap must end the chain")
		})
	})
}

// TestUnwrapAllocs checks that the As functions allocate nothing through
// two decorators. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
func TestUnwrapAllocs(t *testing.T) {
	var s sign.Signer = signerDecorator{signerDecorator{newECDSA(t)}}
	var e sign.Signer = signerDecorator{signerDecorator{newEd25519(t)}}

	for _, tt := range []struct {
		name string
		find func() bool
	}{
		{name: "AsStreamingSigner", find: func() bool { _, ok := sign.AsStreamingSigner(s); return ok }},
		{name: "AsStreamingVerifier", find: func() bool { _, ok := sign.AsStreamingVerifier(s); return ok }},
		{name: "AsContextSigner", find: func() bool { _, ok := sign.AsContextSigner(s); return !ok }},
		{name: "AsAppendSigner", find: func() bool { _, ok := sign.AsAppendSigner(e); return ok }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { ok = tt.find() }, 0, "a search of the chain must not allocate")
			assert.True(t, ok, "the test must measure a search with its expected result")
		})
	}
}

// BenchmarkUnwrap reports the cost of each As function through two
// decorators, and fails when one of them allocates.
func BenchmarkUnwrap(b *testing.B) {
	var s sign.Signer = signerDecorator{signerDecorator{newECDSA(b)}}
	var e sign.Signer = signerDecorator{signerDecorator{newEd25519(b)}}

	for _, tt := range []struct {
		name string
		find func() bool
	}{
		{name: "AsStreamingSigner", find: func() bool { _, ok := sign.AsStreamingSigner(s); return ok }},
		{name: "AsStreamingVerifier", find: func() bool { _, ok := sign.AsStreamingVerifier(s); return ok }},
		{name: "AsContextSigner", find: func() bool { _, ok := sign.AsContextSigner(s); return !ok }},
		{name: "AsAppendSigner", find: func() bool { _, ok := sign.AsAppendSigner(e); return ok }},
	} {
		b.Run(tt.name, func(b *testing.B) {
			var ok bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				ok = tt.find()
			}

			assert.True(b, ok, "the benchmark must measure a search with its expected result")
		})
	}
}

// newECDSA returns an ECDSA P-384 signer over a new key. It fails tb when
// Generate fails.
func newECDSA(tb testing.TB) *ecdsap384.Signer {
	tb.Helper()

	s, err := ecdsap384.Generate()
	assert.NoError(tb, err, "Generate must succeed")

	return s
}
