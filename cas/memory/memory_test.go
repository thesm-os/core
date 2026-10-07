// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/coretest/castest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
)

// payload is the value of the cases that store one value.
const payload = "verified at put time"

// putStreamAllocs is the number of allocations of a PutStream of payload
// under a new address: the staging buffer of io.ReadAll, the reader that
// io.TeeReader returns, and the growth of the map of a new store.
const putStreamAllocs = 3

// gatedReader closes entered at its first Read and then waits for
// release, so a case can commit a competing write during a transfer.
type gatedReader struct {
	io.Reader

	entered chan struct{}
	release chan struct{}
	first   bool
}

// Read closes entered and waits for release at the first call, and then
// reads from the wrapped reader.
func (g *gatedReader) Read(p []byte) (int, error) {
	if !g.first {
		g.first = true
		close(g.entered)
		<-g.release
	}

	return g.Reader.Read(p) //nolint:wrapcheck // the wrapped reader's result passes through
}

// cancelOnRead cancels its context at every Read, so a transfer ends
// after its caller gave up.
type cancelOnRead struct {
	io.Reader

	cancel context.CancelFunc
}

// Read reads from the wrapped reader and cancels the context.
func (c cancelOnRead) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.cancel()

	return n, err //nolint:wrapcheck // the wrapped reader's result passes through
}

// TestMemoryStoreConformance runs the conformance suite with every
// option. The storage of a memory Store is the Store itself, so a reopen
// returns it unchanged and a crash comes after write returns. The test
// does not call t.Parallel, because [castest.WithZeroAllocGet] measures
// with testing.AllocsPerRun.
//
//nolint:paralleltest // see above
func TestMemoryStoreConformance(t *testing.T) {
	castest.AssertStore(t,
		func(h crypto.Hasher) cas.Store { return memory.New(h) },
		castest.WithReopen(func(_ *testing.T, s cas.Store) cas.Store { return s }),
		castest.WithCrash(func(_ *testing.T, s cas.Store, write func()) cas.Store {
			write()

			return s
		}),
		castest.WithZeroAllocGet(),
	)
}

func TestStore(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	d := h.Hash([]byte(payload))

	t.Run("Put", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the stored bytes when the caller changes its slice", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			data := []byte(payload)
			wrote, err := s.Put(t.Context(), d, data)
			assert.NoError(t, err, "Put must succeed")
			assert.True(t, wrote, "the first Put must write")
			data[0] ^= 0xFF
			got, err := s.Get(t.Context(), d, nil)
			assert.NoError(t, err, "Get must succeed after the caller changed its slice")
			assert.Equal(t, string(got), payload, "a change of the caller's slice must not reach the store")
		})
	})

	t.Run("PutStream", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of a ctx that ends during the transfer", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			ctx, cancel := context.WithCancel(t.Context())
			wrote, err := s.PutStream(ctx, d, cancelOnRead{Reader: bytes.NewReader([]byte(payload)), cancel: cancel})
			expect.ErrorIs(t, err, context.Canceled, "PutStream must return the context's error")
			expect.False(t, wrote, "a cancelled transfer must not report wrote")
		})

		t.Run("stores nothing for a transfer that outlives its ctx", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			ctx, cancel := context.WithCancel(t.Context())
			_, _ = s.PutStream(ctx, d, cancelOnRead{Reader: bytes.NewReader([]byte(payload)), cancel: cancel})
			ok, err := s.Has(t.Context(), d)
			assert.NoError(t, err, "Has must succeed after the cancellation")
			assert.False(t, ok, "a cancelled transfer must store nothing")
		})

		t.Run("returns the error of a done ctx for a present address", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			_, err := s.Put(t.Context(), d, []byte(payload))
			assert.NoError(t, err, "Put must succeed")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := s.PutStream(ctx, d, bytes.NewReader([]byte(payload)))

				return err
			}, "PutStream must read ctx before the presence check")
		})

		t.Run("leaves the reader of a present address unread", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			_, err := s.Put(t.Context(), d, []byte(payload))
			assert.NoError(t, err, "Put must succeed")
			r := bytes.NewReader([]byte(payload))
			wrote, err := s.PutStream(t.Context(), d, r)
			assert.NoError(t, err, "a duplicate PutStream must not fail")
			expect.False(t, wrote, "a duplicate PutStream must report wrote=false")
			expect.Equal(t, r.Len(), len(payload), "PutStream must not read a duplicate")
		})

		t.Run("reports wrote=false for a transfer that a Put overtakes", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				s := memory.New(h)
				r := &gatedReader{
					Reader:  bytes.NewReader([]byte(payload)),
					entered: make(chan struct{}),
					release: make(chan struct{}),
				}
				var wrote bool
				var err error
				done := make(chan struct{})
				go func() {
					defer close(done)
					wrote, err = s.PutStream(t.Context(), d, r)
				}()
				<-r.entered
				won, perr := s.Put(t.Context(), d, []byte(payload))
				assert.NoError(t, perr, "the competing Put must succeed")
				assert.True(t, won, "the competing Put must be the one that wrote")
				close(r.release)
				<-done
				expect.NoError(t, err, "a transfer that loses the race must not fail")
				expect.False(t, wrote, "the transfer that loses the race must not report wrote")
			})
		})
	})

	t.Run("GetStream", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves the store unlocked", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			_, err := s.Put(t.Context(), d, []byte(payload))
			assert.NoError(t, err, "Put must succeed")
			rc, err := s.GetStream(t.Context(), d)
			assert.NoError(t, err, "GetStream must succeed")
			assert.CompletesWithin(t, time.Second, func(ctx context.Context) error {
				_, err := s.Has(ctx, d)

				return err
			}, "a call after GetStream must not wait for the lock")
			assert.NoError(t, rc.Close(), "the reader must close")
		})
	})
}

// TestStoreAllocs checks the allocation contract of the methods of a
// Store. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestStoreAllocs(t *testing.T) {
	h := sha256.New()
	d := h.Hash([]byte(payload))
	s := memory.New(h)
	ctx := t.Context()
	_, err := s.Put(ctx, d, []byte(payload))
	assert.NoError(t, err, "Put must succeed")

	t.Run("Get", func(t *testing.T) {
		dst := make([]byte, 0, len(payload))
		var got []byte
		expect.MaxAllocs(t, func() { got, err = s.Get(ctx, d, dst[:0]) }, 0,
			"Get into a buffer with room must not allocate")
		assert.NoError(t, err, "the test must measure a Get that succeeds")
		assert.Equal(t, string(got), payload, "the test must measure a read of the value")
	})

	t.Run("Has", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { ok, err = s.Has(ctx, d) }, 0, "Has must not allocate")
		assert.True(t, ok, "the test must measure a present address")
	})

	t.Run("GetStream", func(t *testing.T) {
		var rc io.ReadCloser
		expect.MaxAllocs(t, func() { rc, err = s.GetStream(ctx, d) }, 1, "GetStream must allocate only the reader")
		assert.NoError(t, err, "the test must measure a GetStream that succeeds")
		assert.NoError(t, rc.Close(), "the reader must close")
	})

	t.Run("PutStream", func(t *testing.T) {
		data, r := []byte(payload), bytes.NewReader(nil)
		var wrote bool
		expect.MaxAllocs(t, func() {
			r.Reset(data)
			wrote, err = s.PutStream(ctx, d, r)
		}, 0, "a PutStream of a present address must not allocate")
		assert.False(t, wrote, "the test must measure a present address")
		expect.MaxAllocsWithSetup(t, func() *memory.Store {
			r.Reset(data)

			return memory.New(h)
		}, func(fresh *memory.Store) {
			wrote, err = fresh.PutStream(ctx, d, r)
		}, putStreamAllocs, "a PutStream of a new address must allocate the staged value")
		assert.True(t, wrote, "the test must measure a PutStream that writes")
	})
}

// BenchmarkStore reports the cost of the methods of a Store on a short
// value, and fails when a method allocates more than its allocation
// contract allows.
func BenchmarkStore(b *testing.B) {
	h := sha256.New()
	d := h.Hash([]byte(payload))
	s := memory.New(h)
	ctx := b.Context()
	_, err := s.Put(ctx, d, []byte(payload))
	assert.NoError(b, err, "Put must succeed")

	b.Run("Get", func(b *testing.B) {
		dst := make([]byte, 0, len(payload))
		var got []byte
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = s.Get(ctx, d, dst[:0])
		}

		assert.NoError(b, err, "the benchmark must measure a Get that succeeds")
		assert.Equal(b, string(got), payload, "the benchmark must measure a read of the value")
	})

	b.Run("Has", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok, _ = s.Has(ctx, d)
		}

		assert.True(b, ok, "the benchmark must measure a present address")
	})

	b.Run("GetStream", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = s.GetStream(ctx, d)
		}

		assert.NoError(b, err, "the benchmark must measure a GetStream that succeeds")
	})

	b.Run("PutStream", func(b *testing.B) {
		b.Run("of a present address", func(b *testing.B) {
			data, r := []byte(payload), bytes.NewReader(nil)
			wrote := true

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				r.Reset(data)
				wrote, _ = s.PutStream(ctx, d, r)
			}

			assert.False(b, wrote, "the benchmark must measure a present address")
		})
	})
}
