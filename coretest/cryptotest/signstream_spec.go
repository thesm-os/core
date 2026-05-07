// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"testing"

	"go.thesmos.sh/core/crypto/sign"
)

// SignStreamContractAssertions returns the generic assertions
// every [sign.SignStream] implementation must satisfy: Write +
// SignAndReset round-trips through the supplied verify function
// (the stream's matching [sign.Verifier]); split-Write equivalent
// to single-Write of the concatenation; SignAndReset resets the
// stream so the next message can be streamed into the same
// instance.
//
// Sign is non-deterministic for ECDSA, so the assertions never
// compare two signature byte sequences — they only assert each
// signature verifies under the supplied verify function. The
// verify closure carries the stream's public key.
//
//	cryptotest.AssertSignStreamContract(t, factory,
//	    cryptotest.SignStreamContractAssertions(verify)...,
//	)
func SignStreamContractAssertions(verify func(msg, sig []byte) bool) []SignStreamOption {
	return []SignStreamOption{
		SignStreamCustom("Write+SignAndReset round-trips via verify", func(t *testing.T, s sign.SignStream) {
			payload := []byte("the quick brown fox jumps over the lazy dog")
			if _, err := s.Write(payload); err != nil {
				t.Fatalf("Write: %v", err)
			}
			sig, err := s.SignAndReset()
			if err != nil {
				t.Fatalf("SignAndReset: %v", err)
			}
			if !verify(payload, sig) {
				t.Fatal("verify rejected stream signature")
			}
		}),

		SignStreamCustom("split-Write equals single Write of concatenation", func(t *testing.T, s sign.SignStream) {
			full := []byte("agentic-context-payload-streamed-across-many-writes")
			_, _ = s.Write(full[:10])
			_, _ = s.Write(full[10:25])
			_, _ = s.Write(full[25:])
			sig, err := s.SignAndReset()
			if err != nil {
				t.Fatalf("SignAndReset: %v", err)
			}
			if !verify(full, sig) {
				t.Fatal("verify rejected split-Write signature")
			}
		}),

		SignStreamCustom("SignAndReset clears state",
			func(t *testing.T, s sign.SignStream) {
				_, _ = s.Write([]byte("first"))
				if _, err := s.SignAndReset(); err != nil {
					t.Fatalf("SignAndReset(first): %v", err)
				}
				_, _ = s.Write([]byte("second"))
				sig, err := s.SignAndReset()
				if err != nil {
					t.Fatalf("SignAndReset(second): %v", err)
				}
				if !verify([]byte("second"), sig) {
					t.Fatal("verify rejected the post-reset signature — state leaked")
				}
			}),

		SignStreamCustom("Write of nil and empty slice are no-ops", func(t *testing.T, s sign.SignStream) {
			payload := []byte("body")
			_, _ = s.Write(nil)
			_, _ = s.Write([]byte{})
			_, _ = s.Write(payload)
			sig, err := s.SignAndReset()
			if err != nil {
				t.Fatalf("SignAndReset: %v", err)
			}
			if !verify(payload, sig) {
				t.Fatal("verify rejected signature after nil/empty interleave")
			}
		}),
	}
}
