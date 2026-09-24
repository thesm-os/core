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

// TestMemoryStoreConformance runs the conformance suite with every
// option. The storage of a memory Store is the Store itself, so a
// reopen returns it unchanged and a crash comes after write returns.
// The test does not call t.Parallel, because [castest.WithZeroAllocGet]
// measures with testing.AllocsPerRun.
//
//nolint:paralleltest // see comment above
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

// TestPut covers a contract of the memory Store beyond the seam. Put
// verified the bytes against the address, so a caller that changes its
// slice after Put must not change what later readers receive.
func TestPut(t *testing.T) {
	t.Parallel()

	t.Run("clones its input", func(t *testing.T) {
		t.Parallel()

		h := sha256.New()
		s := memory.New(h)

		data := []byte("verified at put time")
		d := h.Hash(data)

		wrote, err := s.Put(t.Context(), d, data)
		testkit.NoError(t, err, "Put must succeed")
		testkit.True(t, wrote, "the first Put must write")

		data[0] ^= 0xFF

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get must succeed after the caller changed its slice")
		testkit.Equal(t, got, []byte("verified at put time"),
			"a caller changing its input after Put must not change the store")
		testkit.True(t, h.Hash(got).Equal(d),
			"the bytes the store serves must still hash to their address")
	})
}

// TestPutStream covers a contract of the memory Store beyond the seam.
// Bytes can verify and still belong to a caller whose context ended
// during the read, so PutStream checks the context again after the
// read.
func TestPutStream(t *testing.T) {
	t.Parallel()

	t.Run("a transfer that outlives its context does not commit", func(t *testing.T) {
		t.Parallel()

		h := sha256.New()
		s := memory.New(h)

		data := []byte("cancelled during the transfer")
		d := h.Hash(data)

		ctx, cancel := context.WithCancel(t.Context())

		wrote, err := s.PutStream(ctx, d,
			cancelOnRead{Reader: bytes.NewReader(data), cancel: cancel})
		testkit.ErrorIs(t, err, context.Canceled, "PutStream must return the context's error")
		testkit.False(t, wrote, "a cancelled transfer must not report wrote")

		ok, err := s.Has(t.Context(), d)
		testkit.NoError(t, err, "Has must succeed after the cancellation")
		testkit.False(t, ok, "a cancelled transfer must store nothing")
	})
}

// gatedReader closes entered on its first Read and then waits for
// release, so a test can commit a competing write during a transfer.
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

// cancelOnRead cancels its context on every Read, so a transfer ends
// after its caller has given up.
type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (c cancelOnRead) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.cancel()

	return n, err //nolint:wrapcheck // the wrapped reader's result passes through
}

// TestPutStreamRace covers a contract of the memory Store beyond the
// seam. The presence check before the read saves the transfer of a
// duplicate. The presence check under the lock lets exactly one of two
// racing writes report wrote=true, and only a transfer held open while
// a competing write commits reaches it.
func TestPutStreamRace(t *testing.T) {
	t.Parallel()

	t.Run("a transfer that loses the race reports wrote=false", func(t *testing.T) {
		t.Parallel()

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

		testkit.NoError(t, err, "losing the race must not be an error")
		testkit.False(t, wrote, "the loser must not report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "the value must be readable")
		testkit.Equal(t, got, data, "the winner's bytes must be intact")
	})
}
