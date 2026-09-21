// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cas_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
)

// wholeValueOnly hides a store's streaming capability by embedding
// the interface rather than the concrete type, so [cas.PutStream]
// and [cas.GetStream] take their buffering branch. [memory.Store]
// implements [cas.Streamer] natively, which covers the other one.
type wholeValueOnly struct{ cas.Store }

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

func TestStreamDispatch(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	payload := []byte("streamed through the native path")

	t.Run("a native streaming store skips a duplicate transfer", func(t *testing.T) {
		t.Parallel()

		// The observable difference between the two branches, and the
		// reason a remote adapter implements the capability: a store
		// that can ask before it reads does not move the bytes twice.
		s := memory.New(h)
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
		testkit.Equal(t, r.Len(), 0,
			"the fallback has no way to ask before reading, and the reader pays for it")
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

func TestVerify(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	payload := []byte("bytes that must hash to their address")

	open := func(b []byte) io.ReadCloser {
		return io.NopCloser(bytes.NewReader(b))
	}

	// drain reads to completion under a bound. io.ReadAll would spin
	// forever against a reader that returns (0, nil) without end,
	// and a suite must name that defect rather than outlast it.
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
		testkit.Equal(t, got, payload,
			"the caller sees the bytes before the verdict — that is the trade streaming makes")
	})

	t.Run("the verdict latches across further reads", func(t *testing.T) {
		t.Parallel()

		// A caller that ignores the first failure must not then be
		// able to observe a clean EOF.
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

		// A digest over a prefix proves nothing about the whole, so
		// an early close is silent rather than a failure.
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

// errReader fails on every read, standing in for a transfer that
// breaks part-way.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
