// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/pool"
)

// counter builds values while recording how many were made, which is
// how the lazy-construction and never-exceeds-limit properties are
// observed.
func counter() (newFn func() *int, made *atomic.Int64) {
	made = &atomic.Int64{}

	return func() *int {
		n := int(made.Add(1))

		return &n
	}, made
}

func mustBounded(tb testing.TB, limit int) (*pool.Bounded[*int], *atomic.Int64) {
	tb.Helper()

	newFn, made := counter()
	p, err := pool.NewBounded(limit, newFn)
	testkit.NoError(tb, err, "NewBounded must accept a positive limit")

	return p, made
}

func TestNewBounded(t *testing.T) {
	t.Parallel()

	t.Run("accepts a positive limit", func(t *testing.T) {
		t.Parallel()
		p, err := pool.NewBounded(1, func() int { return 0 })
		testkit.NoError(t, err, "a positive limit must be accepted")
		testkit.Equal(t, p.Cap(), 1, "Cap must report the limit")
	})

	for _, limit := range []int{0, -1, -100} {
		t.Run("rejects limit "+strconv.Itoa(limit), func(t *testing.T) {
			t.Parallel()
			// A zero-capacity pool would block every Get forever,
			// which is a configuration error rather than a runtime
			// condition.
			_, err := pool.NewBounded(limit, func() int { return 0 })
			testkit.ErrorIs(t, err, pool.ErrLimit, "a non-positive limit must be rejected")
		})
	}
}

func TestBoundedLazyConstruction(t *testing.T) {
	t.Parallel()

	t.Run("constructs nothing until Get", func(t *testing.T) {
		t.Parallel()
		p, made := mustBounded(t, 4)
		testkit.Equal(t, int(made.Load()), 0, "NewBounded must not construct eagerly")
		testkit.Equal(t, p.Created(), 0, "Created must report nothing built yet")
	})

	t.Run("constructs one value per Get up to the limit", func(t *testing.T) {
		t.Parallel()
		p, made := mustBounded(t, 3)

		for i := 1; i <= 3; i++ {
			_, err := p.Get(t.Context())
			testkit.NoError(t, err, "Get must succeed below the limit")
			testkit.Equal(t, int(made.Load()), i, "each Get below the limit must build one value")
			testkit.Equal(t, p.Created(), i, "Created must track construction")
		}
	})

	t.Run("reuses a returned value rather than constructing", func(t *testing.T) {
		t.Parallel()
		p, made := mustBounded(t, 4)

		v, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		p.Put(v)

		got, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		testkit.Equal(t, got, v, "a returned value must be handed back out")
		testkit.Equal(t, int(made.Load()), 1, "reuse must not construct")
	})
}

func TestBoundedNeverExceedsLimit(t *testing.T) {
	t.Parallel()

	// The property the type exists for: at most limit values are ever
	// constructed, no matter how many callers ask concurrently.
	const (
		limit   = 4
		workers = 32
		rounds  = 50
	)

	p, made := mustBounded(t, limit)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range rounds {
				v, err := p.Get(t.Context())
				if err != nil {
					return
				}
				p.Put(v)
			}
		})
	}
	wg.Wait()

	testkit.True(t, int(made.Load()) <= limit,
		"the pool must never construct more than limit values")
	testkit.Equal(t, p.Created(), int(made.Load()), "Created must match what newFn produced")
	testkit.Equal(t, p.Len(), int(made.Load()), "every value must be back in the pool")
}

func TestBoundedGetBlocksAtCapacity(t *testing.T) {
	t.Parallel()

	t.Run("blocks until a value is returned", func(t *testing.T) {
		t.Parallel()
		p, _ := mustBounded(t, 1)

		held, err := p.Get(t.Context())
		testkit.NoError(t, err, "the first Get must succeed")

		got := make(chan *int, 1)
		go func() {
			v, err := p.Get(t.Context())
			if err == nil {
				got <- v
			}
		}()

		select {
		case <-got:
			t.Fatal("Get must block while the pool is exhausted")
		case <-time.After(20 * time.Millisecond):
		}

		p.Put(held)

		select {
		case v := <-got:
			testkit.Equal(t, v, held, "the blocked Get must receive the returned value")
		case <-time.After(time.Second):
			t.Fatal("Get did not unblock after Put")
		}
	})

	t.Run("returns ctx.Err when the context ends first", func(t *testing.T) {
		t.Parallel()
		p, _ := mustBounded(t, 1)

		_, err := p.Get(t.Context())
		testkit.NoError(t, err, "the first Get must succeed")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = p.Get(ctx)
		testkit.ErrorIs(t, err, context.Canceled,
			"a cancelled Get must report the context error, not block")
	})

	t.Run("an already-cancelled context still serves an available value", func(t *testing.T) {
		t.Parallel()
		// The fast path checks the free list first. A caller with a
		// cancelled context asking for a value that is sitting right
		// there should not be made to fail.
		p, _ := mustBounded(t, 1)
		v, err := p.Get(t.Context())
		testkit.NoError(t, err, "the first Get must succeed")
		p.Put(v)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got, err := p.Get(ctx)
		testkit.NoError(t, err, "an available value must be served without consulting ctx")
		testkit.Equal(t, got, v, "the available value must be returned")
	})
}

func TestBoundedAccessors(t *testing.T) {
	t.Parallel()

	t.Run("Cap reports the limit", func(t *testing.T) {
		t.Parallel()
		p, _ := mustBounded(t, 7)
		testkit.Equal(t, p.Cap(), 7, "Cap must report the configured limit")
	})

	t.Run("Len counts only values available to Get", func(t *testing.T) {
		t.Parallel()
		p, _ := mustBounded(t, 2)
		testkit.Equal(t, p.Len(), 0, "a fresh pool holds nothing")

		v, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		testkit.Equal(t, p.Len(), 0, "a value in a caller's hands is not available")

		p.Put(v)
		testkit.Equal(t, p.Len(), 1, "a returned value is available again")
	})

	t.Run("Created never falls", func(t *testing.T) {
		t.Parallel()
		p, _ := mustBounded(t, 2)

		a, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		b, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		testkit.Equal(t, p.Created(), 2, "Created must count both")

		p.Put(a)
		p.Put(b)
		testkit.Equal(t, p.Created(), 2, "returning values must not change Created")
	})
}

func TestBoundedPutNeverBlocks(t *testing.T) {
	t.Parallel()

	// Put must not stall a cleanup path. Every value the pool issued
	// has a slot waiting for it, so returning all of them cannot
	// block.
	p, _ := mustBounded(t, 3)

	values := make([]*int, 0, 3)
	for range 3 {
		v, err := p.Get(t.Context())
		testkit.NoError(t, err, "Get must succeed")
		values = append(values, v)
	}

	done := make(chan struct{})
	go func() {
		for _, v := range values {
			p.Put(v)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Put blocked while returning values the pool had issued")
	}

	testkit.Equal(t, p.Len(), 3, "every returned value must be available again")
}

func BenchmarkBoundedGetPut(b *testing.B) {
	p, err := pool.NewBounded(1, func() *int { n := 0; return &n })
	testkit.NoError(b, err, "NewBounded must succeed")
	b.ReportAllocs()

	for b.Loop() {
		v, _ := p.Get(b.Context())
		p.Put(v)
	}
}

func BenchmarkBoundedGetPutParallel(b *testing.B) {
	p, err := pool.NewBounded(8, func() *int { n := 0; return &n })
	testkit.NoError(b, err, "NewBounded must succeed")
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			v, _ := p.Get(b.Context())
			p.Put(v)
		}
	})
}
