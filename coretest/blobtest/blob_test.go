// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blobtest_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
	"go.thesmos.sh/core/version"
)

// origin is the start time of the fake clock of every store of the tests.
var origin = time.Unix(0, 0).UTC()

// errPurged is the error of a reader of a purging store once its version is
// removed.
var errPurged = fmt.Errorf("blobtest_test: version purged: %w", version.ErrMismatch)

// purgingReader reads the version of an object that Get opened, and fails with
// errPurged once Stat of the key fails or returns another version, as the
// reader of a store that purges a removed version does.
type purgingReader struct {
	io.ReadCloser

	ctx     context.Context //nolint:containedctx // the reader states the object at each Read
	s       blob.Store
	key     string
	version version.Version
}

// Read reads from the version that Get opened, and returns errPurged once that
// version is removed.
func (r *purgingReader) Read(p []byte) (int, error) {
	if info, err := r.s.Stat(r.ctx, r.key); err != nil || info.Version != r.version {
		return 0, errPurged
	}

	return r.ReadCloser.Read(p)
}

// TestBlob checks that the store in memory passes the checks of the suites of
// blob.Store and blob.RangeReader, and that a store whose readers fail with
// version.ErrMismatch once their version is removed passes the checks of the
// suite of blob.Store.
func TestBlob(t *testing.T) {
	t.Parallel()

	t.Run("Store", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the store in memory", func(t *testing.T) {
			t.Parallel()
			blobtest.RunStoreSuite(t, func() *memory.Store { return memory.New(fake.New(origin)) }, storeOptions()...)
		})

		t.Run("passes a store whose readers fail once their version is removed", func(t *testing.T) {
			t.Parallel()
			blobtest.RunStoreSuiteFromHandle(t, func(h *blobtest.StoreHandle) blob.Store {
				s := memory.New(fake.New(origin))

				return blobtest.NewStoreStub(h, blobtest.StoreStubDelegateTo(s),
					blobtest.WithStoreGet(func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
						rc, info, err := s.Get(ctx, key)
						if err != nil {
							return rc, info, err
						}

						purging := &purgingReader{ReadCloser: rc, ctx: ctx, s: s, key: key, version: info.Version}

						return purging, info, nil
					}))
			}, storeOptions()...)
		})
	})

	t.Run("RangeReader", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the store in memory", func(t *testing.T) {
			t.Parallel()
			blobtest.RunRangeReaderSuite(t, func() *memory.Store { return memory.New(fake.New(origin)) })
		})
	})
}

// storeOptions returns the doors of a store in memory: a restart and a crash
// return the store itself, which keeps its objects across both. The crash runs
// call before it returns.
func storeOptions() []blobtest.StoreOption {
	return []blobtest.StoreOption{
		conform.Restartable(func(_ *blobtest.StoreHandle, s blob.Store) blob.Store { return s }),
		conform.Crashable(func(_ *blobtest.StoreHandle, s blob.Store, call func()) blob.Store {
			call()

			return s
		}),
	}
}
