// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cas_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
)

// wholeValueOnly embeds the Store interface and has no Unwrap, so
// [cas.AsStreamer] cannot see a Streamer behind it, and [cas.PutStream]
// and [cas.GetStream] take their buffering branch. [memory.Store]
// implements [cas.Streamer] natively, which covers the other branch.
type wholeValueOnly struct{ cas.Store }

// decorated wraps a store and returns it from Unwrap, as a tracing
// decorator does.
type decorated struct{ cas.Store }

func (d decorated) Unwrap() cas.Store { return d.Store }

// selfStreaming is a decorator that implements [cas.Streamer] itself,
// by delegating to the Streamer it wraps.
type selfStreaming struct {
	decorated

	inner cas.Streamer
}

func (s selfStreaming) PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
	return s.inner.PutStream(ctx, d, r) //nolint:wrapcheck // the test double passes the error through
}

func (s selfStreaming) GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error) {
	return s.inner.GetStream(ctx, d) //nolint:wrapcheck // the test double passes the error through
}

func TestAsStreamer(t *testing.T) {
	t.Parallel()

	h := sha256.New()

	t.Run("finds a Streamer on the store itself", func(t *testing.T) {
		t.Parallel()
		s := memory.New(h)
		st, ok := cas.AsStreamer(s)
		testkit.True(t, ok, "a streaming store must be found")
		testkit.True(t, st == cas.Streamer(s), "the store itself must be returned")
	})

	t.Run("finds a Streamer through two decorators", func(t *testing.T) {
		t.Parallel()
		s := memory.New(h)
		st, ok := cas.AsStreamer(decorated{decorated{s}})
		testkit.True(t, ok, "a Streamer behind decorators must be found")
		testkit.True(t, st == cas.Streamer(s), "the wrapped store must be returned")
	})

	t.Run("finds a decorator that implements Streamer before the store it wraps", func(t *testing.T) {
		t.Parallel()
		s := memory.New(h)
		outer := selfStreaming{decorated: decorated{s}, inner: s}
		st, ok := cas.AsStreamer(outer)
		testkit.True(t, ok, "the decorator must be found")
		testkit.True(t, st == cas.Streamer(outer), "the outermost Streamer must be returned")
	})

	t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
		t.Parallel()
		_, ok := cas.AsStreamer(wholeValueOnly{memory.New(h)})
		testkit.False(t, ok, "a decorator without Unwrap must hide the capability")
	})

	t.Run("reports false when the chain ends without a Streamer", func(t *testing.T) {
		t.Parallel()
		_, ok := cas.AsStreamer(decorated{wholeValueOnly{memory.New(h)}})
		testkit.False(t, ok, "a chain without a Streamer must not report one")
	})

	t.Run("reports false for a nil store and a decorator of nil", func(t *testing.T) {
		t.Parallel()
		_, ok := cas.AsStreamer(nil)
		testkit.False(t, ok, "a nil store must not report a Streamer")

		_, ok = cas.AsStreamer(decorated{})
		testkit.False(t, ok, "a decorator that wraps nothing must not report a Streamer")
	})
}

// closeRecorder reports whether it was closed, so the wrapper's
// Close can be shown to reach the reader it wraps.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true

	return nil
}

// TestAsStreamerZeroAlloc enforces the allocation contract of
// AsStreamer through two decorators. testing.AllocsPerRun reads a
// process-global malloc counter, so this test does not call
// t.Parallel.
//
//nolint:paralleltest // see comment above
func TestAsStreamerZeroAlloc(t *testing.T) {
	var s cas.Store = decorated{decorated{memory.New(sha256.New())}}

	t.Run("AsStreamer", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() { _, _ = cas.AsStreamer(s) }), float64(0),
			"AsStreamer must not allocate")
	})
}

func BenchmarkAsStreamer(b *testing.B) {
	var s cas.Store = decorated{decorated{memory.New(sha256.New())}}
	b.ReportAllocs()

	for b.Loop() {
		_, _ = cas.AsStreamer(s)
	}
}

// TestStreamDispatch covers the two branches of PutStream and
// GetStream. The observable difference between them is the reason a
// remote adapter implements the capability: a store that checks
// presence before it reads does not transfer a duplicate's bytes.
func TestStreamDispatch(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	payload := []byte("streamed through the native path")

	for name, wrap := range map[string]func(cas.Store) cas.Store{
		"a native streaming store":           func(s cas.Store) cas.Store { return s },
		"a native streaming store decorated": func(s cas.Store) cas.Store { return decorated{s} },
	} {
		t.Run(name+" skips a duplicate transfer", func(t *testing.T) {
			t.Parallel()
			inner := memory.New(h)
			s := wrap(inner)
			d := h.Hash(payload)

			_, err := s.Put(t.Context(), d, payload)
			testkit.NoError(t, err, "Put must succeed")

			r := bytes.NewReader(payload)

			wrote, err := cas.PutStream(t.Context(), s, d, r)
			testkit.NoError(t, err, "a duplicate streamed Put must not error")
			testkit.False(t, wrote, "a duplicate streamed Put must report wrote=false")
			testkit.Equal(t, r.Len(), len(payload),
				"a native PutStream must leave a duplicate's reader untouched")
		})
	}

	t.Run("the buffering fallback reads before it can ask", func(t *testing.T) {
		t.Parallel()

		s := wholeValueOnly{memory.New(h)}
		d := h.Hash(payload)

		_, err := s.Put(t.Context(), d, payload)
		testkit.NoError(t, err, "Put must succeed")

		r := bytes.NewReader(payload)

		wrote, err := cas.PutStream(t.Context(), s, d, r)
		testkit.NoError(t, err, "the fallback must not error on a duplicate")
		testkit.False(t, wrote, "the fallback must report wrote=false")
		testkit.Equal(t, r.Len(), 0, "the fallback must read the whole body before it can check presence")
	})

	t.Run("both branches round-trip the same bytes", func(t *testing.T) {
		t.Parallel()

		for name, s := range map[string]cas.Store{
			"native":   memory.New(h),
			"fallback": wholeValueOnly{memory.New(h)},
		} {
			d := h.Hash(payload)

			wrote, err := cas.PutStream(t.Context(), s, d, bytes.NewReader(payload))
			testkit.NoError(t, err, name+": PutStream must succeed")
			testkit.True(t, wrote, name+": the first PutStream must write")

			rc, err := cas.GetStream(t.Context(), s, d)
			testkit.NoError(t, err, name+": GetStream must succeed")

			got, err := io.ReadAll(rc)
			testkit.NoError(t, err, name+": the stream must drain")
			testkit.NoError(t, rc.Close(), name+": the stream must close")
			testkit.Equal(t, got, payload, name+": the round trip must be exact")
		}
	})

	t.Run("the fallback surfaces a reader that fails mid-transfer", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("cas: transfer interrupted")
		s := wholeValueOnly{memory.New(h)}
		d := h.Hash(payload)

		_, err := cas.PutStream(t.Context(), s, d,
			io.MultiReader(bytes.NewReader(payload[:4]), errReader{boom}))
		testkit.ErrorIs(t, err, boom, "the reader's own failure must surface")

		ok, err := s.Has(t.Context(), d)
		testkit.NoError(t, err, "Has must succeed after the failed transfer")
		testkit.False(t, ok, "a failed transfer must store nothing")
	})

	t.Run("the fallback reports an absent address as NotFound", func(t *testing.T) {
		t.Parallel()

		s := wholeValueOnly{memory.New(h)}

		_, err := cas.GetStream(t.Context(), s, h.Hash([]byte("never stored")))
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"the fallback must classify absence like the seam does")
	})
}

// TestVerify covers the verifying reader. The verdict latches, so a
// caller that ignores the first failure cannot then observe a clean
// EOF. A digest over a prefix proves nothing about the whole, so a
// reader closed early reports nothing.
//
// drain reads to completion within 64 reads. io.ReadAll would loop
// forever on a reader that returns (0, nil) without end, and the bound
// turns that defect into a named failure.
func TestVerify(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	payload := []byte("bytes that must hash to their address")

	open := func(b []byte) io.ReadCloser {
		return io.NopCloser(bytes.NewReader(b))
	}

	drain := func(t *testing.T, r io.Reader) ([]byte, error) {
		t.Helper()

		const maxReads = 64

		var out []byte

		buf := make([]byte, 8)
		for range maxReads {
			n, err := r.Read(buf)
			out = append(out, buf[:n]...)

			if err != nil {
				return out, err //nolint:wrapcheck // the subject's own error is the assertion
			}
		}

		t.Fatalf("the reader yielded no terminal error within %d reads", maxReads)

		return nil, nil
	}

	t.Run("a stream that hashes to its address reads clean", func(t *testing.T) {
		t.Parallel()

		rc := cas.Verify(h, h.Hash(payload), open(payload))

		got, err := drain(t, rc)
		testkit.ErrorIs(t, err, io.EOF, "a matching stream must end in EOF")
		testkit.NoError(t, rc.Close(), "the stream must close")
		testkit.Equal(t, got, payload, "the bytes must pass through unchanged")
	})

	t.Run("a stream that does not hash to its address is Integrity at EOF", func(t *testing.T) {
		t.Parallel()

		wrong := h.Hash([]byte("some other content"))
		rc := cas.Verify(h, wrong, open(payload))

		got, err := drain(t, rc)
		testkit.Equal(t, errs.Classify(err), errs.Integrity,
			"a stream that does not hash to its address must classify as Integrity")
		testkit.NoError(t, rc.Close(), "the stream must still close")
		testkit.Equal(t, got, payload, "the caller must receive the bytes before the verdict")
	})

	t.Run("the verdict latches across further reads", func(t *testing.T) {
		t.Parallel()

		wrong := h.Hash([]byte("some other content"))
		rc := cas.Verify(h, wrong, open(payload))

		_, first := drain(t, rc)
		testkit.Equal(t, errs.Classify(first), errs.Integrity,
			"the first verdict must be Integrity")

		buf := make([]byte, 8)
		n, again := rc.Read(buf)
		testkit.Equal(t, n, 0, "a latched reader must yield no more bytes")
		testkit.Equal(t, errs.Classify(again), errs.Integrity,
			"every read after the verdict must repeat it")
	})

	t.Run("a reader closed before EOF verifies nothing", func(t *testing.T) {
		t.Parallel()

		wrong := h.Hash([]byte("some other content"))
		rc := cas.Verify(h, wrong, open(payload))

		buf := make([]byte, 4)
		_, err := rc.Read(buf)
		testkit.NoError(t, err, "a partial read must not report a verdict")
		testkit.NoError(t, rc.Close(), "an early close must succeed")
	})

	t.Run("the wrapped reader's own failure passes through", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("cas: transfer interrupted")
		rc := cas.Verify(h, h.Hash(payload),
			io.NopCloser(io.MultiReader(bytes.NewReader(payload[:4]), errReader{boom})))

		_, err := drain(t, rc)
		testkit.ErrorIs(t, err, boom, "the wrapped reader's error must surface unchanged")
		testkit.NoError(t, rc.Close(), "the stream must still close")
	})

	t.Run("Close reaches the reader it wraps", func(t *testing.T) {
		t.Parallel()

		inner := &closeRecorder{Reader: bytes.NewReader(payload)}
		rc := cas.Verify(h, h.Hash(payload), inner)

		testkit.NoError(t, rc.Close(), "Close must succeed")
		testkit.True(t, inner.closed, "Close must reach the wrapped reader")
	})
}

// errReader fails on every read, as a transfer that breaks part-way
// does.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
