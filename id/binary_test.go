// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"encoding"
	"errors"
	"runtime"
	"strconv"
	"testing"

	"go.thesmos.sh/kanon/kanontest"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
)

// benchRuns is the number of calls over which a benchmark averages the
// allocations that it checks.
const benchRuns = 100

// The encoding interfaces ID satisfies. A missing method is a build
// failure.
var (
	_ encoding.BinaryAppender    = id.Zero
	_ encoding.BinaryMarshaler   = id.Zero
	_ encoding.BinaryUnmarshaler = (*id.ID)(nil)
)

// shapes are an ID of every size, with the bytes the encoding must
// contain. The bytes are the recorded form of the frozen layout.
var shapes = []struct {
	name string
	id   id.ID
	want []byte
}{
	{"zero", id.Zero, []byte{}},
	{"Size128", id.New128(fill128(0x11)), fill128Slice(0x11)},
	{"Size160", id.New160(fill160(0x22)), fill160Slice(0x22)},
	{"Size256", id.New256(fill256(0x33)), fill256Slice(0x33)},
}

func TestMarshalBinary(t *testing.T) {
	t.Parallel()

	for _, tc := range shapes {
		t.Run("returns the bytes of an ID of "+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.id.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must succeed")
			testkit.Equal(t, got, tc.want, "the encoding must be the identifier's bytes")
		})
	}
}

func TestAppendBinary(t *testing.T) {
	t.Parallel()

	t.Run("appends the bytes of the ID to dst", func(t *testing.T) {
		t.Parallel()
		got, err := id.New128(fill128(0x44)).AppendBinary([]byte{0xAA})
		testkit.NoError(t, err, "AppendBinary must succeed")
		testkit.Equal(t, got, append([]byte{0xAA}, fill128Slice(0x44)...), "AppendBinary must extend dst")
	})

	t.Run("appends nothing for the zero ID", func(t *testing.T) {
		t.Parallel()
		got, err := id.Zero.AppendBinary([]byte{0xAA})
		testkit.NoError(t, err, "AppendBinary must succeed")
		testkit.Equal(t, got, []byte{0xAA}, "the zero ID must encode as no bytes")
	})
}

func TestUnmarshalBinary(t *testing.T) {
	t.Parallel()

	for _, tc := range shapes {
		t.Run("decodes the encoding of an ID of "+tc.name, func(t *testing.T) {
			t.Parallel()
			got := id.New160(fill160(0x55))
			testkit.NoError(t, got.UnmarshalBinary(tc.want), "UnmarshalBinary must accept the encoding")
			testkit.True(t, got == tc.id, "the round trip must return the same ID")
		})
	}

	t.Run("decodes nil as the zero ID", func(t *testing.T) {
		t.Parallel()
		got := id.New128(fill128(0x66))
		testkit.NoError(t, got.UnmarshalBinary(nil), "UnmarshalBinary must accept nil")
		testkit.True(t, got.IsZero(), "nil must decode to the zero ID")
	})

	tests := []struct {
		name string
		give int
	}{
		{name: "returns ErrSize for one byte", give: 1},
		{name: "returns ErrSize for one byte short of Size128", give: id.Size128 - 1},
		{name: "returns ErrSize for a length between Size128 and Size160", give: 18},
		{name: "returns ErrSize for one byte past Size256", give: id.Size256 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := id.New128(fill128(0x77))
			got := want
			err := got.UnmarshalBinary(make([]byte, tt.give))
			testkit.ErrorIs(t, err, id.ErrSize, "a wrong length must be a decode error")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrSize must classify as Invalid")
			testkit.True(t, got == want, "a rejected decode must leave the ID unchanged")
		})
	}

	t.Run("does not alias the input", func(t *testing.T) {
		t.Parallel()
		data := fill128Slice(0x88)
		var got id.ID
		testkit.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must accept a valid length")
		data[0] = 0
		testkit.Equal(t, got.Bytes()[0], byte(0x88), "changing the input must not change the ID")
	})

	t.Run("returns the result of FromBytes for every length from 1 to Size256+1", func(t *testing.T) {
		t.Parallel()
		for n := 1; n <= id.Size256+1; n++ {
			data := make([]byte, n)
			for k := range data {
				data[k] = byte(k + 1)
			}
			want, wantErr := id.FromBytes(data)
			var got id.ID
			err := got.UnmarshalBinary(data)
			testkit.True(t, errors.Is(err, wantErr),
				"UnmarshalBinary must return the error of FromBytes for "+strconv.Itoa(n)+" bytes")
			testkit.True(t, got == want,
				"UnmarshalBinary must decode the ID of FromBytes for "+strconv.Itoa(n)+" bytes")
		}
	})

	cleared := []struct {
		name string
		held id.ID
		give id.ID
	}{
		{
			name: "clears the bytes of a Size256 ID under a Size128 one",
			held: id.New256(fill256(0x33)),
			give: id.New128(fill128(0x11)),
		},
		{
			name: "clears the bytes of a Size256 ID under a Size160 one",
			held: id.New256(fill256(0x33)),
			give: id.New160(fill160(0x22)),
		},
		{
			name: "clears the bytes of a Size160 ID under a Size128 one",
			held: id.New160(fill160(0x22)),
			give: id.New128(fill128(0x11)),
		},
	}
	for _, tt := range cleared {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.held
			testkit.NoError(t, got.UnmarshalBinary(tt.give.Bytes()), "UnmarshalBinary must accept the encoding")
			testkit.True(t, got == tt.give, "the decoded ID must equal the ID built from the same bytes")
		})
	}
}

func TestSizeKanon(t *testing.T) {
	t.Parallel()

	for _, tc := range shapes {
		t.Run("returns the length of the encoding of an ID of "+tc.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.id.SizeKanon(), len(tc.want), "SizeKanon must equal the length of the encoding")
		})
	}
}

// TestExactKanon checks the two guarantees that ExactKanon declares with
// kanon's conformance suite. An ID without the methods of kanon.Exact does
// not compile here.
func TestExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[id.ID](t)
}

func BenchmarkAppendBinary(b *testing.B) {
	u := id.New256(fill256(0x7f))
	buf := make([]byte, 0, id.MaxSize)
	b.ReportAllocs()
	for b.Loop() {
		buf, _ = u.AppendBinary(buf[:0])
	}
	runtime.KeepAlive(buf)
}

// BenchmarkAppendKanon reports the cost of AppendKanon of a 32-byte ID
// into a buffer with room, and fails when it allocates. The allocation
// check appends to a buffer of its own, so the closure that captures it
// does not change the code of the timed loop.
func BenchmarkAppendKanon(b *testing.B) {
	u := id.New256(fill256(0x7f))

	probe := make([]byte, 0, id.MaxSize)
	if allocs := testing.AllocsPerRun(benchRuns, func() { probe = u.AppendKanon(probe[:0]) }); allocs != 0 {
		b.Fatalf("AppendKanon allocates %v times per call, want 0", allocs)
	}

	buf := make([]byte, 0, id.MaxSize)
	b.ReportAllocs()
	for b.Loop() {
		buf = u.AppendKanon(buf[:0])
	}
	runtime.KeepAlive(buf)
}

func BenchmarkMarshalBinary(b *testing.B) {
	u := id.New256(fill256(0x7f))
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		sink, _ = u.MarshalBinary()
	}
	runtime.KeepAlive(sink)
}

// BenchmarkUnmarshalBinary reports the cost of a decode of 32 bytes into
// an ID, and fails when the decode allocates. The allocation check
// decodes into an ID of its own, so the closure that captures it does not
// change the code of the timed loop.
func BenchmarkUnmarshalBinary(b *testing.B) {
	data := fill256Slice(0x7f)

	var probe id.ID
	if allocs := testing.AllocsPerRun(benchRuns, func() { _ = probe.UnmarshalBinary(data) }); allocs != 0 {
		b.Fatalf("UnmarshalBinary allocates %v times per call, want 0", allocs)
	}

	var u id.ID
	b.ReportAllocs()
	for b.Loop() {
		_ = u.UnmarshalBinary(data)
	}
	runtime.KeepAlive(u)
}

func fill128Slice(b byte) []byte {
	a := fill128(b)
	return a[:]
}

func fill160Slice(b byte) []byte {
	a := fill160(b)
	return a[:]
}

func fill256Slice(b byte) []byte {
	a := fill256(b)
	return a[:]
}
