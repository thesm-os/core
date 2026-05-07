// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign"
)

// VerifyStreamSample is a canonical (split-Write, signature) pair
// the [sign.VerifyStream] under test must accept. The consumer
// produces the signature externally — typically via the matching
// signer — and supplies the message bytes plus the resulting
// signature.
type VerifyStreamSample struct {
	// Message is the absorbed bytes the signature was produced
	// over.
	Message []byte
	// Signature is the canonical signature over Message under
	// the verify stream's public key.
	Signature []byte
}

// VerifyStreamContractAssertions returns the generic assertions
// every [sign.VerifyStream] implementation must satisfy: Write +
// Verify accepts the canonical sample; Write rejects a tampered
// signature; split-Write of the same concatenation accepts; nil /
// empty Write is a no-op.
//
// VerifyStream is single-use — each subtest gets a fresh stream
// from the factory.
//
//	cryptotest.AssertVerifyStreamContract(t, factory,
//	    cryptotest.VerifyStreamContractAssertions(sample)...,
//	)
func VerifyStreamContractAssertions(sample VerifyStreamSample) []VerifyStreamOption {
	return []VerifyStreamOption{
		VerifyStreamCustom("Write+Verify accepts canonical signature", func(t *testing.T, vs sign.VerifyStream) {
			_, err := vs.Write(sample.Message)
			testkit.NoError(t, err, "Write")
			testkit.True(t, vs.Verify(sample.Signature),
				"Verify must accept the canonical (msg, sig) sample")
		}),

		// split-Write needs a fresh stream — VerifyStream is single-use.
		// We exercise it via VerifyStreamCustom which calls the
		// factory once per subtest.
		VerifyStreamCustom("split-Write equals single Write",
			func(t *testing.T, vs sign.VerifyStream) {
				if len(sample.Message) < 4 {
					return // can't split a sub-4-byte message meaningfully
				}
				mid := len(sample.Message) / 2
				_, _ = vs.Write(sample.Message[:mid])
				_, _ = vs.Write(sample.Message[mid:])
				testkit.True(t, vs.Verify(sample.Signature),
					"Verify must accept the canonical signature after split-Write")
			}),

		VerifyStreamCustom("Verify rejects flipped signature bit", func(t *testing.T, vs sign.VerifyStream) {
			tampered := bytes.Clone(sample.Signature)
			tampered[0] ^= 0x01
			_, _ = vs.Write(sample.Message)
			testkit.False(t, vs.Verify(tampered), "Verify must reject a tampered signature")
		}),

		VerifyStreamCustom("Write of nil and empty slice are no-ops", func(t *testing.T, vs sign.VerifyStream) {
			_, _ = vs.Write(nil)
			_, _ = vs.Write(sample.Message)
			_, _ = vs.Write([]byte{})
			testkit.True(t, vs.Verify(sample.Signature),
				"Verify must accept signature after nil/empty interleave")
		}),

		VerifyStreamCustom("Verify rejects nil and empty signatures", func(t *testing.T, vs sign.VerifyStream) {
			_, _ = vs.Write(sample.Message)
			testkit.False(t, vs.Verify(nil), "Verify must reject a nil signature")
		}),
	}
}
