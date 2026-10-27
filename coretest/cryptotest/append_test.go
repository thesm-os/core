// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/sign/ed25519"
)

// uncancellableAppender is an Ed25519 Signer whose AppendSign signs under a
// context that ended.
type uncancellableAppender struct {
	*ed25519.Signer
}

// AppendSign appends the signature of msg to dst under a context that does not
// end.
func (s uncancellableAppender) AppendSign(ctx context.Context, dst, msg []byte) ([]byte, error) {
	return s.Signer.AppendSign(context.WithoutCancel(ctx), dst, msg)
}

// TestAppend checks that AssertAppendSignerContract passes for an Ed25519
// Signer, and fails for a signer whose AppendSign signs under an ended
// context.
func TestAppend(t *testing.T) {
	t.Parallel()

	t.Run("AssertAppendSignerContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for an Ed25519 Signer", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertAppendSignerContract(t, newEd25519(t))
		})

		t.Run("fails for a signer whose AppendSign signs under an ended context", func(t *testing.T) {
			t.Parallel()
			s := uncancellableAppender{Signer: newEd25519(t)}
			got := assert.Rejects(t, "a signer that ignores its context fails the contract", func(tb assert.TB) {
				cryptotest.AssertAppendSignerContract(tb, s)
			})
			assert.Length(t, got, 1, "the contract fails once")
			assert.Equal(t, got[0].Contract, "AppendSign returns an error that wraps the cause of the context",
				"the contract fails at the error of AppendSign")
		})
	})
}
