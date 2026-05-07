// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	"bytes"
	stdhmac "crypto/hmac"
	stdsha256 "crypto/sha256"
	"encoding/hex"
	"testing"

	"go.thesmos.sh/core/crypto"
	hmacsha256 "go.thesmos.sh/core/crypto/hmac/sha256"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

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

func TestID(t *testing.T) {
	t.Parallel()

	t.Run("returns the canonical hmac-sha256/v1 tag", func(t *testing.T) {
		t.Parallel()
		got := hmacsha256.New([]byte("k")).ID()
		want := crypto.ID{'h', 'm', 'a', 'c', '-', 's', 'h', 'a', '2', '5', '6', '/', 'v', '1'}
		if got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
	})
}

func TestAlgorithm(t *testing.T) {
	t.Parallel()
	if got := hmacsha256.New([]byte("k")).Algorithm(); got != crypto.AlgHMACSHA256 {
		t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgHMACSHA256)
	}
}

func TestSize(t *testing.T) {
	t.Parallel()
	if got := hmacsha256.New([]byte("k")).Size(); got != crypto.DigestSize256 {
		t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize256)
	}
}

func TestSignRFC4231(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231Vectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			want := mustDecodeHex(t, tc.wantHex)

			m := hmacsha256.New(key)
			got := m.Sign(data)
			if got.Size() != crypto.DigestSize256 {
				t.Fatalf("Sign size: got %d, want %d", got.Size(), crypto.DigestSize256)
			}
			if !crypto.NewDigest256(asArray256(t, want)).Equal(got) {
				t.Fatalf("Sign: got %x, want %x", got.Bytes(), want)
			}
		})
	}
}

func TestVerifyRFC4231(t *testing.T) {
	t.Parallel()
	for _, tc := range rfc4231Vectors {
		t.Run(tc.name+"/accepts canonical MAC", func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			expected := mustDecodeHex(t, tc.wantHex)

			m := hmacsha256.New(key)
			if !m.Verify(data, expected) {
				t.Fatal("Verify rejected the canonical MAC")
			}
		})

		t.Run(tc.name+"/rejects flipped bit", func(t *testing.T) {
			t.Parallel()
			key := mustDecodeHex(t, tc.keyHex)
			data := mustDecodeHex(t, tc.dataHex)
			expected := mustDecodeHex(t, tc.wantHex)
			expected[0] ^= 0x01

			m := hmacsha256.New(key)
			if m.Verify(data, expected) {
				t.Fatal("Verify accepted a tampered MAC")
			}
		})
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()

	t.Run("rejects expected of wrong length", func(t *testing.T) {
		t.Parallel()
		m := hmacsha256.New([]byte("key"))
		// Short
		if m.Verify([]byte("data"), make([]byte, 16)) {
			t.Fatal("Verify accepted a short expected slice")
		}
		// Long
		if m.Verify([]byte("data"), make([]byte, 64)) {
			t.Fatal("Verify accepted an over-long expected slice")
		}
		// Empty
		if m.Verify([]byte("data"), nil) {
			t.Fatal("Verify accepted nil expected")
		}
	})

	t.Run("rejects different key over identical data", func(t *testing.T) {
		t.Parallel()
		data := []byte("payload")
		m1 := hmacsha256.New([]byte("key-one"))
		m2 := hmacsha256.New([]byte("key-two"))
		if m2.Verify(data, m1.Sign(data).Bytes()) {
			t.Fatal("Verify accepted a MAC computed under a different key")
		}
	})
}

func TestNewKeyIsCopied(t *testing.T) {
	t.Parallel()
	key := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	data := []byte("the quick brown fox")

	m := hmacsha256.New(key)
	want := m.Sign(data)

	// Mutate the caller's buffer; the MAC must continue to
	// produce the same digest under the original key.
	for i := range key {
		key[i] = 0xff
	}
	got := m.Sign(data)
	if !got.Equal(want) {
		t.Fatalf("MAC depends on caller's mutable key buffer:\n got=%s\nwant=%s", got, want)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()

	key := []byte("streaming-key")
	full := []byte("agentic-context-payload-streamed-across-many-writes")
	m := hmacsha256.New(key)

	t.Run("equals Sign over the same bytes", func(t *testing.T) {
		t.Parallel()
		want := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full)
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("Stream Sum != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("multiple writes equal a single write of the concatenation", func(t *testing.T) {
		t.Parallel()
		want := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full[:10])
		_, _ = s.Write(full[10:30])
		_, _ = s.Write(full[30:])
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("split Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("byte-by-byte writes equal a single write of length N", func(t *testing.T) {
		t.Parallel()
		// Locks the byte-additive contract at every write
		// boundary, not just the few sampled by the split
		// subtest above.
		want := m.Sign(full)
		s := m.NewStream()
		for i := range full {
			_, _ = s.Write(full[i : i+1])
		}
		if got := s.Sum(); !got.Equal(want) {
			t.Fatalf("byte-by-byte Stream != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("Reset preserves the key and yields a fresh MAC", func(t *testing.T) {
		t.Parallel()
		s := m.NewStream()
		_, _ = s.Write([]byte("first message"))
		_ = s.Sum()

		s.Reset()
		_, _ = s.Write([]byte("second message"))
		got := s.Sum()
		if want := m.Sign([]byte("second message")); !got.Equal(want) {
			t.Fatalf("Stream after Reset != Sign:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("Sum does not reset; further writes extend the same MAC", func(t *testing.T) {
		t.Parallel()
		s := m.NewStream()
		_, _ = s.Write([]byte("ab"))
		_ = s.Sum()
		_, _ = s.Write([]byte("c"))
		got := s.Sum()
		if want := m.Sign([]byte("abc")); !got.Equal(want) {
			t.Fatal("Sum reset state — must snapshot only")
		}
	})

	t.Run("verification via ConstantTimeEqual on streamed input", func(t *testing.T) {
		t.Parallel()
		expected := m.Sign(full)
		s := m.NewStream()
		_, _ = s.Write(full)
		if got := s.Sum(); !got.ConstantTimeEqual(expected) {
			t.Fatalf("ConstantTimeEqual rejected canonical MAC:\n got=%s\nwant=%s",
				got, expected)
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
		{"empty key, empty data", 0, 0},
		{"short key, short data", 8, 16},
		{"block-size key", 64, 64},
		{"long key (longer than block)", 200, 256},
		{"short key, long data", 16, 8192},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := make([]byte, tc.keyLen)
			for i := range key {
				key[i] = byte(i + 1)
			}
			data := make([]byte, tc.dataLen)
			for i := range data {
				data[i] = byte(255 - i)
			}

			ours := hmacsha256.New(key).Sign(data)

			h := stdhmac.New(stdsha256.New, key)
			_, _ = h.Write(data)
			want := h.Sum(nil)

			if !bytes.Equal(ours.Bytes(), want) {
				t.Fatalf("diverged from stdlib HMAC-SHA-256:\n ours=%x\n std =%x",
					ours.Bytes(), want)
			}
		})
	}
}

func TestZeroAlloc(t *testing.T) {
	m := hmacsha256.New([]byte("zero-alloc-key"))

	cases := []struct {
		name string
		fn   func()
	}{
		{"ID", func() { _ = m.ID() }},
		{"Algorithm", func() { _ = m.Algorithm() }},
		{"Size", func() { _ = m.Size() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	t.Run("Stream Write/Sum/Reset on a reused stream is zero-alloc", func(t *testing.T) {
		s := m.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("Stream loop: %v allocs/op, want 0", got)
		}
	})
}

// FuzzVerifyConsistency asserts the round-trip + tamper-rejection
// property: for any (key, data, tamper), Verify(data, Sign(data))
// is true and Verify(data, tamper) is true iff tamper equals
// Sign(data). Fuzz catches any divergence between Sign and Verify
// — they must agree on a single keyed function.
func FuzzVerifyConsistency(f *testing.F) {
	f.Add([]byte("k"), []byte("data"), []byte{})
	f.Add([]byte("longer-key"), []byte{}, make([]byte, 32))
	f.Add(make([]byte, 200), make([]byte, 1024), make([]byte, 32))

	f.Fuzz(func(t *testing.T, key, data, tamper []byte) {
		m := hmacsha256.New(key)

		canonical := m.Sign(data)
		if !m.Verify(data, canonical.Bytes()) {
			t.Fatalf("Verify rejected canonical MAC:\n key=%x\n data=%x", key, data)
		}

		// Tamper acceptance must be equivalent to byte-equal-with-canonical.
		got := m.Verify(data, tamper)
		want := bytes.Equal(tamper, canonical.Bytes())
		if got != want {
			t.Fatalf("Verify(tamper) inconsistent: got=%v want=%v", got, want)
		}
	})
}

// FuzzCrossStdlib asserts byte-for-byte equality with the
// stdlib's reference HMAC-SHA-256 across arbitrary keys and
// inputs.
func FuzzCrossStdlib(f *testing.F) {
	f.Add([]byte("k"), []byte("data"))
	f.Add([]byte{}, []byte{})
	f.Add(make([]byte, 200), make([]byte, 8192))

	f.Fuzz(func(t *testing.T, key, data []byte) {
		ours := hmacsha256.New(key).Sign(data)

		h := stdhmac.New(stdsha256.New, key)
		_, _ = h.Write(data)
		want := h.Sum(nil)

		if !bytes.Equal(ours.Bytes(), want) {
			t.Fatalf("diverged from stdlib:\n ours=%x\n std =%x", ours.Bytes(), want)
		}
	})
}

func BenchmarkSign(b *testing.B) {
	m := hmacsha256.New([]byte("benchmark-key"))
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

func BenchmarkVerify(b *testing.B) {
	m := hmacsha256.New([]byte("benchmark-key"))
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
			expected := m.Sign(data).Bytes()
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = m.Verify(data, expected)
			}
		})
	}
}

// BenchmarkStream covers the canonical hot-path pattern: one
// [crypto.Stream] reused across many messages via Reset.
// Sub-benchmarks: sequential is the steady-state single-
// goroutine cost; parallel exercises one-stream-per-goroutine
// fan-out, the ceiling consumers hit when they spread MAC work
// across cores.
func BenchmarkStream(b *testing.B) {
	m := hmacsha256.New([]byte("benchmark-key"))
	sizes := []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"256B", 256},
		{"4K", 4096},
		{"64K", 65536},
	}

	b.Run("sequential", func(b *testing.B) {
		for _, sz := range sizes {
			b.Run(sz.name, func(b *testing.B) {
				s := m.NewStream()
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
		for _, sz := range sizes {
			b.Run(sz.name, func(b *testing.B) {
				data := make([]byte, sz.n)
				b.ReportAllocs()
				b.SetBytes(int64(sz.n))
				b.RunParallel(func(pb *testing.PB) {
					s := m.NewStream()
					for pb.Next() {
						s.Reset()
						_, _ = s.Write(data)
						_ = s.Sum()
					}
				})
			})
		}
	})
}

// BenchmarkSignParallel exercises [MAC.Sign] under fan-out
// across cores — validates that the per-MAC [pool.Pool] of
// pre-keyed [hash.Hash] instances scales without lock
// contention. Compare against [BenchmarkSign] for the
// single-thread-vs-parallel ratio.
func BenchmarkSignParallel(b *testing.B) {
	m := hmacsha256.New([]byte("benchmark-key"))
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
					_ = m.Sign(data)
				}
			})
		})
	}
}

func asArray256(t *testing.T, b []byte) [crypto.DigestSize256]byte {
	t.Helper()
	if len(b) != crypto.DigestSize256 {
		t.Fatalf("expected %d bytes, got %d", crypto.DigestSize256, len(b))
	}
	var out [crypto.DigestSize256]byte
	copy(out[:], b)
	return out
}
