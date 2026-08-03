// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package batch

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// LoaderConfig has no defaults. Both thresholds are required, because
// a wrong window is wrong silently: too short and nothing coalesces,
// too long and every request pays it as latency, and neither shows up
// as a failure.
type LoaderConfig struct {
	// Clock times the accumulation window. Under a virtual clock the
	// window is exact, which is what makes a coalescer testable.
	Clock clock.Clock

	// Wait is how long to accumulate keys before dispatching, measured
	// from the first key of a batch. Must be > 0.
	Wait time.Duration

	// MaxBatch dispatches early once this many keys accumulate, so one
	// burst does not produce a call larger than the dependency
	// accepts. Must be > 0.
	MaxBatch int
}

// Loader coalesces loads. Concurrent calls to [Loader.Load] for
// distinct keys within one window are delivered to fn as a single
// batch; concurrent calls for the same key share one result.
//
// Loader is not a cache: results are not retained beyond the in-flight
// window. Caching is a separate decision with separate invalidation
// semantics.
//
// One Loader serves one key/value pairing, so a caller resolving three
// entity kinds constructs three Loaders.
//
// # Concurrency
//
// Safe for concurrent use.
type Loader[K comparable, V any] struct {
	clock clock.Clock
	fn    func(context.Context, []K) (map[K]V, error)

	// pending is every key with a call outstanding, whether still
	// accumulating or already dispatched. It is what makes a second
	// load of the same key join the first rather than issue its own.
	pending map[K]*call[K, V]

	// batch is the one currently accumulating, or nil between batches.
	batch *batch[K, V]

	wait     time.Duration
	maxBatch int
	waiting  int
	closed   bool

	mu sync.Mutex
}

// call is one key's outcome, shared by every caller waiting on it.
// The fields are written before done is closed and read only after,
// which is what orders them.
type call[K comparable, V any] struct {
	batch *batch[K, V]
	done  chan struct{}
	v     V
	err   error
}

// batch is one accumulating or in-flight group of keys.
//
// The batch's own context is not held here: it is handed straight to
// the dispatch goroutine, and only the cancel survives, for the moment
// the last caller leaves.
type batch[K comparable, V any] struct {
	cancel context.CancelFunc

	// fire is closed when the batch reaches MaxBatch, releasing the
	// dispatch goroutine before its window elapses.
	fire chan struct{}

	keys  []K
	calls []*call[K, V]

	// waiters is how many callers still want this batch's result.
	// When it reaches zero there is nobody left to serve and ctx is
	// cancelled.
	waiters int
}

// NewLoader returns a Loader that resolves keys through fn.
//
// fn receives the accumulated keys and returns what it could resolve.
// A key absent from its map is reported to that key's caller as
// [ErrNotFound]; an error from fn reaches every caller in the batch.
//
// Returns [ErrConfig] when Clock or fn is nil, or Wait or MaxBatch is
// not positive.
func NewLoader[K comparable, V any](
	cfg LoaderConfig,
	fn func(context.Context, []K) (map[K]V, error),
) (*Loader[K, V], error) {
	if cfg.Clock == nil || fn == nil || cfg.Wait <= 0 || cfg.MaxBatch <= 0 {
		return nil, ErrConfig
	}

	return &Loader[K, V]{
		clock:    cfg.Clock,
		fn:       fn,
		pending:  make(map[K]*call[K, V]),
		wait:     cfg.Wait,
		maxBatch: cfg.MaxBatch,
	}, nil
}

// Load resolves one key, waiting for it to travel with whatever else
// arrives in its window.
//
// Returns [ErrNotFound] when the batch function did not resolve the
// key, [ErrClosed] after [Loader.Close], ctx.Err() when the caller's
// own context ends first, and otherwise whatever error the batch
// function returned.
//
// # The batch function's context is not the caller's
//
// A batch serves several callers with several contexts, and honouring
// one would cancel work the others still need. The context handed to
// fn carries no caller's values and is cancelled only once every
// waiting caller has gone.
//
// This is the package's least ordinary behaviour and it cannot be made
// ordinary: a trace span, a deadline or a request identifier attached
// to the caller's context does not reach fn, and code inside fn that
// expects one will not find it. Taking them from the first caller to
// arrive would make fn's environment depend on goroutine scheduling,
// which is worse — one caller's batch would be attributed to another,
// non-deterministically.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	var zero V

	c, err := l.enqueue(key)
	if err != nil {
		return zero, err
	}

	defer l.leave(c.batch)

	select {
	case <-c.done:
		return c.v, c.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// LoadAll resolves keys as one batch immediately, without waiting for
// the accumulation window. A caller that already holds the full key
// set has nothing to wait for, and making it wait would add the window
// to a request that cannot benefit from it.
//
// Duplicate keys are collapsed, and keys beyond MaxBatch are split
// across several concurrent batches. Keys the batch function did not
// resolve are absent from the returned map rather than an error: the
// return is a map, so absence is representable, where [Loader.Load]
// has no such option.
//
// Unlike Load, this serves exactly one caller, so ctx is passed to the
// batch function unchanged. An error from any batch fails the call
// rather than returning a partial result, which a caller would have no
// way to tell from a set of missing keys.
//
// Returns [ErrClosed] after [Loader.Close].
func (l *Loader[K, V]) LoadAll(ctx context.Context, keys []K) (map[K]V, error) {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()

	if closed {
		return nil, ErrClosed
	}

	uniq := make([]K, 0, len(keys))
	seen := make(map[K]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			uniq = append(uniq, k)
		}
	}

	var (
		mu    sync.Mutex
		out   = make(map[K]V, len(uniq))
		first error
		wg    sync.WaitGroup
	)
	for chunk := range slices.Chunk(uniq, l.maxBatch) {
		wg.Go(func() {
			got, err := l.fn(ctx, chunk)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				if first == nil {
					first = err
				}

				return
			}
			maps.Copy(out, got)
		})
	}
	wg.Wait()

	if first != nil {
		return nil, first
	}

	return out, nil
}

// Pending reports how many callers are waiting for a result, for
// emission as a gauge.
//
// A point-in-time observation, and racy by nature. Read against the
// batch sizes fn receives, it is the coalescing ratio: many callers
// against few keys means the window is earning its latency, and one
// caller per batch means it is not.
func (l *Loader[K, V]) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.waiting
}

// Close refuses further work. In-flight and accumulating batches run
// to completion, so a caller already waiting is still served.
//
// Close is idempotent and always returns nil; the error is in the
// signature so a Loader satisfies [io.Closer] and belongs in the same
// shutdown path as everything else.
func (l *Loader[K, V]) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.closed = true

	return nil
}

// enqueue joins key to the accumulating batch, starting one if there
// is none, and returns the call the caller waits on.
func (l *Loader[K, V]) enqueue(key K) (*call[K, V], error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil, ErrClosed
	}

	l.waiting++

	if c, ok := l.pending[key]; ok {
		c.batch.waiters++

		return c, nil
	}

	if l.batch == nil {
		l.batch = l.arm()
	}

	b := l.batch
	c := &call[K, V]{batch: b, done: make(chan struct{})}

	l.pending[key] = c
	b.keys = append(b.keys, key)
	b.calls = append(b.calls, c)
	b.waiters++

	if len(b.keys) >= l.maxBatch {
		// Detach before releasing the lock so the next caller starts a
		// fresh batch rather than joining one already on its way.
		l.batch = nil
		close(b.fire)
	}

	return c, nil
}

// arm starts a batch and the goroutine that dispatches it. Must be
// called with l.mu held.
func (l *Loader[K, V]) arm() *batch[K, V] {
	// Background rather than any caller's context: the batch outlives
	// each individual caller and must carry none of their values. See
	// [Loader.Load].
	//
	//nolint:gosec // G118: cancel is called by dispatch and by leave, both of which the analysis cannot see from here.
	ctx, cancel := context.WithCancel(context.Background())
	b := &batch[K, V]{cancel: cancel, fire: make(chan struct{})}

	// Created here rather than in the goroutine so the window runs
	// from the first key's arrival, not from when the scheduler gets
	// round to it.
	t := l.clock.NewTimer(l.wait)

	go func() {
		defer t.Stop()

		select {
		case <-t.C():
		case <-b.fire:
		}

		l.dispatch(ctx, b)
	}()

	return b
}

// dispatch runs b's batch under ctx and delivers the outcome to its
// callers.
func (l *Loader[K, V]) dispatch(ctx context.Context, b *batch[K, V]) {
	defer b.cancel()

	l.mu.Lock()
	if l.batch == b {
		// The window elapsed rather than the batch filling up.
		l.batch = nil
	}
	l.mu.Unlock()

	got, err := l.fn(ctx, b.keys)

	// Retained results would make this a cache with a hidden TTL, so
	// the keys leave as soon as the call that owned them is done.
	l.mu.Lock()
	for _, k := range b.keys {
		delete(l.pending, k)
	}
	l.mu.Unlock()

	for i, k := range b.keys {
		c := b.calls[i]

		if err != nil {
			c.err = err
		} else if v, ok := got[k]; ok {
			c.v = v
		} else {
			c.err = fmt.Errorf("%w: %v", ErrNotFound, k)
		}

		close(c.done)
	}
}

// leave records that one caller has stopped waiting on b, abandoning
// the batch once nobody is left to serve.
func (l *Loader[K, V]) leave(b *batch[K, V]) {
	l.mu.Lock()
	l.waiting--
	b.waiters--
	last := b.waiters == 0
	l.mu.Unlock()

	if last {
		b.cancel()
	}
}
