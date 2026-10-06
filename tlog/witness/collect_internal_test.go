// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

func TestCollectInternal(t *testing.T) {
	t.Parallel()

	t.Run("collect", func(t *testing.T) {
		t.Parallel()

		a := newInternalLog(t, "example.com/a")

		// three returns a fixture and a server whose state committed three
		// records of a.
		three := func(tb testing.TB) (*internalFixture, *Server) {
			tb.Helper()

			f := newInternalFixture(tb, a)
			s := f.server(tb)

			for size := uint64(1); size <= 3; size++ {
				a.advance(tb, s, size-1, size, nil)
			}

			return f, s
		}

		t.Run("deletes a record off the chain at the Seq of the head", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)

			// A record of another process that lost the replacement of the head.
			orphan := recordPrefix + string(appendName(nil, 3, []byte("an orphan")))
			putObject(t, f.store, orphan, []byte("an orphan"))

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), orphan)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "the snapshot must delete the record off the chain")
		})

		t.Run("deletes another snapshot of the Seq of the new one", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)
			other := snapshotPrefix + string(appendName(nil, 3, []byte("another snapshot")))
			putObject(t, f.store, other, []byte("another snapshot"))

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), other)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "the snapshot must delete the other snapshot")
		})

		t.Run("keeps a group of the Seq of the new snapshot that no snapshot refers to", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)

			// The writer of another process that started at the same head
			// creates groups of that Seq, which its snapshot can still install.
			group := groupPrefix + string(appendName(nil, 3, []byte("a group")))
			putObject(t, f.store, group, []byte("a group"))

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), group)
			testkit.NoError(t, err, "the snapshot must keep the group")
		})

		t.Run("keeps the record of the base when no position refers to it", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)

			s.mu.RLock()
			first, _ := s.st.chain.Get(1)
			second, _ := s.st.chain.Get(2)
			s.mu.RUnlock()

			// The base of the writer is at the record of Seq 2, and neither the
			// base nor the snapshot refers to that record.
			w := &snapper{base: installed{seq: 2}, seq: 3}
			testkit.NoError(t, s.collect(bounded(t), w, &snapshot{}, "", 3), "collect must walk the store")

			_, err := f.store.Stat(t.Context(), recordPrefix+second)
			testkit.NoError(t, err, "collect must keep the record of the base")

			_, err = f.store.Stat(t.Context(), recordPrefix+first)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "collect must delete a record below the base")
		})
	})
}
