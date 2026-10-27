// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"context"
	"errors"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto/sign"
)

// signPrefix is the bytes of dst that AssertAppendSignerContract passes to
// AppendSign.
const signPrefix = "prefix"

// AssertAppendSignerContract asserts the [sign.AppendSigner] capability of s:
//
//   - s implements it.
//   - AppendSign appends a signature that verifies after the bytes that dst
//     has.
//   - AppendSign of a context that ended returns dst unchanged and an error
//     that wraps the cause of the context.
//
// The contexts derive from the context of tb.
func AssertAppendSignerContract(tb assert.TB, s sign.Signer) {
	tb.Helper()

	as, ok := sign.AsAppendSigner(s)
	assert.True(tb, ok, "the signer implements AppendSigner")

	out, err := as.AppendSign(assert.Context(tb), []byte(signPrefix), signPayload)
	assert.NoError(tb, err, "AppendSign signs the message")
	assert.HasPrefix(tb, out, signPrefix, "AppendSign keeps the bytes of dst")
	assert.True(tb, s.Verify(signPayload, out[len(signPrefix):]), "the signature that AppendSign appended verifies")

	cause := errors.New("cryptotest: the caller gave up")
	ctx, cancel := context.WithCancelCause(assert.Context(tb))
	cancel(cause)

	out, err = as.AppendSign(ctx, []byte(signPrefix), signPayload)
	assert.ErrorIs(tb, err, cause, "AppendSign returns an error that wraps the cause of the context")
	assert.Equal(tb, out, []byte(signPrefix), "AppendSign returns dst unchanged with an error")
}
