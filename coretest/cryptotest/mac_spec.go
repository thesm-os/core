// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

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
			if !m.Sign(input).Equal(m.Sign(input)) {
				t.Fatal("Sign(x) != Sign(x): not deterministic for fixed key")
			}
		}),

		MACCustom("Sign of nil equals Sign of empty slice", func(t *testing.T, m crypto.MAC) {
			if !m.Sign(nil).Equal(m.Sign([]byte{})) {
				t.Fatal("nil and empty slice produced different MACs")
			}
		}),

		MACCustom("distinct inputs produce distinct MACs", func(t *testing.T, m crypto.MAC) {
			if m.Sign([]byte("alpha")).Equal(m.Sign([]byte("beta"))) {
				t.Fatal("distinct inputs collided")
			}
		}),

		// --- Verify ---

		MACCustom("Verify accepts canonical MAC", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			canonical := m.Sign(data)
			if !m.Verify(data, canonical.Bytes()) {
				t.Fatal("Verify rejected the canonical MAC")
			}
		}),

		MACCustom("Verify rejects tampered MAC", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			canonical := m.Sign(data).Bytes()
			tampered := bytes.Clone(canonical)
			tampered[0] ^= 0x01
			if m.Verify(data, tampered) {
				t.Fatal("Verify accepted a tampered MAC")
			}
		}),

		MACCustom("Verify rejects expected of wrong length", func(t *testing.T, m crypto.MAC) {
			data := []byte("verify-me")
			if m.Verify(data, make([]byte, 16)) {
				t.Fatal("Verify accepted a short expected slice")
			}
			if m.Verify(data, make([]byte, m.Size()*2)) {
				t.Fatal("Verify accepted an over-long expected slice")
			}
			if m.Verify(data, nil) {
				t.Fatal("Verify accepted nil expected")
			}
		}),

		// --- Size ---

		MACCustom("Sign output size matches Size()", func(t *testing.T, m crypto.MAC) {
			got := m.Sign([]byte("any"))
			if got.Size() != m.Size() {
				t.Fatalf("Sign output size %d != reported Size() %d", got.Size(), m.Size())
			}
		}),

		// --- Stream ---

		MACCustom("Stream Write+Sum equals Sign over same bytes", func(t *testing.T, m crypto.MAC) {
			payload := []byte("the quick brown fox jumps over the lazy dog")
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write(payload)
			if !s.Sum().Equal(m.Sign(payload)) {
				t.Fatal("Stream Write+Sum != Sign over the same bytes")
			}
		}),

		MACCustom("Stream split-Write equals single Write of concatenation", func(t *testing.T, m crypto.MAC) {
			full := []byte("agentic-context-payload-streamed-across-many-writes")
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write(full[:10])
			_, _ = s.Write(full[10:25])
			_, _ = s.Write(full[25:])
			if !s.Sum().Equal(m.Sign(full)) {
				t.Fatal("split Stream != single Sign of concatenation")
			}
		}),

		MACCustom("Stream Reset clears state but preserves key", func(t *testing.T, m crypto.MAC) {
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("first"))
			_ = s.Sum()
			s.Reset()
			_, _ = s.Write([]byte("second"))
			if !s.Sum().Equal(m.Sign([]byte("second"))) {
				t.Fatal("Stream after Reset != Sign(\"second\")")
			}
		}),

		MACCustom("Stream Sum is non-resetting (snapshot only)", func(t *testing.T, m crypto.MAC) {
			s := m.NewStream()
			defer s.Close()
			_, _ = s.Write([]byte("ab"))
			_ = s.Sum() // snapshot
			_, _ = s.Write([]byte("c"))
			if !s.Sum().Equal(m.Sign([]byte("abc"))) {
				t.Fatal("Sum reset state — should snapshot only")
			}
		}),
	}
}

// MACIDAssertion verifies [crypto.MAC.ID] returns the expected
// stable build-local identifier.
func MACIDAssertion(want crypto.ID) MACOption {
	return MACCustom("ID matches", func(t *testing.T, m crypto.MAC) {
		if got := m.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
	})
}

// MACAlgorithmAssertion verifies [crypto.MAC.Algorithm] returns
// the expected long-term cross-build algorithm name.
func MACAlgorithmAssertion(want crypto.Algorithm) MACOption {
	return MACCustom("Algorithm matches", func(t *testing.T, m crypto.MAC) {
		if got := m.Algorithm(); got != want {
			t.Fatalf("Algorithm: got %q, want %q", got, want)
		}
	})
}

// MACSizeAssertion verifies [crypto.MAC.Size] returns the
// expected output size in bytes.
func MACSizeAssertion(want int) MACOption {
	return MACCustom("Size matches", func(t *testing.T, m crypto.MAC) {
		if got := m.Size(); got != want {
			t.Fatalf("Size: got %d, want %d", got, want)
		}
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
			got := m.Sign(tc.data).Bytes()
			want := stdlibSign(tc.data)
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: cross-stdlib mismatch:\n got=%x\nwant=%x",
					tc.name, got, want)
			}
		}
	})
}
