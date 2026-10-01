// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"errors"
	"runtime"
	"strconv"
	"testing"

	"go.thesmos.sh/kanon/kanontest"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// benchRuns is the number of calls over which a benchmark averages the
// allocations that it checks.
const benchRuns = 100

// sizedDigests are a digest of each size, whose binary forms the tests
// encode and decode.
var sizedDigests = []struct {
	name string
	in   crypto.Digest
}{
	{"DigestSize256", crypto.NewDigest256(fill256(0x11))},
	{"DigestSize384", crypto.NewDigest384(fill384(0x22))},
	{"DigestSize512", crypto.NewDigest512(fill512(0x33))},
}

func TestDigestSizeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give int
		want int
	}{
		{name: "DigestSize256 is 32 bytes", give: crypto.DigestSize256, want: 32},
		{name: "DigestSize384 is 48 bytes", give: crypto.DigestSize384, want: 48},
		{name: "DigestSize512 is 64 bytes", give: crypto.DigestSize512, want: 64},
		{name: "MaxDigestSize is DigestSize512", give: crypto.MaxDigestSize, want: crypto.DigestSize512},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give, tt.want, "the size constant must keep its value")
		})
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

	tests := []struct {
		name string
		give crypto.Digest
		want bool
	}{
		{
			name: "reports true for the zero value",
			give: crypto.Digest{},
			want: true,
		},
		{
			name: "reports false for a digest of DigestSize256",
			give: crypto.NewDigest256(fill256(0x01)),
			want: false,
		},
		{
			name: "reports false for a digest of DigestSize384",
			give: crypto.NewDigest384(fill384(0x02)),
			want: false,
		},
		{
			name: "reports false for a digest of DigestSize512",
			give: crypto.NewDigest512(fill512(0x03)),
			want: false,
		},
		{
			name: "reports false for a digest of DigestSize256 whose bytes are all zero",
			give: crypto.NewDigest256([crypto.DigestSize256]byte{}),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give.IsZero(), tt.want, "IsZero must report whether the digest is zero")
		})
	}
}

func TestDigestEqual(t *testing.T) {
	t.Parallel()

	a := crypto.NewDigest256(fill256(0x42))
	tests := []struct {
		name string
		give crypto.Digest
		want bool
	}{
		{
			name: "reports true for a digest with the same size and bytes",
			give: crypto.NewDigest256(fill256(0x42)),
			want: true,
		},
		{
			name: "reports false for a digest with other bytes",
			give: crypto.NewDigest256(fill256(0x43)),
			want: false,
		},
		{
			name: "reports false for a digest of another size",
			give: crypto.NewDigest384(fill384(0x42)),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, a.Equal(tt.give), tt.want, "Equal must report whether the digests match")
		})
	}
}

func TestDigestConstantTimeEqual(t *testing.T) {
	t.Parallel()

	t.Run("reports true for identical digests", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		testkit.True(t, a.ConstantTimeEqual(b),
			"identical digests must compare equal under ConstantTimeEqual")
	})

	t.Run("reports false for digests that differ in one byte", func(t *testing.T) {
		t.Parallel()
		a256 := fill256(0x42)
		b256 := fill256(0x42)
		b256[31] ^= 0x01
		a := crypto.NewDigest256(a256)
		b := crypto.NewDigest256(b256)
		testkit.False(t, a.ConstantTimeEqual(b),
			"digests differing in one byte must not compare equal")
	})

	t.Run("reports false for digests of different sizes", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x00))
		b := crypto.NewDigest384(fill384(0x00))
		testkit.False(t, a.ConstantTimeEqual(b),
			"digests of different sizes must not compare equal")
		testkit.False(t, b.ConstantTimeEqual(a),
			"ConstantTimeEqual must be symmetric on size mismatch")
	})

	t.Run("reports true for two zero Digests", func(t *testing.T) {
		t.Parallel()
		var a, b crypto.Digest
		testkit.True(t, a.ConstantTimeEqual(b),
			"zero Digest values must compare equal to themselves")
	})

	t.Run("agrees with Equal on same-size inputs", func(t *testing.T) {
		t.Parallel()
		// For inputs of one size, ConstantTimeEqual must return what
		// Equal returns. The loop compares the two over a small corpus.
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

	t.Run("returns 0 for identical digests", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x42))
		b := crypto.NewDigest256(fill256(0x42))
		testkit.Equal(t, a.Compare(b), 0, "Compare on equal digests must return 0")
	})

	t.Run("returns -1 for smaller bytes", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewDigest256(fill256(0x01))
		b := crypto.NewDigest256(fill256(0x02))
		testkit.Equal(t, a.Compare(b), -1, "Compare a<b must return -1")
		testkit.Equal(t, b.Compare(a), 1, "Compare b>a must return 1")
	})

	t.Run("returns -1 for a shorter digest with a matching prefix", func(t *testing.T) {
		t.Parallel()
		short := crypto.NewDigest256([crypto.DigestSize256]byte{})
		long := crypto.NewDigest384([crypto.DigestSize384]byte{})
		testkit.Equal(t, short.Compare(long), -1,
			"smaller size with matching prefix must compare less")
		testkit.Equal(t, long.Compare(short), 1,
			"larger size with matching prefix must compare greater")
	})

	t.Run("returns 0 for identical 384-bit digests", func(t *testing.T) {
		t.Parallel()
		// Two distinct Digest values with equal bytes and equal sizes
		// run Compare past its comparison of the sizes, where it
		// returns 0.
		a := crypto.NewDigest384(fill384(0x55))
		b := crypto.NewDigest384(fill384(0x55))
		testkit.Equal(t, a.Compare(b), 0,
			"Compare on equal-sized identical bytes must return 0")
	})
}

func TestDigestString(t *testing.T) {
	t.Parallel()

	t.Run("returns an empty string for the zero Digest", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, (crypto.Digest{}).String(), "",
			"zero Digest.String must be empty")
	})

	t.Run("returns the bytes in hex in order", func(t *testing.T) {
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

func TestDigestFromBytes(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		size int
	}{
		{"DigestSize256", crypto.DigestSize256},
		{"DigestSize384", crypto.DigestSize384},
		{"DigestSize512", crypto.DigestSize512},
	}
	for _, tc := range sizes {
		t.Run("returns a digest of "+tc.name+" bytes", func(t *testing.T) {
			t.Parallel()
			b := make([]byte, tc.size)
			for i := range b {
				b[i] = byte(i)
			}
			got, err := crypto.DigestFromBytes(b)
			testkit.NoError(t, err, "DigestFromBytes must accept a valid length")
			testkit.Equal(t, got.Size(), tc.size, "Size must be inferred from len(b)")
			testkit.Equal(t, got.Bytes(), b, "DigestFromBytes must round-trip the input")
		})
	}

	bad := []struct {
		name string
		size int
	}{
		{"an empty slice", 0},
		{"a slice one short of DigestSize256", crypto.DigestSize256 - 1},
		{"a slice of 40 bytes", 40},
		{"a slice one past DigestSize512", crypto.DigestSize512 + 1},
	}
	for _, tc := range bad {
		t.Run("returns ErrDigestSize for "+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := crypto.DigestFromBytes(make([]byte, tc.size))
			testkit.ErrorIs(t, err, crypto.ErrDigestSize, "DigestFromBytes must reject an invalid length")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrDigestSize must classify as Invalid")
			testkit.True(t, got.IsZero(), "DigestFromBytes must return the zero Digest on error")
		})
	}

	t.Run("does not alias the input", func(t *testing.T) {
		t.Parallel()
		b := make([]byte, crypto.DigestSize256)
		got, err := crypto.DigestFromBytes(b)
		testkit.NoError(t, err, "DigestFromBytes must accept a valid length")
		b[0] = 0xFF
		testkit.Equal(t, got.Bytes()[0], byte(0), "mutating the input must not change the Digest")
	})

	t.Run("round-trips NewDigest256", func(t *testing.T) {
		t.Parallel()
		want := crypto.NewDigest256(fill256(0x42))
		got, err := crypto.DigestFromBytes(want.Bytes())
		testkit.NoError(t, err, "DigestFromBytes must accept NewDigest256 output")
		testkit.Equal(t, got, want, "DigestFromBytes must round-trip a constructed Digest")
	})
}

func TestDigestBinaryRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range sizedDigests {
		t.Run("returns a digest of "+tc.name+" from its encoding", func(t *testing.T) {
			t.Parallel()

			encoded, err := tc.in.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must not fail for a sized digest")
			testkit.Equal(t, len(encoded), tc.in.Size(), "encoding carries no length header")

			var got crypto.Digest
			testkit.NoError(t, got.UnmarshalBinary(encoded), "UnmarshalBinary must accept its own output")
			testkit.Equal(t, got, tc.in, "round-trip must preserve bytes and size")
		})
	}
}

func TestDigestAppendBinary(t *testing.T) {
	t.Parallel()

	t.Run("appends to a non-empty destination", func(t *testing.T) {
		t.Parallel()
		d := crypto.NewDigest256(fill256(0x7f))
		got, err := d.AppendBinary([]byte{0xAA})
		testkit.NoError(t, err, "AppendBinary must not fail for a sized digest")
		testkit.Equal(t, len(got), 1+crypto.DigestSize256, "AppendBinary must not overwrite dst")
		testkit.Equal(t, got[0], byte(0xAA), "existing dst bytes must be preserved")
		testkit.Equal(t, got[1:], d.Bytes(), "appended bytes must be the digest's active prefix")
	})

	t.Run("returns the bytes that MarshalBinary returns", func(t *testing.T) {
		t.Parallel()
		d := crypto.NewDigest384(fill384(0x5a))
		appended, err := d.AppendBinary(nil)
		testkit.NoError(t, err, "AppendBinary must not fail")
		marshalled, err := d.MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must not fail")
		testkit.Equal(t, appended, marshalled, "AppendBinary and MarshalBinary must agree")
	})
}

func TestDigestUnmarshalBinary(t *testing.T) {
	t.Parallel()

	t.Run("leaves the digest unchanged for a length that no digest has", func(t *testing.T) {
		t.Parallel()
		want := crypto.NewDigest384(fill384(0x22))
		got := want
		testkit.ErrorIs(t, got.UnmarshalBinary(make([]byte, 40)), crypto.ErrDigestSize,
			"UnmarshalBinary must reject 40 bytes")
		testkit.True(t, got == want, "a rejected decode must leave the digest unchanged")
	})

	t.Run("returns the result of DigestFromBytes for every length up to MaxDigestSize+1", func(t *testing.T) {
		t.Parallel()
		for n := range crypto.MaxDigestSize + 2 {
			data := make([]byte, n)
			for k := range data {
				data[k] = byte(k + 1)
			}
			want, wantErr := crypto.DigestFromBytes(data)
			var got crypto.Digest
			err := got.UnmarshalBinary(data)
			testkit.True(t, errors.Is(err, wantErr),
				"UnmarshalBinary must return the error of DigestFromBytes for "+strconv.Itoa(n)+" bytes")
			testkit.True(t, got == want,
				"UnmarshalBinary must decode the digest of DigestFromBytes for "+strconv.Itoa(n)+" bytes")
		}
	})

	tests := []struct {
		name string
		held crypto.Digest
		give crypto.Digest
	}{
		{
			name: "clears the bytes of a DigestSize512 digest under a DigestSize256 one",
			held: crypto.NewDigest512(fill512(0x33)),
			give: crypto.NewDigest256(fill256(0x11)),
		},
		{
			name: "clears the bytes of a DigestSize512 digest under a DigestSize384 one",
			held: crypto.NewDigest512(fill512(0x33)),
			give: crypto.NewDigest384(fill384(0x22)),
		},
		{
			name: "clears the bytes of a DigestSize384 digest under a DigestSize256 one",
			held: crypto.NewDigest384(fill384(0x22)),
			give: crypto.NewDigest256(fill256(0x11)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.held
			testkit.NoError(t, got.UnmarshalBinary(tt.give.Bytes()), "UnmarshalBinary must accept the encoding")
			testkit.True(t, got == tt.give, "the decoded digest must equal the digest built from the same bytes")
		})
	}
}

func TestDigestZeroHasNoBinaryEncoding(t *testing.T) {
	t.Parallel()

	t.Run("MarshalBinary returns ErrDigestZero", func(t *testing.T) {
		t.Parallel()
		var zero crypto.Digest
		got, err := zero.MarshalBinary()
		testkit.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must not marshal")
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrDigestZero must classify as Invalid")
		testkit.Equal(t, got, []byte(nil), "MarshalBinary must return nil on error")
	})

	t.Run("AppendBinary returns dst unchanged with ErrDigestZero", func(t *testing.T) {
		t.Parallel()
		var zero crypto.Digest
		got, err := zero.AppendBinary([]byte{0xAA})
		testkit.ErrorIs(t, err, crypto.ErrDigestZero, "the zero Digest must not append")
		testkit.Equal(t, got, []byte{0xAA}, "AppendBinary must leave dst unchanged on error")
	})

	t.Run("UnmarshalBinary returns ErrDigestSize for empty input", func(t *testing.T) {
		t.Parallel()
		// The zero Digest has no wire form, so a truncated read does
		// not decode to a digest that the caller never wrote.
		var d crypto.Digest
		testkit.ErrorIs(t, d.UnmarshalBinary(nil), crypto.ErrDigestSize,
			"empty input must be a size error, not the zero Digest")
	})
}

func TestDigestSizeKanon(t *testing.T) {
	t.Parallel()

	for _, tc := range sizedDigests {
		t.Run("returns the length of the encoding of a digest of "+tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := tc.in.AppendBinary(nil)
			testkit.NoError(t, err, "AppendBinary must not fail for a sized digest")
			testkit.Equal(t, tc.in.SizeKanon(), len(encoded), "SizeKanon must equal the length of the encoding")
		})
	}

	t.Run("returns 0 for the zero Digest", func(t *testing.T) {
		t.Parallel()
		var zero crypto.Digest
		testkit.Equal(t, zero.SizeKanon(), 0, "the zero Digest must have no encoding")
	})
}

// TestDigestExactKanon checks the two guarantees that ExactKanon declares
// with kanon's conformance suite. A Digest without the methods of
// kanon.Exact does not compile here.
func TestDigestExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[crypto.Digest](t)
}

func BenchmarkDigestFromBytes(b *testing.B) {
	src := crypto.NewDigest256(fill256(0x7f)).Bytes()
	b.ReportAllocs()
	var sink crypto.Digest
	for b.Loop() {
		sink, _ = crypto.DigestFromBytes(src)
	}
	runtime.KeepAlive(sink)
}

// BenchmarkDigestUnmarshalBinary reports the cost of a decode of 32 bytes
// into a digest, and fails when the decode allocates. The allocation check
// decodes into a digest of its own, so the closure that captures it does
// not change the code of the timed loop.
func BenchmarkDigestUnmarshalBinary(b *testing.B) {
	data := crypto.NewDigest256(fill256(0x7f)).Bytes()

	var probe crypto.Digest
	if allocs := testing.AllocsPerRun(benchRuns, func() { _ = probe.UnmarshalBinary(data) }); allocs != 0 {
		b.Fatalf("UnmarshalBinary allocates %v times per call, want 0", allocs)
	}

	var d crypto.Digest
	b.ReportAllocs()
	for b.Loop() {
		_ = d.UnmarshalBinary(data)
	}
	runtime.KeepAlive(d)
}

func BenchmarkDigestAppendBinary(b *testing.B) {
	d := crypto.NewDigest256(fill256(0x7f))
	dst := make([]byte, 0, crypto.DigestSize256)
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		sink, _ = d.AppendBinary(dst[:0])
	}
	runtime.KeepAlive(sink)
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
