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
// [crypto.Hasher] implementation must satisfy. Compose them with
// [HasherIDAssertion], [HasherAlgorithmAssertion] and
// [HasherCrossStdlibAssertion] at the call site to add the
// implementation's constants and byte-exact stdlib equivalence.
//
//	cryptotest.AssertHasherContract(t, factory,
//	    append(cryptotest.HasherContractAssertions(),
//	        cryptotest.HasherIDAssertion(wantID),
//	        cryptotest.HasherAlgorithmAssertion(crypto.AlgSHA256),
//	        cryptotest.HasherCrossStdlibAssertion(stdlibSum),
//	    )...,
//	)
//
// The assertions cover:
//
//   - Hash: determinism, nil equal to empty, and distinct digests for
//     distinct inputs.
//   - The tagged byte layout: HashTagged is one role byte followed by
//     the data, and CombineTagged is one role byte followed by both
//     operands, with no framing or length prefix.
//   - Domain separation: a payload of two concatenated sibling digests,
//     offered as a leaf, does not hash to their interior node. Distinct
//     binary roles over one pair, such as a chain link, a batch node
//     and an accumulator node, give distinct digests.
//   - Each arity half refuses the other's roles at the 0x80 boundary,
//     and the roles on either side of it are accepted. Only a refusal
//     test proves the guard exists, and without it one role could serve
//     both a leaf and a node.
//   - CombineTagged refuses the zero Digest, because a chain's first
//     link is a unary role over one operand, and refuses an operand of
//     the wrong width.
//   - Stream: Write and Sum equal Hash, split writes equal one write of
//     the concatenation, Reset clears the state, and Sum does not
//     reset it.
//
// The roles are example bytes. Core ships none, and a hasher that
// passes makes no claim about the roles any protocol assigns.
func HasherContractAssertions() []HasherOption {
	return []HasherOption{
		HasherCustom("Hash is deterministic", func(t *testing.T, h crypto.Hasher) {
			input := []byte("the quick brown fox jumps over the lazy dog")
			testkit.True(t, h.Hash(input).Equal(h.Hash(input)),
				"Hash(x) must equal Hash(x)")
		}),

		HasherCustom("Hash of nil equals Hash of empty slice", func(t *testing.T, h crypto.Hasher) {
			testkit.True(t, h.Hash(nil).Equal(h.Hash([]byte{})),
				"Hash(nil) must equal Hash([]byte{})")
		}),

		HasherCustom("distinct inputs produce distinct digests", func(t *testing.T, h crypto.Hasher) {
			testkit.False(t, h.Hash([]byte("alpha")).Equal(h.Hash([]byte("beta"))),
				"distinct inputs must not collide to the same digest")
		}),

		HasherCustom("HashTagged is the role byte followed by the data", func(t *testing.T, h crypto.Hasher) {
			data := []byte(`{"act":"infer","id":1}`)
			framed := append([]byte{0x01}, data...)
			testkit.True(t, h.HashTagged(0x01, data).Equal(h.Hash(framed)),
				"HashTagged(r, data) must equal Hash(r || data), with one role byte and no framing")
		}),

		HasherCustom("CombineTagged is the role byte followed by both operands", func(t *testing.T, h crypto.Hasher) {
			left := h.Hash([]byte("left"))
			right := h.Hash([]byte("right"))
			framed := append([]byte{0x84}, left.Bytes()...)
			framed = append(framed, right.Bytes()...)
			testkit.True(t, h.CombineTagged(0x84, left, right).Equal(h.Hash(framed)),
				"CombineTagged(r, l, x) must equal Hash(r || l || x), with no length prefixes")
		}),

		HasherCustom("HashTagged accepts empty data", func(t *testing.T, h crypto.Hasher) {
			testkit.True(t, h.HashTagged(0x01, nil).Equal(h.Hash([]byte{0x01})),
				"HashTagged(r, nil) must equal Hash of the role byte alone")
		}),

		HasherCustom("tagged operations are deterministic", func(t *testing.T, h crypto.Hasher) {
			left := h.Hash([]byte("left"))
			right := h.Hash([]byte("right"))
			testkit.True(t, h.HashTagged(0x01, []byte("x")).Equal(h.HashTagged(0x01, []byte("x"))),
				"HashTagged must be deterministic")
			testkit.True(t, h.CombineTagged(0x84, left, right).Equal(h.CombineTagged(0x84, left, right)),
				"CombineTagged must be deterministic")
			testkit.False(t, h.CombineTagged(0x84, left, right).Equal(h.CombineTagged(0x84, right, left)),
				"CombineTagged must depend on operand order")
		}),

		HasherCustom("a crafted leaf cannot collide with an interior node", func(t *testing.T, h crypto.Hasher) {
			left := h.Hash([]byte("left"))
			right := h.Hash([]byte("right"))
			payload := append(append([]byte{}, left.Bytes()...), right.Bytes()...)
			testkit.False(t, h.HashTagged(0x01, payload).Equal(h.CombineTagged(0x84, left, right)),
				"a payload of two concatenated digests must not hash to their interior node")
		}),

		HasherCustom("distinct binary roles over one pair give distinct digests", func(t *testing.T, h crypto.Hasher) {
			left := h.Hash([]byte("left"))
			right := h.Hash([]byte("right"))
			link := h.CombineTagged(0x83, left, right)
			node := h.CombineTagged(0x84, left, right)
			mmr := h.CombineTagged(0x85, left, right)
			testkit.False(t, link.Equal(node), "roles 0x83 and 0x84 must not collide")
			testkit.False(t, node.Equal(mmr), "roles 0x84 and 0x85 must not collide")
			testkit.False(t, link.Equal(mmr), "roles 0x83 and 0x85 must not collide")
		}),

		HasherCustom("the arity halves refuse each other at 0x80", func(t *testing.T, h crypto.Hasher) {
			d := h.Hash(nil)

			_ = h.HashTagged(0x7F, nil)
			_ = h.CombineTagged(0x80, d, d)

			testkit.Panics(t, func() { _ = h.HashTagged(0x80, nil) },
				"HashTagged must refuse the lowest binary role")
			testkit.Panics(t, func() { _ = h.HashTagged(0xFF, []byte("x")) },
				"HashTagged must refuse the highest binary role")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x7F, d, d) },
				"CombineTagged must refuse the highest unary role")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x00, d, d) },
				"CombineTagged must refuse the lowest unary role")
		}),

		HasherCustom("CombineTagged refuses the zero Digest", func(t *testing.T, h crypto.Hasher) {
			var zero crypto.Digest
			d := h.Hash(nil)

			testkit.True(t, zero.IsZero(), "the zero Digest must report IsZero")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x84, zero, d) },
				"CombineTagged(r, zero, x) must panic")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x84, d, zero) },
				"CombineTagged(r, x, zero) must panic")
		}),

		HasherCustom("CombineTagged refuses a wrong-width operand", func(t *testing.T, h crypto.Hasher) {
			d := h.Hash(nil)
			wrong := otherWidthDigest(d.Size())

			testkit.False(t, wrong.IsZero(),
				"the wrong-width probe must not be the zero Digest, which is refused for another reason")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x84, wrong, d) },
				"CombineTagged(r, wrong-left, correct) must panic")
			testkit.Panics(t, func() { _ = h.CombineTagged(0x84, d, wrong) },
				"CombineTagged(r, correct, wrong-right) must panic")
		}),

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
			_ = s.Sum()
			_, _ = s.Write([]byte("c"))
			testkit.True(t, s.Sum().Equal(h.Hash([]byte("abc"))),
				`Sum must take a snapshot and not reset the state`)
		}),
	}
}

// otherWidthDigest returns a non-zero [crypto.Digest] whose width is
// not size, so a width precondition can be probed without a
// consumer-supplied fixture. The bytes are 0xAB: an all-zero digest of
// the wrong width is also refused, but for the reason the zero-Digest
// assertion already covers.
func otherWidthDigest(size int) crypto.Digest {
	if size == crypto.DigestSize256 {
		var b [crypto.DigestSize512]byte
		for i := range b {
			b[i] = 0xAB
		}

		return crypto.NewDigest512(b)
	}

	var b [crypto.DigestSize256]byte
	for i := range b {
		b[i] = 0xAB
	}

	return crypto.NewDigest256(b)
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
// produces byte-identical output to the supplied stdlib reference
// across a sweep of inputs: empty, short, a FIPS 180-4 §B-style
// two-block message, and 4 KiB of 0xAB. It pins the implementation to
// the published algorithm.
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

// HasherZeroAllocCases returns one call for each zero-alloc operation
// of [crypto.Hasher] and [crypto.Stream], keyed by name, for a test
// that measures them with testing.AllocsPerRun.
//
// Every call goes through the interfaces, over data. data must be on
// the heap, as a caller that must not allocate passes it: a slice
// passed through an interface escapes at the call site, so stack data
// would measure the caller and not the implementation. The stream
// calls borrow a stream from h's pool and return it, so every call
// after the first finds the pool warm.
func HasherZeroAllocCases(h crypto.Hasher, data []byte) map[string]func() {
	d := h.Hash(data)

	return map[string]func(){
		"ID and Algorithm": func() {
			_ = h.ID()
			_ = h.Algorithm()
		},
		"Hash":          func() { _ = h.Hash(data) },
		"HashTagged":    func() { _ = h.HashTagged(SampleUnaryRole(h), data) },
		"CombineTagged": func() { _ = h.CombineTagged(SampleBinaryRole(h), d, d) },
		"NewStream, Write, Sum and Close": func() {
			s := h.NewStream()
			_, _ = s.Write(data)
			_ = s.Sum()
			s.Close()
		},
	}
}
