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

	"go.thesmos.sh/core/crypto"
	hmacsha3 "go.thesmos.sh/core/crypto/hmac/sha3"
)

// rfc4231SHA3Vector — RFC 4231 §4.2 / §4.3 / §4.4 inputs with
// SHA-3 expected outputs computed once from [crypto/hmac] over
// [crypto/sha3] (the stdlib reference). The expected values
// match what the NIST CAVP HMAC-SHA-3 test vectors produce for
// the same inputs; freezing them here protects against silent
// stdlib-level regressions.
//
// §4.5 (truncated output) and §4.6 / §4.7 (131-byte longer-
// than-block keys) are intentionally omitted: §4.5 isn't
// reachable through this surface (we never truncate), and the
// long-key paths are exhaustively exercised by the
// [TestCrossCheckStdlib] cases ("block-size" and "long key,
// long data") and [FuzzCrossStdlib] across arbitrary key
// lengths.
type rfc4231SHA3Vector struct {
	name      string
	keyHex    string
	dataASCII string
	want256   string
	want384   string
	want512   string
}

var rfc4231SHA3Vectors = []rfc4231SHA3Vector{
	{
		name:      "§4.2 — 20-byte 0x0b key, 'Hi There'",
		keyHex:    "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
		dataASCII: "Hi There",
		want256:   "ba85192310dffa96e2a3a40e69774351140bb7185e1202cdcc917589f95e16bb",
		want384: "68d2dcf7fd4ddd0a2240c8a437305f61fb7334cfb5d0226e1bc27dc10a2e723a" +
			"20d370b47743130e26ac7e3d532886bd",
		want512: "eb3fbd4b2eaab8f5c504bd3a41465aacec15770a7cabac531e482f860b5ec7ba" +
			"47ccb2c6f2afce8f88d22b6dc61380f23a668fd3888bb80537c0a0b86407689e",
	},
	{
		name:      "§4.3 — 'Jefe' key, 'what do ya want for nothing?'",
		keyHex:    "4a656665",
		dataASCII: "what do ya want for nothing?",
		want256:   "c7d4072e788877ae3596bbb0da73b887c9171f93095b294ae857fbe2645e1ba5",
		want384: "f1101f8cbf9766fd6764d2ed61903f21ca9b18f57cf3e1a23ca13508a93243ce" +
			"48c045dc007f26a21b3f5e0e9df4c20a",
		want512: "5a4bfeab6166427c7a3647b747292b8384537cdb89afb3bf5665e4c5e709350b" +
			"287baec921fd7ca0ee7a0c31d022a95e1fc92ba9d77df883960275beb4e62024",
	},
	{
		name:      "§4.4 — 20-byte 0xaa key, 50 bytes 0xdd",
		keyHex:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		dataASCII: "<<DD>>",
		want256:   "84ec79124a27107865cedd8bd82da9965e5ed8c37b0ac98005a7f39ed58a4207",
		want384: "275cd0e661bb8b151c64d288f1f782fb91a8abd56858d72babb2d476f0458373" +
			"b41b6ab5bf174bec422e53fc3135ac6e",
		want512: "309e99f9ec075ec6c6d475eda1180687fcf1531195802a99b5677449a8625182" +
			"851cb332afb6a89c411325fbcbcd42afcb7b6e5aab7ea42c660f97fd8584bf03",
	},
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

func vectorData(v rfc4231SHA3Vector) []byte {
	if v.dataASCII == "<<DD>>" {
		out := make([]byte, 50)
		for i := range out {
			out[i] = 0xdd
		}
		return out
	}
	return []byte(v.dataASCII)
}

func TestSHA3_256(t *testing.T) {
	t.Parallel()

	t.Run("ID/Algorithm/Size", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_256([]byte("k"))
		wantID := crypto.ID{
			'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1',
		}
		if got := m.ID(); got != wantID {
			t.Fatalf("ID: got %v, want %v", got, wantID)
		}
		if got := m.Algorithm(); got != crypto.AlgHMACSHA3_256 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA3_256)
		}
		if got := m.Size(); got != crypto.DigestSize256 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize256)
		}
	})

	for _, v := range rfc4231SHA3Vectors {
		t.Run("Sign/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want256)
			got := hmacsha3.NewSHA3_256(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})

		t.Run("Verify/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want256)
			m := hmacsha3.NewSHA3_256(key)
			if !m.Verify(data, want) {
				t.Fatal("Verify rejected canonical MAC")
			}
			want[0] ^= 0x01
			if m.Verify(data, want) {
				t.Fatal("Verify accepted tampered MAC")
			}
		})
	}

	t.Run("Verify rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_256([]byte("key"))
		if m.Verify([]byte("data"), make([]byte, 16)) {
			t.Fatal("Verify accepted a short expected slice")
		}
		if m.Verify([]byte("data"), make([]byte, 64)) {
			t.Fatal("Verify accepted an over-long expected slice")
		}
		if m.Verify([]byte("data"), nil) {
			t.Fatal("Verify accepted nil expected")
		}
	})

	t.Run("New copies the key", func(t *testing.T) {
		t.Parallel()
		key := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		m := hmacsha3.NewSHA3_256(key)
		want := m.Sign([]byte("data"))
		for i := range key {
			key[i] = 0xff
		}
		if !m.Sign([]byte("data")).Equal(want) {
			t.Fatal("MAC depends on caller's mutable key buffer")
		}
	})

	t.Run("Stream split-write equals Sign", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_256([]byte("streaming-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full[:10])
		_, _ = s.Write(full[10:])
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("byte-by-byte writes equal a single write of length N", func(t *testing.T) {
		t.Parallel()
		// Locks the byte-additive contract at every write
		// boundary, including across SHA-3's sponge-rate
		// boundary internal to the underlying state.
		m := hmacsha3.NewSHA3_256([]byte("additive-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		for i := range full {
			_, _ = s.Write(full[i : i+1])
		}
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("byte-by-byte Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("Stream Reset preserves the key", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_256([]byte("reset-key"))
		s := m.NewStream()
		_, _ = s.Write([]byte("first"))
		_ = s.Sum()
		s.Reset()
		_, _ = s.Write([]byte("second"))
		got := s.Sum()
		if want := m.Sign([]byte("second")); !got.Equal(want) {
			t.Fatalf("Stream after Reset != Sign:\n got=%s\nwant=%s", got, want)
		}
	})
}

func TestSHA3_384(t *testing.T) {
	t.Parallel()

	t.Run("ID/Algorithm/Size", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_384([]byte("k"))
		wantID := crypto.ID{
			'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1',
		}
		if got := m.ID(); got != wantID {
			t.Fatalf("ID: got %v, want %v", got, wantID)
		}
		if got := m.Algorithm(); got != crypto.AlgHMACSHA3_384 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA3_384)
		}
		if got := m.Size(); got != crypto.DigestSize384 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize384)
		}
	})

	for _, v := range rfc4231SHA3Vectors {
		t.Run("Sign/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want384)
			got := hmacsha3.NewSHA3_384(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})

		t.Run("Verify/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want384)
			m := hmacsha3.NewSHA3_384(key)
			if !m.Verify(data, want) {
				t.Fatal("Verify rejected canonical MAC")
			}
			want[0] ^= 0x01
			if m.Verify(data, want) {
				t.Fatal("Verify accepted tampered MAC")
			}
		})
	}

	t.Run("Verify rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_384([]byte("key"))
		if m.Verify([]byte("data"), make([]byte, 32)) {
			t.Fatal("Verify accepted a short expected slice")
		}
		if m.Verify([]byte("data"), make([]byte, 64)) {
			t.Fatal("Verify accepted an over-long expected slice")
		}
	})

	t.Run("New copies the key", func(t *testing.T) {
		t.Parallel()
		key := []byte{0xab, 0xcd, 0xef}
		m := hmacsha3.NewSHA3_384(key)
		want := m.Sign([]byte("data"))
		for i := range key {
			key[i] = 0
		}
		if !m.Sign([]byte("data")).Equal(want) {
			t.Fatal("MAC depends on caller's mutable key buffer")
		}
	})

	t.Run("Stream split-write equals Sign", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_384([]byte("streaming-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full[:20])
		_, _ = s.Write(full[20:])
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("byte-by-byte writes equal a single write of length N", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_384([]byte("additive-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		for i := range full {
			_, _ = s.Write(full[i : i+1])
		}
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("byte-by-byte Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})
}

func TestSHA3_512(t *testing.T) {
	t.Parallel()

	t.Run("ID/Algorithm/Size", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_512([]byte("k"))
		wantID := crypto.ID{
			'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1',
		}
		if got := m.ID(); got != wantID {
			t.Fatalf("ID: got %v, want %v", got, wantID)
		}
		if got := m.Algorithm(); got != crypto.AlgHMACSHA3_512 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA3_512)
		}
		if got := m.Size(); got != crypto.DigestSize512 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize512)
		}
	})

	for _, v := range rfc4231SHA3Vectors {
		t.Run("Sign/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want512)
			got := hmacsha3.NewSHA3_512(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})

		t.Run("Verify/"+v.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, v.keyHex)
			data := vectorData(v)
			want := mustDecodeHex(t, v.want512)
			m := hmacsha3.NewSHA3_512(key)
			if !m.Verify(data, want) {
				t.Fatal("Verify rejected canonical MAC")
			}
			want[0] ^= 0x01
			if m.Verify(data, want) {
				t.Fatal("Verify accepted tampered MAC")
			}
		})
	}

	t.Run("Verify rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_512([]byte("key"))
		if m.Verify([]byte("data"), make([]byte, 32)) {
			t.Fatal("Verify accepted a short expected slice")
		}
		if m.Verify([]byte("data"), make([]byte, 96)) {
			t.Fatal("Verify accepted an over-long expected slice")
		}
	})

	t.Run("New copies the key", func(t *testing.T) {
		t.Parallel()
		key := []byte{0x10, 0x20, 0x30, 0x40}
		m := hmacsha3.NewSHA3_512(key)
		want := m.Sign([]byte("data"))
		for i := range key {
			key[i] = 0
		}
		if !m.Sign([]byte("data")).Equal(want) {
			t.Fatal("MAC depends on caller's mutable key buffer")
		}
	})

	t.Run("Stream split-write equals Sign", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_512([]byte("streaming-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full[:5])
		_, _ = s.Write(full[5:25])
		_, _ = s.Write(full[25:])
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("byte-by-byte writes equal a single write of length N", func(t *testing.T) {
		t.Parallel()
		m := hmacsha3.NewSHA3_512([]byte("additive-key"))
		full := []byte("agentic-context-payload-streamed-across-many-writes")
		want := m.Sign(full)
		s := m.NewStream()
		for i := range full {
			_, _ = s.Write(full[i : i+1])
		}
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("byte-by-byte Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})
}

func TestCrossCheckStdlib(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		keyLen  int
		dataLen int
	}{
		{"empty", 0, 0},
		{"short", 8, 16},
		{"block-size SHA3-256 (136B rate)", 136, 64},
		{"block-size SHA3-512 (72B rate)", 72, 64},
		{"long key, long data", 200, 8192},
	}
	for _, tc := range cases {
		t.Run("SHA3-256/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := bytesSeq(tc.keyLen, 1)
			data := bytesSeq(tc.dataLen, 0xff)
			ours := hmacsha3.NewSHA3_256(key).Sign(data)
			want := stdHMACSum(stdsha3.New256, key, data)
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA3-256:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})
		t.Run("SHA3-384/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := bytesSeq(tc.keyLen, 1)
			data := bytesSeq(tc.dataLen, 0xff)
			ours := hmacsha3.NewSHA3_384(key).Sign(data)
			want := stdHMACSum(stdsha3.New384, key, data)
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA3-384:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})
		t.Run("SHA3-512/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := bytesSeq(tc.keyLen, 1)
			data := bytesSeq(tc.dataLen, 0xff)
			ours := hmacsha3.NewSHA3_512(key).Sign(data)
			want := stdHMACSum(stdsha3.New512, key, data)
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA3-512:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})
	}
}

func TestZeroAlloc(t *testing.T) {
	m256 := hmacsha3.NewSHA3_256([]byte("k"))
	m384 := hmacsha3.NewSHA3_384([]byte("k"))
	m512 := hmacsha3.NewSHA3_512([]byte("k"))

	cases := []struct {
		name string
		fn   func()
	}{
		{"SHA3-256 ID", func() { _ = m256.ID() }},
		{"SHA3-256 Algorithm", func() { _ = m256.Algorithm() }},
		{"SHA3-256 Size", func() { _ = m256.Size() }},
		{"SHA3-384 ID", func() { _ = m384.ID() }},
		{"SHA3-384 Algorithm", func() { _ = m384.Algorithm() }},
		{"SHA3-384 Size", func() { _ = m384.Size() }},
		{"SHA3-512 ID", func() { _ = m512.ID() }},
		{"SHA3-512 Algorithm", func() { _ = m512.Algorithm() }},
		{"SHA3-512 Size", func() { _ = m512.Size() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	streams := []struct {
		stream crypto.Stream
		name   string
	}{
		{m256.NewStream(), "SHA3-256 Stream"},
		{m384.NewStream(), "SHA3-384 Stream"},
		{m512.NewStream(), "SHA3-512 Stream"},
	}
	for _, sc := range streams {
		t.Run(sc.name+" loop is zero-alloc", func(t *testing.T) {
			s := sc.stream
			buf := make([]byte, 256)
			fn := func() {
				s.Reset()
				_, _ = s.Write(buf)
				_ = s.Sum()
			}
			if got := testing.AllocsPerRun(100, fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", sc.name, got)
			}
		})
	}

}

func FuzzCrossStdlib(f *testing.F) {
	f.Add([]byte("k"), []byte("data"))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, key, data []byte) {
		check := func(name string, ours crypto.Digest, want []byte) {
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("%s: diverged from stdlib", name)
			}
		}
		check("SHA3-256",
			hmacsha3.NewSHA3_256(key).Sign(data),
			stdHMACSum(stdsha3.New256, key, data))
		check("SHA3-384",
			hmacsha3.NewSHA3_384(key).Sign(data),
			stdHMACSum(stdsha3.New384, key, data))
		check("SHA3-512",
			hmacsha3.NewSHA3_512(key).Sign(data),
			stdHMACSum(stdsha3.New512, key, data))
	})
}

// macSigner is the minimal Sign / Verify / NewStream surface
// shared by the SHA3 MAC variants. Letting the benchmarks
// dispatch over an interface keeps the matrix (algorithm × size
// × mode) in one place while still exercising the concrete
// pooled-hash code path.
type macSigner interface {
	Sign(data []byte) crypto.Digest
	Verify(data, mac []byte) bool
	NewStream() crypto.Stream
}

var benchSizes = []struct {
	name string
	n    int
}{
	{"8B", 8},
	{"64B", 64},
	{"256B", 256},
	{"4K", 4096},
	{"64K", 65536},
}

func benchAlgs() []struct {
	name string
	m    macSigner
} {
	key := []byte("benchmark-key")
	return []struct {
		name string
		m    macSigner
	}{
		{"sha3-256", hmacsha3.NewSHA3_256(key)},
		{"sha3-384", hmacsha3.NewSHA3_384(key)},
		{"sha3-512", hmacsha3.NewSHA3_512(key)},
	}
}

func BenchmarkSign(b *testing.B) {
	for _, alg := range benchAlgs() {
		b.Run(alg.name, func(b *testing.B) {
			for _, sz := range benchSizes {
				b.Run(sz.name, func(b *testing.B) {
					data := make([]byte, sz.n)
					b.ReportAllocs()
					b.SetBytes(int64(sz.n))
					for b.Loop() {
						_ = alg.m.Sign(data)
					}
				})
			}
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	for _, alg := range benchAlgs() {
		b.Run(alg.name, func(b *testing.B) {
			for _, sz := range benchSizes {
				b.Run(sz.name, func(b *testing.B) {
					data := make([]byte, sz.n)
					expected := alg.m.Sign(data).Bytes()
					b.ReportAllocs()
					b.SetBytes(int64(sz.n))
					for b.Loop() {
						_ = alg.m.Verify(data, expected)
					}
				})
			}
		})
	}
}

// BenchmarkStream covers the canonical hot-path pattern: one
// [crypto.Stream] reused across many messages via Reset.
func BenchmarkStream(b *testing.B) {
	for _, alg := range benchAlgs() {
		b.Run(alg.name, func(b *testing.B) {
			b.Run("sequential", func(b *testing.B) {
				for _, sz := range benchSizes {
					b.Run(sz.name, func(b *testing.B) {
						s := alg.m.NewStream()
						data := make([]byte, sz.n)
						b.ReportAllocs()
						b.SetBytes(int64(sz.n))
						for b.Loop() {
							s.Reset()
							_, _ = s.Write(data)
							_ = s.Sum()
						}
					})
				}
			})
			b.Run("parallel", func(b *testing.B) {
				for _, sz := range benchSizes {
					b.Run(sz.name, func(b *testing.B) {
						data := make([]byte, sz.n)
						b.ReportAllocs()
						b.SetBytes(int64(sz.n))
						b.RunParallel(func(pb *testing.PB) {
							s := alg.m.NewStream()
							for pb.Next() {
								s.Reset()
								_, _ = s.Write(data)
								_ = s.Sum()
							}
						})
					})
				}
			})
		})
	}
}

// BenchmarkSignParallel exercises [MAC.Sign] under fan-out
// across cores — validates the per-MAC [pool.Pool] of pre-keyed
// [hash.Hash] instances scales without lock contention.
func BenchmarkSignParallel(b *testing.B) {
	for _, alg := range benchAlgs() {
		b.Run(alg.name, func(b *testing.B) {
			for _, sz := range []struct {
				name string
				n    int
			}{
				{"8B", 8},
				{"64B", 64},
				{"4K", 4096},
				{"64K", 65536},
			} {
				b.Run(sz.name, func(b *testing.B) {
					data := make([]byte, sz.n)
					b.ReportAllocs()
					b.SetBytes(int64(sz.n))
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							_ = alg.m.Sign(data)
						}
					})
				})
			}
		})
	}
}

// stdHMACSum returns HMAC(newH, key, data) using the stdlib —
// the reference oracle for the cross-check tests in this file.
func stdHMACSum(newH func() *stdsha3.SHA3, key, data []byte) []byte {
	h := stdhmac.New(func() hash.Hash { return newH() }, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func bytesSeq(n int, start byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}
