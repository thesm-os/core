// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon/kanontest"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
)

// limits are the examples of the properties over one instant: the zero
// Instant, an instant of 2026, one before the Unix epoch, and the limits of
// each field.
var limits = []prop.FormOption{
	prop.Example(clock.Instant{}),
	prop.Example(clock.Instant{Wall: 1_767_225_600_000_000_000, Logical: 7, Node: 42}),
	prop.Example(clock.Instant{Wall: -1_000_000_000, Logical: 1, Node: 1}),
	prop.Example(clock.Instant{Wall: math.MinInt64}),
	prop.Example(clock.Instant{Wall: math.MaxInt64}),
	prop.Example(clock.Instant{Logical: math.MaxUint32, Node: math.MaxUint32}),
}

// Generators of the properties over instants.
var (
	// near generates instants of a small domain, so two draws often tie in
	// one field or more, and the zero Instant is among them.
	near = prop.Composite(func(c *prop.Case) clock.Instant {
		return clock.Instant{
			Wall:    c.Draw(prop.Integer[int64](-1, 2), "wall"),
			Logical: c.Draw(prop.Integer[uint32](0, 2), "logical"),
			Node:    c.Draw(prop.Integer[clock.NodeID](0, 2), "node"),
		}
	})

	// bounded generates instants whose Wall is far enough from the limits
	// of int64 that a duration of span added to it cannot overflow.
	bounded = prop.Composite(func(c *prop.Case) clock.Instant {
		return clock.Instant{
			Wall:    c.Draw(prop.Integer[int64](-1<<62, 1<<62), "wall"),
			Logical: c.Draw(prop.Integer[uint32](0, math.MaxUint32), "logical"),
			Node:    c.Draw(prop.Integer[clock.NodeID](0, math.MaxUint32), "node"),
		}
	})

	// span generates the durations that a bounded instant takes.
	span = prop.Duration(-1<<62, 1<<62)
)

func TestInstant(t *testing.T) {
	t.Parallel()

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether every field is zero", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, clock.Instant.IsZero, func(i clock.Instant) bool { return i == clock.Instant{} },
				"IsZero must report true for the zero Instant alone",
				prop.Example(clock.Instant{}), prop.Example(clock.Instant{Wall: 1}),
				prop.Example(clock.Instant{Logical: 1}), prop.Example(clock.Instant{Node: 1}))
		})
	})

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		t.Run("orders instants by Wall, then Logical, then Node", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare must order instants by Wall, then Logical, then Node", func(c *prop.Case) {
				a, b := c.Draw(near, "a"), c.Draw(near, "b")
				want := cmp.Or(cmp.Compare(a.Wall, b.Wall), cmp.Compare(a.Logical, b.Logical),
					cmp.Compare(a.Node, b.Node))
				assert.Equal(c, a.Compare(b), want, "Compare must follow the order of the fields")
			})
		})
	})

	t.Run("HappensBefore", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether Compare orders the instant first", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "HappensBefore must report Compare(other) < 0", func(c *prop.Case) {
				a, b := c.Draw(near, "a"), c.Draw(near, "b")
				assert.Equal(c, a.HappensBefore(b), a.Compare(b) < 0, "HappensBefore must follow Compare")
			})
		})
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("advances Wall by d and keeps Logical and Node", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Add must move Wall alone", func(c *prop.Case) {
				i, d := c.Draw(bounded, "i"), c.Draw(span, "d")
				got := i.Add(d)
				assert.Equal(c, got.Sub(i), d, "Add must advance Wall by d")
				assert.Equal(c, got.Logical, i.Logical, "Add must keep Logical")
				assert.Equal(c, got.Node, i.Node, "Add must keep Node")
			})
		})
	})

	t.Run("Sub", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the wall-clock time between two instants", func(t *testing.T) {
			t.Parallel()
			a := clock.Instant{Wall: 2_000_000_000, Logical: 1}
			b := clock.Instant{Wall: 1_000_000_000, Logical: 9, Node: 3}
			assert.Equal(t, a.Sub(b), time.Second, "Sub must read Wall alone")
		})

		t.Run("returns the negated duration for the instants swapped", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a.Sub(b) must be the negation of b.Sub(a)", func(c *prop.Case) {
				a, b := c.Draw(bounded, "a"), c.Draw(bounded, "b")
				assert.Equal(c, a.Sub(b), -b.Sub(a), "Sub must be antisymmetric")
			})
		})
	})

	t.Run("Time", func(t *testing.T) {
		t.Parallel()

		t.Run("returns Wall as a time in UTC", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Time must return the instant of Wall in UTC", func(c *prop.Case) {
				i := c.Draw(prop.Of[clock.Instant](), "i")
				got := i.Time()
				assert.Equal(c, got.UnixNano(), i.Wall, "Time must keep every nanosecond of Wall")
				assert.Equal(c, got.Location(), time.UTC, "Time must be in UTC", assert.ByIdentity())
			})
		})
	})

	t.Run("UnixMilli", func(t *testing.T) {
		t.Parallel()

		t.Run("truncates Wall toward zero to milliseconds", func(t *testing.T) {
			t.Parallel()
			prop.True(t, func(i clock.Instant) bool {
				rest := i.Wall - i.UnixMilli()*1e6

				return rest > -1e6 && rest < 1e6 && (rest == 0 || (rest > 0) == (i.Wall > 0))
			}, "UnixMilli must drop less than a millisecond, toward zero",
				append([]prop.FormOption{
					prop.Example(clock.Instant{Wall: 1_500_000_123}),
					prop.Example(clock.Instant{Wall: -1_500_000}),
				}, limits...)...)
		})
	})

	t.Run("UnixMicro", func(t *testing.T) {
		t.Parallel()

		t.Run("truncates Wall toward zero to microseconds", func(t *testing.T) {
			t.Parallel()
			prop.True(t, func(i clock.Instant) bool {
				rest := i.Wall - i.UnixMicro()*1e3

				return rest > -1e3 && rest < 1e3 && (rest == 0 || (rest > 0) == (i.Wall > 0))
			}, "UnixMicro must drop less than a microsecond, toward zero",
				append([]prop.FormOption{
					prop.Example(clock.Instant{Wall: 1_500_000_123}),
					prop.Example(clock.Instant{Wall: -1_500}),
				}, limits...)...)
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("encodes Wall then Logical then Node in big-endian order", func(t *testing.T) {
			t.Parallel()
			got, err := clock.Instant{Wall: 1, Logical: 2, Node: 3}.AppendBinary(nil)
			assert.NoError(t, err, "AppendBinary must not fail")
			assert.Equal(t, got, []byte{
				0, 0, 0, 0, 0, 0, 0, 1, // Wall
				0, 0, 0, 2, // Logical
				0, 0, 0, 3, // Node
			}, "the binary form is a stable wire layout")
		})

		t.Run("appends the binary form to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendBinary must extend dst with the binary form", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(2*clock.InstantSize)), "dst")
				i := c.Draw(prop.Of[clock.Instant](), "i")
				form, err := i.MarshalBinary()
				assert.NoError(c, err, "MarshalBinary must not fail")

				got, err := i.AppendBinary(dst)
				assert.NoError(c, err, "AppendBinary must not fail")
				assert.Equal(c, got, append(slices.Clip(dst), form...), "AppendBinary must keep dst and append")
			})
		})

		t.Run("sorts the forms of instants from the Unix epoch on as Compare orders them", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the binary forms of instants from the epoch on must sort as Compare", func(c *prop.Case) {
				a, b := c.Draw(near, "a"), c.Draw(near, "b")
				c.Assume(a.Wall >= 0 && b.Wall >= 0)
				x, _ := a.MarshalBinary()
				y, _ := b.MarshalBinary()
				assert.Equal(c, bytes.Compare(x, y), a.Compare(b), "the bytewise order must be the order of Compare")
			})
		})
	})

	t.Run("AppendKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("appends what AppendBinary appends", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(i clock.Instant) []byte { return i.AppendKanon(nil) },
				func(i clock.Instant) []byte {
					form, _ := i.AppendBinary(nil)

					return form
				}, "AppendKanon must append the binary form", limits...)
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the instant that MarshalBinary encodes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, clock.Instant.MarshalBinary, func(b []byte) (clock.Instant, error) {
				var i clock.Instant
				err := i.UnmarshalBinary(b)

				return i, err
			}, "UnmarshalBinary must undo MarshalBinary for every instant", limits...)
		})

		t.Run("returns ErrInstantSize for any length but InstantSize", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalBinary must refuse every length but InstantSize", func(c *prop.Case) {
				data := c.Draw(prop.Bytes(prop.MaxSize(2*clock.InstantSize)).Filter(func(b []byte) bool {
					return len(b) != clock.InstantSize
				}), "data")
				got := clock.Instant{Wall: 7, Logical: 7, Node: 7}

				var err error
				assert.Pure(c, func() clock.Instant { return got }, func() { err = got.UnmarshalBinary(data) },
					"a refused decode must leave the instant unchanged")
				assert.ErrorIs(c, err, clock.ErrInstantSize, "a wrong length must return ErrInstantSize")
				assert.Equal(c, errs.Classify(err), errs.Invalid, "ErrInstantSize must classify as Invalid")
			})
		})
	})

	t.Run("SizeKanon", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the binary form", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, clock.Instant.SizeKanon, func(i clock.Instant) int {
				form, _ := i.MarshalBinary()

				return len(form)
			}, "SizeKanon must be the length of every binary form", limits...)
		})
	})

	t.Run("ExactKanon", func(t *testing.T) {
		t.Parallel()
		kanontest.RunExact[clock.Instant](t)
	})

	t.Run("InstantRange", func(t *testing.T) {
		t.Parallel()

		t.Run("Contains", func(t *testing.T) {
			t.Parallel()

			a, b, c := clock.Instant{Wall: 100}, clock.Instant{Wall: 200}, clock.Instant{Wall: 300}
			window := clock.InstantRange{Since: a, Until: c}
			tests := []struct {
				name string
				give clock.InstantRange
				in   clock.Instant
				want bool
			}{
				{name: "reports false below Since", give: window, in: clock.Instant{Wall: 50}},
				{name: "reports true at Since", give: window, in: a, want: true},
				{name: "reports true between Since and Until", give: window, in: b, want: true},
				{name: "reports false at Until", give: window, in: c},
				{name: "reports false above Until", give: window, in: clock.Instant{Wall: 400}},
				{
					name: "reports true below Until for a zero Since",
					give: clock.InstantRange{Until: b},
					in:   clock.Instant{Wall: -1},
					want: true,
				},
				{name: "reports false at Until for a zero Since", give: clock.InstantRange{Until: b}, in: b},
				{name: "reports false below Since for a zero Until", give: clock.InstantRange{Since: b}, in: a},
				{
					name: "reports true above Since for a zero Until",
					give: clock.InstantRange{Since: b},
					in:   c,
					want: true,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Contains(tt.in), tt.want, "Contains must apply a half-open interval")
				})
			}

			t.Run("reports true for every instant in the zero range", func(t *testing.T) {
				t.Parallel()
				prop.True(t, clock.InstantRange{}.Contains, "the zero range must contain every instant", limits...)
			})

			t.Run("reports true in one of two adjacent ranges for an instant of their union", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "[A, B) and [B, C) must cover [A, C) exactly once", func(pc *prop.Case) {
					bounds := []clock.Instant{pc.Draw(near, "a"), pc.Draw(near, "b"), pc.Draw(near, "c")}
					pc.Assume(!slices.ContainsFunc(bounds, clock.Instant.IsZero))
					slices.SortFunc(bounds, clock.Instant.Compare)
					i := pc.Draw(near, "i")

					in := 0
					for _, r := range []clock.InstantRange{
						{Since: bounds[0], Until: bounds[1]},
						{Since: bounds[1], Until: bounds[2]},
					} {
						if r.Contains(i) {
							in++
						}
					}
					union := clock.InstantRange{Since: bounds[0], Until: bounds[2]}
					assert.Equal(pc, in == 1, union.Contains(i), "an instant of the union must be in one range alone")
				})
			})
		})

		t.Run("IsZero", func(t *testing.T) {
			t.Parallel()

			t.Run("reports whether both endpoints are zero", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, clock.InstantRange.IsZero,
					func(r clock.InstantRange) bool { return r == clock.InstantRange{} },
					"IsZero must report true for the zero range alone",
					prop.Example(clock.InstantRange{}),
					prop.Example(clock.InstantRange{Since: clock.Instant{Node: 1}}),
					prop.Example(clock.InstantRange{Until: clock.Instant{Logical: 1}}))
			})
		})
	})
}

// TestInstantAllocs checks the allocation contract of each method of
// Instant and InstantRange. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestInstantAllocs(t *testing.T) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	b := clock.Instant{Wall: 2_000_000_000, Logical: 2, Node: 2}
	dst := make([]byte, 0, clock.InstantSize)

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = a.Compare(b) }, 0, "Compare must not allocate")
		assert.Equal(t, got, -1, "the test must measure an instant before the other")
	})

	t.Run("HappensBefore", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = a.HappensBefore(b) }, 0, "HappensBefore must not allocate")
		assert.True(t, got, "the test must measure an instant before the other")
	})

	t.Run("Sub", func(t *testing.T) {
		var got time.Duration
		expect.MaxAllocs(t, func() { got = b.Sub(a) }, 0, "Sub must not allocate")
		assert.Equal(t, got, time.Second, "the test must measure one second")
	})

	t.Run("Add", func(t *testing.T) {
		var got clock.Instant
		expect.MaxAllocs(t, func() { got = a.Add(time.Second) }, 0, "Add must not allocate")
		assert.Equal(t, got.Wall, b.Wall, "the test must measure an advance of one second")
	})

	t.Run("Time", func(t *testing.T) {
		var got time.Time
		expect.MaxAllocs(t, func() { got = a.Time() }, 0, "Time must not allocate")
		assert.Equal(t, got.UnixNano(), a.Wall, "the test must measure the time of Wall")
	})

	t.Run("IsZero", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = a.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure an instant that is not zero")
	})

	t.Run("UnixMilli", func(t *testing.T) {
		var got int64
		expect.MaxAllocs(t, func() { got = a.UnixMilli() }, 0, "UnixMilli must not allocate")
		assert.Equal(t, got, int64(1000), "the test must measure one second in milliseconds")
	})

	t.Run("UnixMicro", func(t *testing.T) {
		var got int64
		expect.MaxAllocs(t, func() { got = a.UnixMicro() }, 0, "UnixMicro must not allocate")
		assert.Equal(t, got, int64(1_000_000), "the test must measure one second in microseconds")
	})

	t.Run("AppendBinary", func(t *testing.T) {
		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = a.AppendBinary(dst[:0]) }, 0,
			"AppendBinary into a dst with room must not allocate")
		assert.NoError(t, err, "AppendBinary must not fail")
		assert.Length(t, got, clock.InstantSize, "the test must measure the binary form")
	})

	t.Run("AppendKanon", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got = a.AppendKanon(dst[:0]) }, 0,
			"AppendKanon into a dst with room must not allocate")
		assert.Length(t, got, clock.InstantSize, "the test must measure the binary form")
	})

	t.Run("MarshalBinary", func(t *testing.T) {
		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = a.MarshalBinary() }, 1,
			"MarshalBinary must allocate only the returned slice")
		assert.NoError(t, err, "MarshalBinary must not fail")
		assert.Length(t, got, clock.InstantSize, "the test must measure the binary form")
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		form, _ := a.MarshalBinary()

		var (
			got clock.Instant
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(form) }, 0, "UnmarshalBinary must not allocate")
		assert.NoError(t, err, "UnmarshalBinary must accept the binary form")
		assert.Equal(t, got, a, "the test must measure a decode of the instant")
	})

	t.Run("SizeKanon", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = a.SizeKanon() }, 0, "SizeKanon must not allocate")
		assert.Equal(t, got, clock.InstantSize, "the test must measure the size of the binary form")
	})

	t.Run("InstantRange", func(t *testing.T) {
		r := clock.InstantRange{Since: a, Until: b}

		t.Run("Contains", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = r.Contains(a) }, 0, "Contains must not allocate")
			assert.True(t, got, "the test must measure an instant inside the range")
		})

		t.Run("IsZero", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = r.IsZero() }, 0, "IsZero must not allocate")
			assert.False(t, got, "the test must measure a range with endpoints")
		})
	})
}

// BenchmarkInstant reports the cost of each method of Instant and
// InstantRange, and fails when a method allocates more than its allocation
// contract states.
func BenchmarkInstant(b *testing.B) {
	x := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	y := clock.Instant{Wall: 1_000_000_000, Logical: 2, Node: 1}

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.Compare(y)
		}

		assert.Equal(b, got, -1, "the benchmark must measure an instant before the other")
	})

	b.Run("HappensBefore", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.HappensBefore(y)
		}

		assert.True(b, got, "the benchmark must measure an instant before the other")
	})

	b.Run("Sub", func(b *testing.B) {
		later := x.Add(time.Second)

		var got time.Duration

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = later.Sub(x)
		}

		assert.Equal(b, got, time.Second, "the benchmark must measure one second")
	})

	b.Run("Add", func(b *testing.B) {
		var got clock.Instant

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.Add(time.Second)
		}

		assert.Equal(b, got.Sub(x), time.Second, "the benchmark must measure an advance of one second")
	})

	b.Run("Time", func(b *testing.B) {
		var got time.Time

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.Time()
		}

		assert.Equal(b, got.UnixNano(), x.Wall, "the benchmark must measure the time of Wall")
	})

	b.Run("IsZero", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.IsZero()
		}

		assert.False(b, got, "the benchmark must measure an instant that is not zero")
	})

	b.Run("UnixMilli", func(b *testing.B) {
		var got int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.UnixMilli()
		}

		assert.Equal(b, got, int64(1000), "the benchmark must measure one second in milliseconds")
	})

	b.Run("UnixMicro", func(b *testing.B) {
		var got int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.UnixMicro()
		}

		assert.Equal(b, got, int64(1_000_000), "the benchmark must measure one second in microseconds")
	})

	b.Run("AppendBinary", func(b *testing.B) {
		dst := make([]byte, 0, clock.InstantSize)

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = x.AppendBinary(dst[:0])
		}

		assert.NoError(b, err, "AppendBinary must not fail")
		assert.Length(b, got, clock.InstantSize, "the benchmark must measure the binary form")
	})

	b.Run("AppendKanon", func(b *testing.B) {
		dst := make([]byte, 0, clock.InstantSize)

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.AppendKanon(dst[:0])
		}

		assert.Length(b, got, clock.InstantSize, "the benchmark must measure the binary form")
	})

	b.Run("MarshalBinary", func(b *testing.B) {
		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, err = x.MarshalBinary()
		}

		assert.NoError(b, err, "MarshalBinary must not fail")
		assert.Length(b, got, clock.InstantSize, "the benchmark must measure the binary form")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		form, _ := x.MarshalBinary()

		var (
			got clock.Instant
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalBinary(form)
		}

		assert.NoError(b, err, "UnmarshalBinary must accept the binary form")
		assert.Equal(b, got, x, "the benchmark must measure a decode of the instant")
	})

	b.Run("SizeKanon", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = x.SizeKanon()
		}

		assert.Equal(b, got, clock.InstantSize, "the benchmark must measure the size of the binary form")
	})

	b.Run("InstantRange", func(b *testing.B) {
		r := clock.InstantRange{Since: clock.Instant{Wall: 1_000_000_000}, Until: clock.Instant{Wall: 2_000_000_000}}

		b.Run("Contains", func(b *testing.B) {
			i := clock.Instant{Wall: 1_500_000_000}

			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = r.Contains(i)
			}

			assert.True(b, got, "the benchmark must measure an instant inside the range")
		})

		b.Run("IsZero", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = r.IsZero()
			}

			assert.False(b, got, "the benchmark must measure a range with endpoints")
		})
	})
}
