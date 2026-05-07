// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"testing"

	"go.thesmos.sh/testkit"
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
		stdErr := testkit.TestError("entropy source failure")
		_, err := wrapGenerate(nil, stdErr)
		testkit.Error(t, err, "wrapGenerate must return non-nil error")
		testkit.ErrorIs(t, err, stdErr, "wrap must preserve the stdlib cause")
		testkit.Contains(t, err.Error(), "ecdsap384: generate:",
			"wrap must include package context")
	})

	t.Run("success path delegates to New", func(t *testing.T) {
		t.Parallel()
		// Generate a real keypair via the production path so
		// New(priv) actually succeeds.
		priv, err := ecdsa.GenerateKey(elliptic.P384(), stdrand.Reader)
		testkit.NoError(t, err, "GenerateKey")
		s, werr := wrapGenerate(priv, nil)
		testkit.NoError(t, werr, "wrapGenerate(priv, nil)")
		testkit.True(t, s != nil, "wrapGenerate must return non-nil signer on success")
	})
}

// TestWrapSign exercises the error branch of [wrapSign]
// directly. Same rationale as [TestWrapGenerate].
func TestWrapSign(t *testing.T) {
	t.Parallel()

	t.Run("propagates stdlib error wrapped with package context", func(t *testing.T) {
		t.Parallel()
		stdErr := testkit.TestError("signing failure")
		_, err := wrapSign(nil, stdErr)
		testkit.Error(t, err, "wrapSign must return non-nil error")
		testkit.ErrorIs(t, err, stdErr, "wrap must preserve the stdlib cause")
		testkit.Contains(t, err.Error(), "ecdsap384: sign:",
			"wrap must include package context")
	})

	t.Run("success path returns the signature unchanged", func(t *testing.T) {
		t.Parallel()
		want := []byte{0x01, 0x02, 0x03}
		got, err := wrapSign(want, nil)
		testkit.NoError(t, err, "wrapSign(sig, nil)")
		testkit.Equal(t, got, want, "wrapSign must return the signature unchanged on success")
	})
}
