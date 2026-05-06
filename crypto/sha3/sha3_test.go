// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sha3_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"go.thesmos.sh/core/crypto"
	cryptosha3 "go.thesmos.sh/core/crypto/sha3"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex fixture: %v", err)
	}
	return b
}

// NIST FIPS 202 / NIST CAVP test vectors for the empty input and "abc".

// Test-fixture names reused across the per-algorithm tables.
const (
	emptyName = "empty"
	abcName   = "\"abc\""
)

func TestSHA3_256(t *testing.T) {
	t.Parallel()

	h := cryptosha3.New256()

	t.Run("ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		want := crypto.ID{'s', 'h', 'a', '3', '-', '2', '5', '6', '/', 'v', '1'}
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
		if got := h.Algorithm(); got != crypto.AlgSHA3_256 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA3_256)
		}
	})

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input:   []byte{},
			wantHex: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
		},
		abcName: {
			input:   []byte("abc"),
			wantHex: "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532",
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
			if got.Size() != crypto.DigestSize256 {
				t.Fatalf("digest size: got %d, want %d", got.Size(), crypto.DigestSize256)
			}
		})
	}

	t.Run("Combine of two zero digests is well-defined", func(t *testing.T) {
		t.Parallel()
		zero := crypto.NewDigest256([crypto.DigestSize256]byte{})
		if h.Combine(zero, zero).IsZero() {
			t.Fatal("SHA3-256 of 64 zero bytes must be non-zero")
		}
	})

	t.Run("Stream equals Hash over the same bytes", func(t *testing.T) {
		t.Parallel()
		input := []byte("the quick brown fox jumps over the lazy dog")
		want := h.Hash(input)
		s := h.NewStream()
		_, _ = s.Write(input[:10])
		_, _ = s.Write(input[10:])
		if !s.Sum().Equal(want) {
			t.Fatalf("Stream != Hash: got=%s want=%s", s.Sum(), want)
		}
	})
}

func TestSHA3_384(t *testing.T) {
	t.Parallel()

	h := cryptosha3.New384()

	t.Run("ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		want := crypto.ID{'s', 'h', 'a', '3', '-', '3', '8', '4', '/', 'v', '1'}
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
		if got := h.Algorithm(); got != crypto.AlgSHA3_384 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA3_384)
		}
	})

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input: []byte{},
			wantHex: "0c63a75b845e4f7d01107d852e4c2485c51a50aaaa94fc61995e71bbee983a2a" +
				"c3713831264adb47fb6bd1e058d5f004",
		},
		abcName: {
			input: []byte("abc"),
			wantHex: "ec01498288516fc926459f58e2c6ad8df9b473cb0fc08c2596da7cf0e49be4b2" +
				"98d88cea927ac7f539f1edf228376d25",
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
		})
	}
}

func TestSHA3_512(t *testing.T) {
	t.Parallel()

	h := cryptosha3.New512()

	t.Run("ID and Algorithm", func(t *testing.T) {
		t.Parallel()
		want := crypto.ID{'s', 'h', 'a', '3', '-', '5', '1', '2', '/', 'v', '1'}
		if got := h.ID(); got != want {
			t.Fatalf("ID: got %v, want %v", got, want)
		}
		if got := h.Algorithm(); got != crypto.AlgSHA3_512 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgSHA3_512)
		}
	})

	cases := map[string]struct {
		wantHex string
		input   []byte
	}{
		emptyName: {
			input: []byte{},
			wantHex: "a69f73cca23a9ac5c8b567dc185a756e97c982164fe25859e0d1dcc1475c80a6" +
				"15b2123af1f5f94c11e3e9402c3ac558f500199d95b6d3e301758586281dcd26",
		},
		abcName: {
			input: []byte("abc"),
			wantHex: "b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e" +
				"10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0",
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
		})
	}
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
			combine:  cryptosha3.New256().Combine,
			name:     "SHA3-256",
			wantSize: "32",
			wantTag:  "SHA3-256",
			correct:  d256,
			wrong:    d512,
		},
		{
			combine:  cryptosha3.New384().Combine,
			name:     "SHA3-384",
			wantSize: "48",
			wantTag:  "SHA3-384",
			correct:  d384,
			wrong:    d256,
		},
		{
			combine:  cryptosha3.New512().Combine,
			name:     "SHA3-512",
			wantSize: "64",
			wantTag:  "SHA3-512",
			correct:  d512,
			wrong:    d384,
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
					if !strings.Contains(msg, "crypto/sha3") {
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
	h256 := cryptosha3.New256()
	h384 := cryptosha3.New384()
	h512 := cryptosha3.New512()
	data := make([]byte, 256)
	l256 := crypto.NewDigest256([crypto.DigestSize256]byte{1})
	r256 := crypto.NewDigest256([crypto.DigestSize256]byte{2})
	l384 := crypto.NewDigest384([crypto.DigestSize384]byte{1})
	r384 := crypto.NewDigest384([crypto.DigestSize384]byte{2})
	l512 := crypto.NewDigest512([crypto.DigestSize512]byte{1})
	r512 := crypto.NewDigest512([crypto.DigestSize512]byte{2})

	cases := []struct {
		name string
		fn   func()
	}{
		{"SHA3-256 Hash", func() { _ = h256.Hash(data) }},
		{"SHA3-256 Combine", func() { _ = h256.Combine(l256, r256) }},
		{"SHA3-384 Hash", func() { _ = h384.Hash(data) }},
		{"SHA3-384 Combine", func() { _ = h384.Combine(l384, r384) }},
		{"SHA3-512 Hash", func() { _ = h512.Hash(data) }},
		{"SHA3-512 Combine", func() { _ = h512.Combine(l512, r512) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}

	t.Run("SHA3-256 Stream loop is zero-alloc", func(t *testing.T) {
		s := h256.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("SHA3-256 Stream: %v allocs/op, want 0", got)
		}
	})

	t.Run("SHA3-384 Stream loop is zero-alloc", func(t *testing.T) {
		s := h384.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("SHA3-384 Stream: %v allocs/op, want 0", got)
		}
	})

	t.Run("SHA3-512 Stream loop is zero-alloc", func(t *testing.T) {
		s := h512.NewStream()
		buf := make([]byte, 256)
		fn := func() {
			s.Reset()
			_, _ = s.Write(buf)
			_ = s.Sum()
		}
		if got := testing.AllocsPerRun(100, fn); got != 0 {
			t.Fatalf("SHA3-512 Stream: %v allocs/op, want 0", got)
		}
	})
}

func TestStreams(t *testing.T) {
	t.Parallel()

	t.Run("SHA3-384 Stream equals Hash", func(t *testing.T) {
		t.Parallel()
		h := cryptosha3.New384()
		input := []byte("agentic context bytes for SHA3-384 streaming")
		want := h.Hash(input)
		s := h.NewStream()
		_, _ = s.Write(input[:15])
		_, _ = s.Write(input[15:])
		if !s.Sum().Equal(want) {
			t.Fatalf("SHA3-384 Stream != Hash: got=%s want=%s", s.Sum(), want)
		}
	})

	t.Run("SHA3-512 Stream equals Hash and Reset reuses", func(t *testing.T) {
		t.Parallel()
		h := cryptosha3.New512()
		s := h.NewStream()
		_, _ = s.Write([]byte("first"))
		first := s.Sum()
		s.Reset()
		_, _ = s.Write([]byte("second"))
		second := s.Sum()
		if first.Equal(second) {
			t.Fatal("Reset failed: distinct inputs produced identical digests")
		}
		if !second.Equal(h.Hash([]byte("second"))) {
			t.Fatal("post-Reset Stream != Hash on the same bytes")
		}
	})
}

func BenchmarkSHA3_256Hash(b *testing.B) {
	h := cryptosha3.New256()
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

// largeBenchSizes are the input sizes the wider SHA-3 hashers are
// benchmarked at. SHA3-384 and SHA3-512 share the same set so the
// numbers compare side-by-side.
var largeBenchSizes = []struct {
	name string
	n    int
}{
	{"512B", 512},
	{"64K", 65536},
}

func BenchmarkSHA3_384Hash(b *testing.B) {
	h := cryptosha3.New384()
	for _, sz := range largeBenchSizes {
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

func BenchmarkSHA3_512Hash(b *testing.B) {
	h := cryptosha3.New512()
	for _, sz := range largeBenchSizes {
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
