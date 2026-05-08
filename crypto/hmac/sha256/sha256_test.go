// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	stdhmac "crypto/hmac"
	stdsha256 "crypto/sha256"
	"hash"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	hmacsha256 "go.thesmos.sh/core/crypto/hmac/sha256"
)

// hmacSHA256ID is the canonical build-local identifier.
var hmacSHA256ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '2', '5', '6', '/', 'v', '1'}

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

func newMAC() crypto.MAC { return hmacsha256.New(testKey) }

var stdlibSpec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA256,
	ID:        hmacSHA256ID,
	Size:      crypto.DigestSize256,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha256.New() },
}

// stdlibSign returns the stdlib HMAC-SHA-256 of data under
// testKey. Used as the reference function for
// MACCrossStdlibAssertion.
func stdlibSign(data []byte) []byte {
	h := stdhmac.New(stdsha256.New, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

// --- testkit-driven contract layer ---

func TestHMACSHA256Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newMAC,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA256ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA256),
			cryptotest.MACSizeAssertion(crypto.DigestSize256),
			cryptotest.MACCrossStdlibAssertion(stdlibSign),
		)...,
	)
}

func TestHMACSHA256Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newMAC,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, stdlibSpec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA256Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newMAC,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, stdlibSpec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA256(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newMAC,
		cryptotest.MACBenchOnAlgorithm(bench.PureAllocsWithin[crypto.MAC, crypto.Algorithm](0)),
		cryptotest.MACBenchOnID(bench.PureAllocsWithin[crypto.MAC, crypto.ID](0)),
		cryptotest.MACBenchOnSize(bench.PureAllocsWithin[crypto.MAC, int](0)),
		cryptotest.MACBenchOnSign(
			bench.PureAllocsWithin[crypto.MAC, crypto.Digest](0),
			bench.PureConcurrentThroughput[crypto.MAC, crypto.Digest](32),
		),
		cryptotest.MACBenchOnVerify(bench.PredicateAllocsWithin[crypto.MAC](0)),
	)
}

// --- HMAC-SHA-256-specific tests ---

// rfc4231Vectors are the test vectors from RFC 4231 §4.2 — §4.7.
// §4.5 (truncated output) is omitted: we always produce the full
// 32-byte output and never truncate.
var rfc4231Vectors = []struct {
	name    string
	keyHex  string
	dataHex string
	wantHex string
}{
	{
		name:    "RFC 4231 §4.2 — 20-byte 0x0b key, ASCII 'Hi There'",
		keyHex:  "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
		dataHex: "4869205468657265",
		wantHex: "b0344c61d8db38535ca8afceaf0bf12b" +
			"881dc200c9833da726e9376c2e32cff7",
	},
	{
		name:    "RFC 4231 §4.3 — 4-byte 'Jefe' key, 28-byte 'what do ya want…'",
		keyHex:  "4a656665",
		dataHex: "7768617420646f2079612077616e7420666f72206e6f7468696e673f",
		wantHex: "5bdcc146bf60754e6a042426089575c7" +
			"5a003f089d2739839dec58b964ec3843",
	},
	{
		name:   "RFC 4231 §4.4 — 20-byte 0xaa key, 50 bytes 0xdd",
		keyHex: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		dataHex: "dddddddddddddddddddddddddddddddddddddddddddddddddd" +
			"dddddddddddddddddddddddddddddddddddddddddddddddddd",
		wantHex: "773ea91e36800e46854db8ebd09181a7" +
			"2959098b3ef8c122d9635514ced565fe",
	},
	{
		name: "RFC 4231 §4.6 — 131-byte 0xaa key (longer than block), short data",
		keyHex: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaa",
		dataHex: "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a" +
			"65204b6579202d2048617368204b6579204669727374",
		wantHex: "60e431591ee0b67f0d8a26aacbf5b77f" +
			"8e0bc6213728c5140546040f0ee37f54",
	},
	{
		name: "RFC 4231 §4.7 — 131-byte 0xaa key, 152-byte data",
		keyHex: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaa",
		dataHex: "5468697320697320612074657374207573696e672061206c6172676572207468" +
			"616e20626c6f636b2d73697a65206b657920616e642061206c61726765722074" +
			"68616e20626c6f636b2d73697a6520646174612e20546865206b6579206e6565" +
			"647320746f20626520686173686564206265666f7265206265696e6720757365" +
			"642062792074686520484d414320616c676f726974686d2e",
		wantHex: "9b09ffa71b942fcb27635fbcd5b0e944" +
			"bfdc63644f0713938a7f51535c3a35e2",
	},
}

// TestRFC4231Vectors locks the impl against RFC 4231 known-
// answer vectors. The contract suite's MACCrossStdlibAssertion
// covers byte-equivalence with stdlib across canned inputs, but
// the RFC vectors are the algorithm-of-record reference: failure
// here means our impl AND stdlib have both drifted from the spec.
func TestRFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := testkit.MustDecodeHex(t, tc.keyHex)
			data := testkit.MustDecodeHex(t, tc.dataHex)
			want := testkit.MustDecodeHex(t, tc.wantHex)
			got := hmacsha256.New(key).Sign(data)
			testkit.Equal(t, got.Bytes(), want, "Sign output must byte-match RFC 4231 vector")
		})
	}
}

// TestNewKeyIsCopied locks the documented "key is copied at
// construction" property — callers can mutate or zero the
// supplied key buffer without affecting subsequent Sign calls.
func TestNewKeyIsCopied(t *testing.T) {
	t.Parallel()

	key := []byte("mutable-key")
	data := []byte("payload")
	m := hmacsha256.New(key)
	want := m.Sign(data)

	for i := range key {
		key[i] = 0xff
	}
	got := m.Sign(data)
	testkit.True(t, got.Equal(want),
		"MAC must hold a defensive copy — caller's mutated key must not affect Sign")
}
