// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

// bodies generates the bodies of the objects of the cases.
var bodies = prop.Bytes(prop.MaxSize(64))

// failingGetStore hands out a body that breaks part-way, which no
// in-memory store can be made to do.
type failingGetStore struct {
	blob.Store
	err error
}

// Get returns a reader that yields four bytes and then the store's error.
func (s failingGetStore) Get(context.Context, string) (io.ReadCloser, blob.Info, error) {
	return io.NopCloser(io.MultiReader(bytes.NewReader([]byte("part")), iotestErrReader{s.err})), blob.Info{}, nil
}

// failingCloseStore reads cleanly and then fails to close.
type failingCloseStore struct {
	blob.Store
	err error
}

// Get returns a reader whose Close returns the store's error.
func (s failingCloseStore) Get(context.Context, string) (io.ReadCloser, blob.Info, error) {
	return badCloser{Reader: bytes.NewReader([]byte("fine")), err: s.err}, blob.Info{}, nil
}

// badCloser is a reader whose Close fails with err.
type badCloser struct {
	io.Reader
	err error
}

// Close returns the reader's error.
func (b badCloser) Close() error { return b.err }

// iotestErrReader is a reader whose every Read fails with err.
type iotestErrReader struct{ err error }

// Read returns no bytes and the reader's error.
func (e iotestErrReader) Read([]byte) (int, error) { return 0, e.err }

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
		_, _, err := blob.GetBytes(t.Context(), failingGetStore{err: boom}, "k", 1024)
		assert.ErrorIs(t, err, boom, "the reader's own failure must reach the caller")
	})

	t.Run("returns the error of Close after a read that succeeded", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("blob_test: close failed")
		_, _, err := blob.GetBytes(t.Context(), failingCloseStore{err: boom}, "k", 1024)
		assert.ErrorIs(t, err, boom, "a Close failure must not be swallowed")
	})
}
