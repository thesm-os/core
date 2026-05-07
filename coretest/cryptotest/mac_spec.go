// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// MACContractAssertions returns the generic assertions every
// [crypto.MAC] implementation must satisfy: Sign determinism,
// Verify round-trip + tamper rejection, Verify size guarding,
// and Stream / Sign equivalence. Compose with [MACIDAssertion],
// [MACAlgorithmAssertion], [MACSizeAssertion], and
// [MACCrossStdlibAssertion] at the consumer call site to add
// impl-specific constants and byte-exact stdlib equivalence.
//
//	cryptotest.AssertMACContract(t, factory,
//	    append(cryptotest.MACContractAssertions(),
//	        cryptotest.MACIDAssertion(wantID),
//	        cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA256),
//	        cryptotest.MACSizeAssertion(crypto.DigestSize256),
//	        cryptotest.MACCrossStdlibAssertion(stdlibSign),
//	    )...,
//	)
func MACContractAssertions() []MACOption {
	return []MACOption{
		// --- Sign ---

		MACCustom("Sign is deterministic", func(t *testing.T, m crypto.MAC) {
			input := []byte("the quick brown fox jumps over the lazy dog")
			testkit.True(t, m.Sign(input).Equal(m.Sign(input)),
				"Sign(x) must equal Sign(x) — Sign must be deterministic for fixed key")
		}),

		MACCustom("Sign of nil equals Sign of empty slice", func(t *testing.T, m crypto.MAC) {
			testkit.True(t, m.Sign(nil).Equal(m.Sign([]byte{})),
				"Sign(nil) must equal Sign([]byte{})")
		}),

		MACCustom("distinct inputs produce distinct MACs", func(t *testing.T, m crypto.MAC) {
			testkit.False(t, m.Sign([]byte("alpha")).Equal(m.Sign([]byte("beta"))),
				"distinct inputs must not collide to the same MAC")
		}),

		// --- Verify ---

		MACCustom("Verify accepts canonical MAC", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			canonical := m.Sign(data)
			testkit.True(t, m.Verify(data, canonical.Bytes()),
				"Verify must accept the canonical MAC")
		}),

		MACCustom("Verify rejects tampered MAC", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			canonical := m.Sign(data).Bytes()
			tampered := bytes.Clone(canonical)
			tampered[0] ^= 0x01
			testkit.False(t, m.Verify(data, tampered),
				"Verify must reject a tampered MAC")
		}),

		MACCustom("Verify rejects expected of wrong length", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			testkit.False(t, m.Verify(data, make([]byte, 16)),
				"Verify must reject a short expected slice")
			testkit.False(t, m.Verify(data, make([]byte, m.Size()*2)),
				"Verify must reject an over-long expected slice")
			testkit.False(t, m.Verify(data, nil),
				"Verify must reject a nil expected slice")
		}),

		// --- Size ---

		MACCustom("Sign output size matches Size()", func(t *testing.T, m crypto.MAC) {
			got := m.Sign([]byte("any"))
			testkit.Equal(t, got.Size(), m.Size(),
				"Sign output size must match the Hasher's reported Size()")
		}),

		// --- Stream ---

		MACCustom("Stream Write+Sum equals Sign over same bytes", func(t *testing.T, m crypto.MAC) {
			payload := []byte("the quick brown fox jumps over the lazy dog")
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write(payload)
			testkit.True(t, s.Sum().Equal(m.Sign(payload)),
				"Stream Write+Sum must equal Sign over the same bytes")
		}),

		MACCustom("Stream split-Write equals single Write of concatenation", func(t *testing.T, m crypto.MAC) {
			full := []byte("agentic-context-payload-streamed-across-many-writes")
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write(full[:10])
			_, _ = s.Write(full[10:25])
			_, _ = s.Write(full[25:])
			testkit.True(t, s.Sum().Equal(m.Sign(full)),
				"split-Write must equal Sign over the concatenation")
		}),

		MACCustom("Stream Reset clears state but preserves key", func(t *testing.T, m crypto.MAC) {
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("first"))
			_ = s.Sum()
			s.Reset()
			_, _ = s.Write([]byte("second"))
			testkit.True(t, s.Sum().Equal(m.Sign([]byte("second"))),
				`Stream after Reset must equal Sign("second")`)
		}),

		MACCustom("Stream Sum is non-resetting (snapshot only)", func(t *testing.T, m crypto.MAC) {
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("ab"))
			_ = s.Sum() // snapshot
			_, _ = s.Write([]byte("c"))
			testkit.True(t, s.Sum().Equal(m.Sign([]byte("abc"))),
				"Sum must snapshot only — must not reset state")
		}),
	}
}

// MACIDAssertion verifies [crypto.MAC.ID] returns the expected
// stable build-local identifier.
func MACIDAssertion(want crypto.ID) MACOption {
	return MACCustom("ID matches", func(t *testing.T, m crypto.MAC) {
		testkit.Equal(t, m.ID(), want, "ID must match expected build-local identifier")
	})
}

// MACAlgorithmAssertion verifies [crypto.MAC.Algorithm] returns
// the expected long-term cross-build algorithm name.
func MACAlgorithmAssertion(want crypto.Algorithm) MACOption {
	return MACCustom("Algorithm matches", func(t *testing.T, m crypto.MAC) {
		testkit.Equal(t, m.Algorithm(), want, "Algorithm must match expected name")
	})
}

// MACSizeAssertion verifies [crypto.MAC.Size] returns the
// expected output size in bytes.
func MACSizeAssertion(want int) MACOption {
	return MACCustom("Size matches", func(t *testing.T, m crypto.MAC) {
		testkit.Equal(t, m.Size(), want, "Size must match expected output size")
	})
}

// MACCrossStdlibAssertion verifies that [crypto.MAC.Sign]
// produces byte-identical output to the supplied stdlib HMAC
// reference across a sweep of inputs (empty, short, RFC 4231
// §4-style data, 4 KiB).
//
// Lock byte-exact compatibility with the stdlib reference so the
// MAC implementation cannot drift silently from the published
// HMAC algorithm.
func MACCrossStdlibAssertion(stdlibSign func([]byte) []byte) MACOption {
	return MACCustom("Sign matches stdlib byte-for-byte", func(t *testing.T, m crypto.MAC) {
		cases := []struct {
			name string
			data []byte
		}{
			{"empty", []byte{}},
			{`"Hi There"`, []byte("Hi There")},
			{`"what do ya want for nothing?"`, []byte("what do ya want for nothing?")},
			{"4 KiB 0xDD", bytes.Repeat([]byte{0xDD}, 4096)},
		}
		for _, tc := range cases {
			testkit.Equal(t, m.Sign(tc.data).Bytes(), stdlibSign(tc.data),
				tc.name+": Sign output must byte-match stdlib")
		}
	})
}
