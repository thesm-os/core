// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha512 "crypto/sha512"
	"encoding/hex"
	"testing"

	"go.thesmos.sh/core/crypto"
	hmacsha512 "go.thesmos.sh/core/crypto/hmac/sha512"
)

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

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

func TestSHA384(t *testing.T) {
	t.Parallel()

	t.Run("ID/Algorithm/Size", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA384([]byte("k"))
		wantID := crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
		if got := m.ID(); got != wantID {
			t.Fatalf("ID: got %v, want %v", got, wantID)
		}
		if got := m.Algorithm(); got != crypto.AlgHMACSHA384 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA384)
		}
		if got := m.Size(); got != crypto.DigestSize384 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize384)
		}
	})

	for _, tc := range rfc4231Vectors {
		t.Run("Sign/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want384)
			got := hmacsha512.NewSHA384(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})

		t.Run("Verify accepts canonical/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want384)
			if !hmacsha512.NewSHA384(key).Verify(data, want) {
				t.Fatal("Verify rejected the canonical MAC")
			}
		})

		t.Run("Verify rejects flipped bit/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want384)
			want[0] ^= 0x01
			if hmacsha512.NewSHA384(key).Verify(data, want) {
				t.Fatal("Verify accepted a tampered MAC")
			}
		})
	}

	t.Run("Verify rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA384([]byte("key"))
		if m.Verify([]byte("data"), make([]byte, 32)) {
			t.Fatal("Verify accepted an under-length expected slice")
		}
		if m.Verify([]byte("data"), make([]byte, 64)) {
			t.Fatal("Verify accepted an over-length expected slice")
		}
		if m.Verify([]byte("data"), nil) {
			t.Fatal("Verify accepted nil expected")
		}
	})

	t.Run("New copies the key", func(t *testing.T) {
		t.Parallel()
		key := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		m := hmacsha512.NewSHA384(key)
		want := m.Sign([]byte("data"))
		for i := range key {
			key[i] = 0xff
		}
		if !m.Sign([]byte("data")).Equal(want) {
			t.Fatal("MAC depends on caller's mutable key buffer")
		}
	})

	t.Run("Stream equals Sign over the same bytes", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA384([]byte("streaming-key"))
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
		// boundary.
		m := hmacsha512.NewSHA384([]byte("additive-key"))
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
		m := hmacsha512.NewSHA384([]byte("reset-key"))
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

func TestSHA512(t *testing.T) {
	t.Parallel()

	t.Run("ID/Algorithm/Size", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA512([]byte("k"))
		wantID := crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
		if got := m.ID(); got != wantID {
			t.Fatalf("ID: got %v, want %v", got, wantID)
		}
		if got := m.Algorithm(); got != crypto.AlgHMACSHA512 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA512)
		}
		if got := m.Size(); got != crypto.DigestSize512 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize512)
		}
	})

	for _, tc := range rfc4231Vectors {
		t.Run("Sign/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want512)
			got := hmacsha512.NewSHA512(key).Sign(data)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Sign:\n got=%x\nwant=%x", got.Bytes(), want)
			}
		})

		t.Run("Verify accepts canonical/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want512)
			if !hmacsha512.NewSHA512(key).Verify(data, want) {
				t.Fatal("Verify rejected the canonical MAC")
			}
		})

		t.Run("Verify rejects flipped bit/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.want512)
			want[0] ^= 0x01
			if hmacsha512.NewSHA512(key).Verify(data, want) {
				t.Fatal("Verify accepted a tampered MAC")
			}
		})
	}

	t.Run("Verify rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA512([]byte("key"))
		if m.Verify([]byte("data"), make([]byte, 32)) {
			t.Fatal("Verify accepted an under-length expected slice")
		}
		if m.Verify([]byte("data"), make([]byte, 96)) {
			t.Fatal("Verify accepted an over-length expected slice")
		}
		if m.Verify([]byte("data"), nil) {
			t.Fatal("Verify accepted nil expected")
		}
	})

	t.Run("New copies the key", func(t *testing.T) {
		t.Parallel()
		key := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		m := hmacsha512.NewSHA512(key)
		want := m.Sign([]byte("data"))
		for i := range key {
			key[i] = 0xff
		}
		if !m.Sign([]byte("data")).Equal(want) {
			t.Fatal("MAC depends on caller's mutable key buffer")
		}
	})

	t.Run("Stream equals Sign over the same bytes", func(t *testing.T) {
		t.Parallel()
		m := hmacsha512.NewSHA512([]byte("streaming-key"))
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
		// Locks the byte-additive contract at every write
		// boundary.
		m := hmacsha512.NewSHA512([]byte("additive-key"))
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
		m := hmacsha512.NewSHA512([]byte("reset-key"))
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

func TestCrossCheckStdlib(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		keyLen  int
		dataLen int
	}{
		{"empty", 0, 0},
		{"short", 8, 16},
		{"block-size key SHA-512 (128B)", 128, 64},
		{"long key, long data", 200, 8192},
	}
	for _, tc := range cases {
		t.Run("SHA-384/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := bytesSeq(tc.keyLen, 1)
			data := bytesSeq(tc.dataLen, 0xff)
			ours := hmacsha512.NewSHA384(key).Sign(data)

			h := stdhmac.New(stdsha512.New384, key)
			_, _ = h.Write(data)
			want := h.Sum(nil)
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA-384:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})

		t.Run("SHA-512/"+tc.name, func(t *testing.T) {
			t.Parallel()
			key := bytesSeq(tc.keyLen, 1)
			data := bytesSeq(tc.dataLen, 0xff)
			ours := hmacsha512.NewSHA512(key).Sign(data)

			h := stdhmac.New(stdsha512.New, key)
			_, _ = h.Write(data)
			want := h.Sum(nil)
			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA-512:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})
	}
}

func TestZeroAlloc(t *testing.T) {
	m384 := hmacsha512.NewSHA384([]byte("zero-alloc-384"))
	m512 := hmacsha512.NewSHA512([]byte("zero-alloc-512"))

	cases := []struct {
		name string
		fn   func()
	}{
		{"SHA384 ID", func() { _ = m384.ID() }},
		{"SHA384 Algorithm", func() { _ = m384.Algorithm() }},
		{"SHA384 Size", func() { _ = m384.Size() }},
		{"SHA512 ID", func() { _ = m512.ID() }},
		{"SHA512 Algorithm", func() { _ = m512.Algorithm() }},
		{"SHA512 Size", func() { _ = m512.Size() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	t.Run("SHA384 Stream loop is zero-alloc", func(t *testing.T) {
		s := m384.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("SHA384 Stream: %v allocs/op, want 0", got)
		}
	})

	t.Run("SHA512 Stream loop is zero-alloc", func(t *testing.T) {
		s := m512.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("SHA512 Stream: %v allocs/op, want 0", got)
		}
	})
}

func FuzzCrossStdlib(f *testing.F) {
	f.Add([]byte("k"), []byte("data"))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, key, data []byte) {
		ours384 := hmacsha512.NewSHA384(key).Sign(data)
		h384 := stdhmac.New(stdsha512.New384, key)
		_, _ = h384.Write(data)
		if !bytes.Equal(ours384.Bytes(), h384.Sum(nil)) {
			t.Fatal("SHA-384 diverged from stdlib")
		}

		ours512 := hmacsha512.NewSHA512(key).Sign(data)
		h512 := stdhmac.New(stdsha512.New, key)
		_, _ = h512.Write(data)
		if !bytes.Equal(ours512.Bytes(), h512.Sum(nil)) {
			t.Fatal("SHA-512 diverged from stdlib")
		}
	})
}

func BenchmarkSHA384Sign(b *testing.B) {
	m := hmacsha512.NewSHA384([]byte("benchmark-key"))
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"256B", 256},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = m.Sign(data)
			}
		})
	}
}

func BenchmarkSHA512Sign(b *testing.B) {
	m := hmacsha512.NewSHA512([]byte("benchmark-key"))
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"256B", 256},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = m.Sign(data)
			}
		})
	}
}

func bytesSeq(n int, start byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}
