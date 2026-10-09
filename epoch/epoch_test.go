// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"cmp"
	"math"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/epoch"
)

// epochPair is the input of a property over two epochs.
type epochPair struct {
	A, B epoch.Epoch
}

func TestEpochZero(t *testing.T) {
	t.Parallel()

	t.Run("Zero is the reserved sentinel", func(t *testing.T) {
		t.Parallel()
		assert.True(t, epoch.Zero.IsZero(), "Zero.IsZero must return true")
		assert.Equal(t, epoch.Zero, epoch.Epoch(0), "Zero must equal 0")
	})

	t.Run("IsZero reports false for every epoch above Zero", func(t *testing.T) {
		t.Parallel()
		prop.False(t, epoch.Epoch.IsZero, "IsZero must report false for an epoch above Zero",
			prop.Using(prop.Integer[epoch.Epoch](1, math.MaxUint64)))
	})
}

func TestEpochCompare(t *testing.T) {
	t.Parallel()

	t.Run("orders two epochs as their values", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(p epochPair) int { return p.A.Compare(p.B) },
			func(p epochPair) int { return cmp.Compare(uint64(p.A), uint64(p.B)) },
			"Compare must return -1, 0 or +1 as the values of the epochs order",
			prop.Example(epochPair{A: 1, B: 2}),
			prop.Example(epochPair{A: 5, B: 3}),
			prop.Example(epochPair{A: 7, B: 7}),
			prop.Example(epochPair{A: epoch.Zero, B: 1}),
		)
	})
}

func TestEpochSuccessor(t *testing.T) {
	t.Parallel()

	t.Run("Successor advances by one", func(t *testing.T) {
		t.Parallel()
		var e epoch.Epoch = 7
		assert.Equal(t, e.Successor(), epoch.Epoch(8),
			"Successor(7) must equal 8")
	})

	t.Run("Successor of Zero is 1", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, epoch.Zero.Successor(), epoch.Epoch(1),
			"Successor(Zero) must equal 1")
	})

	t.Run("Successor wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		var maxEpoch epoch.Epoch = math.MaxUint64
		// Documented: monotonicity wraps at MaxUint64; the wrap is
		// unreachable in practice (~584 years at 1 ns/epoch) and
		// therefore not guarded.
		assert.Equal(t, maxEpoch.Successor(), epoch.Zero,
			"Successor(MaxUint64) must wrap to Zero")
	})
}

func TestEpochString(t *testing.T) {
	t.Parallel()

	t.Run("returns the decimal form of the epoch", func(t *testing.T) {
		t.Parallel()
		prop.RoundTrip(t,
			func(e epoch.Epoch) (string, error) { return e.String(), nil },
			func(s string) (epoch.Epoch, error) {
				v, err := strconv.ParseUint(s, 10, 64)

				return epoch.Epoch(v), err
			},
			"strconv.ParseUint must read the text of String back as the epoch",
			prop.Example(epoch.Zero),
			prop.Example(epoch.Epoch(math.MaxUint64)),
		)
	})
}

// TestEpochAllocs checks the allocation ceiling of every method of an
// Epoch. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel. String formats a value above 99,
// because strconv returns a constant string below 100.
func TestEpochAllocs(t *testing.T) {
	e, other := epoch.Epoch(123456789), epoch.Epoch(123456790)

	t.Run("IsZero", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = e.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a non-zero epoch")
	})

	t.Run("Compare", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = e.Compare(other) }, 0, "Compare must not allocate")
		assert.Equal(t, got, -1, "the test must measure an earlier epoch")
	})

	t.Run("Successor", func(t *testing.T) {
		var got epoch.Epoch
		expect.MaxAllocs(t, func() { got = e.Successor() }, 0, "Successor must not allocate")
		assert.Equal(t, got, other, "the test must measure the next epoch")
	})

	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = e.String() }, 1, "String must allocate only its result")
		assert.Equal(t, got, "123456789", "the test must measure the decimal form")
	})
}

func BenchmarkEpoch(b *testing.B) {
	e, other := epoch.Epoch(123456789), epoch.Epoch(123456790)

	b.Run("IsZero", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = e.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a non-zero epoch")
	})

	b.Run("Compare", func(b *testing.B) {
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = e.Compare(other)
		}

		assert.Equal(b, got, -1, "the benchmark must measure an earlier epoch")
	})

	b.Run("Successor", func(b *testing.B) {
		var got epoch.Epoch

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = e.Successor()
		}

		assert.Equal(b, got, other, "the benchmark must measure the next epoch")
	})

	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = e.String()
		}

		assert.Equal(b, got, "123456789", "the benchmark must measure the decimal form")
	})
}
