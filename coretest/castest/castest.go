// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package castest provides the conformance suite for
// [go.thesmos.sh/core/cas.Store].
//
// [AssertStore] runs one subtest per law of the seam. [WithReopen] and
// [WithCrash] add the laws that only a restart can test, for a store
// over durable storage. [WithZeroAllocGet] adds the allocation law for
// a store that claims it.
package castest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/crypto/sha3"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// errInterrupted is the error a [failingReader] returns after its data.
var errInterrupted = errors.New("castest: transfer interrupted")

// Option configures a run of [AssertStore].
type Option func(*config)

// config is the result of applying every [Option] of one run.
type config struct {
	reopen       func(t *testing.T, s cas.Store) cas.Store
	crash        func(t *testing.T, s cas.Store, write func()) cas.Store
	zeroAllocGet bool
}

// WithReopen gives the suite a way to open the storage behind s again,
// as after a process restart, and adds a case that requires:
//
//   - every value whose Put or PutStream returned to be readable with
//     the same bytes;
//   - every refused Put to have stored nothing;
//   - the reopened store to be bound to the same algorithm;
//   - a second Put of a value stored before the reopen to report
//     wrote=false.
//
// reopen returns a new Store over the storage behind s. It fails t
// when it cannot open the storage.
func WithReopen(reopen func(t *testing.T, s cas.Store) cas.Store) Option {
	return func(c *config) { c.reopen = reopen }
}

// WithCrash gives the suite a way to crash the storage behind s while
// write runs, and adds a case that requires each address that write
// puts to be absent or to hold the whole value. An address whose Put or
// PutStream returned without error must hold the whole value.
//
// crash calls write once, crashes the storage at a point the adapter
// chooses, and returns after write returns. It returns a new Store over
// the storage as the crash left it, and fails t when it cannot open the
// storage.
func WithCrash(crash func(t *testing.T, s cas.Store, write func()) cas.Store) Option {
	return func(c *config) { c.crash = crash }
}

// WithZeroAllocGet adds a case that requires [cas.Store.Get] into a
// buffer with enough capacity not to allocate.
//
// The case measures with [testing.AllocsPerRun], which panics while a
// parallel test runs. The test that calls [AssertStore] with this
// option must not call t.Parallel.
func WithZeroAllocGet() Option {
	return func(c *config) { c.zeroAllocGet = true }
}

// failingReader returns its data and then err, as a transfer that fails
// part-way does.
type failingReader struct {
	err  error
	data []byte
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// decorator wraps a Store and returns it from Unwrap, as a tracing
// decorator does.
type decorator struct{ cas.Store }

func (d decorator) Unwrap() cas.Store { return d.Store }

// AssertStore runs the laws of [cas.Store] against the stores that
// newStore returns. newStore returns an empty store bound to h. Each
// case builds its own store, so no case observes the values of another.
//
// The cases are:
//
//   - Hasher reports the algorithm the store was built with, and an
//     address it computes is writable.
//   - Put and Get round-trip the value. Get appends to the caller's
//     buffer, returns the buffer unchanged when it fails, and returns
//     bytes the caller may change without changing the store.
//   - A second Put of the same bytes succeeds and reports wrote=false.
//     Of concurrent Puts of one address, exactly one reports
//     wrote=true, because callers count writes by that result.
//   - A Put whose data does not hash to its address fails as Integrity
//     and stores nothing. The suite also puts the SHA3-256 digest of the
//     data under a SHA-256 store. The two digests have the same width,
//     so only recomputing the digest rejects it.
//   - Has agrees with Get, and absence classifies as NotFound.
//   - Every method, [cas.PutStream] and [cas.GetStream] reject the zero
//     digest as Invalid.
//   - The digest of zero bytes is a valid address.
//   - Every method returns the error of a done context.
//   - Bytes written by PutStream read back through Get, and bytes
//     written by Put read back through GetStream.
//   - A PutStream that fails for a digest mismatch, a reader error or a
//     done context stores nothing, although it consumed the bytes.
//   - [cas.AsStreamer] finds a Streamer through a decorator exactly
//     when it finds one in the store.
//
// The streaming cases call [cas.PutStream] and [cas.GetStream], which
// work on every Store. A store that implements [cas.Streamer] and a
// store that leaves streaming to the buffering fallback must satisfy
// the same laws.
//
// Each [Option] adds the cases its docblock lists.
func AssertStore(t *testing.T, newStore func(h crypto.Hasher) cas.Store, options ...Option) {
	t.Helper()

	var cfg config
	for _, o := range options {
		o(&cfg)
	}

	h := sha256.New()
	payload := []byte("cas conformance payload")

	if cfg.zeroAllocGet {
		t.Run("Get into a buffer with room does not allocate", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash(payload)

			_, err := s.Put(t.Context(), d, payload)
			testkit.NoError(t, err, "Put must succeed")

			buf := make([]byte, 0, len(payload))
			ctx := t.Context()

			allocs := testing.AllocsPerRun(100, func() {
				buf, _ = s.Get(ctx, d, buf[:0])
			})
			testkit.Equal(t, allocs, float64(0),
				"Get into a buffer with room must not allocate")
			testkit.Equal(t, buf, payload, "the reused buffer must hold the value")
		})
	}

	t.Run("Hasher computes the addresses the store accepts", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		bound := s.Hasher()

		testkit.Equal(t, bound.Algorithm(), h.Algorithm(),
			"Hasher must report the algorithm the store was built with")

		wrote, err := s.Put(t.Context(), bound.Hash(payload), payload)
		testkit.NoError(t, err, "an address computed by Hasher must verify")
		testkit.True(t, wrote, "an address computed by Hasher must be writable")
	})

	t.Run("Put and Get round-trip an independent slice", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		wrote, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "a verified Put must succeed")
		testkit.True(t, wrote, "the first Put of an address must report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get of a present address must succeed")
		testkit.Equal(t, got, payload, "the round trip must be exact")

		got[0] ^= 0xFF

		again, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get must succeed after a caller mutation")
		testkit.Equal(t, again, payload,
			"a caller changing its slice must not change the store")
	})

	t.Run("Get appends to the caller's buffer", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)
		absent := h.Hash([]byte("never stored"))

		_, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "Put must succeed")

		prefix := []byte("keep-")

		got, err := s.Get(t.Context(), d, prefix)
		testkit.NoError(t, err, "Get into a non-empty buffer must succeed")
		testkit.Equal(t, string(got), "keep-"+string(payload),
			"Get must append to dst and keep its contents")

		back, err := s.Get(t.Context(), absent, prefix)
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"an absent address on Get must classify as NotFound")
		testkit.Equal(t, string(back), "keep-",
			"a failed Get must return dst unchanged")
	})

	t.Run("a second Put of identical bytes reports wrote=false", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		_, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "the first Put must succeed")

		wrote, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "a duplicate Put must not fail")
		testkit.False(t, wrote, "a duplicate Put must report wrote=false")
	})

	t.Run("a wrong digest is Integrity and stores nothing", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash([]byte("other bytes entirely"))

		wrote, err := s.Put(t.Context(), d, payload)
		testkit.False(t, wrote, "a failed Put must not report wrote")
		testkit.Equal(t, errs.Classify(err), errs.Integrity,
			"data that does not hash to its address must classify as Integrity")

		ok, err := s.Has(t.Context(), d)
		testkit.NoError(t, err, "Has must succeed after a rejected Put")
		testkit.False(t, ok, "a rejected Put must store nothing")
	})

	t.Run("a digest from another algorithm is Integrity", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		foreign := sha3.New256().Hash(payload)

		wrote, err := s.Put(t.Context(), foreign, payload)
		testkit.False(t, wrote, "a foreign address must not be written")
		testkit.Equal(t, errs.Classify(err), errs.Integrity,
			"an address from another algorithm must fail verification")

		ok, err := s.Has(t.Context(), foreign)
		testkit.NoError(t, err, "Has must succeed after the rejection")
		testkit.False(t, ok, "nothing may be stored under a foreign address")
	})

	t.Run("Has agrees with Get on presence and absence", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		present := h.Hash(payload)
		absent := h.Hash([]byte("never stored"))

		_, err := s.Put(t.Context(), present, payload)
		testkit.NoError(t, err, "Put must succeed")

		ok, err := s.Has(t.Context(), present)
		testkit.NoError(t, err, "Has of a present address must succeed")
		testkit.True(t, ok, "Has must report a stored address")

		ok, err = s.Has(t.Context(), absent)
		testkit.NoError(t, err, "Has of an absent address must succeed")
		testkit.False(t, ok, "Has must not report an absent address")

		_, err = s.Get(t.Context(), absent, nil)
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"an absent address on Get must classify as NotFound")
	})

	t.Run("every method rejects the zero digest as Invalid", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)

		var zero crypto.Digest

		_, perr := s.Put(t.Context(), zero, payload)
		testkit.Equal(t, errs.Classify(perr), errs.Invalid,
			"Put of the zero digest must classify as Invalid")

		_, gerr := s.Get(t.Context(), zero, nil)
		testkit.Equal(t, errs.Classify(gerr), errs.Invalid,
			"Get of the zero digest must classify as Invalid")

		_, herr := s.Has(t.Context(), zero)
		testkit.Equal(t, errs.Classify(herr), errs.Invalid,
			"Has of the zero digest must classify as Invalid")

		_, serr := cas.PutStream(t.Context(), s, zero, bytes.NewReader(payload))
		testkit.Equal(t, errs.Classify(serr), errs.Invalid,
			"PutStream of the zero digest must classify as Invalid")

		_, oerr := cas.GetStream(t.Context(), s, zero)
		testkit.Equal(t, errs.Classify(oerr), errs.Invalid,
			"GetStream of the zero digest must classify as Invalid")
	})

	t.Run("the digest of zero bytes is a valid address", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(nil)

		wrote, err := s.Put(t.Context(), d, nil)
		testkit.NoError(t, err, "the digest of zero bytes must verify")
		testkit.True(t, wrote, "the first Put of the empty value must write")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get of the empty value must succeed")
		testkit.Len(t, got, 0, "the empty value must round-trip empty")
	})

	t.Run("every method returns the error of a done context", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := s.Put(ctx, d, payload)
		testkit.ErrorIs(t, err, context.Canceled, "Put must return the context's error")

		_, err = s.Get(ctx, d, nil)
		testkit.ErrorIs(t, err, context.Canceled, "Get must return the context's error")

		_, err = s.Has(ctx, d)
		testkit.ErrorIs(t, err, context.Canceled, "Has must return the context's error")

		_, err = cas.GetStream(ctx, s, d)
		testkit.ErrorIs(t, err, context.Canceled,
			"GetStream must return the context's error")
	})

	t.Run("concurrent Puts of one address write exactly once", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		var wrote atomic.Int64
		err := putConcurrently(t.Context(), func(ctx context.Context) (bool, error) {
			return s.Put(ctx, d, payload)
		}, &wrote)
		testkit.NoError(t, err, "concurrent identical Puts must all succeed")
		testkit.Equal(t, wrote.Load(), int64(1),
			"exactly one concurrent Put must report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "the value must be readable after the race")
		testkit.Equal(t, got, payload, "the raced value must be intact")
	})

	t.Run("the streamed and whole-value paths agree", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		wrote, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
		testkit.NoError(t, err, "a verified streamed Put must succeed")
		testkit.True(t, wrote, "the first streamed Put of an address must report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get must read what PutStream wrote")
		testkit.Equal(t, got, payload, "PutStream then Get must round-trip exactly")

		other := []byte("written whole, read streamed")
		od := h.Hash(other)

		_, err = s.Put(t.Context(), od, other)
		testkit.NoError(t, err, "Put must succeed")

		rc, err := cas.GetStream(t.Context(), s, od)
		testkit.NoError(t, err, "GetStream must open what Put wrote")

		body, err := io.ReadAll(rc)
		testkit.NoError(t, err, "the stream must drain")
		testkit.NoError(t, rc.Close(), "the stream must close")
		testkit.Equal(t, body, other, "Put then GetStream must round-trip exactly")
	})

	t.Run("a second streamed Put of identical bytes reports wrote=false", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		_, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
		testkit.NoError(t, err, "the first streamed Put must succeed")

		wrote, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
		testkit.NoError(t, err, "a duplicate streamed Put must not fail")
		testkit.False(t, wrote, "a duplicate streamed Put must report wrote=false")
	})

	t.Run("a failed streamed Put stores nothing", func(t *testing.T) {
		t.Parallel()

		assertAbsent := func(t *testing.T, s cas.Store, d crypto.Digest) {
			t.Helper()

			ok, err := s.Has(t.Context(), d)
			testkit.NoError(t, err, "Has must succeed after a failed streamed Put")
			testkit.False(t, ok, "a failed streamed Put must store nothing")
		}

		t.Run("bytes that do not hash to the address", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash([]byte("other bytes entirely"))

			wrote, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
			testkit.False(t, wrote, "a failed streamed Put must not report wrote")
			testkit.Equal(t, errs.Classify(err), errs.Integrity,
				"streamed bytes that do not hash to their address must classify as Integrity")
			assertAbsent(t, s, d)
		})

		t.Run("a reader that fails part-way", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash(payload)

			_, err := cas.PutStream(t.Context(), s, d,
				&failingReader{data: payload[:5], err: errInterrupted})
			testkit.ErrorIs(t, err, errInterrupted, "PutStream must return the reader's error")
			assertAbsent(t, s, d)
		})

		t.Run("a done context", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash(payload)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := cas.PutStream(ctx, s, d, bytes.NewReader(payload))
			testkit.ErrorIs(t, err, context.Canceled, "PutStream must return the context's error")
			assertAbsent(t, s, d)
		})
	})

	t.Run("concurrent streamed Puts of one address write exactly once", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		var wrote atomic.Int64
		err := putConcurrently(t.Context(), func(ctx context.Context) (bool, error) {
			return cas.PutStream(ctx, s, d, bytes.NewReader(payload))
		}, &wrote)
		testkit.NoError(t, err, "concurrent identical streamed Puts must all succeed")
		testkit.Equal(t, wrote.Load(), int64(1),
			"exactly one concurrent streamed Put must report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "the value must be readable after the race")
		testkit.Equal(t, got, payload, "the raced value must be intact")
	})

	t.Run("GetStream of an absent address is NotFound", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)

		_, err := cas.GetStream(t.Context(), s, h.Hash([]byte("never stored")))
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"an absent address on GetStream must classify as NotFound")
	})

	t.Run("AsStreamer finds a Streamer through a decorator", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		_, direct := cas.AsStreamer(s)
		_, decorated := cas.AsStreamer(decorator{s})

		testkit.Equal(t, decorated, direct,
			"AsStreamer must find a Streamer through a decorator exactly when the store has one")
	})

	if cfg.reopen != nil {
		t.Run("a reopened store keeps every write that returned", func(t *testing.T) {
			t.Parallel()

			s := newStore(h)
			d := h.Hash(payload)
			streamed := []byte("streamed before the reopen")
			sd := h.Hash(streamed)
			refused := h.Hash([]byte("other bytes entirely"))

			_, err := s.Put(t.Context(), d, payload)
			testkit.NoError(t, err, "Put must succeed")

			_, err = cas.PutStream(t.Context(), s, sd, bytes.NewReader(streamed))
			testkit.NoError(t, err, "PutStream must succeed")

			_, err = s.Put(t.Context(), refused, payload)
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "the mismatched Put must fail")

			r := cfg.reopen(t, s)

			testkit.Equal(t, r.Hasher().Algorithm(), h.Algorithm(),
				"the reopened store must be bound to the same algorithm")

			got, err := r.Get(t.Context(), d, nil)
			testkit.NoError(t, err, "a value put before the reopen must be readable after it")
			testkit.Equal(t, got, payload, "a value put before the reopen must keep its bytes")

			got, err = r.Get(t.Context(), sd, nil)
			testkit.NoError(t, err, "a value streamed before the reopen must be readable after it")
			testkit.Equal(t, got, streamed, "a value streamed before the reopen must keep its bytes")

			ok, err := r.Has(t.Context(), refused)
			testkit.NoError(t, err, "Has must succeed after the reopen")
			testkit.False(t, ok, "a refused Put must have stored nothing")

			wrote, err := r.Put(t.Context(), d, payload)
			testkit.NoError(t, err, "a second Put after the reopen must succeed")
			testkit.False(t, wrote,
				"a second Put of a value stored before the reopen must report wrote=false")
		})
	}

	if cfg.crash != nil {
		t.Run("a crash leaves each address absent or whole", func(t *testing.T) {
			t.Parallel()

			s := newStore(h)
			d := h.Hash(payload)
			streamed := []byte("streamed during the crash")
			sd := h.Hash(streamed)

			var putErr, streamErr error
			r := cfg.crash(t, s, func() {
				_, putErr = s.Put(t.Context(), d, payload)
				_, streamErr = cas.PutStream(t.Context(), s, sd, bytes.NewReader(streamed))
			})

			assertAfterCrash := func(d crypto.Digest, want []byte, err error) {
				t.Helper()

				got, gerr := r.Get(t.Context(), d, nil)
				if err != nil && errs.Classify(gerr) == errs.NotFound {
					return
				}
				testkit.NoError(t, gerr, "an address whose write returned must be readable after the crash")
				testkit.Equal(t, got, want, "an address present after the crash must hold the whole value")
			}

			assertAfterCrash(d, payload, putErr)
			assertAfterCrash(sd, streamed, streamErr)
		})
	}
}

// putConcurrently calls put from 16 goroutines at once, adds one to
// wrote for each call that reports wrote=true, and returns the first
// error. The error returns to the test goroutine, because FailNow must
// not run on the goroutine of a Put.
func putConcurrently(ctx context.Context, put func(context.Context) (bool, error), wrote *atomic.Int64) error {
	return task.Run(ctx, 16, func(_ context.Context, g *task.Group) error {
		for range 16 {
			if err := g.Go(func(ctx context.Context) error {
				ok, err := put(ctx)
				if ok {
					wrote.Add(1)
				}

				return err
			}); err != nil {
				return err
			}
		}

		return nil
	})
}
