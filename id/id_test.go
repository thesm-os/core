// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
)

func TestSizeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give int
		want int
	}{
		{name: "Size128 is 16 bytes", give: id.Size128, want: 16},
		{name: "Size160 is 20 bytes", give: id.Size160, want: 20},
		{name: "Size256 is 32 bytes", give: id.Size256, want: 32},
		{name: "MaxSize is Size256", give: id.MaxSize, want: id.Size256},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give, tt.want, "the size constant must keep its value")
		})
	}
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

	tests := []struct {
		name string
		give id.ID
		want bool
	}{
		{
			name: "reports true for the zero value",
			give: id.ID{},
			want: true,
		},
		{
			name: "reports true for Zero",
			give: id.Zero,
			want: true,
		},
		{
			name: "reports false for an ID of Size128",
			give: id.New128([id.Size128]byte{1}),
			want: false,
		},
		{
			name: "reports false for an ID of Size160",
			give: id.New160([id.Size160]byte{2}),
			want: false,
		},
		{
			name: "reports false for an ID of Size256",
			give: id.New256([id.Size256]byte{3}),
			want: false,
		},
		{
			name: "reports false for an ID of Size128 whose bytes are all zero",
			give: id.New128([id.Size128]byte{}),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give.IsZero(), tt.want, "IsZero must report whether the ID is Zero")
		})
	}
}

func TestIDEqual(t *testing.T) {
	t.Parallel()

	a := id.New128(fill128(0x42))
	tests := []struct {
		name string
		give id.ID
		want bool
	}{
		{
			name: "reports true for an ID with the same size and bytes",
			give: id.New128(fill128(0x42)),
			want: true,
		},
		{
			name: "reports false for an ID with other bytes",
			give: id.New128(fill128(0x43)),
			want: false,
		},
		{
			name: "reports false for an ID of another size",
			give: id.New160(fill160(0x42)),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, a.Equal(tt.give), tt.want, "Equal must report whether the IDs match")
		})
	}
}

func TestIDCompare(t *testing.T) {
	t.Parallel()

	t.Run("returns 0 for identical IDs", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x42))
		b := id.New128(fill128(0x42))
		testkit.Equal(t, a.Compare(b), 0, "Compare on equal IDs must return 0")
	})

	t.Run("returns -1 for smaller bytes", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x01))
		b := id.New128(fill128(0x02))
		testkit.Equal(t, a.Compare(b), -1, "Compare a<b must return -1")
		testkit.Equal(t, b.Compare(a), 1, "Compare b>a must return 1")
	})

	t.Run("returns -1 for a shorter ID with a matching prefix", func(t *testing.T) {
		t.Parallel()
		short := id.New128([id.Size128]byte{})
		long := id.New160([id.Size160]byte{})
		testkit.Equal(t, short.Compare(long), -1, "smaller size with matching prefix must compare less")
		testkit.Equal(t, long.Compare(short), 1, "larger size with matching prefix must compare greater")
	})
}

func TestIDString(t *testing.T) {
	t.Parallel()

	t.Run("returns id: for Zero", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, id.Zero.String(), "id:", "Zero must encode to bare 'id:' prefix")
	})

	t.Run("returns the hex of a 128-bit ID after id:", func(t *testing.T) {
		t.Parallel()
		var b [id.Size128]byte
		b[0], b[1], b[2] = 0x01, 0x23, 0x45
		b[13], b[14], b[15] = 0xab, 0xcd, 0xef
		const middleZeros = "00000000000000000000000000000000"
		want := "id:012345" + middleZeros[:20] + "abcdef"
		testkit.Equal(t, id.New128(b).String(), want, "String must encode hex with 'id:' prefix")
	})

	t.Run("starts with the prefix id:", func(t *testing.T) {
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

// TestIDZeroAlloc cannot run in parallel. testing.AllocsPerRun
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

// BenchmarkIsZero exercises [ID.IsZero] on the zero ID and on an ID
// of each supported width.
func BenchmarkIsZero(b *testing.B) {
	cases := []struct {
		name string
		a    id.ID
	}{
		{"zero", id.Zero},
		{"128", id.New128(fill128(0x42))},
		{"160", id.New160(fill160(0x42))},
		{"256", id.New256(fill256(0x42))},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = tc.a.IsZero()
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

func TestFromBytes(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		size int
	}{
		{"Size128", id.Size128},
		{"Size160", id.Size160},
		{"Size256", id.Size256},
	}
	for _, tc := range sizes {
		t.Run("returns an ID of "+tc.name+" bytes", func(t *testing.T) {
			t.Parallel()
			b := make([]byte, tc.size)
			for i := range b {
				b[i] = byte(i)
			}
			got, err := id.FromBytes(b)
			testkit.NoError(t, err, "FromBytes must accept a valid length")
			testkit.Equal(t, got.Size(), tc.size, "FromBytes must infer Size from len(b)")
			testkit.Equal(t, got.Bytes(), b, "FromBytes must round-trip the input")
		})
	}

	bad := []struct {
		name string
		size int
	}{
		{"an empty slice", 0},
		{"a slice one short of Size128", id.Size128 - 1},
		{"a slice of 18 bytes", 18},
		{"a slice one past Size256", id.Size256 + 1},
	}
	for _, tc := range bad {
		t.Run("returns ErrSize for "+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := id.FromBytes(make([]byte, tc.size))
			testkit.ErrorIs(t, err, id.ErrSize, "FromBytes must reject an invalid length")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrSize must classify as Invalid")
			testkit.True(t, got.IsZero(), "FromBytes must return Zero on error")
		})
	}

	t.Run("does not alias the input", func(t *testing.T) {
		t.Parallel()
		b := make([]byte, id.Size128)
		got, err := id.FromBytes(b)
		testkit.NoError(t, err, "FromBytes must accept a valid length")
		b[0] = 0xFF
		testkit.Equal(t, got.Bytes()[0], byte(0), "mutating the input must not change the ID")
	})

	t.Run("round-trips New128", func(t *testing.T) {
		t.Parallel()
		want := id.New128(fill128(0x42))
		got, err := id.FromBytes(want.Bytes())
		testkit.NoError(t, err, "FromBytes must accept New128 output")
		testkit.Equal(t, got, want, "FromBytes must round-trip a constructed ID")
	})
}

func BenchmarkFromBytes(b *testing.B) {
	src := id.New256(fill256(0x7f)).Bytes()
	b.ReportAllocs()
	var sink id.ID
	for b.Loop() {
		sink, _ = id.FromBytes(src)
	}
	runtime.KeepAlive(sink)
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
