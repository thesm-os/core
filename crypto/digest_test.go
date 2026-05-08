// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

func TestDigestSizeConstants(t *testing.T) {
	t.Parallel()

	testkit.Equal(t, crypto.DigestSize256, 32, "DigestSize256 must equal 32")
	testkit.Equal(t, crypto.DigestSize384, 48, "DigestSize384 must equal 48")
	testkit.Equal(t, crypto.DigestSize512, 64, "DigestSize512 must equal 64")
	testkit.Equal(t, crypto.MaxDigestSize, crypto.DigestSize512,
		"MaxDigestSize must equal DigestSize512")
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
		testkit.Equal(t, d.Size(), crypto.DigestSize256, "NewDigest256.Size must equal DigestSize256")
		testkit.Equal(t, d.Bytes(), b[:], "NewDigest256.Bytes must round-trip the input")
	})

	t.Run("NewDigest384 produces a 48-byte digest", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize384]byte
		for i := range b {
			b[i] = byte(i + 1)
		}
		d := crypto.NewDigest384(b)
		testkit.Equal(t, d.Size(), crypto.DigestSize384, "NewDigest384.Size must equal DigestSize384")
		testkit.Equal(t, d.Bytes(), b[:], "NewDigest384.Bytes must round-trip the input")
	})

	t.Run("NewDigest512 produces a 64-byte digest", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize512]byte
		for i := range b {
			b[i] = byte(i + 2)
		}
		d := crypto.NewDigest512(b)
		testkit.Equal(t, d.Size(), crypto.DigestSize512, "NewDigest512.Size must equal DigestSize512")
		testkit.Equal(t, d.Bytes(), b[:], "NewDigest512.Bytes must round-trip the input")
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
			testkit.Equal(t, tc.in.IsZero(), tc.want, "IsZero must match expected")
		})
	}
}

func TestDigestEqual(t *testing.T) {
	t.Parallel()

	a := crypto.NewDigest256(fill256(0x42))
	b := crypto.NewDigest256(fill256(0x42))
	c := crypto.NewDigest256(fill256(0x43))
	d384 := crypto.NewDigest384(fill384(0x42))

	testkit.True(t, a.Equal(b), "identical digests must compare Equal")
	testkit.False(t, a.Equal(c), "differing digests must not compare Equal")
	testkit.False(t, a.Equal(d384), "digests of different sizes must not compare Equal")
}

func TestDigestConstantTimeEqual(t *testing.T) {
	t.Parallel()

	t.Run("identical digests compare equal", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		testkit.True(t, a.ConstantTimeEqual(b),
			"identical digests must compare equal under ConstantTimeEqual")
	})

	t.Run("differing single byte returns false", func(t *testing.T) {
		t.Parallel()
		a256 := fill256(0x42)
		b256 := fill256(0x42)
		b256[31] ^= 0x01
		a := crypto.NewDigest256(a256)
		b := crypto.NewDigest256(b256)
		testkit.False(t, a.ConstantTimeEqual(b),
			"digests differing in one byte must not compare equal")
	})

	t.Run("different sizes return false", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x00))
		b := crypto.NewDigest384(fill384(0x00))
		testkit.False(t, a.ConstantTimeEqual(b),
			"digests of different sizes must not compare equal")
		testkit.False(t, b.ConstantTimeEqual(a),
			"ConstantTimeEqual must be symmetric on size mismatch")
	})

	t.Run("both zero-Digest values compare equal", func(t *testing.T) {
		t.Parallel()
		var a, b crypto.Digest
		testkit.True(t, a.ConstantTimeEqual(b),
			"zero Digest values must compare equal to themselves")
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
			testkit.Equal(t, a.ConstantTimeEqual(b), a.Equal(b),
				"ConstantTimeEqual must agree with Equal on same-size inputs")
		}
	})
}

func TestDigestCompare(t *testing.T) {
	t.Parallel()

	t.Run("identical digests compare equal", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		testkit.Equal(t, a.Compare(b), 0, "Compare on equal digests must return 0")
	})

	t.Run("smaller bytes compare less", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x01))
		b := crypto.NewDigest256(fill256(0x02))
		testkit.Equal(t, a.Compare(b), -1, "Compare a<b must return -1")
		testkit.Equal(t, b.Compare(a), 1, "Compare b>a must return 1")
	})

	t.Run("smaller size compares less when prefixes match", func(t *testing.T) {
		t.Parallel()
		short := crypto.NewDigest256([crypto.DigestSize256]byte{})
		long := crypto.NewDigest384([crypto.DigestSize384]byte{})
		testkit.Equal(t, short.Compare(long), -1,
			"smaller size with matching prefix must compare less")
		testkit.Equal(t, long.Compare(short), 1,
			"larger size with matching prefix must compare greater")
	})

	t.Run("equal sizes equal bytes returns 0", func(t *testing.T) {
		t.Parallel()
		// Two distinct Digest values that compare byte-equal AND
		// size-equal — exercises the size-tie-breaker fall-through
		// (returns 0 rather than -1 / +1).
		a := crypto.NewDigest384(fill384(0x55))
		b := crypto.NewDigest384(fill384(0x55))
		testkit.Equal(t, a.Compare(b), 0,
			"Compare on equal-sized identical bytes must return 0")
	})
}

func TestDigestString(t *testing.T) {
	t.Parallel()

	t.Run("zero digest hex-encodes to empty", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, (crypto.Digest{}).String(), "",
			"zero Digest.String must be empty")
	})

	t.Run("specific 256-bit digest hex-encodes in order", func(t *testing.T) {
		t.Parallel()
		var b [crypto.DigestSize256]byte
		b[0], b[1], b[2] = 0x01, 0x23, 0x45
		b[29], b[30], b[31] = 0xab, 0xcd, 0xef
		const middleZeros = "00000000000000000000000000000000000000000000000000000000"
		want := "012345" + middleZeros[:52] + "abcdef"
		testkit.Equal(t, crypto.NewDigest256(b).String(), want,
			"String must hex-encode bytes in order")
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
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkEqual(b *testing.B) {
	d := crypto.NewDigest256(fill256(0x42))
	other := crypto.NewDigest256(fill256(0x43))
	b.ReportAllocs()
	for b.Loop() {
		_ = d.Equal(other)
	}
}

func BenchmarkConstantTimeEqual(b *testing.B) {
	d := crypto.NewDigest256(fill256(0x42))
	other := crypto.NewDigest256(fill256(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = d.ConstantTimeEqual(other)
	}
}

func BenchmarkCompare(b *testing.B) {
	d := crypto.NewDigest256(fill256(0x42))
	other := crypto.NewDigest256(fill256(0x43))
	b.ReportAllocs()
	for b.Loop() {
		_ = d.Compare(other)
	}
}

func BenchmarkString(b *testing.B) {
	d := crypto.NewDigest256(fill256(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = d.String()
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
