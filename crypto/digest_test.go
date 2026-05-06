// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/core/crypto"
)

func TestDigestSizeConstants(t *testing.T) {
	t.Parallel()

	if crypto.DigestSize256 != 32 {
		t.Fatalf("DigestSize256: got %d, want 32", crypto.DigestSize256)
	}
	if crypto.DigestSize384 != 48 {
		t.Fatalf("DigestSize384: got %d, want 48", crypto.DigestSize384)
	}
	if crypto.DigestSize512 != 64 {
		t.Fatalf("DigestSize512: got %d, want 64", crypto.DigestSize512)
	}
	if crypto.MaxDigestSize != crypto.DigestSize512 {
		t.Fatalf("MaxDigestSize: got %d, want %d", crypto.MaxDigestSize, crypto.DigestSize512)
	}
}

func TestNewDigestConstructors(t *testing.T) {
	t.Parallel()

	t.Run("NewDigest256 produces a 32-byte digest", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize256]byte
		for i := range b {
			b[i] = byte(i)
		}
		d := crypto.NewDigest256(b)
		if got := d.Size(); got != crypto.DigestSize256 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize256)
		}
		if !bytes.Equal(d.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", d.Bytes(), b[:])
		}
	})

	t.Run("NewDigest384 produces a 48-byte digest", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize384]byte
		for i := range b {
			b[i] = byte(i + 1)
		}
		d := crypto.NewDigest384(b)
		if got := d.Size(); got != crypto.DigestSize384 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize384)
		}
		if !bytes.Equal(d.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", d.Bytes(), b[:])
		}
	})

	t.Run("NewDigest512 produces a 64-byte digest", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize512]byte
		for i := range b {
			b[i] = byte(i + 2)
		}
		d := crypto.NewDigest512(b)
		if got := d.Size(); got != crypto.DigestSize512 {
			t.Fatalf("Size: got %d, want %d", got, crypto.DigestSize512)
		}
		if !bytes.Equal(d.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", d.Bytes(), b[:])
		}
	})
}

func TestDigestIsZero(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   crypto.Digest
		want bool
	}{
		"zero value":         {crypto.Digest{}, true},
		"nonzero 256-byte":   {crypto.NewDigest256(fill256(0x01)), false},
		"nonzero 384-byte":   {crypto.NewDigest384(fill384(0x02)), false},
		"nonzero 512-byte":   {crypto.NewDigest512(fill512(0x03)), false},
		"all-zero with size": {crypto.NewDigest256([crypto.DigestSize256]byte{}), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsZero(); got != tc.want {
				t.Fatalf("IsZero: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDigestEqual(t *testing.T) {
	t.Parallel()

	a := crypto.NewDigest256(fill256(0x42))
	b := crypto.NewDigest256(fill256(0x42))
	c := crypto.NewDigest256(fill256(0x43))
	d384 := crypto.NewDigest384(fill384(0x42))

	if !a.Equal(b) {
		t.Fatal("two digests with identical bytes must be equal")
	}
	if a.Equal(c) {
		t.Fatal("two digests with different bytes must not be equal")
	}
	if a.Equal(d384) {
		t.Fatal("digests of different sizes must not be equal")
	}
}

func TestDigestConstantTimeEqual(t *testing.T) {
	t.Parallel()

	t.Run("identical digests compare equal", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		if !a.ConstantTimeEqual(b) {
			t.Fatal("identical 256-bit digests must compare equal")
		}
	})

	t.Run("differing single byte returns false", func(t *testing.T) {
		t.Parallel()
		a256 := fill256(0x42)
		b256 := fill256(0x42)
		b256[31] ^= 0x01
		a := crypto.NewDigest256(a256)
		b := crypto.NewDigest256(b256)
		if a.ConstantTimeEqual(b) {
			t.Fatal("digests differing in one byte must not compare equal")
		}
	})

	t.Run("different sizes return false", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x00))
		b := crypto.NewDigest384(fill384(0x00))
		if a.ConstantTimeEqual(b) {
			t.Fatal("digests of different sizes must not compare equal")
		}
		if b.ConstantTimeEqual(a) {
			t.Fatal("ConstantTimeEqual must be symmetric on size mismatch")
		}
	})

	t.Run("both zero-Digest values compare equal", func(t *testing.T) {
		t.Parallel()
		var a, b crypto.Digest
		if !a.ConstantTimeEqual(b) {
			t.Fatal("zero Digest values must compare equal to themselves")
		}
	})

	t.Run("agrees with Equal on same-size inputs", func(t *testing.T) {
		t.Parallel()
		// ConstantTimeEqual must be a strict drop-in for Equal
		// when both inputs are the same size. Differential check
		// across a small corpus.
		corpus := [][2][crypto.DigestSize256]byte{
			{fill256(0x00), fill256(0x00)},
			{fill256(0x01), fill256(0x02)},
			{fill256(0xff), fill256(0xff)},
		}
		for _, pair := range corpus {
			a := crypto.NewDigest256(pair[0])
			b := crypto.NewDigest256(pair[1])
			if a.Equal(b) != a.ConstantTimeEqual(b) {
				t.Fatalf("disagreement: Equal=%v ConstantTimeEqual=%v",
					a.Equal(b), a.ConstantTimeEqual(b))
			}
		}
	})
}

func TestDigestCompare(t *testing.T) {
	t.Parallel()

	t.Run("identical digests compare equal", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		if got := a.Compare(b); got != 0 {
			t.Fatalf("Compare on equal: got %d, want 0", got)
		}
	})

	t.Run("smaller bytes compare less", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x01))
		b := crypto.NewDigest256(fill256(0x02))
		if got := a.Compare(b); got != -1 {
			t.Fatalf("Compare a<b: got %d, want -1", got)
		}
		if got := b.Compare(a); got != 1 {
			t.Fatalf("Compare b>a: got %d, want 1", got)
		}
	})

	t.Run("smaller size compares less when prefixes match", func(t *testing.T) {
		t.Parallel()
		short := crypto.NewDigest256([crypto.DigestSize256]byte{})
		long := crypto.NewDigest384([crypto.DigestSize384]byte{})
		if got := short.Compare(long); got != -1 {
			t.Fatalf("Compare short<long: got %d, want -1", got)
		}
		if got := long.Compare(short); got != 1 {
			t.Fatalf("Compare long>short: got %d, want 1", got)
		}
	})

	t.Run("equal sizes equal bytes returns 0", func(t *testing.T) {
		t.Parallel()
		// Two distinct Digest values that compare byte-equal AND
		// size-equal — exercises the size-tie-breaker fall-through
		// (returns 0 rather than -1 / +1).
		a := crypto.NewDigest384(fill384(0x55))
		b := crypto.NewDigest384(fill384(0x55))
		if got := a.Compare(b); got != 0 {
			t.Fatalf("Compare on equal-sized identical bytes: got %d, want 0", got)
		}
	})
}

func TestDigestString(t *testing.T) {
	t.Parallel()

	t.Run("zero digest hex-encodes to empty", func(t *testing.T) {
		t.Parallel()
		if got := (crypto.Digest{}).String(); got != "" {
			t.Fatalf("String: got %q, want \"\"", got)
		}
	})

	t.Run("specific 256-bit digest hex-encodes in order", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize256]byte
		b[0], b[1], b[2] = 0x01, 0x23, 0x45
		b[29], b[30], b[31] = 0xab, 0xcd, 0xef
		got := crypto.NewDigest256(b).String()
		const middleZeros = "00000000000000000000000000000000000000000000000000000000"
		want := "012345" + middleZeros[:52] + "abcdef"
		if got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})
}

func TestZeroAlloc(t *testing.T) {
	d := crypto.NewDigest256(fill256(0x42))
	other := crypto.NewDigest256(fill256(0x43))
	var raw256 [crypto.DigestSize256]byte
	var raw384 [crypto.DigestSize384]byte
	var raw512 [crypto.DigestSize512]byte

	cases := []struct {
		name string
		fn   func()
	}{
		{"NewDigest256", func() { _ = crypto.NewDigest256(raw256) }},
		{"NewDigest384", func() { _ = crypto.NewDigest384(raw384) }},
		{"NewDigest512", func() { _ = crypto.NewDigest512(raw512) }},
		{"IsZero", func() { _ = d.IsZero() }},
		{"Equal", func() { _ = d.Equal(other) }},
		{"ConstantTimeEqual", func() { _ = d.ConstantTimeEqual(other) }},
		{"Compare", func() { _ = d.Compare(other) }},
		{"Size", func() { _ = d.Size() }},
		{"Bytes", func() { _ = d.Bytes() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func fill256(b byte) [crypto.DigestSize256]byte {
	var out [crypto.DigestSize256]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func fill384(b byte) [crypto.DigestSize384]byte {
	var out [crypto.DigestSize384]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func fill512(b byte) [crypto.DigestSize512]byte {
	var out [crypto.DigestSize512]byte
	for i := range out {
		out[i] = b
	}
	return out
}
