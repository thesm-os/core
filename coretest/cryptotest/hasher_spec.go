// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// HasherContractAssertions returns the generic assertions every
// [crypto.Hasher] implementation must satisfy: determinism,
// stream / Hash equivalence, Sum-non-resetting, Reset semantics.
// Compose with [HasherIDAssertion], [HasherAlgorithmAssertion],
// and [HasherCrossStdlibAssertion] at the consumer call site to
// add impl-specific constants and byte-exact stdlib equivalence.
//
//	cryptotest.AssertHasherContract(t, factory,
//	    append(cryptotest.HasherContractAssertions(),
//	        cryptotest.HasherIDAssertion(wantID),
//	        cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA256),
//	        cryptotest.HasherCrossStdlibAssertion(stdlibSum),
//	    )...,
//	)
func HasherContractAssertions() []HasherOption {
	return []HasherOption{
		// --- Hash ---

		HasherCustom("Hash is deterministic", func(t *testing.T, h crypto.Hasher) {
			input := []byte("the quick brown fox jumps over the lazy dog")
			testkit.True(t, h.Hash(input).Equal(h.Hash(input)),
				"Hash(x) must equal Hash(x) — Hash must be deterministic")
		}),

		HasherCustom("Hash of nil equals Hash of empty slice", func(t *testing.T, h crypto.Hasher) {
			testkit.True(t, h.Hash(nil).Equal(h.Hash([]byte{})),
				"Hash(nil) must equal Hash([]byte{})")
		}),

		HasherCustom("distinct inputs produce distinct digests", func(t *testing.T, h crypto.Hasher) {
			testkit.False(t, h.Hash([]byte("alpha")).Equal(h.Hash([]byte("beta"))),
				"distinct inputs must not collide to the same digest")
		}),

		// --- Combine ---

		HasherCustom("Combine is deterministic", func(t *testing.T, h crypto.Hasher) {
			a := h.Hash([]byte("a"))
			b := h.Hash([]byte("b"))
			testkit.True(t, h.Combine(a, b).Equal(h.Combine(a, b)),
				"Combine(a,b) must equal Combine(a,b) — Combine must be deterministic")
		}),

		HasherCustom("Combine is asymmetric", func(t *testing.T, h crypto.Hasher) {
			a := h.Hash([]byte("a"))
			b := h.Hash([]byte("b"))
			testkit.False(t, h.Combine(a, b).Equal(h.Combine(b, a)),
				"Combine(a,b) must not equal Combine(b,a) — order must matter")
		}),

		// --- Stream ---

		HasherCustom("Stream Write+Sum equals Hash over same bytes", func(t *testing.T, h crypto.Hasher) {
			payload := []byte("the quick brown fox jumps over the lazy dog")
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write(payload)
			testkit.True(t, s.Sum().Equal(h.Hash(payload)),
				"Stream Write+Sum must equal Hash over the same bytes")
		}),

		HasherCustom("Stream split-Write equals single Write of concatenation", func(t *testing.T, h crypto.Hasher) {
			full := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write(full[:10])
			_, _ = s.Write(full[10:25])
			_, _ = s.Write(full[25:])
			testkit.True(t, s.Sum().Equal(h.Hash(full)),
				"split-Write must equal Hash over the concatenation")
		}),

		HasherCustom("Stream Reset clears state", func(t *testing.T, h crypto.Hasher) {
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("first"))
			_ = s.Sum()
			s.Reset()
			_, _ = s.Write([]byte("second"))
			testkit.True(t, s.Sum().Equal(h.Hash([]byte("second"))),
				`Stream after Reset must equal Hash("second")`)
		}),

		HasherCustom("Stream Sum is non-resetting (snapshot only)", func(t *testing.T, h crypto.Hasher) {
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("ab"))
			_ = s.Sum() // snapshot
			_, _ = s.Write([]byte("c"))
			testkit.True(t, s.Sum().Equal(h.Hash([]byte("abc"))),
				`Sum must snapshot only — must not reset state`)
		}),
	}
}

// HasherIDAssertion verifies [crypto.Hasher.ID] returns the
// expected stable build-local identifier.
func HasherIDAssertion(want crypto.ID) HasherOption {
	return HasherCustom("ID matches", func(t *testing.T, h crypto.Hasher) {
		testkit.Equal(t, h.ID(), want, "ID must match expected build-local identifier")
	})
}

// HasherAlgorithmAssertion verifies [crypto.Hasher.Algorithm]
// returns the expected long-term cross-build algorithm name.
func HasherAlgorithmAssertion(want crypto.Algorithm) HasherOption {
	return HasherCustom("Algorithm matches", func(t *testing.T, h crypto.Hasher) {
		testkit.Equal(t, h.Algorithm(), want, "Algorithm must match expected name")
	})
}

// HasherCrossStdlibAssertion verifies that [crypto.Hasher.Hash]
// produces byte-identical output to the supplied stdlib
// reference across a sweep of test inputs (empty, short, FIPS
// 180-4 §B-style two-block, and 4 KiB random-ish data).
//
// Lock byte-exact compatibility with the stdlib reference so the
// hash implementation cannot drift silently from the published
// algorithm.
func HasherCrossStdlibAssertion(stdlib func([]byte) []byte) HasherOption {
	return HasherCustom("Hash matches stdlib byte-for-byte", func(t *testing.T, h crypto.Hasher) {
		cases := []struct {
			name string
			data []byte
		}{
			{"empty", []byte{}},
			{`"abc"`, []byte("abc")},
			{
				"FIPS 180-4 §B-style two-block",
				[]byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"),
			},
			{"4 KiB 0xAB", bytes.Repeat([]byte{0xAB}, 4096)},
		}
		for _, tc := range cases {
			testkit.Equal(t, h.Hash(tc.data).Bytes(), stdlib(tc.data),
				tc.name+": Hash output must byte-match stdlib")
		}
	})
}

// HasherCombinePanicsOnSizeMismatch verifies [crypto.Hasher.Combine]
// panics when given digests whose [crypto.Digest.Size] does not
// match this Hasher's output size. The contract requires
// surfacing programmer errors rather than silently producing a
// truncated digest.
//
// The zero [crypto.Digest] is exempt — it is a documented sentinel
// rather than a programmer error, and is covered by
// [HasherCombineAdmitsZeroDigest].
//
// expectedSize is the Hasher's output size in bytes; the panic
// message must mention this number.
func HasherCombinePanicsOnSizeMismatch(expectedSize int, wrongSizeDigest crypto.Digest) HasherOption {
	return HasherCustom("Combine panics on size mismatch", func(t *testing.T, h crypto.Hasher) {
		testkit.False(t, wrongSizeDigest.IsZero(),
			"wrongSizeDigest must not be the zero Digest — that is an admitted sentinel")
		correct := h.Hash([]byte{}) // canonical correctly-sized digest
		testkit.Panics(t, func() { _ = h.Combine(wrongSizeDigest, correct) },
			"Combine(wrong-left, correct) must panic")
		testkit.Panics(t, func() { _ = h.Combine(correct, wrongSizeDigest) },
			"Combine(correct, wrong-right) must panic")
		testkit.Panics(t, func() { _ = h.Combine(wrongSizeDigest, wrongSizeDigest) },
			"Combine(wrong, wrong) must panic")
		_ = expectedSize // future use: assert panic message contains size info
	})
}

// HasherCombineAdmitsZeroDigest verifies [crypto.Hasher.Combine]
// accepts the zero [crypto.Digest] as either operand, zero-padded to
// this Hasher's digest width, per ADR-0007.
//
// The zero Digest is the documented sentinel for "no digest
// computed" — the predecessor anchor of a hash chain's genesis
// entry. Panicking on it would make the documentation a trap, so it
// is admitted while every other size mismatch still panics.
//
// zeroPadded must be a digest of this Hasher's own width whose bytes
// are all zero: the assertion checks that combining with the
// sentinel produces the same digest as combining with that value,
// which is what "zero-padded to the hasher's width" means.
func HasherCombineAdmitsZeroDigest(zeroPadded crypto.Digest) HasherOption {
	return HasherCustom("Combine admits the zero Digest", func(t *testing.T, h crypto.Hasher) {
		var zero crypto.Digest
		correct := h.Hash([]byte{})

		testkit.True(t, zero.IsZero(), "the zero Digest must report IsZero")
		testkit.Equal(t, zeroPadded.Size(), correct.Size(),
			"zeroPadded must match the hasher's digest width")

		// None of the Combine calls below may panic. A regression to
		// the old reject-everything behaviour surfaces as a panic
		// that fails this test directly, carrying the
		// implementation's own size-mismatch message.
		testkit.True(t, h.Combine(zero, correct).Equal(h.Combine(zeroPadded, correct)),
			"Combine(zero, x) must equal Combine(zero-padded, x)")
		testkit.True(t, h.Combine(correct, zero).Equal(h.Combine(correct, zeroPadded)),
			"Combine(x, zero) must equal Combine(x, zero-padded)")
		testkit.True(t, h.Combine(zero, zero).Equal(h.Combine(zeroPadded, zeroPadded)),
			"Combine(zero, zero) must equal Combine(zero-padded, zero-padded)")

		// The genesis digest must still be a well-formed digest of
		// this hasher's width, so a chain can keep combining from it.
		testkit.Equal(t, h.Combine(zero, correct).Size(), correct.Size(),
			"Combine with the sentinel must return a full-width digest")
	})
}
