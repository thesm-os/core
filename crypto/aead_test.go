// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"io"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/fixed"
)

// These tests cover the Seal / Open helpers, which are the seam's
// nonce-management layer. Per-implementation behaviour is asserted by
// the contract suite in each implementation's own package.

func newAEAD(tb testing.TB) crypto.AEAD {
	tb.Helper()

	k := make([]byte, aesgcm.KeySize256)
	for i := range k {
		k[i] = byte(i)
	}
	a, err := aesgcm.New(k)
	testkit.NoError(tb, err, "New must accept a 32-byte key")

	return a
}

func TestSealPropagatesEntropyFailure(t *testing.T) {
	t.Parallel()

	// Seal must not fall back to a partial or predictable nonce when
	// the entropy source fails: a repeated nonce breaks the whole
	// construction, so failing loudly is the only safe answer.
	a := newAEAD(t)
	failing := randcrypto.NewWithReader(&testkit.FailingReader{
		Source:     bytes.NewReader(nil),
		BeforeFail: 0,
		Err:        io.ErrUnexpectedEOF,
	})

	sealed, err := crypto.Seal(a, failing, []byte("payload"), nil)
	testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "Seal must surface the entropy failure")
	testkit.Equal(t, sealed, []byte(nil), "Seal must not return output alongside an error")
}

func TestSealWithDeterministicSourceRepeatsTheNonce(t *testing.T) {
	t.Parallel()

	// Documents the hazard the Seal doc warns about rather than
	// endorsing it: a fixed source produces the same nonce every
	// call, which is exactly what must never happen in production.
	a := newAEAD(t)

	first, err := crypto.Seal(a, fixed.New(1), []byte("payload"), nil)
	testkit.NoError(t, err, "Seal must succeed")
	second, err := crypto.Seal(a, fixed.New(1), []byte("payload"), nil)
	testkit.NoError(t, err, "Seal must succeed")

	testkit.Equal(t, first, second,
		"a deterministic source repeats the nonce — the documented hazard")
}

func TestOpenRejectsTruncatedInput(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)
	for _, n := range []int{0, 1, a.NonceSize() - 1} {
		_, err := crypto.Open(a, make([]byte, n), nil)
		testkit.ErrorIs(t, err, crypto.ErrCiphertextShort,
			"input too short to contain a nonce must report ErrCiphertextShort")
	}
}

func TestOpenAcceptsExactlyANonce(t *testing.T) {
	t.Parallel()

	// A nonce with no ciphertext is long enough to attempt, so it
	// must fail authentication rather than report a size error —
	// the two are different failures and must stay distinguishable.
	a := newAEAD(t)
	_, err := crypto.Open(a, make([]byte, a.NonceSize()), nil)
	testkit.Error(t, err, "a nonce with no tag must fail")
	testkit.ErrorIsNot(t, err, crypto.ErrCiphertextShort,
		"a full-length nonce is not a size error")
}

func BenchmarkSealSeam(b *testing.B) {
	a := newAEAD(b)
	r := randcrypto.New()
	plaintext := bytes.Repeat([]byte{0x5A}, 1024)
	b.ReportAllocs()

	var sink []byte
	for b.Loop() {
		sink, _ = crypto.Seal(a, r, plaintext, nil)
	}
	testkit.True(b, sink != nil, "Seal must produce output")
}
