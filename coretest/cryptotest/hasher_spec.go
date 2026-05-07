// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

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
			if !h.Hash(input).Equal(h.Hash(input)) {
				t.Fatal("Hash(x) != Hash(x): not deterministic")
			}
		}),

		HasherCustom("Hash of nil equals Hash of empty slice", func(t *testing.T, h crypto.Hasher) {
			if !h.Hash(nil).Equal(h.Hash([]byte{})) {
				t.Fatal("nil and empty slice produced different digests")
			}
		}),

		HasherCustom("distinct inputs produce distinct digests", func(t *testing.T, h crypto.Hasher) {
			if h.Hash([]byte("alpha")).Equal(h.Hash([]byte("beta"))) {
				t.Fatal("distinct inputs collided")
			}
		}),

		// --- Combine ---

		HasherCustom("Combine is deterministic", func(t *testing.T, h crypto.Hasher) {
			a := h.Hash([]byte("a"))
			b := h.Hash([]byte("b"))
			if !h.Combine(a, b).Equal(h.Combine(a, b)) {
				t.Fatal("Combine(a,b) != Combine(a,b): not deterministic")
			}
		}),

		HasherCustom("Combine is asymmetric", func(t *testing.T, h crypto.Hasher) {
			a := h.Hash([]byte("a"))
			b := h.Hash([]byte("b"))
			if h.Combine(a, b).Equal(h.Combine(b, a)) {
				t.Fatal("Combine(a,b) == Combine(b,a): unexpectedly symmetric")
			}
		}),

		// --- Stream ---

		HasherCustom("Stream Write+Sum equals Hash over same bytes", func(t *testing.T, h crypto.Hasher) {
			payload := []byte("the quick brown fox jumps over the lazy dog")
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write(payload)
			if !s.Sum().Equal(h.Hash(payload)) {
				t.Fatal("Stream Write+Sum != Hash over the same bytes")
			}
		}),

		HasherCustom("Stream split-Write equals single Write of concatenation", func(t *testing.T, h crypto.Hasher) {
			full := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write(full[:10])
			_, _ = s.Write(full[10:25])
			_, _ = s.Write(full[25:])
			if !s.Sum().Equal(h.Hash(full)) {
				t.Fatal("split Stream != single Hash of concatenation")
			}
		}),

		HasherCustom("Stream Reset clears state", func(t *testing.T, h crypto.Hasher) {
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("first"))
			_ = s.Sum()
			s.Reset()
			_, _ = s.Write([]byte("second"))
			if !s.Sum().Equal(h.Hash([]byte("second"))) {
				t.Fatal("Stream after Reset != Hash(\"second\")")
			}
		}),

		HasherCustom("Stream Sum is non-resetting (snapshot only)", func(t *testing.T, h crypto.Hasher) {
			s := h.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("ab"))
			_ = s.Sum() // snapshot
			_, _ = s.Write([]byte("c"))
			if !s.Sum().Equal(h.Hash([]byte("abc"))) {
				t.Fatal("Sum reset state — should snapshot only")
			}
		}),
	}
}

// HasherIDAssertion verifies [crypto.Hasher.ID] returns the
// expected stable build-local identifier.
func HasherIDAssertion(want crypto.ID) HasherOption {
	return HasherCustom("ID matches", func(t *testing.T, h crypto.Hasher) {
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
	})
}

// HasherAlgorithmAssertion verifies [crypto.Hasher.Algorithm]
// returns the expected long-term cross-build algorithm name.
func HasherAlgorithmAssertion(want crypto.Algorithm) HasherOption {
	return HasherCustom("Algorithm matches", func(t *testing.T, h crypto.Hasher) {
		if got := h.Algorithm(); got != want {
			t.Fatalf("Algorithm: got %q, want %q", got, want)
		}
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
			got := h.Hash(tc.data).Bytes()
			want := stdlib(tc.data)
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: cross-stdlib mismatch:\n got=%x\nwant=%x",
					tc.name, got, want)
			}
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
		assertPanic := func(name string, fn func()) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s: expected panic, got none", name)
				}
			}()
			fn()
		}
		assertPanic("wrong-left", func() { _ = h.Combine(wrongSizeDigest, correct) })
		assertPanic("wrong-right", func() { _ = h.Combine(correct, wrongSizeDigest) })
		assertPanic("both-wrong", func() { _ = h.Combine(wrongSizeDigest, wrongSizeDigest) })
		_ = expectedSize // future use: assert panic message contains size info
	})
}
