// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"context"
	stded25519 "crypto/ed25519"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto/sign"
)

// appending is a counting signer that also implements sign.AppendSigner,
// refusing a context that has ended.
type appending struct {
	counting

	appends atomic.Int32
}

// AppendSign counts the call, and appends the signature of the embedded
// signer over message to dst. It returns dst unchanged with the cause of a
// context that has ended, and with the error of the signer.
func (a *appending) AppendSign(ctx context.Context, dst, message []byte) ([]byte, error) {
	a.appends.Add(1)

	if err := context.Cause(ctx); err != nil {
		return dst, fmt.Errorf("appending: %w", err)
	}

	sig, err := a.Signer.Sign(message)
	if err != nil {
		return dst, err //nolint:wrapcheck // the test double passes the error through
	}

	return append(dst, sig...), nil
}

func TestAppend(t *testing.T) {
	t.Parallel()

	t.Run("AppendSign", func(t *testing.T) {
		t.Parallel()

		t.Run("calls AppendSign on a signer that has it", func(t *testing.T) {
			t.Parallel()
			s := &appending{Signer: newEd25519(t)}
			sig, err := sign.AppendSign(t.Context(), s, nil, []byte("payload"))
			assert.NoError(t, err, "AppendSign must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.appends.Load(), int32(1), "AppendSign must be called once")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls AppendSign on a signer behind a decorator", func(t *testing.T) {
			t.Parallel()
			s := &appending{Signer: newEd25519(t)}
			_, err := sign.AppendSign(t.Context(), signerDecorator{s}, nil, []byte("payload"))
			assert.NoError(t, err, "AppendSign must succeed")
			assert.Equal(t, s.appends.Load(), int32(1), "AppendSign must be called on the wrapped signer")
		})

		t.Run("calls SignContext on a ContextSigner without AppendSign", func(t *testing.T) {
			t.Parallel()
			s := &contextual{counting{Signer: newEd25519(t)}}
			sig, err := sign.AppendSign(t.Context(), s, nil, []byte("payload"))
			assert.NoError(t, err, "AppendSign must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.signContexts.Load(), int32(1), "SignContext must be called once")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls Sign on a signer without either capability", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			sig, err := sign.AppendSign(t.Context(), opaqueSigner{s}, nil, []byte("payload"))
			assert.NoError(t, err, "AppendSign must succeed")
			expect.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			expect.Equal(t, s.signs.Load(), int32(1), "Sign must be called once")
		})

		t.Run("appends the signature after the bytes of dst", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			out, err := sign.AppendSign(t.Context(), opaqueSigner{s}, []byte("prefix"), []byte("payload"))
			assert.NoError(t, err, "AppendSign must succeed")
			assert.HasPrefix(t, string(out), "prefix", "AppendSign must keep the bytes of dst")
			assert.True(t, s.Verify([]byte("payload"), out[len("prefix"):]), "the appended signature must verify")
		})

		t.Run("returns the cause of an ended context", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			cause := errors.New("the caller gave up")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			_, err := sign.AppendSign(ctx, opaqueSigner{s}, []byte("prefix"), []byte("payload"))
			assert.ErrorIs(t, err, cause, "AppendSign must return the cause of the context")
		})

		t.Run("returns dst unchanged with the cause of an ended context", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(errors.New("the caller gave up"))
			out, err := sign.AppendSign(ctx, opaqueSigner{s}, []byte("prefix"), []byte("payload"))
			assert.HasError(t, err, "the test must sign with a context that has ended")
			expect.Equal(t, string(out), "prefix", "AppendSign must return dst unchanged with an error")
			expect.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})
	})
}

// TestAppendAllocs checks that AppendSign of an Ed25519 signer into a dst
// with room allocates nothing. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestAppendAllocs(t *testing.T) {
	s := newEd25519(t)
	msg := []byte("payload")
	buf := make([]byte, 0, stded25519.SignatureSize)

	t.Run("AppendSign", func(t *testing.T) {
		expect.MaxAllocs(t, func() { buf, _ = sign.AppendSign(t.Context(), s, buf[:0], msg) }, 0,
			"AppendSign of an Ed25519 signer must not allocate")
		assert.True(t, s.Verify(msg, buf), "the test must measure a signature that verifies")
	})
}

// BenchmarkAppend reports the cost of AppendSign of an Ed25519 signer
// into a dst with room, and fails when it allocates.
func BenchmarkAppend(b *testing.B) {
	b.Run("AppendSign", func(b *testing.B) {
		s := newEd25519(b)
		msg := []byte("payload")
		buf := make([]byte, 0, stded25519.SignatureSize)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			buf, _ = sign.AppendSign(b.Context(), s, buf[:0], msg)
		}

		assert.True(b, s.Verify(msg, buf), "the benchmark must measure a signature that verifies")
	})
}
