// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// counting wraps a Signer and counts its Sign and SignContext calls.
type counting struct {
	sign.Signer

	signs        atomic.Int32
	signContexts atomic.Int32
}

// Sign counts the call and signs message with the wrapped Signer.
func (c *counting) Sign(message []byte) ([]byte, error) {
	c.signs.Add(1)

	return c.Signer.Sign(message) //nolint:wrapcheck // the test double passes the error through
}

// contextual is a counting signer that also implements
// sign.ContextSigner, refusing a context that has ended.
type contextual struct {
	counting
}

// SignContext counts the call, returns the cause of a context that has
// ended, and signs message with the wrapped Signer otherwise.
func (c *contextual) SignContext(ctx context.Context, message []byte) ([]byte, error) {
	c.signContexts.Add(1)

	if err := context.Cause(ctx); err != nil {
		return nil, fmt.Errorf("contextual: %w", err)
	}

	return c.Signer.Sign(message) //nolint:wrapcheck // the test double passes the error through
}

// TestContextSignerContract runs the contract suite of sign.Signer with
// the assertion of the ContextSigner capability.
func TestContextSignerContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertSignerContract(t,
		func() sign.Signer { return &contextual{counting{Signer: newEd25519(t)}} },
		append(cryptotest.SignerContractAssertions(),
			cryptotest.ContextSignerAssertion(),
		)...,
	)
}

func TestContext(t *testing.T) {
	t.Parallel()

	t.Run("SignContext", func(t *testing.T) {
		t.Parallel()

		t.Run("calls SignContext on a signer that has it", func(t *testing.T) {
			t.Parallel()
			s := &contextual{counting{Signer: newEd25519(t)}}
			sig, err := sign.SignContext(t.Context(), s, []byte("payload"))
			assert.NoError(t, err, "SignContext must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.signContexts.Load(), int32(1), "SignContext must be called once")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls SignContext on a signer behind a decorator", func(t *testing.T) {
			t.Parallel()
			s := &contextual{counting{Signer: newEd25519(t)}}
			sig, err := sign.SignContext(t.Context(), signerDecorator{s}, []byte("payload"))
			assert.NoError(t, err, "SignContext must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.signContexts.Load(), int32(1), "SignContext must be called on the wrapped signer")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls Sign on a signer without SignContext", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			sig, err := sign.SignContext(t.Context(), s, []byte("payload"))
			assert.NoError(t, err, "SignContext must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.signs.Load(), int32(1), "Sign must be called once")
		})

		t.Run("returns the cause of an ended context without calling Sign", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			cause := errors.New("the caller gave up")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			sig, err := sign.SignContext(ctx, s, []byte("payload"))
			assert.ErrorIs(t, err, cause, "SignContext must return the cause of the context")
			expect.Nil(t, sig, "SignContext must return no signature with an error")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})
	})
}

// TestContextAllocs checks that SignContext of an Ed25519 signer
// allocates only the signature that Sign allocates. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestContextAllocs(t *testing.T) {
	s := newEd25519(t)
	msg := []byte("payload")

	t.Run("SignContext", func(t *testing.T) {
		var sig []byte
		expect.MaxAllocs(t, func() { sig, _ = sign.SignContext(t.Context(), s, msg) }, 1,
			"SignContext must add no allocation to the signature of Sign")
		assert.True(t, s.Verify(msg, sig), "the test must measure a signature that verifies")
	})
}

// BenchmarkContext reports the cost of SignContext of an Ed25519 signer,
// and fails when it allocates more than the signature.
func BenchmarkContext(b *testing.B) {
	b.Run("SignContext", func(b *testing.B) {
		s := newEd25519(b)
		msg := []byte("payload")
		var sig []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			sig, _ = sign.SignContext(b.Context(), s, msg)
		}

		assert.True(b, s.Verify(msg, sig), "the benchmark must measure a signature that verifies")
	})
}

// newEd25519 returns an Ed25519 signer over a new key. It fails tb when
// Generate fails.
func newEd25519(tb testing.TB) sign.Signer {
	tb.Helper()

	s, err := ed25519.Generate(randcrypto.New())
	assert.NoError(tb, err, "Generate must succeed")

	return s
}
