// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"errors"
	"strings"
	"testing"
)

// TestWrapGenerate exercises the error branch of [wrapGenerate]
// directly. The branch is unreachable through the public
// [Generate] API because Go 1.26's [ecdsa.GenerateKey] uses an
// internal secure RNG that cannot fail in default mode; the
// extraction lets us cover the wrap behaviour without depending
// on stdlib-internal failure modes.
func TestWrapGenerate(t *testing.T) {
	t.Parallel()

	t.Run("propagates stdlib error wrapped with package context", func(t *testing.T) {
		t.Parallel()
		stdErr := errors.New("entropy source failure")
		_, err := wrapGenerate(nil, stdErr)
		if err == nil {
			t.Fatal("wrapGenerate(nil, err) returned nil error")
		}
		if !errors.Is(err, stdErr) {
			t.Fatalf("wrap lost the stdlib cause: got %v, want errors.Is to match", err)
		}
		if !strings.Contains(err.Error(), "crypto/sign/ecdsap384: generate:") {
			t.Fatalf("wrap missing package context: %v", err)
		}
	})

	t.Run("success path delegates to New", func(t *testing.T) {
		t.Parallel()
		// Generate a real keypair via the production path so
		// New(priv) actually succeeds.
		priv, err := ecdsa.GenerateKey(elliptic.P384(), stdrand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		s, werr := wrapGenerate(priv, nil)
		if werr != nil {
			t.Fatalf("wrapGenerate(priv, nil): %v", werr)
		}
		if s == nil {
			t.Fatal("wrapGenerate returned nil signer on success")
		}
	})
}

// TestWrapSign exercises the error branch of [wrapSign]
// directly. Same rationale as [TestWrapGenerate].
func TestWrapSign(t *testing.T) {
	t.Parallel()

	t.Run("propagates stdlib error wrapped with package context", func(t *testing.T) {
		t.Parallel()
		stdErr := errors.New("signing failure")
		_, err := wrapSign(nil, stdErr)
		if err == nil {
			t.Fatal("wrapSign(nil, err) returned nil error")
		}
		if !errors.Is(err, stdErr) {
			t.Fatalf("wrap lost the stdlib cause: got %v, want errors.Is to match", err)
		}
		if !strings.Contains(err.Error(), "crypto/sign/ecdsap384: sign:") {
			t.Fatalf("wrap missing package context: %v", err)
		}
	})

	t.Run("success path returns the signature unchanged", func(t *testing.T) {
		t.Parallel()
		want := []byte{0x01, 0x02, 0x03}
		got, err := wrapSign(want, nil)
		if err != nil {
			t.Fatalf("wrapSign(sig, nil): %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("wrapSign altered the signature: got %x, want %x", got, want)
		}
	})
}
