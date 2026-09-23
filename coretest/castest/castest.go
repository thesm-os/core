// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package castest holds the conformance suite for
// [go.thesmos.sh/core/cas.Store]: the verification MUST, idempotent
// writes with an exactly-once wrote signal, absence and zero-digest
// classification, rejection without mutation, and the streaming
// laws. Hand-rolled; nothing here is generated.
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

// failingReader yields its data and then fails, standing in for a
// transfer interrupted mid-stream.
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

// AssertStore drives a [cas.Store] implementation through the CAS
// laws. newStore must return an empty store bound to the given
// hasher; the suite constructs a fresh one per case, so cases
// cannot observe each other's contents.
//
// The suite verifies with SHA-256 as the bound algorithm and uses
// SHA3-256 — same digest width, different function — to prove the
// store rejects addresses from a foreign algorithm rather than
// storing under a foreign address space.
//
// The streaming laws are stated against [cas.PutStream] and
// [cas.GetStream] rather than against [cas.Streamer]. Those
// functions work on every Store, so a store that implements the
// capability is held to the same laws as one that leaves it to the
// buffering fallback, and neither has to be detected.
func AssertStore(t *testing.T, newStore func(h crypto.Hasher) cas.Store) {
	t.Helper()

	h := sha256.New()
	payload := []byte("cas conformance payload")

	t.Run("Hasher mints the addresses the store accepts", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		bound := s.Hasher()

		testkit.Equal(t, bound.Algorithm(), h.Algorithm(),
			"Hasher must report the algorithm the store was constructed with")

		wrote, err := s.Put(t.Context(), bound.Hash(payload), payload)
		testkit.NoError(t, err, "an address minted from Hasher must verify")
		testkit.True(t, wrote, "an address minted from Hasher must be writable")
	})

	t.Run("put-get round-trips an independent slice", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		wrote, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "a verified Put must succeed")
		testkit.True(t, wrote, "the first Put of an address must report wrote")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get of a present address must succeed")
		testkit.Equal(t, got, payload, "the round trip must be exact")

		// The returned slice is the caller's: mutating it must not
		// affect what a later reader observes.
		got[0] ^= 0xFF

		again, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get must succeed after a caller mutation")
		testkit.Equal(t, again, payload,
			"a caller mutating its slice must not reach the store")
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
			"Get must append to dst rather than replace it")

		back, err := s.Get(t.Context(), absent, prefix)
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"an absent address on Get must classify as NotFound")
		testkit.Equal(t, string(back), "keep-",
			"a failed Get must return dst unchanged")
	})

	t.Run("re-put of identical bytes is an idempotent no-op", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		_, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "the first Put must succeed")

		wrote, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "a duplicate Put must not error")
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

	t.Run("a foreign algorithm's digest is Integrity", func(t *testing.T) {
		t.Parallel()

		// SHA3-256 of the same bytes: same width, different
		// function. Size cannot discriminate algorithm; only
		// recomputation can.
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

	t.Run("the zero digest is Invalid on every method", func(t *testing.T) {
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

	t.Run("empty data is a legal value", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(nil)

		wrote, err := s.Put(t.Context(), d, nil)
		testkit.NoError(t, err, "the digest of zero bytes is a valid address")
		testkit.True(t, wrote, "the first Put of the empty value must write")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "Get of the empty value must succeed")
		testkit.Len(t, got, 0, "the empty value must round-trip empty")
	})

	t.Run("a done context surfaces on every method", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := s.Put(ctx, d, payload)
		testkit.ErrorIs(t, err, context.Canceled, "a cancelled Put must report the context")

		_, err = s.Get(ctx, d, nil)
		testkit.ErrorIs(t, err, context.Canceled, "a cancelled Get must report the context")

		_, err = s.Has(ctx, d)
		testkit.ErrorIs(t, err, context.Canceled, "a cancelled Has must report the context")

		_, err = cas.GetStream(ctx, s, d)
		testkit.ErrorIs(t, err, context.Canceled,
			"a cancelled GetStream must report the context")
	})

	t.Run("concurrent puts of one address write exactly once", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		// The error comes back to the test goroutine, because FailNow
		// must not run on the goroutine of a Put.
		var wrote atomic.Int64
		err := task.Run(t.Context(), 16, func(_ context.Context, g *task.Group) error {
			for range 16 {
				if err := g.Go(func(ctx context.Context) error {
					ok, err := s.Put(ctx, d, payload)
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
		testkit.NoError(t, err, "concurrent identical Puts must all succeed")

		testkit.Equal(t, wrote.Load(), int64(1),
			"exactly one concurrent Put may report wrote — it is an accounting signal")

		got, err := s.Get(t.Context(), d, nil)
		testkit.NoError(t, err, "the value must be readable after the race")
		testkit.Equal(t, got, payload, "the raced value must be intact")
	})

	t.Run("the streamed and whole-value paths agree", func(t *testing.T) {
		t.Parallel()

		// The law that keeps a store's two paths from drifting: bytes
		// written through one must be readable through the other.
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

	t.Run("a duplicate streamed put is an idempotent no-op", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		_, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
		testkit.NoError(t, err, "the first streamed Put must succeed")

		wrote, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
		testkit.NoError(t, err, "a duplicate streamed Put must not error")
		testkit.False(t, wrote, "a duplicate streamed Put must report wrote=false")
	})

	t.Run("a streamed put leaves nothing behind on failure", func(t *testing.T) {
		t.Parallel()

		// A streaming implementation has consumed the bytes by the
		// time it can check them, so "stores nothing" is a law about
		// what it does with a transfer it cannot commit.
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

		t.Run("a reader that fails mid-stream", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash(payload)
			boom := errors.New("castest: transfer interrupted")

			_, err := cas.PutStream(t.Context(), s, d,
				&failingReader{data: payload[:5], err: boom})
			testkit.ErrorIs(t, err, boom, "the reader's own error must surface")
			assertAbsent(t, s, d)
		})

		t.Run("a done context", func(t *testing.T) {
			s := newStore(h)
			d := h.Hash(payload)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := cas.PutStream(ctx, s, d, bytes.NewReader(payload))
			testkit.ErrorIs(t, err, context.Canceled, "the context must surface")
			assertAbsent(t, s, d)
		})
	})

	t.Run("concurrent streamed puts of one address write exactly once", func(t *testing.T) {
		t.Parallel()

		s := newStore(h)
		d := h.Hash(payload)

		var wrote atomic.Int64
		err := task.Run(t.Context(), 16, func(_ context.Context, g *task.Group) error {
			for range 16 {
				if err := g.Go(func(ctx context.Context) error {
					ok, err := cas.PutStream(ctx, s, d, bytes.NewReader(payload))
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
		testkit.NoError(t, err, "concurrent identical streamed Puts must all succeed")

		testkit.Equal(t, wrote.Load(), int64(1),
			"exactly one concurrent streamed Put may report wrote")

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
}
