// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha256_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"go.thesmos.sh/core/crypto"
	cryptosha256 "go.thesmos.sh/core/crypto/sha256"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

func mustDigest256(t *testing.T, s string) crypto.Digest {
	t.Helper()
	b := mustDecodeHex(t, s)
	if len(b) != crypto.DigestSize256 {
		t.Fatalf("digest fixture length: got %d, want %d", len(b), crypto.DigestSize256)
	}
	var raw [crypto.DigestSize256]byte
	copy(raw[:], b)
	return crypto.NewDigest256(raw)
}

func TestID(t *testing.T) {
	t.Parallel()

	t.Run("returns the canonical sha256/v1 tag", func(t *testing.T) {
		t.Parallel()
		got := cryptosha256.New().ID()
		want := crypto.ID{'s', 'h', 'a', '2', '5', '6', '/', 'v', '1'}
		if got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
	})

	t.Run("zero-value Hasher returns the same ID", func(t *testing.T) {
		t.Parallel()
		var z cryptosha256.Hasher
		if got, want := z.ID(), cryptosha256.New().ID(); got != want {
			t.Fatalf("zero-value ID: got %v, want %v", got, want)
		}
	})
}

func TestAlgorithm(t *testing.T) {
	t.Parallel()
	if got := cryptosha256.New().Algorithm(); got != crypto.AlgSHA256 {
		t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA256)
	}
}

func TestHash(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		"empty": {
			input:   []byte{},
			wantHex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"\"abc\"": {
			input:   []byte("abc"),
			wantHex: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
		"two-block message (FIPS 180-4 §B.2)": {
			input:   []byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"),
			wantHex: "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cryptosha256.New().Hash(tc.input)
			want := mustDecodeHex(t, tc.wantHex)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Hash: got %x, want %s", got.Bytes(), tc.wantHex)
			}
		})
	}

	t.Run("nil and empty slice produce the same digest", func(t *testing.T) {
		t.Parallel()
		h := cryptosha256.New()
		if !h.Hash(nil).Equal(h.Hash([]byte{})) {
			t.Fatal("nil and empty slice produced different digests")
		}
	})

	t.Run("distinct inputs produce distinct digests", func(t *testing.T) {
		t.Parallel()
		h := cryptosha256.New()
		if h.Hash([]byte("alpha")).Equal(h.Hash([]byte("beta"))) {
			t.Fatal("distinct inputs produced identical digests")
		}
	})
}

func TestCombinePanicsOnSizeMismatch(t *testing.T) {
	t.Parallel()
	h := cryptosha256.New()
	correct := crypto.NewDigest256([crypto.DigestSize256]byte{})
	wrong := crypto.NewDigest384([crypto.DigestSize384]byte{})

	cases := []struct {
		name        string
		left, right crypto.Digest
	}{
		{"wrong-left", wrong, correct},
		{"wrong-right", correct, wrong},
		{"both-wrong", wrong, wrong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("expected panic on size mismatch, got none")
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panic value type: got %T, want string", r)
				}
				if !strings.Contains(msg, "crypto/sha256") {
					t.Fatalf("panic message %q lacks package tag", msg)
				}
				if !strings.Contains(msg, "32") {
					t.Fatalf("panic message %q lacks expected-size info", msg)
				}
			}()
			_ = h.Combine(tc.left, tc.right)
		})
	}
}

func TestCombine(t *testing.T) {
	t.Parallel()

	t.Run("matches a manual SHA-256 over left||right", func(t *testing.T) {
		t.Parallel()
		left := mustDigest256(t,
			"0000000000000000000000000000000000000000000000000000000000000001")
		right := mustDigest256(t,
			"0000000000000000000000000000000000000000000000000000000000000002")
		var concat [64]byte
		copy(concat[:32], left.Bytes())
		copy(concat[32:], right.Bytes())
		want := crypto.NewDigest256(sha256.Sum256(concat[:]))

		got := cryptosha256.New().Combine(left, right)
		if !got.Equal(want) {
			t.Fatalf("Combine:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("not symmetric: Combine(a,b) != Combine(b,a)", func(t *testing.T) {
		t.Parallel()
		a := mustDigest256(t,
			"1111111111111111111111111111111111111111111111111111111111111111")
		b := mustDigest256(t,
			"2222222222222222222222222222222222222222222222222222222222222222")
		h := cryptosha256.New()
		if h.Combine(a, b).Equal(h.Combine(b, a)) {
			t.Fatal("Combine produced the same digest for swapped arguments")
		}
	})

	t.Run("Combine of two zero-content digests produces SHA-256 of 64 zero bytes", func(t *testing.T) {
		t.Parallel()
		zero := crypto.NewDigest256([crypto.DigestSize256]byte{})
		got := cryptosha256.New().Combine(zero, zero)
		want := mustDigest256(t,
			"f5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b")
		if !got.Equal(want) {
			t.Fatalf("Combine(0,0): got %s, want %s", got, want)
		}
	})
}

func TestStream(t *testing.T) {
	t.Parallel()

	h := cryptosha256.New()

	t.Run("equals Hash over the same bytes", func(t *testing.T) {
		t.Parallel()
		payload := []byte("the quick brown fox jumps over the lazy dog")
		want := h.Hash(payload)

		s := h.NewStream()
		_, err := s.Write(payload)
		if err != nil {
			t.Fatalf("Write: unexpected error %v", err)
		}
		got := s.Sum()
		if !got.Equal(want) {
			t.Fatalf("Stream Sum != Hash:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("multiple Writes equal a single Write of the concatenation", func(t *testing.T) {
		t.Parallel()
		full := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
		want := h.Hash(full)

		s := h.NewStream()
		_, _ = s.Write(full[:10])
		_, _ = s.Write(full[10:25])
		_, _ = s.Write(full[25:])
		got := s.Sum()

		if !got.Equal(want) {
			t.Fatalf("split Stream != single Hash:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("Reset allows reuse for a fresh hash", func(t *testing.T) {
		t.Parallel()
		s := h.NewStream()
		_, _ = s.Write([]byte("first message"))
		_ = s.Sum()

		s.Reset()
		_, _ = s.Write([]byte("second message"))
		got := s.Sum()
		want := h.Hash([]byte("second message"))
		if !got.Equal(want) {
			t.Fatalf("Stream after Reset != Hash:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("Sum does not reset; further Writes extend the same hash", func(t *testing.T) {
		t.Parallel()
		s := h.NewStream()
		_, _ = s.Write([]byte("ab"))
		_ = s.Sum()
		_, _ = s.Write([]byte("c"))
		got := s.Sum()
		// The cumulative digest must equal Hash("abc"), not Hash("c").
		if !got.Equal(h.Hash([]byte("abc"))) {
			t.Fatal("Sum reset state — must snapshot only")
		}
	})

	t.Run("io.Copy from a Reader produces the same digest", func(t *testing.T) {
		t.Parallel()
		input := strings.Repeat("agentic-context-bytes ", 1024) // ~22 KB
		want := h.Hash([]byte(input))
		got, err := crypto.HashReader(h, strings.NewReader(input))
		if err != nil {
			t.Fatalf("HashReader: unexpected error %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("HashReader != Hash:\n got=%s\nwant=%s", got, want)
		}
	})
}

func TestZeroAlloc(t *testing.T) {
	h := cryptosha256.New()
	data := make([]byte, 256)
	left := crypto.NewDigest256([crypto.DigestSize256]byte{1})
	right := crypto.NewDigest256([crypto.DigestSize256]byte{2})

	cases := []struct {
		name string
		fn   func()
	}{
		{"ID", func() { _ = h.ID() }},
		{"Algorithm", func() { _ = h.Algorithm() }},
		{"Hash", func() { _ = h.Hash(data) }},
		{"Combine", func() { _ = h.Combine(left, right) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	t.Run("Stream Write/Sum/Reset on a reused stream is zero-alloc", func(t *testing.T) {
		// The output buffer lives on the heap-allocated stream
		// (see stream struct), so Sum reuses already-owned heap
		// memory rather than escaping a stack-local through the
		// hash.Hash interface boundary.
		s := h.NewStream()
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

func BenchmarkHash(b *testing.B) {
	h := cryptosha256.New()
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"128B", 128},
		{"512B", 512},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			for i := range data {
				data[i] = byte(i)
			}
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = h.Hash(data)
			}
		})
	}
}

func BenchmarkCombine(b *testing.B) {
	h := cryptosha256.New()
	left := crypto.NewDigest256([crypto.DigestSize256]byte{1, 2, 3})
	right := crypto.NewDigest256([crypto.DigestSize256]byte{4, 5, 6})
	b.ReportAllocs()
	b.SetBytes(2 * crypto.DigestSize256)
	for b.Loop() {
		_ = h.Combine(left, right)
	}
}

func BenchmarkStream_64K(b *testing.B) {
	h := cryptosha256.New()
	s := h.NewStream()
	data := make([]byte, 65536)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		s.Reset()
		_, _ = s.Write(data)
		_ = s.Sum()
	}
}
