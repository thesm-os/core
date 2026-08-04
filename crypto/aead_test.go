// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"crypto/cipher"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	randcrypto "go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/fixed"
)

// These tests cover the Seal / Open helpers, which are the seam's
// envelope and nonce-management layer. Per-implementation behaviour is
// asserted by the contract suite in each implementation's own package.

func newAEAD(tb testing.TB) crypto.AEAD {
	tb.Helper()

	return newAEADOfSize(tb, aesgcm.KeySize256)
}

func newAEADOfSize(tb testing.TB, size int) crypto.AEAD {
	tb.Helper()

	k := make([]byte, size)
	for i := range k {
		k[i] = byte(i)
	}
	a, err := aesgcm.New(k)
	testkit.NoError(tb, err, "New must accept a valid key")

	return a
}

// relabelled wraps an AEAD and reports a different Algorithm, leaving
// the key and the underlying cipher untouched. It is what isolates the
// algorithm binding: two relabelled AEADs over one key differ in
// nothing a tag could otherwise notice.
type relabelled struct {
	crypto.AEAD
	name crypto.Algorithm
}

func (r relabelled) Algorithm() crypto.Algorithm { return r.name }

// header returns the envelope bytes preceding the nonce.
func header(tb testing.TB, sealed []byte) []byte {
	tb.Helper()
	testkit.True(tb, len(sealed) >= 2, "an envelope carries a version and a length")

	return sealed[:2+int(sealed[1])]
}

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)

	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), []byte("aad"))
	testkit.NoError(t, err, "Seal must succeed")

	opened, err := crypto.Open(a, sealed, []byte("aad"))
	testkit.NoError(t, err, "Open must succeed on Seal's own output")
	testkit.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
}

func TestSealEnvelopeLayout(t *testing.T) {
	t.Parallel()

	// The wire format is a contract core owns, so its shape is asserted
	// rather than inferred from a round trip.
	a := newAEAD(t)
	plaintext := []byte("payload")

	sealed, err := crypto.Seal(a, randcrypto.New(), plaintext, nil)
	testkit.NoError(t, err, "Seal must succeed")

	alg := string(a.Algorithm())
	testkit.Equal(t, sealed[0], byte(crypto.EnvelopeVersion), "the version leads the envelope")
	testkit.Equal(t, int(sealed[1]), len(alg), "the algorithm length follows the version")
	testkit.Equal(t, string(sealed[2:2+len(alg)]), alg, "the algorithm name follows its length")
	testkit.Equal(t, len(sealed), 2+len(alg)+a.NonceSize()+len(plaintext)+a.Overhead(),
		"the envelope is exactly its header, nonce, ciphertext and tag")
}

func TestSealSizesItsBufferExactly(t *testing.T) {
	t.Parallel()

	// Seal sizes the envelope up front rather than regrowing per
	// field. An exact fit is the observable proof: any growth, or any
	// over-estimate, leaves spare capacity behind.
	a := newAEAD(t)

	for _, n := range []int{0, 1, 1024} {
		sealed, err := crypto.Seal(a, randcrypto.New(), make([]byte, n), nil)
		testkit.NoError(t, err, "Seal must succeed")
		testkit.Equal(t, cap(sealed), len(sealed),
			"the envelope buffer must be sized exactly, never regrown")
	}
}

func TestSealAcceptsTheLongestExpressibleAlgorithm(t *testing.T) {
	t.Parallel()

	// 255 is what the length byte can express, so it must be accepted;
	// 256 is rejected by TestSealRejectsAnUnrepresentableAlgorithm.
	base := newAEAD(t)
	name := crypto.Algorithm(strings.Repeat("x", 255))
	a := relabelled{AEAD: base, name: name}

	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
	testkit.NoError(t, err, "a 255-byte name must fit the header")

	got, err := crypto.PeekAlgorithm(sealed)
	testkit.NoError(t, err, "the header must parse back")
	testkit.Equal(t, got, name, "the whole name must survive")
}

func TestSealBindsTheAlgorithmIntoTheTag(t *testing.T) {
	t.Parallel()

	// The crown of the envelope design: rewriting the algorithm name
	// must invalidate the tag. Both AEADs share one key and one cipher
	// and differ only in the name they report, so nothing but the
	// binding can distinguish them.
	const (
		nameA = "test-aead-aaa"
		nameB = "test-aead-bbb"
	)
	testkit.Equal(t, len(nameA), len(nameB), "the rewrite must not change any length")

	base := newAEAD(t)
	from := relabelled{AEAD: base, name: nameA}
	to := relabelled{AEAD: base, name: nameB}

	sealed, err := crypto.Seal(from, randcrypto.New(), []byte("payload"), nil)
	testkit.NoError(t, err, "Seal must succeed")

	// Rewrite the name in place. The envelope now parses cleanly and
	// names an algorithm the opening AEAD agrees to.
	forged := bytes.Clone(sealed)
	copy(forged[2:2+len(nameA)], nameB)

	_, err = crypto.Open(to, forged, nil)
	testkit.Error(t, err, "a rewritten algorithm must not authenticate")
	testkit.ErrorIsNot(t, err, crypto.ErrAlgorithmMismatch,
		"the rewrite defeats the name check — only the tag catches it")
}

func TestSealFramesTheAssociatedData(t *testing.T) {
	t.Parallel()

	// Pins the construction: an independently rebuilt frame must open
	// what Seal produced. If the framing changes, this fails rather
	// than silently redefining a format other implementations rebuild.
	a := newAEAD(t)
	callerAAD := []byte("caller")

	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), callerAAD)
	testkit.NoError(t, err, "Seal must succeed")

	f := crypto.NewFramer(nil, crypto.Domain{
		Name:    crypto.EnvelopeDomainName,
		Version: crypto.EnvelopeVersion,
	})
	f.String(string(a.Algorithm()))
	f.Bytes(callerAAD)

	rest := sealed[len(header(t, sealed)):]
	nonce, body := rest[:a.NonceSize()], rest[a.NonceSize():]

	opened, err := cipher.AEAD.Open(a, nil, nonce, body, f.Frame())
	testkit.NoError(t, err, "a rebuilt frame must open the envelope")
	testkit.Equal(t, opened, []byte("payload"), "the plaintext must survive the rebuild")
}

func TestPeekAlgorithm(t *testing.T) {
	t.Parallel()

	t.Run("reads the name without a key", func(t *testing.T) {
		t.Parallel()
		a := newAEAD(t)
		sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
		testkit.NoError(t, err, "Seal must succeed")

		got, err := crypto.PeekAlgorithm(sealed)
		testkit.NoError(t, err, "the header is readable without opening")
		testkit.Equal(t, got, a.Algorithm(), "the name must be the one that sealed it")
	})

	t.Run("rejects a truncated envelope", func(t *testing.T) {
		t.Parallel()
		for _, n := range []int{0, 1} {
			_, err := crypto.PeekAlgorithm(make([]byte, n))
			testkit.ErrorIs(t, err, crypto.ErrCiphertextShort,
				"too short to carry a version and a length")
		}

		// Declares a name longer than the bytes that follow.
		_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 8, 'a', 'b'})
		testkit.ErrorIs(t, err, crypto.ErrCiphertextShort, "the name must fit")
	})

	t.Run("rejects an unknown version", func(t *testing.T) {
		t.Parallel()
		_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion + 1, 1, 'x'})
		testkit.ErrorIs(t, err, crypto.ErrEnvelopeVersion, "an unknown layout must be refused")
	})

	t.Run("rejects a zero-length name", func(t *testing.T) {
		t.Parallel()
		_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 0})
		testkit.ErrorIs(t, err, crypto.ErrAlgorithmSize, "an envelope must name something")
	})

	t.Run("accepts a header with nothing after it", func(t *testing.T) {
		t.Parallel()
		// The name ends exactly at the end of the input. Peeking reads
		// only the header, so there is nothing further to require.
		got, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 2, 'h', 'i'})
		testkit.NoError(t, err, "a complete header needs no body to parse")
		testkit.Equal(t, got, crypto.Algorithm("hi"), "the name must be read whole")
	})
}

func TestOpenRejections(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)
	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), []byte("aad"))
	testkit.NoError(t, err, "Seal must succeed")

	t.Run("an unknown version", func(t *testing.T) {
		t.Parallel()
		forged := bytes.Clone(sealed)
		forged[0]++

		_, err := crypto.Open(a, forged, []byte("aad"))
		testkit.ErrorIs(t, err, crypto.ErrEnvelopeVersion,
			"an unknown layout is refused before any key is used")
	})

	t.Run("a truncated envelope", func(t *testing.T) {
		t.Parallel()
		// Every prefix short of a complete header and nonce.
		for n := range len(header(t, sealed)) + a.NonceSize() {
			_, err := crypto.Open(a, sealed[:n], []byte("aad"))
			testkit.ErrorIs(t, err, crypto.ErrCiphertextShort,
				"a prefix too short to hold a header and nonce must report a size error")
		}
	})

	t.Run("a different algorithm", func(t *testing.T) {
		t.Parallel()
		// Sealed under AES-256-GCM, opened with AES-128-GCM.
		other := newAEADOfSize(t, aesgcm.KeySize128)

		_, err := crypto.Open(other, sealed, []byte("aad"))
		testkit.ErrorIs(t, err, crypto.ErrAlgorithmMismatch,
			"the envelope names an algorithm this AEAD is not")
	})

	t.Run("a zero-length name", func(t *testing.T) {
		t.Parallel()
		_, err := crypto.Open(a, []byte{crypto.EnvelopeVersion, 0}, nil)
		testkit.ErrorIs(t, err, crypto.ErrAlgorithmSize, "an envelope must name something")
	})

	t.Run("a nonce with no body", func(t *testing.T) {
		t.Parallel()
		// Long enough to attempt, so it must fail authentication rather
		// than report a size error — the two are different failures and
		// must stay distinguishable.
		_, err := crypto.Open(a, sealed[:len(header(t, sealed))+a.NonceSize()], []byte("aad"))
		testkit.Error(t, err, "a nonce with no tag must fail")
		testkit.ErrorIsNot(t, err, crypto.ErrCiphertextShort,
			"a full-length nonce is not a size error")
	})

	t.Run("the wrong associated data", func(t *testing.T) {
		t.Parallel()
		_, err := crypto.Open(a, sealed, []byte("other"))
		testkit.Error(t, err, "associated data is authenticated")
		testkit.ErrorIsNot(t, err, crypto.ErrCiphertextShort, "not a size failure")
		testkit.ErrorIsNot(t, err, crypto.ErrAlgorithmMismatch, "not a name failure")
	})

	t.Run("a tampered body", func(t *testing.T) {
		t.Parallel()
		forged := bytes.Clone(sealed)
		forged[len(forged)-1] ^= 0xFF

		_, err := crypto.Open(a, forged, []byte("aad"))
		testkit.Error(t, err, "a modified tag must not authenticate")
	})

	t.Run("a tampered nonce", func(t *testing.T) {
		t.Parallel()
		forged := bytes.Clone(sealed)
		forged[len(header(t, sealed))] ^= 0xFF

		_, err := crypto.Open(a, forged, []byte("aad"))
		testkit.Error(t, err, "a modified nonce must not authenticate")
	})

	t.Run("a headerless ciphertext", func(t *testing.T) {
		t.Parallel()
		// The pre-envelope framing. Open must not fall back to it: a
		// reader that does is one an attacker forces into the weaker
		// mode by stripping bytes.
		legacy := sealed[len(header(t, sealed)):]

		_, err := crypto.Open(a, legacy, []byte("aad"))
		testkit.Error(t, err, "a bare nonce-prefixed ciphertext must not open")
	})
}

func TestSealRejectsAnUnrepresentableAlgorithm(t *testing.T) {
	t.Parallel()

	// A defect in the implementation rather than in the input, but this
	// module does not panic in production paths.
	base := newAEAD(t)
	for _, name := range []crypto.Algorithm{"", crypto.Algorithm(strings.Repeat("x", 256))} {
		_, err := crypto.Seal(relabelled{AEAD: base, name: name}, randcrypto.New(), nil, nil)
		testkit.ErrorIs(t, err, crypto.ErrAlgorithmSize,
			"a name the header cannot express must be refused")
	}
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

func TestAppendSealAndAppendOpen(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)

	t.Run("append into a used buffer", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("keep me")
		dst := make([]byte, len(prefix), 512)
		copy(dst, prefix)

		dst, err := crypto.AppendSeal(dst, a, randcrypto.New(), []byte("payload"), nil)
		testkit.NoError(t, err, "AppendSeal must succeed")
		testkit.Equal(t, dst[:len(prefix)], prefix, "AppendSeal must not disturb the prefix")

		out := make([]byte, 0, 64)
		out, err = crypto.AppendOpen(out, a, dst[len(prefix):], nil)
		testkit.NoError(t, err, "AppendOpen must succeed")
		testkit.Equal(t, out, []byte("payload"), "AppendOpen must recover the plaintext")
	})

	t.Run("a failure leaves the destination alone", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("keep me")
		dst := make([]byte, len(prefix), 512)
		copy(dst, prefix)

		got, err := crypto.AppendOpen(dst, a, []byte{crypto.EnvelopeVersion + 1, 1, 'x'}, nil)
		testkit.ErrorIs(t, err, crypto.ErrEnvelopeVersion, "the version must be refused")
		testkit.Equal(t, got, []byte(nil), "a failed append returns no partial output")
		testkit.Equal(t, dst, prefix, "the caller's buffer is untouched")
	})
}

func BenchmarkAEAD(b *testing.B) {
	a := newAEAD(b)
	r := randcrypto.New()
	plaintext := bytes.Repeat([]byte{0x5A}, 1024)

	b.Run("Seal", func(b *testing.B) {
		var sink []byte

		b.ReportAllocs()
		for b.Loop() {
			sink, _ = crypto.Seal(a, r, plaintext, nil)
		}
		testkit.True(b, sink != nil, "Seal must produce output")
	})

	b.Run("AppendSeal", func(b *testing.B) {
		dst := make([]byte, 0, 2048)

		b.ReportAllocs()
		for b.Loop() {
			dst, _ = crypto.AppendSeal(dst[:0], a, r, plaintext, nil)
		}
		testkit.True(b, dst != nil, "AppendSeal must produce output")
	})

	b.Run("BaselineSeal", func(b *testing.B) {
		// The embedded primitive with a sized destination and a caller
		// -managed nonce. The envelope's cost is the difference between
		// this and AppendSeal.
		nonce := make([]byte, a.NonceSize())
		dst := make([]byte, 0, 2048)

		b.ReportAllocs()
		for b.Loop() {
			dst = cipher.AEAD.Seal(a, dst[:0], nonce, plaintext, nil)
		}
		testkit.True(b, dst != nil, "the primitive must produce output")
	})

	b.Run("AppendOpen", func(b *testing.B) {
		sealed, err := crypto.AppendSeal(nil, a, r, plaintext, nil)
		testkit.NoError(b, err, "AppendSeal must succeed")
		dst := make([]byte, 0, 2048)

		b.ReportAllocs()
		for b.Loop() {
			dst, _ = crypto.AppendOpen(dst[:0], a, sealed, nil)
		}
		testkit.True(b, dst != nil, "AppendOpen must produce output")
	})

	b.Run("PeekAlgorithm", func(b *testing.B) {
		sealed, err := crypto.Seal(a, r, plaintext, nil)
		testkit.NoError(b, err, "Seal must succeed")

		var sink crypto.Algorithm

		b.ReportAllocs()
		for b.Loop() {
			sink, _ = crypto.PeekAlgorithm(sealed)
		}
		testkit.True(b, sink != "", "PeekAlgorithm must produce a name")
	})
}
