// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/coretest/castest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
)

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	castest.AssertStore(t, func(h crypto.Hasher) cas.Store {
		return memory.New(h)
	})
}

func TestPut(t *testing.T) {
	t.Parallel()

	t.Run("clones its input", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: the bytes were
		// verified against the address at put time, so a caller
		// mutating its slice afterwards must not be able to corrupt
		// what future readers receive.
		h := sha256.New()
		s := memory.New(h)

		data := []byte("verified at put time")
		d := h.Hash(data)

		wrote, err := s.Put(t.Context(), d, data)
		testkit.NoError(t, err, "Put must succeed")
		testkit.True(t, wrote, "the first Put must write")

		data[0] ^= 0xFF

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get must succeed after the caller mutation")
		testkit.Equal(t, got, []byte("verified at put time"),
			"a caller mutating its input after Put must not reach the store")
		testkit.True(t, h.Hash(got).Equal(d),
			"what the store serves must still hash to its address")
	})
}

// TestGet does not run in parallel, and neither does its subtest.
// [testing.AllocsPerRun] panics when any parallel test is in flight,
// because it pins GOMAXPROCS to one while it measures and a sibling
// beside it would be counted as the subject.
func TestPutStream(t *testing.T) {
	t.Parallel()

	t.Run("a transfer that outlives its context does not commit", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: the bytes can
		// verify perfectly and still belong to a caller who has gone
		// away, so the context is re-checked after the read rather
		// than only before it.
		h := sha256.New()
		s := memory.New(h)

		data := []byte("cancelled while in flight")
		d := h.Hash(data)

		ctx, cancel := context.WithCancel(t.Context())

		wrote, err := s.PutStream(ctx, d,
			cancelOnRead{Reader: bytes.NewReader(data), cancel: cancel})
		testkit.ErrorIs(t, err, context.Canceled, "the context must surface")
		testkit.False(t, wrote, "a cancelled transfer must not report wrote")

		ok, err := s.Has(t.Context(), d)
		testkit.NoError(t, err, "Has must succeed after the cancellation")
		testkit.False(t, ok, "a cancelled transfer must store nothing")
	})
}

// gatedReader announces that it has been entered and then waits, so
// a test can commit a competing write while a transfer is in flight.
type gatedReader struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedReader) Read(p []byte) (int, error) {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})

	return g.Reader.Read(p) //nolint:wrapcheck // the wrapped reader's result passes through
}

// cancelOnRead cancels its context as soon as it is read from, so a
// transfer can be made to finish after its caller has given up.
type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (c cancelOnRead) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.cancel()

	return n, err //nolint:wrapcheck // the wrapped reader's result passes through
}

func TestPutStreamRace(t *testing.T) {
	t.Parallel()

	t.Run("a transfer that loses the race reports wrote=false", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: the presence check
		// before the read is an optimisation, and the one under the
		// lock is the correctness. Only a transfer held open while a
		// competing write commits can reach the second.
		h := sha256.New()
		s := memory.New(h)

		data := []byte("raced into the map")
		d := h.Hash(data)

		r := &gatedReader{
			Reader:  bytes.NewReader(data),
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}

		var (
			wrote bool
			err   error
			done  = make(chan struct{})
		)
		go func() {
			defer close(done)
			wrote, err = s.PutStream(t.Context(), d, r)
		}()

		select {
		case <-r.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("PutStream did not reach its read")
		}

		won, perr := s.Put(t.Context(), d, data)
		testkit.NoError(t, perr, "the competing Put must succeed")
		testkit.True(t, won, "the competing Put must be the one that wrote")

		close(r.release)

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("PutStream did not return after the reader was released")
		}

		testkit.NoError(t, err, "losing the race is not an error")
		testkit.False(t, wrote, "the loser must not report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "the value must be readable")
		testkit.Equal(t, got, data, "the winner's bytes must be intact")
	})
}

//nolint:paralleltest // AllocsPerRun panics when a parallel test is in flight.
func TestGet(t *testing.T) {
	t.Run("reads into a reused buffer without allocating", func(t *testing.T) {
		// Implementation contract beyond the seam: the append shape
		// exists so a caller reading at rate allocates nothing. A
		// store that copied into a fresh slice would satisfy the seam
		// and lose the reason the signature takes a buffer.
		h := sha256.New()
		s := memory.New(h)

		data := []byte("read at rate")
		d := h.Hash(data)

		_, err := s.Put(t.Context(), d, data)
		testkit.NoError(t, err, "Put must succeed")

		buf := make([]byte, 0, len(data))
		ctx := t.Context()

		allocs := testing.AllocsPerRun(100, func() {
			buf, _ = s.Get(ctx, d, buf[:0])
		})

		testkit.Equal(t, allocs, float64(0),
			"Get into a buffer with room must not allocate")
		testkit.Equal(t, buf, data, "the reused buffer must hold the value")
	})
}
