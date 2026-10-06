// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"errors"
	"maps"
	"math"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/coretest/epochtest"
	"go.thesmos.sh/core/epoch"
)

// Parameters of the concurrent case of TestWatermark.
const (
	// watermarkClients is the number of clients that admit at once.
	watermarkClients = 8

	// watermarkAdmits is the number of admits of each client.
	watermarkAdmits = 16

	// watermarkSteps is the most admits that the Monotonic property makes
	// on one watermark.
	watermarkSteps = 16
)

// fencePair is a watermark and the fence epoch of a write, the input of a
// property of Admissible.
type fencePair struct {
	W, E epoch.Epoch
}

func TestAdmissible(t *testing.T) {
	t.Parallel()

	t.Run("admits an epoch at or above the watermark", func(t *testing.T) {
		t.Parallel()
		prop.True(t, func(p fencePair) bool { return epoch.Admissible(p.W, p.E) },
			"Admissible must admit every epoch at or above the watermark",
			prop.Using(prop.Composite(func(c *prop.Case) fencePair {
				w := c.Draw(prop.Of[epoch.Epoch](), "watermark")

				return fencePair{W: w, E: c.Draw(prop.Integer[epoch.Epoch](w, math.MaxUint64), "epoch")}
			})))
	})

	t.Run("admits Zero against every watermark", func(t *testing.T) {
		t.Parallel()
		prop.True(t, func(w epoch.Epoch) bool { return epoch.Admissible(w, epoch.Zero) },
			"Admissible must admit an unfenced write")
	})

	t.Run("refuses a non-zero epoch below the watermark", func(t *testing.T) {
		t.Parallel()
		prop.False(t, func(p fencePair) bool { return epoch.Admissible(p.W, p.E) },
			"Admissible must refuse every non-zero epoch below the watermark",
			prop.Using(prop.Composite(func(c *prop.Case) fencePair {
				w := c.Draw(prop.Integer[epoch.Epoch](2, math.MaxUint64), "watermark")

				return fencePair{W: w, E: c.Draw(prop.Integer[epoch.Epoch](1, w-1), "epoch")}
			})))
	})
}

func TestWatermark(t *testing.T) {
	t.Parallel()

	t.Run("admits and advances above the seed", func(t *testing.T) {
		t.Parallel()

		w := epoch.NewWatermark(3)

		assert.NoError(t, w.Admit(5), "a later epoch must be admitted")
		assert.Equal(t, w.Current(), epoch.Epoch(5),
			"an admitted later epoch must advance the watermark")
	})

	t.Run("admits an equal epoch without moving the watermark", func(t *testing.T) {
		t.Parallel()

		// Admit-equal is load-bearing: one tenure performs many
		// writes, and a multi-write operation reuses one fence.
		w := epoch.NewWatermark(5)

		var err error
		assert.Pure(t, w.Current, func() { err = w.Admit(5) }, "an equal admit must not move the watermark")
		assert.NoError(t, err, "the current epoch must stay admitted")
	})

	t.Run("returns ErrFenced for a superseded epoch", func(t *testing.T) {
		t.Parallel()

		w := epoch.NewWatermark(5)

		var err error
		assert.Pure(t, w.Current, func() { err = w.Admit(4) }, "a fenced admit must not move the watermark")
		assert.ErrorIs(t, err, epoch.ErrFenced, "an epoch behind the watermark must be fenced")
	})

	t.Run("admits Zero without moving the watermark", func(t *testing.T) {
		t.Parallel()

		w := epoch.NewWatermark(5)

		var err error
		assert.Pure(t, w.Current, func() { err = w.Admit(epoch.Zero) },
			"an unfenced admit must not touch the watermark")
		assert.NoError(t, err, "an unfenced write must be admitted unconditionally")
	})

	t.Run("the seed is the authority after construction", func(t *testing.T) {
		t.Parallel()

		// The reseed law: a watermark reconstructed from durable
		// state admits nothing it rejected before.
		w := epoch.NewWatermark(8)

		assert.Equal(t, w.Current(), epoch.Epoch(8),
			"Current must report the seed before any admit")
		assert.ErrorIs(t, w.Admit(7), epoch.ErrFenced,
			"a zombie below the seed must be fenced from the first admit")
	})

	t.Run("never moves backwards", func(t *testing.T) {
		t.Parallel()

		prop.ForAll(t, "Current must never fall under any sequence of admits", func(c *prop.Case) {
			w := epoch.NewWatermark(c.Draw(prop.Of[epoch.Epoch](), "seed"))
			admits := c.Draw(prop.List(prop.Of[epoch.Epoch](), prop.MaxSize(watermarkSteps)), "admits")

			next := 0
			assert.Monotonic(c, w.Current, func() error {
				// A fenced admit is one of the admits of the sequence.
				_ = w.Admit(admits[next])
				next++

				return nil
			}, len(admits), "Current must never fall")
		})
	})

	t.Run("concurrent admits behave as one admit at a time", func(t *testing.T) {
		t.Parallel()

		w := epoch.NewWatermark(epoch.Zero)
		h := history.New()

		outcomes := history.Concurrently(watermarkClients, time.Minute, func(client int) (any, error) {
			var e epoch.Epoch
			for i := range watermarkAdmits {
				e = epoch.Epoch(1 + i*watermarkClients + client)
				call := h.Invoke(client, "admit", []any{e})
				call.OK(w.Admit(e))

				read := h.Invoke(client, "current", nil)
				read.OK(w.Current())
			}

			return e, nil
		})
		for _, o := range outcomes {
			assert.True(t, o.Finished, "every client must finish its admits")
		}

		history.Linearizable(t, h, history.Model[epoch.Epoch]{
			Init: func() epoch.Epoch { return epoch.Zero },
			Step: func(s epoch.Epoch, op history.Op) []epoch.Epoch {
				if op.Operation == "current" {
					if op.Known && op.Output != s {
						return nil
					}

					return []epoch.Epoch{s}
				}

				e := op.Args[0].(epoch.Epoch)
				next, want := e, error(nil)
				if e == epoch.Zero {
					next = s
				} else if e < s {
					next, want = s, epoch.ErrFenced
				}

				if got, _ := op.Output.(error); op.Known && !errors.Is(got, want) {
					return nil
				}

				return []epoch.Epoch{next}
			},
		}, "concurrent admits and reads must behave as a watermark under one lock")
		assert.Equal(t, w.Current(), epoch.Epoch(watermarkClients*watermarkAdmits),
			"the watermark must converge on the highest admitted epoch")
	})
}

// TestFenceAllocs checks the allocation contracts of Admissible and of a
// Watermark. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestFenceAllocs(t *testing.T) {
	t.Run("Admissible", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = epoch.Admissible(5, 6) }, 0, "Admissible must not allocate")
		assert.True(t, got, "the test must measure an admitted epoch")
	})

	t.Run("Watermark", func(t *testing.T) {
		t.Run("Admit", func(t *testing.T) {
			w := epoch.NewWatermark(epoch.Zero)

			var (
				e   epoch.Epoch
				err error
			)
			expect.MaxAllocs(t, func() {
				e++
				err = w.Admit(e)
			}, 0, "Admit must not allocate")
			assert.NoError(t, err, "the test must measure admitted epochs")
		})

		t.Run("Current", func(t *testing.T) {
			w := epoch.NewWatermark(5)

			var got epoch.Epoch
			expect.MaxAllocs(t, func() { got = w.Current() }, 0, "Current must not allocate")
			assert.Equal(t, got, epoch.Epoch(5), "the test must measure the seed")
		})
	})
}

func BenchmarkFence(b *testing.B) {
	b.Run("Admissible", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = epoch.Admissible(5, 6)
		}

		assert.True(b, got, "the benchmark must measure an admitted epoch")
	})

	b.Run("Watermark", func(b *testing.B) {
		b.Run("Admit", func(b *testing.B) {
			w := epoch.NewWatermark(epoch.Zero)

			var (
				e   epoch.Epoch
				err error
			)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				e++
				err = w.Admit(e)
			}

			assert.NoError(b, err, "the benchmark must measure admitted epochs")
		})

		b.Run("Current", func(b *testing.B) {
			w := epoch.NewWatermark(5)

			var got epoch.Epoch

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = w.Current()
			}

			assert.Equal(b, got, epoch.Epoch(5), "the benchmark must measure the seed")
		})
	})
}

// fencedStore is the reference fenced writer: a toy keyed store
// whose every write validates its handle's epoch against a durable
// watermark, atomically under one lock. It exists to be the first
// subject of epochtest.AssertFencedWriter.
type fencedStore struct {
	mu   sync.Mutex
	wm   *epoch.Watermark
	data map[string]string
}

type fencedHandle struct {
	s *fencedStore
	e epoch.Epoch
}

func (s *fencedStore) open(e epoch.Epoch) fencedHandle {
	return fencedHandle{s: s, e: e}
}

func (h fencedHandle) set(k, v string) error {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()

	if err := h.s.wm.Admit(h.e); err != nil {
		return err
	}
	h.s.data[k] = v

	return nil
}

func (h fencedHandle) clear(k string) error {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()

	if err := h.s.wm.Admit(h.e); err != nil {
		return err
	}
	delete(h.s.data, k)

	return nil
}

func TestFencedWriterConformance(t *testing.T) {
	t.Parallel()

	epochtest.AssertFencedWriter(t, epochtest.FencedSystem[fencedHandle]{
		NewScope: func() epochtest.FencedScope[fencedHandle] {
			s := &fencedStore{wm: epoch.NewWatermark(0), data: map[string]string{}}

			return epochtest.FencedScope[fencedHandle]{
				Open: func(e epoch.Epoch) (fencedHandle, error) {
					return s.open(e), nil
				},
				Supersede: func(e epoch.Epoch) {
					s.mu.Lock()
					defer s.mu.Unlock()
					_ = s.wm.Admit(e)
				},
				Snapshot: func() any {
					s.mu.Lock()
					defer s.mu.Unlock()

					return maps.Clone(s.data)
				},
			}
		},
		Writes: map[string]func(fencedHandle) error{
			"set":   func(h fencedHandle) error { return h.set("k", "v") },
			"clear": func(h fencedHandle) error { return h.clear("k") },
		},
	})
}
