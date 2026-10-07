// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cas_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/iotest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
)

// payload is the content of the cases that store one value.
const payload = "bytes that must hash to their address"

// errBroken is the error of a reader or a store that a case breaks.
var errBroken = errors.New("cas: transfer interrupted")

// wholeValueOnly embeds the Store interface and has no Unwrap, so
// [cas.AsStreamer] cannot see a Streamer behind it, and [cas.PutStream]
// and [cas.GetStream] take their buffering branch. [memory.Store]
// implements [cas.Streamer] natively, which covers the other branch.
type wholeValueOnly struct{ cas.Store }

// decorated wraps a store and returns it from Unwrap, as a tracing
// decorator does.
type decorated struct{ cas.Store }

// Unwrap returns the wrapped store.
func (d decorated) Unwrap() cas.Store { return d.Store }

// selfStreaming is a decorator that implements [cas.Streamer] itself,
// by delegating to the Streamer it wraps.
type selfStreaming struct {
	decorated

	inner cas.Streamer
}

// PutStream calls PutStream of the wrapped Streamer.
func (s selfStreaming) PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
	return s.inner.PutStream(ctx, d, r) //nolint:wrapcheck // the test double passes the error through
}

// GetStream calls GetStream of the wrapped Streamer.
func (s selfStreaming) GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error) {
	return s.inner.GetStream(ctx, d) //nolint:wrapcheck // the test double passes the error through
}

// streamOnly is a memory Store whose Get fails, so a read through it
// succeeds only on the native streaming path.
type streamOnly struct{ *memory.Store }

// Get returns dst and errBroken.
func (streamOnly) Get(_ context.Context, _ crypto.Digest, dst []byte) ([]byte, error) {
	return dst, errBroken
}

// resuming returns its bytes with io.EOF at the first Read, and its bytes
// again with no error at every Read after that, as a reader that breaks
// the contract of io.Reader does.
type resuming struct {
	data  []byte
	reads int
}

// Read copies data into p, and returns io.EOF at the first call only.
func (r *resuming) Read(p []byte) (int, error) {
	r.reads++
	n := copy(p, r.data)
	if r.reads == 1 {
		return n, io.EOF
	}

	return n, nil
}

// closeRecorder reports whether it was closed, so the wrapper's Close
// can be shown to reach the reader it wraps.
type closeRecorder struct {
	io.Reader

	closed bool
}

// Close records the call and returns nil.
func (c *closeRecorder) Close() error {
	c.closed = true

	return nil
}

func TestCAS(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	d := h.Hash([]byte(payload))
	wrong := h.Hash([]byte("some other content"))

	t.Run("AsStreamer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the store itself when it is a Streamer", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			st, ok := cas.AsStreamer(s)
			assert.True(t, ok, "a streaming store must be found")
			assert.Equal(t, st, cas.Streamer(s), "the store itself must be returned", assert.ByIdentity())
		})

		t.Run("returns a Streamer behind two decorators", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			st, ok := cas.AsStreamer(decorated{decorated{s}})
			assert.True(t, ok, "a Streamer behind decorators must be found")
			assert.Equal(t, st, cas.Streamer(s), "the wrapped store must be returned", assert.ByIdentity())
		})

		t.Run("returns a decorator that implements Streamer before the store it wraps", func(t *testing.T) {
			t.Parallel()
			s := memory.New(h)
			outer := selfStreaming{decorated: decorated{s}, inner: s}
			st, ok := cas.AsStreamer(outer)
			assert.True(t, ok, "the decorator must be found")
			assert.Equal(t, st, cas.Streamer(outer), "the outermost Streamer must be returned", assert.ByIdentity())
		})

		t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
			t.Parallel()
			_, ok := cas.AsStreamer(wholeValueOnly{memory.New(h)})
			assert.False(t, ok, "a decorator without Unwrap must hide the capability")
		})

		t.Run("reports false when the chain ends without a Streamer", func(t *testing.T) {
			t.Parallel()
			_, ok := cas.AsStreamer(decorated{wholeValueOnly{memory.New(h)}})
			assert.False(t, ok, "a chain without a Streamer must not report one")
		})

		t.Run("reports false for a nil store", func(t *testing.T) {
			t.Parallel()
			_, ok := cas.AsStreamer(nil)
			assert.False(t, ok, "a nil store must not report a Streamer")
		})

		t.Run("reports false for a decorator of nil", func(t *testing.T) {
			t.Parallel()
			_, ok := cas.AsStreamer(decorated{})
			assert.False(t, ok, "a decorator that wraps nothing must not report a Streamer")
		})
	})

	t.Run("PutStream", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			wrap func(cas.Store) cas.Store
		}{
			{
				name: "leaves the reader of a duplicate unread on a native streaming store",
				wrap: func(s cas.Store) cas.Store { return s },
			},
			{
				name: "leaves the reader of a duplicate unread on a decorated native streaming store",
				wrap: func(s cas.Store) cas.Store { return decorated{s} },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := tt.wrap(memory.New(h))
				_, err := s.Put(t.Context(), d, []byte(payload))
				assert.NoError(t, err, "Put must succeed")
				r := bytes.NewReader([]byte(payload))
				wrote, err := cas.PutStream(t.Context(), s, d, r)
				assert.NoError(t, err, "a duplicate streamed Put must not fail")
				expect.False(t, wrote, "a duplicate streamed Put must report wrote=false")
				expect.Equal(t, r.Len(), len(payload), "a native PutStream must not read a duplicate")
			})
		}

		t.Run("reads the whole reader of a duplicate on the buffering fallback", func(t *testing.T) {
			t.Parallel()
			s := wholeValueOnly{memory.New(h)}
			_, err := s.Put(t.Context(), d, []byte(payload))
			assert.NoError(t, err, "Put must succeed")
			r := bytes.NewReader([]byte(payload))
			wrote, err := cas.PutStream(t.Context(), s, d, r)
			assert.NoError(t, err, "the fallback must not fail on a duplicate")
			expect.False(t, wrote, "the fallback must report wrote=false")
			expect.Equal(t, r.Len(), 0, "the fallback must read the whole body before it can check presence")
		})

		t.Run("returns the error of a reader that fails on the buffering fallback", func(t *testing.T) {
			t.Parallel()
			s := wholeValueOnly{memory.New(h)}
			_, err := cas.PutStream(t.Context(), s, d,
				io.MultiReader(bytes.NewReader([]byte(payload[:4])), iotest.ErrReader(errBroken)))
			expect.ErrorIs(t, err, errBroken, "the error of the reader must be returned")
			ok, err := s.Has(t.Context(), d)
			assert.NoError(t, err, "Has must succeed after the failed transfer")
			assert.False(t, ok, "a failed transfer must store nothing")
		})
	})

	t.Run("GetStream", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			store cas.Store
		}{
			{name: "returns the stored bytes from a native streaming store", store: memory.New(h)},
			{name: "returns the stored bytes on the buffering fallback", store: wholeValueOnly{memory.New(h)}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				wrote, err := cas.PutStream(t.Context(), tt.store, d, bytes.NewReader([]byte(payload)))
				assert.NoError(t, err, "PutStream must succeed")
				assert.True(t, wrote, "the first PutStream must write")
				rc, err := cas.GetStream(t.Context(), tt.store, d)
				assert.NoError(t, err, "GetStream must succeed")
				got, err := io.ReadAll(rc)
				assert.NoError(t, err, "the stream must drain")
				assert.NoError(t, rc.Close(), "the stream must close")
				assert.Equal(t, string(got), payload, "the round trip must be exact")
			})
		}

		t.Run("reads through the native path of a Streamer whose Get fails", func(t *testing.T) {
			t.Parallel()
			s := streamOnly{memory.New(h)}
			_, err := s.Put(t.Context(), d, []byte(payload))
			assert.NoError(t, err, "Put must succeed")
			rc, err := cas.GetStream(t.Context(), s, d)
			assert.NoError(t, err, "GetStream must not call Get of a Streamer")
			assert.NoError(t, rc.Close(), "the stream must close")
		})

		t.Run("returns a NotFound error for an absent address on the buffering fallback", func(t *testing.T) {
			t.Parallel()
			_, err := cas.GetStream(t.Context(), wholeValueOnly{memory.New(h)}, d)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "the fallback must classify absence as the seam does")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes of a stream that hashes to its address with io.EOF", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, d, io.NopCloser(bytes.NewReader([]byte(payload))))
			got, err := drain(t, rc)
			expect.ErrorIs(t, err, io.EOF, "a matching stream must end in io.EOF")
			expect.NoError(t, rc.Close(), "the stream must close")
			expect.Equal(t, string(got), payload, "the bytes must pass through unchanged")
		})

		t.Run("returns an Integrity error at the end of a stream of other bytes", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, wrong, io.NopCloser(bytes.NewReader([]byte(payload))))
			got, err := drain(t, rc)
			expect.Equal(t, errs.Classify(err), errs.Integrity, "a stream of other bytes must classify as Integrity")
			expect.NoError(t, rc.Close(), "the stream must still close")
			expect.Equal(t, string(got), payload, "the caller must receive the bytes before the verdict")
		})

		t.Run("returns the bytes of the read that ends the stream with the verdict", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, wrong, io.NopCloser(iotest.DataErrReader(bytes.NewReader([]byte(payload)))))
			got, err := drain(t, rc)
			expect.Equal(t, errs.Classify(err), errs.Integrity, "the last read must return the verdict")
			expect.Equal(t, string(got), payload, "the last read must return its bytes with the verdict")
		})

		t.Run("returns the same error on every read after the verdict", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, wrong, io.NopCloser(bytes.NewReader([]byte(payload))))
			_, first := drain(t, rc)
			n, again := rc.Read(make([]byte, 8))
			expect.Equal(t, n, 0, "a reader after its verdict must yield no more bytes")
			expect.Equal(t, again, first, "every read after the verdict must return the same error")
		})

		t.Run("returns the verdict on every read after it when the wrapped reader resumes", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, wrong, io.NopCloser(&resuming{data: []byte(payload)}))
			_, first := rc.Read(make([]byte, 64))
			assert.Equal(t, errs.Classify(first), errs.Integrity, "the first read must return the verdict")
			n, again := rc.Read(make([]byte, 64))
			expect.Equal(t, n, 0, "a reader after its verdict must yield no more bytes")
			expect.Equal(t, again, first, "every read after the verdict must return the same error")
		})

		t.Run("returns no verdict before the end of the stream", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, wrong, io.NopCloser(bytes.NewReader([]byte(payload))))
			_, err := rc.Read(make([]byte, 4))
			expect.NoError(t, err, "a partial read must not report a verdict")
			expect.NoError(t, rc.Close(), "an early close must succeed")
		})

		t.Run("returns the error of the wrapped reader", func(t *testing.T) {
			t.Parallel()
			rc := cas.Verify(h, d,
				io.NopCloser(io.MultiReader(bytes.NewReader([]byte(payload[:4])), iotest.ErrReader(errBroken))))
			_, err := drain(t, rc)
			expect.ErrorIs(t, err, errBroken, "the error of the wrapped reader must be returned unchanged")
			expect.NoError(t, rc.Close(), "the stream must still close")
		})

		t.Run("returns a reader whose Close closes the reader it wraps", func(t *testing.T) {
			t.Parallel()
			inner := &closeRecorder{Reader: bytes.NewReader([]byte(payload))}
			rc := cas.Verify(h, d, inner)
			expect.NoError(t, rc.Close(), "Close must succeed")
			expect.True(t, inner.closed, "Close must reach the wrapped reader")
		})
	})
}

// TestCASAllocs checks the allocation contract of AsStreamer, of the
// fallback of GetStream and of a verified read. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestCASAllocs(t *testing.T) {
	h := sha256.New()
	d := h.Hash([]byte(payload))
	native := memory.New(h)
	_, err := native.Put(t.Context(), d, []byte(payload))
	assert.NoError(t, err, "Put must succeed")

	t.Run("AsStreamer", func(t *testing.T) {
		var s cas.Store = decorated{decorated{native}}
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = cas.AsStreamer(s) }, 0, "AsStreamer must not allocate")
		assert.True(t, ok, "the test must measure a chain with a Streamer")
	})

	t.Run("GetStream", func(t *testing.T) {
		var s cas.Store = wholeValueOnly{native}
		var rc io.ReadCloser
		expect.MaxAllocs(t, func() { rc, err = cas.GetStream(t.Context(), s, d) }, 2,
			"the fallback must allocate the buffer and the reader")
		assert.NoError(t, err, "the test must measure a GetStream that succeeds")
		assert.NoError(t, rc.Close(), "the stream must close")
	})

	t.Run("Verify", func(t *testing.T) {
		data, r := []byte(payload), bytes.NewReader(nil)
		inner := io.NopCloser(r)
		buf := make([]byte, 64)
		// rc outlives each call, as a caller's reader does, so the wrapper
		// moves to the heap whether or not the compiler inlines Verify.
		var rc io.ReadCloser
		expect.MaxAllocs(t, func() {
			r.Reset(data)
			rc = cas.Verify(h, d, inner)
			err = nil
			for err == nil {
				_, err = rc.Read(buf)
			}
			_ = rc.Close()
		}, 1, "a verified read must allocate only the wrapper")
		assert.ErrorIs(t, err, io.EOF, "the test must measure a stream that verifies")
	})
}

// BenchmarkCAS reports the cost of AsStreamer through two decorators and
// of a verified read of a short value, and fails when either allocates
// more than its allocation contract allows.
func BenchmarkCAS(b *testing.B) {
	h := sha256.New()
	d := h.Hash([]byte(payload))
	native := memory.New(h)

	b.Run("AsStreamer", func(b *testing.B) {
		var s cas.Store = decorated{decorated{native}}
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = cas.AsStreamer(s)
		}

		assert.True(b, ok, "the benchmark must measure a chain with a Streamer")
	})

	b.Run("Verify", func(b *testing.B) {
		data, r := []byte(payload), bytes.NewReader(nil)
		inner := io.NopCloser(r)
		buf := make([]byte, 64)
		var err error
		var rc io.ReadCloser

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			r.Reset(data)
			rc = cas.Verify(h, d, inner)
			err = nil
			for err == nil {
				_, err = rc.Read(buf)
			}
			_ = rc.Close()
		}

		assert.ErrorIs(b, err, io.EOF, "the benchmark must measure a stream that verifies")
	})
}

// drain reads r to its first error within 64 reads of 8 bytes, and returns
// the bytes and the error. io.ReadAll would loop without end on a reader
// that returns (0, nil) forever, and the bound turns that defect into a
// failure of tb.
func drain(tb assert.TB, r io.Reader) ([]byte, error) {
	tb.Helper()
	var out []byte
	buf := make([]byte, 8)
	for range 64 {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, err //nolint:wrapcheck // the subject's own error is the assertion
		}
	}
	tb.Fatalf("the reader returned no error within 64 reads")

	return nil, nil
}
