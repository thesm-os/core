// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha3 "crypto/sha3"
	"encoding/hex"
	"hash"
	"testing"

	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	hmacsha3 "go.thesmos.sh/core/crypto/hmac/sha3"
)

// Canonical build-local IDs.
var (
	hmacSHA3256ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1',
	}
	hmacSHA3384ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1',
	}
	hmacSHA3512ID = crypto.ID{
		'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1',
	}
)

// testKey is the shared HMAC key for SUT and reference. Both
// sides must construct with the same key for byte-exact
// equivalence.
var testKey = []byte("contract-test-key")

func newSHA3256() crypto.MAC { return hmacsha3.NewSHA3_256(testKey) }
func newSHA3384() crypto.MAC { return hmacsha3.NewSHA3_384(testKey) }
func newSHA3512() crypto.MAC { return hmacsha3.NewSHA3_512(testKey) }

// stdlib SHA-3 constructors return *sha3.SHA3 — wrap to the
// hash.Hash type that crypto/hmac.New requires.
var (
	stdNewSHA3256 = func() hash.Hash { return stdsha3.New256() }
	stdNewSHA3384 = func() hash.Hash { return stdsha3.New384() }
	stdNewSHA3512 = func() hash.Hash { return stdsha3.New512() }
)

var sha3256Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA3_256,
	ID:        hmacSHA3256ID,
	Size:      crypto.DigestSize256,
	Key:       testKey,
	NewHash:   stdNewSHA3256,
}

var sha3384Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA3_384,
	ID:        hmacSHA3384ID,
	Size:      crypto.DigestSize384,
	Key:       testKey,
	NewHash:   stdNewSHA3384,
}

var sha3512Spec = cryptotest.StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA3_512,
	ID:        hmacSHA3512ID,
	Size:      crypto.DigestSize512,
	Key:       testKey,
	NewHash:   stdNewSHA3512,
}

// stdlibSign{256,384,512} return the stdlib HMAC-SHA-3 of data
// under testKey, used as the reference for
// MACCrossStdlibAssertion.
func stdlibSign256(data []byte) []byte {
	h := stdhmac.New(stdNewSHA3256, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func stdlibSign384(data []byte) []byte {
	h := stdhmac.New(stdNewSHA3384, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func stdlibSign512(data []byte) []byte {
	h := stdhmac.New(stdNewSHA3512, testKey)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

// --- testkit-driven contract layer (HMAC-SHA3-256) ---

func TestHMACSHA3256Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newSHA3256,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA3256ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA3_256),
			cryptotest.MACSizeAssertion(crypto.DigestSize256),
			cryptotest.MACCrossStdlibAssertion(stdlibSign256),
		)...,
	)
}

func TestHMACSHA3256Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newSHA3256,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha3256Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA3256Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newSHA3256,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha3256Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA3256(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newSHA3256,
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

// --- testkit-driven contract layer (HMAC-SHA3-384) ---

func TestHMACSHA3384Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newSHA3384,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA3384ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA3_384),
			cryptotest.MACSizeAssertion(crypto.DigestSize384),
			cryptotest.MACCrossStdlibAssertion(stdlibSign384),
		)...,
	)
}

func TestHMACSHA3384Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newSHA3384,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha3384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA3384Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newSHA3384,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha3384Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA3384(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newSHA3384,
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

// --- testkit-driven contract layer (HMAC-SHA3-512) ---

func TestHMACSHA3512Contract(t *testing.T) {
	t.Parallel()
	cryptotest.AssertMACContract(t, newSHA3512,
		append(cryptotest.MACContractAssertions(),
			cryptotest.MACIDAssertion(hmacSHA3512ID),
			cryptotest.MACAlgorithmAssertion(crypto.AlgHMACSHA3_512),
			cryptotest.MACSizeAssertion(crypto.DigestSize512),
			cryptotest.MACCrossStdlibAssertion(stdlibSign512),
		)...,
	)
}

func TestHMACSHA3512Model(t *testing.T) {
	t.Parallel()
	cryptotest.MACModelTest(t, newSHA3512,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(t, sha3512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func FuzzHMACSHA3512Model(f *testing.F) {
	cryptotest.MACModelFuzz(f, newSHA3512,
		cryptotest.MACModelReference(func() crypto.MAC {
			return cryptotest.NewStdlibMACStub(f, sha3512Spec)
		}),
		cryptotest.MACModelExtraActions(
			cryptotest.MACSignAction(),
			cryptotest.MACVerifyAction(),
		),
	)
}

func BenchmarkHMACSHA3512(b *testing.B) {
	cryptotest.BenchmarkMACContract(b, newSHA3512,
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

// --- impl-specific RFC 4231-style known-answer vectors ---

// rfc4231SHA3Vector carries the RFC 4231 §4.* keyed inputs along
// with the per-algorithm expected outputs for SHA-3. These values
// were computed once from [crypto/hmac] over [crypto/sha3] (the
// stdlib reference) and match the NIST CAVP HMAC-SHA-3 outputs
// for the same inputs. Freezing them here protects against
// silent stdlib-level regressions.
//
// §4.5 (truncated output) is omitted — we never truncate.
// §4.6/§4.7 (131-byte longer-than-block keys) are exercised by
// the contract sweep and fuzz target.
type rfc4231SHA3Vector struct {
	name    string
	keyHex  string
	dataHex string
	want256 string
	want384 string
	want512 string
}

var rfc4231SHA3Vectors = []rfc4231SHA3Vector{
	{
		name:    "§4.2 — 20-byte 0x0b key, 'Hi There'",
		keyHex:  "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
		dataHex: "4869205468657265",
		want256: "ba85192310dffa96e2a3a40e69774351140bb7185e1202cdcc917589f95e16bb",
		want384: "68d2dcf7fd4ddd0a2240c8a437305f61fb7334cfb5d0226e1bc27dc10a2e723a" +
			"20d370b47743130e26ac7e3d532886bd",
		want512: "eb3fbd4b2eaab8f5c504bd3a41465aacec15770a7cabac531e482f860b5ec7ba" +
			"47ccb2c6f2afce8f88d22b6dc61380f23a668fd3888bb80537c0a0b86407689e",
	},
	{
		name:    "§4.3 — 'Jefe' key, 'what do ya want for nothing?'",
		keyHex:  "4a656665",
		dataHex: "7768617420646f2079612077616e7420666f72206e6f7468696e673f",
		want256: "c7d4072e788877ae3596bbb0da73b887c9171f93095b294ae857fbe2645e1ba5",
		want384: "f1101f8cbf9766fd6764d2ed61903f21ca9b18f57cf3e1a23ca13508a93243ce" +
			"48c045dc007f26a21b3f5e0e9df4c20a",
		want512: "5a4bfeab6166427c7a3647b747292b8384537cdb89afb3bf5665e4c5e709350b" +
			"287baec921fd7ca0ee7a0c31d022a95e1fc92ba9d77df883960275beb4e62024",
	},
	{
		name:   "§4.4 — 20-byte 0xaa key, 50 bytes 0xdd",
		keyHex: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		dataHex: "dddddddddddddddddddddddddddddddddddddddddddddddddd" +
			"dddddddddddddddddddddddddddddddddddddddddddddddddd",
		want256: "84ec79124a27107865cedd8bd82da9965e5ed8c37b0ac98005a7f39ed58a4207",
		want384: "275cd0e661bb8b151c64d288f1f782fb91a8abd56858d72babb2d476f0458373" +
			"b41b6ab5bf174bec422e53fc3135ac6e",
		want512: "309e99f9ec075ec6c6d475eda1180687fcf1531195802a99b5677449a8625182" +
			"851cb332afb6a89c411325fbcbcd42afcb7b6e5aab7ea42c660f97fd8584bf03",
	},
}

// TestSHA3256RFC4231Vectors locks HMAC-SHA3-256 against RFC 4231.
func TestSHA3256RFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231SHA3Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want256)
			got := hmacsha3.NewSHA3_256(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})
	}
}

// TestSHA3384RFC4231Vectors locks HMAC-SHA3-384 against RFC 4231.
func TestSHA3384RFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231SHA3Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want384)
			got := hmacsha3.NewSHA3_384(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})
	}
}

// TestSHA3512RFC4231Vectors locks HMAC-SHA3-512 against RFC 4231.
func TestSHA3512RFC4231Vectors(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231SHA3Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want512)
			got := hmacsha3.NewSHA3_512(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})
	}
}

// --- helpers ---

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}
