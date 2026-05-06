// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha512_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"go.thesmos.sh/core/crypto"
	cryptosha512 "go.thesmos.sh/core/crypto/sha512"
)

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

	h := cryptosha512.New384()

	t.Run("ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		want := crypto.ID{'s', 'h', 'a', '3', '8', '4', '/', 'v', '1'}
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
		if got := h.Algorithm(); got != crypto.AlgSHA384 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA384)
		}
	})

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		// NIST FIPS 180-4 §C.1 / §C.2 SHA-384 vectors.
		"empty": {
			input: []byte{},
			wantHex: "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da" +
				"274edebfe76f65fbd51ad2f14898b95b",
		},
		"\"abc\"": {
			input: []byte("abc"),
			wantHex: "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed" +
				"8086072ba1e7cc2358baeca134c825a7",
		},
		"two-block message (FIPS 180-4 §C.2)": {
			input: []byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmno" +
				"ijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"),
			wantHex: "09330c33f71147e83d192fc782cd1b4753111b173b3b05d22fa08086e3b0f712" +
				"fcc7c71a557e2db966c3e9fa91746039",
		},
	}
	for name, tc := range cases {
		t.Run("Hash/"+name, func(t *testing.T) {
			t.Parallel()
			got := h.Hash(tc.input)
			want := mustDecodeHex(t, tc.wantHex)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Hash: got %x, want %s", got.Bytes(), tc.wantHex)
			}
			if got.Size() != crypto.DigestSize384 {
				t.Fatalf("digest size: got %d, want %d", got.Size(), crypto.DigestSize384)
			}
		})
	}

	t.Run("Combine of two zero digests is well-defined", func(t *testing.T) {
		t.Parallel()
		zero := crypto.NewDigest384([crypto.DigestSize384]byte{})
		got := h.Combine(zero, zero)
		if got.IsZero() {
			t.Fatal("SHA-384 of 96 zero bytes must be non-zero")
		}
		if got.Size() != crypto.DigestSize384 {
			t.Fatalf("digest size: got %d, want %d", got.Size(), crypto.DigestSize384)
		}
	})

	t.Run("Stream equals Hash over the same bytes", func(t *testing.T) {
		t.Parallel()
		input := []byte("the quick brown fox jumps over the lazy dog")
		want := h.Hash(input)
		s := h.NewStream()
		_, _ = s.Write(input)
		if !s.Sum().Equal(want) {
			t.Fatalf("Stream != Hash: got=%s want=%s", s.Sum(), want)
		}
	})
}

func TestSHA512(t *testing.T) {
	t.Parallel()

	h := cryptosha512.New512()

	t.Run("ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		want := crypto.ID{'s', 'h', 'a', '5', '1', '2', '/', 'v', '1'}
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
		if got := h.Algorithm(); got != crypto.AlgSHA512 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA512)
		}
	})

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		"empty": {
			input: []byte{},
			wantHex: "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce" +
				"47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
		},
		"\"abc\"": {
			input: []byte("abc"),
			wantHex: "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a" +
				"2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
		},
		"two-block message (FIPS 180-4 §C.4)": {
			input: []byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmno" +
				"ijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu"),
			wantHex: "8e959b75dae313da8cf4f72814fc143f8f7779c6eb9f7fa17299aeadb6889018" +
				"501d289e4900f7e4331b99dec4b5433ac7d329eeb6dd26545e96e55b874be909",
		},
	}
	for name, tc := range cases {
		t.Run("Hash/"+name, func(t *testing.T) {
			t.Parallel()
			got := h.Hash(tc.input)
			want := mustDecodeHex(t, tc.wantHex)
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("Hash: got %x, want %s", got.Bytes(), tc.wantHex)
			}
			if got.Size() != crypto.DigestSize512 {
				t.Fatalf("digest size: got %d, want %d", got.Size(), crypto.DigestSize512)
			}
		})
	}

	t.Run("Combine of two zero digests is well-defined", func(t *testing.T) {
		t.Parallel()
		zero := crypto.NewDigest512([crypto.DigestSize512]byte{})
		got := h.Combine(zero, zero)
		if got.IsZero() {
			t.Fatal("SHA-512 of 128 zero bytes must be non-zero")
		}
	})

	t.Run("Stream equals Hash over the same bytes", func(t *testing.T) {
		t.Parallel()
		input := []byte("agentic context payload that streams across many writes")
		want := h.Hash(input)
		s := h.NewStream()
		// Split into two writes to exercise multi-write streaming.
		_, _ = s.Write(input[:20])
		_, _ = s.Write(input[20:])
		if !s.Sum().Equal(want) {
			t.Fatalf("split Stream != Hash: got=%s want=%s", s.Sum(), want)
		}
	})
}

func TestCombinePanicsOnSizeMismatch(t *testing.T) {
	t.Parallel()

	d256 := crypto.NewDigest256([crypto.DigestSize256]byte{})
	d384 := crypto.NewDigest384([crypto.DigestSize384]byte{})
	d512 := crypto.NewDigest512([crypto.DigestSize512]byte{})

	type combineFn func(left, right crypto.Digest) crypto.Digest

	hashers := []struct {
		combine  combineFn
		name     string
		wantSize string
		wantTag  string
		correct  crypto.Digest
		wrong    crypto.Digest
	}{
		{
			combine:  cryptosha512.New384().Combine,
			name:     "SHA-384",
			wantSize: "48",
			wantTag:  "SHA-384",
			correct:  d384,
			wrong:    d512,
		},
		{
			combine:  cryptosha512.New512().Combine,
			name:     "SHA-512",
			wantSize: "64",
			wantTag:  "SHA-512",
			correct:  d512,
			wrong:    d256,
		},
	}
	for _, hh := range hashers {
		cases := []struct {
			name        string
			left, right crypto.Digest
		}{
			{"wrong-left", hh.wrong, hh.correct},
			{"wrong-right", hh.correct, hh.wrong},
			{"both-wrong", hh.wrong, hh.wrong},
		}
		for _, tc := range cases {
			t.Run(hh.name+"/"+tc.name, func(t *testing.T) {
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
					if !strings.Contains(msg, "crypto/sha512") {
						t.Fatalf("panic message %q lacks package tag", msg)
					}
					if !strings.Contains(msg, hh.wantTag) {
						t.Fatalf("panic message %q lacks algorithm tag %q", msg, hh.wantTag)
					}
					if !strings.Contains(msg, hh.wantSize) {
						t.Fatalf("panic message %q lacks expected-size %q", msg, hh.wantSize)
					}
				}()
				_ = hh.combine(tc.left, tc.right)
			})
		}
	}
}

func TestZeroAlloc(t *testing.T) {
	h384 := cryptosha512.New384()
	h512 := cryptosha512.New512()
	data := make([]byte, 256)
	left384 := crypto.NewDigest384([crypto.DigestSize384]byte{1})
	right384 := crypto.NewDigest384([crypto.DigestSize384]byte{2})
	left512 := crypto.NewDigest512([crypto.DigestSize512]byte{1})
	right512 := crypto.NewDigest512([crypto.DigestSize512]byte{2})

	cases := []struct {
		name string
		fn   func()
	}{
		{"SHA384 ID", func() { _ = h384.ID() }},
		{"SHA384 Algorithm", func() { _ = h384.Algorithm() }},
		{"SHA384 Hash", func() { _ = h384.Hash(data) }},
		{"SHA384 Combine", func() { _ = h384.Combine(left384, right384) }},
		{"SHA512 ID", func() { _ = h512.ID() }},
		{"SHA512 Algorithm", func() { _ = h512.Algorithm() }},
		{"SHA512 Hash", func() { _ = h512.Hash(data) }},
		{"SHA512 Combine", func() { _ = h512.Combine(left512, right512) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	t.Run("SHA384 Stream loop is zero-alloc", func(t *testing.T) {
		s := h384.NewStream()
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
		s := h512.NewStream()
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

func BenchmarkSHA384Hash(b *testing.B) {
	h := cryptosha512.New384()
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"512B", 512},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = h.Hash(data)
			}
		})
	}
}

func BenchmarkSHA512Hash(b *testing.B) {
	h := cryptosha512.New512()
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"8B", 8},
		{"64B", 64},
		{"512B", 512},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = h.Hash(data)
			}
		})
	}
}
