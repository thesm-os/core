// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

func newStore() blob.Store {
	return memory.New(fake.New(time.Unix(0, 0).UTC()))
}

func TestPutBytes(t *testing.T) {
	t.Parallel()

	t.Run("round-trips a small value without a caller-built reader", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		body := []byte("a short configuration document")

		info, err := blob.PutBytes(t.Context(), s, "cfg", body,
			blob.PutOptions{ContentType: "application/json"})
		testkit.NoError(t, err, "PutBytes must succeed")
		testkit.Equal(t, info.Size, int64(len(body)), "Info must report the consumed size")
		testkit.Equal(t, info.ContentType, "application/json", "ContentType must round-trip")

		got, gotInfo, err := blob.GetBytes(t.Context(), s, "cfg", 1024)
		testkit.NoError(t, err, "GetBytes must succeed")
		testkit.Equal(t, got, body, "the round trip must be exact")
		testkit.Equal(t, gotInfo, info, "GetBytes must report the same Info as Put")
	})

	t.Run("passes its preconditions to the store", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		opts := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}

		_, err := blob.PutBytes(t.Context(), s, "once", []byte("first"), opts)
		testkit.NoError(t, err, "create-only must succeed on an absent key")

		_, err = blob.PutBytes(t.Context(), s, "once", []byte("second"), opts)
		testkit.ErrorIs(t, err, version.ErrExists,
			"PutBytes must carry the precondition rather than swallow it")
	})
}

func TestGetBytes(t *testing.T) {
	t.Parallel()

	t.Run("an object at the limit is legal", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		body := []byte("exactly ten")

		_, err := blob.PutBytes(t.Context(), s, "k", body, blob.PutOptions{})
		testkit.NoError(t, err, "PutBytes must succeed")

		got, _, err := blob.GetBytes(t.Context(), s, "k", int64(len(body)))
		testkit.NoError(t, err, "an object the size of the limit must be readable")
		testkit.Equal(t, got, body, "the bytes must be complete")
	})

	t.Run("an object past the limit is Invalid and yields nothing", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		body := []byte("eleven byte")

		_, err := blob.PutBytes(t.Context(), s, "k", body, blob.PutOptions{})
		testkit.NoError(t, err, "PutBytes must succeed")

		got, _, err := blob.GetBytes(t.Context(), s, "k", int64(len(body))-1)
		testkit.Equal(t, errs.Classify(err), errs.Invalid,
			"an object larger than the limit must classify as Invalid")
		testkit.Len(t, got, 0,
			"a caller that set a limit must not receive a truncated object")
	})

	t.Run("a limit at or below zero is Invalid", func(t *testing.T) {
		t.Parallel()

		s := newStore()

		for _, limit := range []int64{0, -1} {
			_, _, err := blob.GetBytes(t.Context(), s, "k", limit)
			testkit.Equal(t, errs.Classify(err), errs.Invalid,
				"a limit that admits nothing is a caller mistake")
		}
	})

	t.Run("an absent key is NotFound", func(t *testing.T) {
		t.Parallel()

		_, _, err := blob.GetBytes(t.Context(), newStore(), "absent", 1024)
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"absence must classify as NotFound")
	})

	t.Run("a reader that fails mid-stream surfaces its own error", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("blob: read interrupted")
		s := failingGetStore{err: boom}

		_, _, err := blob.GetBytes(t.Context(), s, "k", 1024)
		testkit.ErrorIs(t, err, boom, "the reader's own failure must surface")
	})

	t.Run("a Close failure surfaces when the read succeeded", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("blob: close failed")
		s := failingCloseStore{err: boom}

		_, _, err := blob.GetBytes(t.Context(), s, "k", 1024)
		testkit.ErrorIs(t, err, boom, "a Close failure must not be swallowed")
	})
}

// failingGetStore hands out a body that breaks part-way, which no
// in-memory store can be made to do.
type failingGetStore struct {
	blob.Store
	err error
}

func (s failingGetStore) Get(context.Context, string) (io.ReadCloser, blob.Info, error) {
	return io.NopCloser(io.MultiReader(
		bytes.NewReader([]byte("part")),
		errReader{s.err},
	)), blob.Info{}, nil
}

// failingCloseStore reads cleanly and then fails to close.
type failingCloseStore struct {
	blob.Store
	err error
}

func (s failingCloseStore) Get(context.Context, string) (io.ReadCloser, blob.Info, error) {
	return badCloser{Reader: bytes.NewReader([]byte("fine")), err: s.err}, blob.Info{}, nil
}

type badCloser struct {
	io.Reader
	err error
}

func (b badCloser) Close() error { return b.err }

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
