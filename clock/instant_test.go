// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"bytes"
	"math"
	"runtime"
	"strconv"
	"testing"
	"time"

	"go.thesmos.sh/kanon/kanontest"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
)

// roundTrips are instants at the limits of each field, whose binary
// forms the tests encode and decode.
var roundTrips = []struct {
	name string
	in   clock.Instant
}{
	{"the zero Instant", clock.Instant{}},
	{"an instant of 2026", clock.Instant{Wall: 1_767_225_600_000_000_000, Logical: 7, Node: 42}},
	{"an instant before the Unix epoch", clock.Instant{Wall: -1_000_000_000, Logical: 1, Node: 1}},
	{"the instant of the smallest Wall", clock.Instant{Wall: math.MinInt64}},
	{"the instant of the largest Wall", clock.Instant{Wall: math.MaxInt64}},
	{"the instant of the largest Logical and Node", clock.Instant{Logical: math.MaxUint32, Node: math.MaxUint32}},
}

func TestInstantIsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give clock.Instant
		want bool
	}{
		{name: "reports true for the zero value", give: clock.Instant{}, want: true},
		{name: "reports false for a Wall that is not zero", give: clock.Instant{Wall: 1}, want: false},
		{name: "reports false for a Logical that is not zero", give: clock.Instant{Logical: 1}, want: false},
		{name: "reports false for a Node that is not zero", give: clock.Instant{Node: 1}, want: false},
		{
			name: "reports false for an instant with no field zero",
			give: clock.Instant{Wall: 1, Logical: 1, Node: 1},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.give.IsZero(), tt.want, "IsZero must match expectation")
		})
	}
}

func TestInstantOrdering(t *testing.T) {
	t.Parallel()

	// Lexicographic (Wall, Logical, Node) ordering. Each row is a
	// (a, b) pair where a is causally before b.
	pairs := []struct {
		name string
		a, b clock.Instant
	}{
		{
			name: "orders the earlier Wall first",
			a:    clock.Instant{Wall: 1},
			b:    clock.Instant{Wall: 2},
		},
		{
			name: "orders the earlier Logical first within one Wall",
			a:    clock.Instant{Wall: 1, Logical: 1},
			b:    clock.Instant{Wall: 1, Logical: 2},
		},
		{
			name: "orders the lower Node first within one Wall and Logical",
			a:    clock.Instant{Wall: 1, Logical: 1, Node: 1},
			b:    clock.Instant{Wall: 1, Logical: 1, Node: 2},
		},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, p.a.Compare(p.b), -1, "a.Compare(b) must equal -1")
			testkit.Equal(t, p.b.Compare(p.a), 1, "b.Compare(a) must equal 1")
			testkit.True(t, p.a.HappensBefore(p.b), "a must happen before b")
			testkit.False(t, p.b.HappensBefore(p.a), "b must not happen before a")
		})
	}

	t.Run("Compare returns 0 for identical instants", func(t *testing.T) {
		t.Parallel()
		// Construct two equal instants from independent literals
		// so the assertion does not look like a self-comparison
		// to static analysis.
		x := clock.Instant{Wall: 1, Logical: 1, Node: 1}
		y := clock.Instant{Wall: 1, Logical: 1, Node: 1}
		testkit.Equal(t, x.Compare(y), 0, "Compare on equal instants must return 0")
		testkit.False(t, x.HappensBefore(y), "equal instants must not be ordered")
	})
}

func TestInstantArithmetic(t *testing.T) {
	t.Parallel()

	t.Run("Sub returns wall-clock duration", func(t *testing.T) {
		t.Parallel()
		a := clock.Instant{Wall: 2_000_000_000}
		b := clock.Instant{Wall: 1_000_000_000}
		testkit.Equal(t, a.Sub(b), time.Second, "a.Sub(b) must equal 1 second")
		testkit.Equal(t, b.Sub(a), -time.Second, "b.Sub(a) must equal -1 second")
	})

	t.Run("Add advances Wall alone", func(t *testing.T) {
		t.Parallel()
		in := clock.Instant{Wall: 1_000_000_000, Logical: 5, Node: 3}
		got := in.Add(time.Second)
		testkit.Equal(t, got.Wall, int64(2_000_000_000), "Wall must advance by 1 second")
		testkit.Equal(t, got.Logical, uint32(5), "Logical must be preserved")
		testkit.Equal(t, got.Node, clock.NodeID(3), "Node must be preserved")
	})

	t.Run("Time returns Wall as UTC time.Time", func(t *testing.T) {
		t.Parallel()
		want := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
		got := clock.Instant{Wall: want.UnixNano()}.Time()
		testkit.True(t, got.Equal(want), "Time must equal expected wall instant")
		testkit.True(t, got.Location() == time.UTC, "Time location must be UTC")
	})
}

func TestInstantRange(t *testing.T) {
	t.Parallel()

	a := clock.Instant{Wall: 100}
	b := clock.Instant{Wall: 200}
	c := clock.Instant{Wall: 300}

	t.Run("Contains reports true for every Instant in the zero range", func(t *testing.T) {
		t.Parallel()
		var r clock.InstantRange
		testkit.True(t, r.IsZero(), "zero range must report IsZero")
		for _, i := range []clock.Instant{{}, a, b} {
			testkit.True(t, r.Contains(i), "zero range must contain every Instant")
		}
	})

	t.Run("Contains applies a half-open interval", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Since: a, Until: c}
		testkit.False(t, r.IsZero(), "bounded range must not report IsZero")
		// Below Since.
		testkit.False(t, r.Contains(clock.Instant{Wall: 50}),
			"must not contain instant below Since")
		// At Since (inclusive).
		testkit.True(t, r.Contains(a), "must contain Since (inclusive)")
		// Strictly inside.
		testkit.True(t, r.Contains(b), "must contain instant strictly inside")
		// At Until (exclusive).
		testkit.False(t, r.Contains(c), "must not contain Until (exclusive)")
		// Above Until.
		testkit.False(t, r.Contains(clock.Instant{Wall: 400}),
			"must not contain instant above Until")
	})

	t.Run("Contains applies no lower bound for a zero Since", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Until: b}
		testkit.False(t, r.IsZero(), "range with Until set must not report IsZero")
		testkit.True(t, r.Contains(clock.Instant{Wall: -1}),
			"must accept arbitrarily-low instants when Since is zero")
		testkit.True(t, r.Contains(a), "must contain a (below Until)")
		testkit.False(t, r.Contains(b), "must not contain Until itself")
	})

	t.Run("Contains applies no upper bound for a zero Until", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Since: b}
		testkit.False(t, r.IsZero(), "range with Since set must not report IsZero")
		testkit.False(t, r.Contains(a), "must not contain instant below Since")
		testkit.True(t, r.Contains(b), "must contain Since")
		testkit.True(t, r.Contains(c),
			"must accept arbitrarily-high instants when Until is zero")
	})
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// Instant and InstantRange. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	b := clock.Instant{Wall: 2_000_000_000, Logical: 2, Node: 2}
	r := clock.InstantRange{Since: a, Until: b}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Instant.Compare", func() { _ = a.Compare(b) }},
		{"Instant.HappensBefore", func() { _ = a.HappensBefore(b) }},
		{"Instant.Sub", func() { _ = b.Sub(a) }},
		{"Instant.Add", func() { _ = a.Add(time.Second) }},
		{"Instant.Time", func() { _ = a.Time() }},
		{"Instant.IsZero", func() { _ = a.IsZero() }},
		{"InstantRange.Contains", func() { _ = r.Contains(a) }},
		{"InstantRange.IsZero", func() { _ = r.IsZero() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn), float64(0),
				tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkInstantCompare(b *testing.B) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	c := clock.Instant{Wall: 1_000_000_000, Logical: 2, Node: 1}
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Compare(c)
	}
}

func BenchmarkInstantHappensBefore(b *testing.B) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1}
	c := clock.Instant{Wall: 2_000_000_000}
	b.ReportAllocs()
	for b.Loop() {
		_ = a.HappensBefore(c)
	}
}

func BenchmarkInstantSub(b *testing.B) {
	now := clock.Instant{Wall: time.Now().UnixNano()}
	earlier := clock.Instant{Wall: now.Wall - int64(time.Second)}
	b.ReportAllocs()
	var sink time.Duration
	for b.Loop() {
		sink = now.Sub(earlier)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkInstantAdd(b *testing.B) {
	i := clock.Instant{Wall: time.Now().UnixNano()}
	b.ReportAllocs()
	var sink clock.Instant
	for b.Loop() {
		sink = i.Add(time.Second)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkInstantTime(b *testing.B) {
	i := clock.Instant{Wall: time.Now().UnixNano()}
	b.ReportAllocs()
	for b.Loop() {
		_ = i.Time()
	}
}

func BenchmarkInstantRangeContains(b *testing.B) {
	r := clock.InstantRange{
		Since: clock.Instant{Wall: 1_000_000_000},
		Until: clock.Instant{Wall: 2_000_000_000},
	}
	i := clock.Instant{Wall: 1_500_000_000}
	b.ReportAllocs()
	for b.Loop() {
		_ = r.Contains(i)
	}
}

func TestInstantBinaryRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range roundTrips {
		t.Run("returns "+tc.name+" from its encoding", func(t *testing.T) {
			t.Parallel()

			encoded, err := tc.in.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must not fail")
			testkit.Equal(t, len(encoded), clock.InstantSize, "encoding must be InstantSize bytes")

			var got clock.Instant
			testkit.NoError(t, got.UnmarshalBinary(encoded), "UnmarshalBinary must accept its own output")
			testkit.Equal(t, got, tc.in, "round-trip must preserve every field")
		})
	}
}

func TestInstantSizeKanon(t *testing.T) {
	t.Parallel()

	for _, tc := range roundTrips {
		t.Run("returns the length of the binary form of "+tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := tc.in.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must not fail")
			testkit.Equal(t, tc.in.SizeKanon(), len(encoded), "SizeKanon must equal the length of the binary form")
		})
	}
}

// TestInstantExactKanon checks the two guarantees that ExactKanon declares
// with kanon's conformance suite. An Instant without the methods of
// kanon.Exact does not compile here.
func TestInstantExactKanon(t *testing.T) {
	t.Parallel()
	kanontest.RunExact[clock.Instant](t)
}

func TestInstantAppendBinary(t *testing.T) {
	t.Parallel()

	t.Run("encodes Wall then Logical then Node in big-endian order", func(t *testing.T) {
		t.Parallel()
		i := clock.Instant{Wall: 1, Logical: 2, Node: 3}
		got, err := i.AppendBinary(nil)
		testkit.NoError(t, err, "AppendBinary must not fail")
		testkit.Equal(t, got, []byte{
			0, 0, 0, 0, 0, 0, 0, 1, // Wall
			0, 0, 0, 2, // Logical
			0, 0, 0, 3, // Node
		}, "encoding must match the documented layout")
	})

	t.Run("appends to a non-empty destination", func(t *testing.T) {
		t.Parallel()
		dst := []byte{0xFF}
		got, err := clock.Instant{Wall: 1}.AppendBinary(dst)
		testkit.NoError(t, err, "AppendBinary must not fail")
		testkit.Equal(t, len(got), 1+clock.InstantSize, "AppendBinary must not overwrite dst")
		testkit.Equal(t, got[0], byte(0xFF), "existing dst bytes must be preserved")
	})

	t.Run("encodes post-epoch instants in Compare order", func(t *testing.T) {
		t.Parallel()
		lo := clock.Instant{Wall: 100, Logical: 1, Node: 1}
		hi := clock.Instant{Wall: 100, Logical: 2, Node: 0}
		loEnc, _ := lo.MarshalBinary()
		hiEnc, _ := hi.MarshalBinary()
		testkit.Equal(t, lo.Compare(hi) < 0, bytes.Compare(loEnc, hiEnc) < 0,
			"bytewise order must match Compare for post-epoch instants")
	})
}

func TestInstantUnmarshalBinaryRejectsWrongLength(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, clock.InstantSize - 1, clock.InstantSize + 1} {
		t.Run("returns ErrInstantSize for length "+strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()
			var i clock.Instant
			err := i.UnmarshalBinary(make([]byte, n))
			testkit.ErrorIs(t, err, clock.ErrInstantSize,
				"UnmarshalBinary must reject a wrong-length encoding")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrInstantSize must classify as Invalid")
		})
	}
}

func TestInstantUnixAccessors(t *testing.T) {
	t.Parallel()

	t.Run("UnixMilli truncates nanoseconds to milliseconds", func(t *testing.T) {
		t.Parallel()
		i := clock.Instant{Wall: 1_500_000_123}
		testkit.Equal(t, i.UnixMilli(), int64(1_500), "UnixMilli must divide Wall by 1e6")
	})

	t.Run("UnixMicro truncates nanoseconds to microseconds", func(t *testing.T) {
		t.Parallel()
		i := clock.Instant{Wall: 1_500_000_123}
		testkit.Equal(t, i.UnixMicro(), int64(1_500_000), "UnixMicro must divide Wall by 1e3")
	})

	t.Run("UnixMilli truncates a pre-epoch instant toward zero", func(t *testing.T) {
		t.Parallel()
		i := clock.Instant{Wall: -1_500_000}
		testkit.Equal(t, i.UnixMilli(), int64(-1), "UnixMilli must truncate toward zero, not floor")
	})

	t.Run("returns 0 for the zero Instant", func(t *testing.T) {
		t.Parallel()
		var i clock.Instant
		testkit.Equal(t, i.UnixMilli(), int64(0), "the zero Instant must report 0 ms")
		testkit.Equal(t, i.UnixMicro(), int64(0), "the zero Instant must report 0 us")
	})
}

func BenchmarkInstantAppendBinary(b *testing.B) {
	i := clock.Instant{Wall: 1_767_225_600_000_000_000, Logical: 7, Node: 42}
	dst := make([]byte, 0, clock.InstantSize)
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		sink, _ = i.AppendBinary(dst[:0])
	}
	runtime.KeepAlive(sink)
}
