// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"context"
	"fmt"
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/sign/ed25519"
)

// cancellableSigner is an Ed25519 Signer whose SignContext returns the cause
// of a context that ended.
type cancellableSigner struct {
	*ed25519.Signer
}

// SignContext returns an error that wraps the cause of ctx when ctx ended, and
// the signature of message otherwise.
func (s cancellableSigner) SignContext(ctx context.Context, message []byte) ([]byte, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, fmt.Errorf("cancellableSigner: %w", err)
	}

	return s.Sign(message)
}

// uncancellableSigner is an Ed25519 Signer whose SignContext signs under a
// context that ended.
type uncancellableSigner struct {
	*ed25519.Signer
}

// SignContext returns the signature of message.
func (s uncancellableSigner) SignContext(_ context.Context, message []byte) ([]byte, error) {
	return s.Sign(message)
}

// TestContext checks that AssertContextSignerContract passes for a signer that
// returns the cause of an ended context, and fails for a signer that signs
// under it.
func TestContext(t *testing.T) {
	t.Parallel()

	t.Run("AssertContextSignerContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for a signer that returns the cause of an ended context", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertContextSignerContract(t, cancellableSigner{Signer: newEd25519(t)})
		})

		t.Run("fails for a signer that signs under an ended context", func(t *testing.T) {
			t.Parallel()
			s := uncancellableSigner{Signer: newEd25519(t)}
			got := assert.Rejects(t, "a signer that ignores its context fails the contract", func(tb assert.TB) {
				cryptotest.AssertContextSignerContract(tb, s)
			})
			assert.Length(t, got, 1, "the contract fails once")
			assert.Equal(t, got[0].Contract, "SignContext returns an error that wraps the cause of the context",
				"the contract fails at the error of SignContext")
		})
	})
}
