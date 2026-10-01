// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"fmt"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

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
			testkit.NoError(t, err, "AppendSign must succeed")
			testkit.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			testkit.Equal(t, s.appends.Load(), int32(1), "AppendSign must be called once")
			testkit.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls AppendSign on a signer behind a decorator", func(t *testing.T) {
			t.Parallel()
			s := &appending{Signer: newEd25519(t)}
			_, err := sign.AppendSign(t.Context(), signerDecorator{s}, nil, []byte("payload"))
			testkit.NoError(t, err, "AppendSign must succeed")
			testkit.Equal(t, s.appends.Load(), int32(1), "AppendSign must be called on the wrapped signer")
		})

		t.Run("calls SignContext on a ContextSigner without AppendSign", func(t *testing.T) {
			t.Parallel()
			s := &contextual{counting{Signer: newEd25519(t)}}
			sig, err := sign.AppendSign(t.Context(), s, nil, []byte("payload"))
			testkit.NoError(t, err, "AppendSign must succeed")
			testkit.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			testkit.Equal(t, s.signContexts.Load(), int32(1), "SignContext must be called once")
			testkit.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})

		t.Run("calls Sign on a signer without either capability", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			sig, err := sign.AppendSign(t.Context(), opaqueSigner{s}, nil, []byte("payload"))
			testkit.NoError(t, err, "AppendSign must succeed")
			testkit.True(t, s.Verify([]byte("payload"), sig), "the signature must verify")
			testkit.Equal(t, s.signs.Load(), int32(1), "Sign must be called once")
		})

		t.Run("appends the signature after the bytes of dst", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			out, err := sign.AppendSign(t.Context(), opaqueSigner{s}, []byte("prefix"), []byte("payload"))
			testkit.NoError(t, err, "AppendSign must succeed")
			testkit.True(t, bytes.HasPrefix(out, []byte("prefix")), "AppendSign must keep the bytes of dst")
			testkit.True(t, s.Verify([]byte("payload"), out[len("prefix"):]), "the appended signature must verify")
		})

		t.Run("returns dst unchanged and the cause of an ended context", func(t *testing.T) {
			t.Parallel()
			s := &counting{Signer: newEd25519(t)}
			cause := testkit.TestError("the caller gave up")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			out, err := sign.AppendSign(ctx, opaqueSigner{s}, []byte("prefix"), []byte("payload"))
			testkit.ErrorIs(t, err, cause, "AppendSign must return the context's cause")
			testkit.Equal(t, string(out), "prefix", "AppendSign must return dst unchanged with an error")
			testkit.Equal(t, s.signs.Load(), int32(0), "Sign must not be called")
		})
	})
}

func BenchmarkAppend(b *testing.B) {
	b.Run("AppendSign", func(b *testing.B) {
		s := newEd25519(b)
		msg := []byte("payload")
		buf := make([]byte, 0, stded25519.SignatureSize)
		ctx := b.Context()

		if allocs := testing.AllocsPerRun(100, func() { buf, _ = sign.AppendSign(ctx, s, buf[:0], msg) }); allocs != 0 {
			b.Fatalf("AppendSign of an Ed25519 signer allocates %v times per call, want 0", allocs)
		}

		b.ReportAllocs()
		for b.Loop() {
			buf, _ = sign.AppendSign(ctx, s, buf[:0], msg)
		}
	})
}
