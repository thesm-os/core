// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/id"
)

func TestSizeConstants(t *testing.T) {
	t.Parallel()

	testkit.Equal(t, id.Size128, 16, "Size128 must equal 16 bytes")
	testkit.Equal(t, id.Size160, 20, "Size160 must equal 20 bytes")
	testkit.Equal(t, id.Size256, 32, "Size256 must equal 32 bytes")
	testkit.Equal(t, id.MaxSize, id.Size256, "MaxSize must equal Size256")
}

func TestNewConstructors(t *testing.T) {
	t.Parallel()

	t.Run("New128 produces a 16-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size128]byte
		for i := range b {
			b[i] = byte(i)
		}
		got := id.New128(b)
		testkit.Equal(t, got.Size(), id.Size128, "New128.Size must equal Size128")
		testkit.Equal(t, got.Bytes(), b[:], "New128.Bytes must round-trip the input")
	})

	t.Run("New160 produces a 20-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size160]byte
		for i := range b {
			b[i] = byte(i + 1)
		}
		got := id.New160(b)
		testkit.Equal(t, got.Size(), id.Size160, "New160.Size must equal Size160")
		testkit.Equal(t, got.Bytes(), b[:], "New160.Bytes must round-trip the input")
	})

	t.Run("New256 produces a 32-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size256]byte
		for i := range b {
			b[i] = byte(i + 2)
		}
		got := id.New256(b)
		testkit.Equal(t, got.Size(), id.Size256, "New256.Size must equal Size256")
		testkit.Equal(t, got.Bytes(), b[:], "New256.Bytes must round-trip the input")
	})
}

func TestIDIsZero(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   id.ID
		want bool
	}{
		"zero value":          {id.ID{}, true},
		"Zero sentinel":       {id.Zero, true},
		"size-128 with bytes": {id.New128([id.Size128]byte{1}), false},
		"size-160 with bytes": {id.New160([id.Size160]byte{2}), false},
		"size-256 with bytes": {id.New256([id.Size256]byte{3}), false},
		"all-zero size-128":   {id.New128([id.Size128]byte{}), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.in.IsZero(), tc.want, "IsZero must match expected")
		})
	}
}

func TestIDEqual(t *testing.T) {
	t.Parallel()

	a := id.New128(fill128(0x42))
	b := id.New128(fill128(0x42))
	c := id.New128(fill128(0x43))
	d160 := id.New160(fill160(0x42))

	testkit.True(t, a.Equal(b), "identical IDs must compare Equal")
	testkit.False(t, a.Equal(c), "differing IDs must not compare Equal")
	testkit.False(t, a.Equal(d160), "IDs of different sizes must not compare Equal")
}

func TestIDCompare(t *testing.T) {
	t.Parallel()

	t.Run("identical IDs compare equal", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x42))
		b := id.New128(fill128(0x42))
		testkit.Equal(t, a.Compare(b), 0, "Compare on equal IDs must return 0")
	})

	t.Run("smaller bytes compare less", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x01))
		b := id.New128(fill128(0x02))
		testkit.Equal(t, a.Compare(b), -1, "Compare a<b must return -1")
		testkit.Equal(t, b.Compare(a), 1, "Compare b>a must return 1")
	})

	t.Run("smaller size compares less when prefixes match", func(t *testing.T) {
		t.Parallel()
		short := id.New128([id.Size128]byte{})
		long := id.New160([id.Size160]byte{})
		testkit.Equal(t, short.Compare(long), -1, "smaller size with matching prefix must compare less")
		testkit.Equal(t, long.Compare(short), 1, "larger size with matching prefix must compare greater")
	})
}

func TestIDString(t *testing.T) {
	t.Parallel()

	t.Run("Zero encodes to bare 'id:' prefix", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, id.Zero.String(), "id:", "Zero must encode to bare 'id:' prefix")
	})

	t.Run("128-bit ID encodes as id:<32-hex-chars>", func(t *testing.T) {
		t.Parallel()
		var b [id.Size128]byte
		b[0], b[1], b[2] = 0x01, 0x23, 0x45
		b[13], b[14], b[15] = 0xab, 0xcd, 0xef
		const middleZeros = "00000000000000000000000000000000"
		want := "id:012345" + middleZeros[:20] + "abcdef"
		testkit.Equal(t, id.New128(b).String(), want, "String must encode hex with 'id:' prefix")
	})

	t.Run("prefix is visually distinct from algorithm encodings", func(t *testing.T) {
		t.Parallel()
		// Canonical algorithm encodings start with alphanumeric
		// characters: ULID Crockford base32 ("0..9A-Z"), UUIDv4
		// hyphenated hex, KSUID base62. A diagnostic id.String()
		// starts with "id:", which doesn't match any of those.
		s := id.New128([id.Size128]byte{0x42}).String()
		testkit.True(t, len(s) >= 3 && s[:3] == "id:",
			"String must start with 'id:' prefix")
	})
}

// TestIDZeroAlloc cannot run in parallel — testing.AllocsPerRun
// panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestIDZeroAlloc(t *testing.T) {
	a := id.New128(fill128(0x42))
	other := id.New128(fill128(0x43))
	var raw128 [id.Size128]byte
	var raw160 [id.Size160]byte
	var raw256 [id.Size256]byte

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _ = id.New128(raw128) }, "New128"},
		{func() { _ = id.New160(raw160) }, "New160"},
		{func() { _ = id.New256(raw256) }, "New256"},
		{func() { _ = a.IsZero() }, "IsZero"},
		{func() { _ = a.Equal(other) }, "Equal"},
		{func() { _ = a.Compare(other) }, "Compare"},
		{func() { _ = a.Size() }, "Size"},
		{func() { _ = a.Bytes() }, "Bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

// BenchmarkEqual exercises [ID.Equal] across every supported
// width. Sub-benches: 128 (ULID, UUIDv4), 160 (KSUID), 256
// (cryptographic wide-IDs).
func BenchmarkEqual(b *testing.B) {
	cases := []struct {
		name string
		a, c id.ID
	}{
		{"128", id.New128(fill128(0x42)), id.New128(fill128(0x43))},
		{"160", id.New160(fill160(0x42)), id.New160(fill160(0x43))},
		{"256", id.New256(fill256(0x42)), id.New256(fill256(0x43))},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = tc.a.Equal(tc.c)
			}
		})
	}
}

// BenchmarkCompare exercises [ID.Compare] across every
// supported width.
func BenchmarkCompare(b *testing.B) {
	cases := []struct {
		name string
		a, c id.ID
	}{
		{"128", id.New128(fill128(0x42)), id.New128(fill128(0x43))},
		{"160", id.New160(fill160(0x42)), id.New160(fill160(0x43))},
		{"256", id.New256(fill256(0x42)), id.New256(fill256(0x43))},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = tc.a.Compare(tc.c)
			}
		})
	}
}

func BenchmarkBytes(b *testing.B) {
	a := id.New128(fill128(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Bytes()
	}
}

func BenchmarkString(b *testing.B) {
	a := id.New128(fill128(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.String()
	}
}

func fill128(b byte) [id.Size128]byte {
	var out [id.Size128]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func fill160(b byte) [id.Size160]byte {
	var out [id.Size160]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func fill256(b byte) [id.Size256]byte {
	var out [id.Size256]byte
	for i := range out {
		out[i] = b
	}
	return out
}
