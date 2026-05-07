// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	stdhmac "crypto/hmac"
	stdsha512 "crypto/sha512"
	"encoding/hex"
	"hash"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	hmacsha512 "go.thesmos.sh/core/crypto/hmac/sha512"
)

// Canonical build-local IDs.
var (
	hmacSHA384ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
	hmacSHA512ID = crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
)

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

func newSHA384() crypto.MAC { return hmacsha512.NewSHA384(testKey) }
func newSHA512() crypto.MAC { return hmacsha512.NewSHA512(testKey) }

var sha384Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA384,
	ID:        hmacSHA384ID,
	Size:      crypto.DigestSize384,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha512.New384() },
}

var sha512Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA512,
	ID:        hmacSHA512ID,
	Size:      crypto.DigestSize512,
	Key:       testKey,
	NewHash:   func() hash.Hash { return stdsha512.New() },
}

// stdlibSign{384,512} return the stdlib HMAC of data under
// testKey, used as the reference for MACCrossStdlibAssertion.
func stdlibSign384(data []byte) []byte {
	h := stdhmac.New(stdsha512.New384, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func stdlibSign512(data []byte) []byte {
	h := stdhmac.New(stdsha512.New, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

// --- testkit-driven contract layer (HMAC-SHA-384) ---

func TestHMACSHA384Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newSHA384,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA384ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA384),
			cryptotest.MACSizeAssertion(crypto.DigestSize384),
			cryptotest.MACCrossStdlibAssertion(stdlibSign384),
		)...,
	)
}

func TestHMACSHA384Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newSHA384,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA384Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newSHA384,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA384(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newSHA384,
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

// --- testkit-driven contract layer (HMAC-SHA-512) ---

func TestHMACSHA512Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newSHA512,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA512ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA512),
			cryptotest.MACSizeAssertion(crypto.DigestSize512),
			cryptotest.MACCrossStdlibAssertion(stdlibSign512),
		)...,
	)
}

func TestHMACSHA512Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newSHA512,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA512Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newSHA512,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA512(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newSHA512,
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

// --- impl-specific RFC 4231 known-answer vectors ---

// rfc4231Vector carries the RFC 4231 §4.* keyed inputs along
// with the per-algorithm expected outputs.
type rfc4231Vector struct {
	name    string
	keyHex  string
	dataHex string
	want384 string
	want512 string
}

const (
	// 131-byte 0xaa key used in §4.6 / §4.7 (262 hex chars).
	largeKeyHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"aaaaaa"
)

// rfc4231Vectors enumerates RFC 4231 §4.2, §4.3, §4.4, §4.6,
// §4.7. §4.5 (truncated output) is omitted — we never truncate.
var rfc4231Vectors = []rfc4231Vector{
	{
		name:    "§4.2 — 20-byte 0x0b key, 'Hi There'",
		keyHex:  "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
		dataHex: "4869205468657265",
		want384: "afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59c" +
			"faea9ea9076ede7f4af152e8b2fa9cb6",
		want512: "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cde" +
			"daa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854",
	},
	{
		name:    "§4.3 — 'Jefe' key, 'what do ya want for nothing?'",
		keyHex:  "4a656665",
		dataHex: "7768617420646f2079612077616e7420666f72206e6f7468696e673f",
		want384: "af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e" +
			"8e2240ca5e69e2c78b3239ecfab21649",
		want512: "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea250554" +
			"9758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737",
	},
	{
		name:   "§4.4 — 20-byte 0xaa key, 50 bytes 0xdd",
		keyHex: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		dataHex: "dddddddddddddddddddddddddddddddddddddddddddddddddd" +
			"dddddddddddddddddddddddddddddddddddddddddddddddddd",
		want384: "88062608d3e6ad8a0aa2ace014c8a86f0aa635d947ac9febe83ef4e55966144b" +
			"2a5ab39dc13814b94e3ab6e101a34f27",
		want512: "fa73b0089d56a284efb0f0756c890be9b1b5dbdd8ee81a3655f83e33b2279d39" +
			"bf3e848279a722c806b485a47e67c807b946a337bee8942674278859e13292fb",
	},
	{
		name:   "§4.6 — 131-byte 0xaa key, short 'Test Using Larger Than…' data",
		keyHex: largeKeyHex,
		dataHex: "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a" +
			"65204b6579202d2048617368204b6579204669727374",
		want384: "4ece084485813e9088d2c63a041bc5b44f9ef1012a2b588f3cd11f05033ac4c6" +
			"0c2ef6ab4030fe8296248df163f44952",
		want512: "80b24263c7c1a3ebb71493c1dd7be8b49b46d1f41b4aeec1121b013783f8f352" +
			"6b56d037e05f2598bd0fd2215d6a1e5295e64f73f63f0aec8b915a985d786598",
	},
	{
		name:   "§4.7 — 131-byte 0xaa key, 152-byte data",
		keyHex: largeKeyHex,
		dataHex: "5468697320697320612074657374207573696e672061206c6172676572207468" +
			"616e20626c6f636b2d73697a65206b657920616e642061206c61726765722074" +
			"68616e20626c6f636b2d73697a6520646174612e20546865206b6579206e6565" +
			"647320746f20626520686173686564206265666f7265206265696e6720757365" +
			"642062792074686520484d414320616c676f726974686d2e",
		want384: "6617178e941f020d351e2f254e8fd32c602420feb0b8fb9adccebb82461e99c5" +
			"a678cc31e799176d3860e6110c46523e",
		want512: "e37b6a775dc87dbaa4dfa9f96e5e3ffddebd71f8867289865df5a32d20cdc944" +
			"b6022cac3c4982b10d5eeb55c3e4de15134676fb6de0446065c97440fa8c6a58",
	},
}

// TestSHA384RFC4231Vectors locks HMAC-SHA-384 against RFC 4231.
func TestSHA384RFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want384)
			got := hmacsha512.NewSHA384(key).Sign(data)
			testkit.Equal(t, got.Bytes(), want, "Sign output must byte-match RFC 4231 vector")
		})
	}
}

// TestSHA512RFC4231Vectors locks HMAC-SHA-512 against RFC 4231.
func TestSHA512RFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want512)
			got := hmacsha512.NewSHA512(key).Sign(data)
			testkit.Equal(t, got.Bytes(), want, "Sign output must byte-match RFC 4231 vector")
		})
	}
}

// --- helpers ---

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	testkit.NoError(t, err, "decode hex fixture")
	return b
}
