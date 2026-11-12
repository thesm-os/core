// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// bucketsContract states the law that the machine of a table of buckets
// checks.
const bucketsContract = "a bucket admits the units that it covers, and the units that it admits in a window do not " +
	"exceed Burst plus the units of the window and the units that ReturnN gave back in it"

func TestBuckets(t *testing.T) {
	t.Parallel()

	t.Run("NewBuckets", func(t *testing.T) {
		t.Parallel()

		valid := resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 4}
		tests := []struct {
			name string
			edit func(*resilience.BucketsConfig)
		}{
			{name: "returns ErrConfig for a nil Clock", edit: func(c *resilience.BucketsConfig) { c.Clock = nil }},
			{name: "returns ErrConfig for a Rate of zero", edit: func(c *resilience.BucketsConfig) { c.Rate = 0 }},
			{
				name: "returns ErrConfig for a Rate above a unit per nanosecond",
				edit: func(c *resilience.BucketsConfig) { c.Rate = 1_000_000_001 },
			},
			{name: "returns ErrConfig for a Burst of zero", edit: func(c *resilience.BucketsConfig) { c.Burst = 0 }},
			{
				name: "returns ErrConfig for a Burst above MaxBurst",
				edit: func(c *resilience.BucketsConfig) { c.Burst = resilience.MaxBurst + 1 },
			},
			{name: "returns ErrConfig for a Size of zero", edit: func(c *resilience.BucketsConfig) { c.Size = 0 }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := valid
				tt.edit(&cfg)

				_, err := resilience.NewBuckets(cfg)
				expect.ErrorIs(t, err, resilience.ErrConfig, "NewBuckets must refuse the config")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a table of full buckets", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 4})
			expect.True(t, b.AllowN(0, 10), "the first bucket must have Burst units")
			expect.True(t, b.AllowN(3, 10), "the last bucket must have Burst units")
		})
	})

	t.Run("AllowN", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the units from the bucket i alone", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 2})
			assert.True(t, b.AllowN(0, 10), "the bucket must admit its Burst")
			expect.False(t, b.AllowN(0, 1), "an empty bucket must refuse a unit")
			expect.True(t, b.AllowN(1, 10), "another bucket must keep its units")
		})

		t.Run("takes nothing when it reports false", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 1})
			assert.True(t, b.AllowN(0, 8), "the bucket must admit 8 of its 10 units")
			assert.False(t, b.AllowN(0, 3), "the bucket must not admit 3 of its 2 units")
			assert.True(t, b.AllowN(0, 2), "a refused call must leave the 2 units in the bucket")
		})

		t.Run("adds Rate units per second", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBuckets(t, resilience.BucketsConfig{Clock: c, Rate: 10, Burst: 10, Size: 1})
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")

			c.Advance(100 * time.Millisecond)
			assert.True(t, b.AllowN(0, 1), "a tenth of a second must add one unit at Rate 10")
			assert.False(t, b.AllowN(0, 1), "a tenth of a second must add one unit alone at Rate 10")

			c.Advance(time.Second)
			assert.True(t, b.AllowN(0, 10), "a second must add Rate units")
		})

		t.Run("adds no more than Burst units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBuckets(t, resilience.BucketsConfig{Clock: c, Rate: 10, Burst: 10, Size: 1})
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")

			c.Advance(time.Hour)
			assert.True(t, b.AllowN(0, 10), "a refilled bucket must have Burst units")
			assert.False(t, b.AllowN(0, 1), "a refilled bucket must have no more than Burst units")
		})

		t.Run("adds a unit after its cost rounded up to whole nanoseconds", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBuckets(t, resilience.BucketsConfig{Clock: c, Rate: 3, Burst: 3, Size: 1})
			assert.True(t, b.AllowN(0, 3), "a full bucket must have Burst units")

			c.Advance(time.Second)
			assert.True(t, b.AllowN(0, 2), "a second must add two units of 333,333,334 ns at Rate 3")
			assert.False(t, b.AllowN(0, 1), "a second must add less than three units of 333,333,334 ns")

			c.Advance(2 * time.Nanosecond)
			assert.True(t, b.AllowN(0, 1), "1,000,000,002 ns must add the third unit at Rate 3")
		})

		t.Run("reports true for an n of zero", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 1, Size: 1})
			assert.True(t, b.AllowN(0, 1), "a full bucket must have its unit")
			assert.True(t, b.AllowN(0, 0), "an empty bucket must admit zero units")
		})

		tests := []struct {
			name  string
			giveI int
			giveN int64
		}{
			{name: "reports false for a negative n", giveI: 0, giveN: -1},
			{name: "reports false for an n above Burst", giveI: 0, giveN: 11},
			{name: "reports false for a negative index", giveI: -1, giveN: 1},
			{name: "reports false for an index past the table", giveI: 2, giveN: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 2})
				assert.False(t, b.AllowN(tt.giveI, tt.giveN), "AllowN must refuse the call")
				expect.True(t, b.AllowN(0, 10), "a refused call must take nothing from the first bucket")
				expect.True(t, b.AllowN(1, 10), "a refused call must take nothing from the second bucket")
			})
		}

		t.Run("admits at most the units of the bucket across concurrent callers", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 1})

			outcomes := history.Concurrently(64, patience, func(int) (any, error) { return b.AllowN(0, 1), nil })

			admitted := 0
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
				if o.Output == true {
					admitted++
				}
			}

			assert.Equal(t, admitted, 10, "concurrent calls must share the 10 units of the bucket")
		})
	})

	t.Run("ReturnN", func(t *testing.T) {
		t.Parallel()

		t.Run("gives back the units that AllowN took", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 2})
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")
			assert.True(t, b.AllowN(1, 10), "a full bucket must have Burst units")

			b.ReturnN(0, 3)
			expect.True(t, b.AllowN(0, 3), "the bucket must have the 3 units that ReturnN gave back")
			expect.False(t, b.AllowN(0, 1), "the bucket must have no more units than ReturnN gave back")
			expect.False(t, b.AllowN(1, 1), "ReturnN must give the units back to the bucket i alone")
		})

		t.Run("keeps at most Burst units in the bucket", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 1})
			b.ReturnN(0, 5)
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")
			assert.False(t, b.AllowN(0, 1), "ReturnN must not add units to a full bucket")
		})

		t.Run("gives back fewer units once the bucket gained units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBuckets(t, resilience.BucketsConfig{Clock: c, Rate: 10, Burst: 10, Size: 1})
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")

			c.Advance(500 * time.Millisecond)
			b.ReturnN(0, 8)
			assert.True(t, b.AllowN(0, 10), "the bucket must refill to Burst units")
			assert.False(t, b.AllowN(0, 1), "the bucket must have no more than Burst units")
		})

		t.Run("gives back Burst units for an n above Burst", func(t *testing.T) {
			t.Parallel()
			b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 1})
			assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")

			b.ReturnN(0, math.MaxInt64)
			assert.True(t, b.AllowN(0, 10), "the bucket must have Burst units")
			assert.False(t, b.AllowN(0, 1), "the bucket must have no more than Burst units")
		})

		tests := []struct {
			name  string
			giveI int
			giveN int64
		}{
			{name: "does not give back units for an n of zero", giveI: 0, giveN: 0},
			{name: "does not give back units for a negative n", giveI: 0, giveN: -3},
			{name: "does not give back units for a negative index", giveI: -1, giveN: 3},
			{name: "does not give back units for an index past the table", giveI: 1, giveN: 3},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := newBuckets(t, resilience.BucketsConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10, Size: 1})
				assert.True(t, b.AllowN(0, 10), "a full bucket must have Burst units")

				b.ReturnN(tt.giveI, tt.giveN)
				assert.False(t, b.AllowN(0, 1), "an empty bucket must not gain a unit")
			})
		}
	})

	t.Run("bounds the units of a bucket under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, bucketsContract, bucketsSteps)
	})
}

// TestBucketsAllocs checks the allocation contract of AllowN and ReturnN.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestBucketsAllocs(t *testing.T) {
	t.Run("AllowN", func(t *testing.T) {
		b := newBuckets(t, resilience.BucketsConfig{
			Clock: fake.New(originUTC), Rate: 1_000_000_000,
			Burst: resilience.MaxBurst, Size: 1,
		})

		var got bool
		expect.MaxAllocs(t, func() {
			got = b.AllowN(0, 1)
			b.ReturnN(0, 1)
		}, 0, "AllowN and ReturnN must not allocate")
		assert.True(t, got, "the test must measure an admitted call")
	})
}

// BenchmarkBuckets reports the cost of AllowN and ReturnN, and fails when one
// allocates.
func BenchmarkBuckets(b *testing.B) {
	b.Run("AllowN", func(b *testing.B) {
		bk := newBuckets(b, resilience.BucketsConfig{
			Clock: fake.New(originUTC), Rate: 1_000_000_000,
			Burst: resilience.MaxBurst, Size: 1,
		})

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = bk.AllowN(0, 1)
			bk.ReturnN(0, 1)
		}

		assert.True(b, got, "the benchmark must measure an admitted call")
	})
}

// newBuckets returns the Buckets of cfg, and fails tb when NewBuckets refuses
// cfg.
func newBuckets(tb testing.TB, cfg resilience.BucketsConfig) *resilience.Buckets {
	tb.Helper()

	b, err := resilience.NewBuckets(cfg)
	assert.NoError(tb, err, "NewBuckets must accept the config")

	return b
}

// bucketsSteps runs the steps of a machine over one bucket of a table on a
// fake clock. The actions are an AllowN, a ReturnN and an advance of the
// clock. A unit costs unit nanoseconds, which is 10^9/Rate nanoseconds
// rounded up. A full bucket covers Burst units. A model keeps the time of the
// bucket, and each AllowN must admit exactly the calls that the model covers.
//
// After every step the invariant takes each window from a time s to the
// time of the clock and includes both ends. It checks that the units that
// AllowN admitted in the window do not exceed Burst plus the units of the
// window and the units that each ReturnN in the window gave back.
func bucketsSteps(c *prop.Case) {
	rate := c.Draw(prop.SampledFrom[int64](1, 3, 7, 10, 1_000), "rate")
	burst := c.Draw(prop.Integer[int64](1, 8), "burst")
	clk := fake.New(originUTC)
	b, err := resilience.NewBuckets(resilience.BucketsConfig{Clock: clk, Rate: rate, Burst: burst, Size: 2})
	assert.NoError(c, err, "NewBuckets must accept the config")

	unit := (nsPerSecond + rate - 1) / rate
	now := func() int64 { return int64(clk.Elapsed()) }

	// event is an admission of n units or a return of the units that cost
	// back nanoseconds. at is the time of the event.
	type event struct{ at, n, back int64 }

	var (
		events []event
		// due is the time of the bucket in the model in nanoseconds of the
		// Elapsed of the clock. The bucket would be empty at due.
		due int64
	)

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "AllowN",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer[int64](0, burst+1), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int64)
					next := max(due, now()) + n*unit
					want := n == 0 || n <= burst && next-burst*unit <= now()
					assert.Equal(c, b.AllowN(0, n), want, "AllowN must admit the units that the bucket covers")
					if want && n > 0 {
						due = next
						events = append(events, event{at: now(), n: n})
					}
				},
			},
			{
				Name:  "ReturnN",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer[int64](0, burst+1), "n") },
				Run: func(_ *prop.Case, _ int, in any) {
					n := in.(int64)
					b.ReturnN(0, n)
					if n <= 0 || due <= now() {
						return
					}
					next := max(due-min(n, burst)*unit, now())
					events = append(events, event{at: now(), back: due - next})
					due = next
				},
			},
			{
				Name: "advance",
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.SampledFrom[int64](0, 1, unit-1, unit, burst*unit), "ns")
				},
				Run: func(_ *prop.Case, _ int, in any) { clk.Advance(time.Duration(in.(int64))) },
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			at := now()
			for _, s := range events {
				var cost, back int64
				for _, e := range events {
					if e.at >= s.at {
						cost += e.n * unit
						back += e.back
					}
				}
				// The values are below 2^53, so InRange compares them exactly.
				assert.InRange(c, cost, 0, float64(burst*unit+at-s.at+back),
					"the units that a bucket admits in a window must not exceed Burst plus the units of the window "+
						"and the units that ReturnN gave back in it")
			}
		},
	})
}
