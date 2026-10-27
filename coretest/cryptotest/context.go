// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"context"
	"errors"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto/sign"
)

// AssertContextSignerContract asserts the [sign.ContextSigner] capability of s:
// s implements it, and SignContext of a context that ended returns no
// signature and an error that wraps the cause of the context. A test applies
// it to a signer that crosses a process boundary. The context derives from
// the context of tb.
func AssertContextSignerContract(tb assert.TB, s sign.Signer) {
	tb.Helper()

	cs, ok := sign.AsContextSigner(s)
	assert.True(tb, ok, "the signer implements ContextSigner")

	cause := errors.New("cryptotest: the caller gave up")
	ctx, cancel := context.WithCancelCause(assert.Context(tb))
	cancel(cause)

	sig, err := cs.SignContext(ctx, signPayload)
	assert.ErrorIs(tb, err, cause, "SignContext returns an error that wraps the cause of the context")
	assert.Nil(tb, sig, "SignContext returns no signature with an error")
}
