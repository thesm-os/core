// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"testing/iotest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

// measuredSize is the size of the object of the allocation tests and the
// benchmarks: 8 KiB, the size of a full tile of 256 SHA-256 hashes.
const measuredSize = 8192

// Generators of the cases of the byte helpers.
var (
	// bodies generates the bodies of the objects of the cases, and the
	// bytes of a dst.
	bodies = prop.Bytes(prop.MaxSize(64))

	// reads generates the ways in which a body returns its bytes: all at
	// once, one byte per Read, half of each Read, and the last bytes with
	// io.EOF.
	reads = prop.SampledFrom(
		func(r io.Reader) io.Reader { return r },
		iotest.OneByteReader,
		iotest.HalfReader,
		iotest.DataErrReader,
	)
)

// objectStore hands out one body under every key, and reports size as the
// size of its object, or -1 for a store that cannot report it. It returns
// the bodies and the sizes that no in-memory store can be made to return.
type objectStore struct {
	blob.Store

	body *body
	size int64
}

// Get returns the body of the store, with an Info that reports the size of
// the store.
func (s objectStore) Get(context.Context, string) (io.ReadCloser, blob.Info, error) {
	return s.body, blob.Info{Size: s.size}, nil
}

// body is the body of the object of an objectStore. It records its Close,
// which returns err.
type body struct {
	io.Reader

	err    error
	closed bool
}

// Close records the call and returns the error of the body.
func (b *body) Close() error {
	b.closed = true

	return b.err
}

// endless is a body without an end.
type endless struct{}

// Read fills p with zeros.
func (endless) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

func TestPutBytes(t *testing.T) {
	t.Parallel()

	t.Run("stores a body that GetBytes returns", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		prop.RoundTrip(t, func(body []byte) (string, error) {
			_, err := blob.PutBytes(t.Context(), s, "k", body, blob.PutOptions{})

			return "k", err
		}, func(key string) ([]byte, error) {
			got, _, err := blob.GetBytes(t.Context(), s, key, math.MaxInt64)

			return got, err
		}, "GetBytes must return the body that PutBytes stored", assert.EquateEmpty(), prop.Using(bodies))
	})

	t.Run("returns the Info that Get returns", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		body := []byte("a short configuration document")

		info, err := blob.PutBytes(t.Context(), s, "cfg", body, blob.PutOptions{ContentType: "application/json"})
		assert.NoError(t, err, "PutBytes must succeed")
		assert.Equal(t, info.Size, int64(len(body)), "the Info must report the size of the body")
		assert.Equal(t, info.ContentType, "application/json", "the Info must report the content type")

		_, got, err := blob.GetBytes(t.Context(), s, "cfg", 1024)
		assert.NoError(t, err, "GetBytes must succeed")
		assert.Equal(t, got, info, "GetBytes must report the Info that PutBytes returned")
	})

	t.Run("passes its preconditions to the store", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		opts := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}

		_, err := blob.PutBytes(t.Context(), s, "once", []byte("first"), opts)
		assert.NoError(t, err, "a create-only Put must succeed on an absent key")

		_, err = blob.PutBytes(t.Context(), s, "once", []byte("second"), opts)
		assert.ErrorIs(t, err, version.ErrExists, "PutBytes must pass the precondition to the store")
	})
}

func TestGetBytes(t *testing.T) {
	t.Parallel()

	t.Run("returns an object of at most limit bytes", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "GetBytes must return every object within its limit", func(c *prop.Case) {
			s := memory.New(fake.New(origin))
			body := c.Draw(bodies, "body")
			limit := c.Draw(prop.Integer[int64](max(1, int64(len(body))), 128), "limit")

			_, err := blob.PutBytes(c.Context(), s, "k", body, blob.PutOptions{})
			assert.NoError(c, err, "PutBytes must succeed")

			got, _, err := blob.GetBytes(c.Context(), s, "k", limit)
			assert.NoError(c, err, "an object within the limit must be readable")
			assert.Equal(c, got, body, "the bytes must be complete", assert.EquateEmpty())
		})
	})

	t.Run("returns Invalid and no bytes for an object past the limit", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "GetBytes must refuse every object past its limit", func(c *prop.Case) {
			s := memory.New(fake.New(origin))
			body := c.Draw(prop.Bytes(prop.MinSize(2), prop.MaxSize(64)), "body")
			limit := c.Draw(prop.Integer[int64](1, int64(len(body))-1), "limit")

			_, err := blob.PutBytes(c.Context(), s, "k", body, blob.PutOptions{})
			assert.NoError(c, err, "PutBytes must succeed")

			got, _, err := blob.GetBytes(c.Context(), s, "k", limit)
			assert.Equal(c, errs.Classify(err), errs.Invalid, "an object past the limit must classify as Invalid")
			assert.Nil(c, got, "a caller that set a limit must not receive a truncated object")
		})
	})

	t.Run("returns Invalid for a limit of zero or less", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		prop.Equal(t, func(limit int64) errs.Class {
			_, _, err := blob.GetBytes(t.Context(), s, "k", limit)

			return errs.Classify(err)
		}, func(int64) errs.Class { return errs.Invalid }, "a limit that admits nothing must be a caller mistake",
			prop.Using(prop.Integer[int64](math.MinInt64, 0)), prop.Example(int64(0)), prop.Example(int64(-1)))
	})

	t.Run("returns NotFound for an absent key", func(t *testing.T) {
		t.Parallel()
		_, _, err := blob.GetBytes(t.Context(), memory.New(fake.New(origin)), "absent", 1024)
		assert.Equal(t, errs.Classify(err), errs.NotFound, "absence must classify as NotFound")
	})

	t.Run("returns the error of a reader that fails part-way", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("blob_test: read interrupted")
		part := io.MultiReader(strings.NewReader("part"), iotest.ErrReader(boom))
		_, _, err := blob.GetBytes(t.Context(), objectStore{body: &body{Reader: part}, size: -1}, "k", 1024)
		assert.ErrorIs(t, err, boom, "the reader's own failure must reach the caller")
	})

	t.Run("returns the error of Close after a read that succeeded", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("blob_test: close failed")
		s := objectStore{body: &body{Reader: strings.NewReader("fine"), err: boom}, size: -1}
		_, _, err := blob.GetBytes(t.Context(), s, "k", 1024)
		assert.ErrorIs(t, err, boom, "a Close failure must not be swallowed")
	})
}

func TestAppendBytes(t *testing.T) {
	t.Parallel()

	t.Run("appends an object of at most limit bytes to dst", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "AppendBytes must append every object within its limit, whatever size within the limit "+
			"the store reports", func(c *prop.Case) {
			dst := c.Draw(bodies, "dst")
			obj := c.Draw(bodies, "object")
			limit := c.Draw(prop.Integer[int64](max(1, int64(len(obj))), 128), "limit")
			size := c.Draw(prop.Integer[int64](-1, limit), "size")
			read := c.Draw(reads, "read")
			want := append(bytes.Clone(dst), obj...)

			s := objectStore{body: &body{Reader: read(bytes.NewReader(obj))}, size: size}
			got, _, err := blob.AppendBytes(c.Context(), s, "k", dst, limit)
			assert.NoError(c, err, "an object within the limit must be readable")
			assert.Equal(c, got, want, "the object must follow the bytes of dst", assert.EquateEmpty())
		})
	})

	t.Run("returns Invalid with dst unchanged for an object past the limit", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "AppendBytes must refuse every object past its limit, whatever size within the limit "+
			"the store reports", func(c *prop.Case) {
			dst := c.Draw(bodies, "dst")
			obj := c.Draw(prop.Bytes(prop.MinSize(2), prop.MaxSize(64)), "object")
			limit := c.Draw(prop.Integer[int64](1, int64(len(obj))-1), "limit")
			size := c.Draw(prop.Integer[int64](-1, limit), "size")
			read := c.Draw(reads, "read")

			s := objectStore{body: &body{Reader: read(bytes.NewReader(obj))}, size: size}
			got, _, err := blob.AppendBytes(c.Context(), s, "k", dst, limit)
			assert.Equal(c, errs.Classify(err), errs.Invalid, "an object past the limit must classify as Invalid")
			assert.Equal(c, got, dst, "a caller that set a limit must keep its dst", assert.ByIdentity())
		})
	})

	t.Run("returns Invalid without a read for a reported size past the limit", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("blob_test: the body was read")
		s := objectStore{body: &body{Reader: iotest.ErrReader(boom)}, size: 1025}
		_, _, err := blob.AppendBytes(t.Context(), s, "k", nil, 1024)
		assert.Equal(t, errs.Classify(err), errs.Invalid, "the size must refuse the object before a read of its body")
	})

	t.Run("closes the body of an object whose reported size is past the limit", func(t *testing.T) {
		t.Parallel()
		b := &body{Reader: strings.NewReader("ab")}
		_, _, err := blob.AppendBytes(t.Context(), objectStore{body: b, size: 2}, "k", nil, 1)
		assert.Equal(t, errs.Classify(err), errs.Invalid, "the case must measure a refused size")
		assert.True(t, b.closed, "AppendBytes must close the body that it refuses")
	})

	t.Run("returns Invalid for an endless body without a reported size", func(t *testing.T) {
		t.Parallel()
		s := objectStore{body: &body{Reader: endless{}}, size: -1}
		_, _, err := blob.AppendBytes(t.Context(), s, "k", nil, 1024)
		assert.Equal(t, errs.Classify(err), errs.Invalid, "the read must stop past the limit")
	})

	tests := []struct {
		give  blob.Store
		name  string
		limit int64
	}{
		{
			name:  "returns dst unchanged with the zero Info for a limit of zero",
			give:  memory.New(fake.New(origin)),
			limit: 0,
		},
		{
			name:  "returns dst unchanged with the zero Info for an absent key",
			give:  memory.New(fake.New(origin)),
			limit: 1024,
		},
		{
			name:  "returns dst unchanged with the zero Info for a reported size past the limit",
			give:  objectStore{body: &body{Reader: strings.NewReader("ab")}, size: 2},
			limit: 1,
		},
		{
			name: "returns dst unchanged with the zero Info for a body that fails part-way",
			give: objectStore{
				body: &body{Reader: io.MultiReader(strings.NewReader("part"), iotest.ErrReader(io.ErrUnexpectedEOF))},
				size: -1,
			},
			limit: 1024,
		},
		{
			name:  "returns dst unchanged with the zero Info for a body that fails to close",
			give:  objectStore{body: &body{Reader: strings.NewReader("fine"), err: io.ErrClosedPipe}, size: -1},
			limit: 1024,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dst := make([]byte, 4, 1024)
			got, info, err := blob.AppendBytes(t.Context(), tt.give, "k", dst, tt.limit)
			assert.HasError(t, err, "the case must measure an error")
			expect.Equal(t, got, dst, "dst must be returned unchanged", assert.ByIdentity())
			expect.Equal(t, info, blob.Info{}, "an error must return the zero Info")
		})
	}
}

// TestBytesAllocs checks the allocation contracts of AppendBytes and
// GetBytes over a memory store, whose Get allocates one reader. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestBytesAllocs(t *testing.T) {
	ctx := t.Context()
	s := memory.New(fake.New(origin))
	_, err := blob.PutBytes(ctx, s, "k", make([]byte, measuredSize), blob.PutOptions{})
	assert.NoError(t, err, "PutBytes must succeed")

	t.Run("AppendBytes", func(t *testing.T) {
		dst := make([]byte, 0, measuredSize+bytes.MinRead)

		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, _, err = blob.AppendBytes(ctx, s, "k", dst, measuredSize) }, 1,
			"AppendBytes must allocate only the reader of the store into a dst with room")
		assert.NoError(t, err, "the test must measure a read that succeeds")
		assert.Length(t, got, measuredSize, "the test must measure the whole object")
	})

	t.Run("GetBytes", func(t *testing.T) {
		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, _, err = blob.GetBytes(ctx, s, "k", measuredSize) }, 2,
			"GetBytes must allocate one buffer and the reader of the store")
		assert.NoError(t, err, "the test must measure a read that succeeds")
		assert.Length(t, got, measuredSize, "the test must measure the whole object")
	})
}

// BenchmarkBytes reports the cost of AppendBytes and GetBytes over a
// memory store, and fails above the allocations that their contracts
// state.
func BenchmarkBytes(b *testing.B) {
	s := memory.New(fake.New(origin))
	_, err := blob.PutBytes(b.Context(), s, "k", make([]byte, measuredSize), blob.PutOptions{})
	assert.NoError(b, err, "PutBytes must succeed")

	b.Run("AppendBytes", func(b *testing.B) {
		ctx := b.Context()
		dst := make([]byte, 0, measuredSize+bytes.MinRead)

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _, err = blob.AppendBytes(ctx, s, "k", dst, measuredSize)
		}

		assert.NoError(b, err, "the benchmark must measure a read that succeeds")
		assert.Length(b, got, measuredSize, "the benchmark must measure the whole object")
	})

	b.Run("GetBytes", func(b *testing.B) {
		ctx := b.Context()

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(2)
		defer c.End()

		for c.Loop() {
			got, _, err = blob.GetBytes(ctx, s, "k", measuredSize)
		}

		assert.NoError(b, err, "the benchmark must measure a read that succeeds")
		assert.Length(b, got, measuredSize, "the benchmark must measure the whole object")
	})
}
