// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

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

func (c *counting) Sign(message []byte) ([]byte, error) {
	c.signs.Add(1)

	return c.Signer.Sign(message) //nolint:wrapcheck // the test double passes the error through
}

// contextual is a counting signer that also implements
// sign.ContextSigner, refusing a context that has ended.
type contextual struct {
	counting
}

func (c *contextual) SignContext(ctx context.Context, message []byte) ([]byte, error) {
	c.signContexts.Add(1)

	if err := context.Cause(ctx); err != nil {
		return nil, fmt.Errorf("contextual: %w", err)
	}

	return c.Signer.Sign(message) //nolint:wrapcheck // the test double passes the error through
}

func newEd25519(tb testing.TB) sign.Signer {
	tb.Helper()

	s, err := ed25519.Generate(randcrypto.New())
	testkit.NoError(tb, err, "Generate must succeed")

	return s
}

func TestSignContext(t *testing.T) {
	t.Parallel()

	t.Run("calls SignContext on a signer that has it", func(t *testing.T) {
		t.Parallel()
		s := &contextual{counting{Signer: newEd25519(t)}}

		sig, err := sign.SignContext(t.Context(), s, []byte("payload"))
		testkit.NoError(t, err, "SignContext must succeed")
		testkit.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
		testkit.Equal(t, s.signContexts.Load(), int32(1), "SignContext must be called once")
		testkit.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
	})

	t.Run("calls Sign on a signer without SignContext", func(t *testing.T) {
		t.Parallel()
		s := &counting{Signer: newEd25519(t)}

		sig, err := sign.SignContext(t.Context(), s, []byte("payload"))
		testkit.NoError(t, err, "SignContext must succeed")
		testkit.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
		testkit.Equal(t, s.signs.Load(), int32(1), "Sign must be called once")
	})

	t.Run("returns the cause of an ended context without calling Sign", func(t *testing.T) {
		t.Parallel()
		s := &counting{Signer: newEd25519(t)}
		cause := testkit.TestError("the caller gave up")
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)

		sig, err := sign.SignContext(ctx, s, []byte("payload"))
		testkit.ErrorIs(t, err, cause, "SignContext must return the context's cause")
		testkit.Equal(t, sig, []byte(nil), "SignContext must return no signature with an error")
		testkit.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
	})
}

func TestContextSignerContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertSignerContract(t,
		func() sign.Signer { return &contextual{counting{Signer: newEd25519(t)}} },
		append(cryptotest.SignerContractAssertions(),
			cryptotest.ContextSignerAssertion(),
		)...,
	)
}
