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
// expectedSize is the Hasher's output size in bytes; the panic
// message must mention this number.
func HasherCombinePanicsOnSizeMismatch(expectedSize int, wrongSizeDigest crypto.Digest) HasherOption {
	return HasherCustom("Combine panics on size mismatch", func(t *testing.T, h crypto.Hasher) {
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
