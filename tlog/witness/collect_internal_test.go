// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.dokimi.dev/assert"

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

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), orphan)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "the snapshot must delete the record off the chain")
		})

		t.Run("deletes another snapshot of the Seq of the new one", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)
			other := snapshotPrefix + string(appendName(nil, 3, []byte("another snapshot")))
			putObject(t, f.store, other, []byte("another snapshot"))

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), other)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "the snapshot must delete the other snapshot")
		})

		t.Run("keeps a group of the Seq of the new snapshot that no snapshot refers to", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)

			// The writer of another process that started at the same head
			// creates groups of that Seq, which its snapshot can still install.
			group := groupPrefix + string(appendName(nil, 3, []byte("a group")))
			putObject(t, f.store, group, []byte("a group"))

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err := f.store.Stat(t.Context(), group)
			assert.NoError(t, err, "the snapshot must keep the group")
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
			assert.NoError(t, s.collect(bounded(t), w, &snapshot{}, "", 3), "collect must walk the store")

			_, err := f.store.Stat(t.Context(), recordPrefix+second)
			assert.NoError(t, err, "collect must keep the record of the base")

			_, err = f.store.Stat(t.Context(), recordPrefix+first)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "collect must delete a record below the base")
		})

		// Each case collects with a base at the record of Seq 2, after the
		// three records of a on the chain.
		below := recordPrefix + string(appendName(nil, 1, []byte("a record below the base")))
		later := recordPrefix + string(appendName(nil, 5, []byte("a record of a later head")))
		tests := []struct {
			name    string
			key     string
			base    []object
			objects []object
			headSeq uint64
		}{
			{
				name: "keeps a record below the base that the base refers to", key: below,
				base: []object{{Key: below}}, headSeq: 3,
			},
			{
				name: "keeps a record below the base that the snapshot refers to", key: below,
				objects: []object{{Key: below}}, headSeq: 3,
			},
			{name: "keeps a key under records/ that is not a name", key: recordPrefix + "a key", headSeq: 3},
			{name: "keeps a record whose Seq the state has no name for", key: later, headSeq: 9},
			{
				name: "keeps a record above the Seq of the head whose Seq the state has no name for", key: later,
				headSeq: 3,
			},
			{
				name:    "keeps a record off the chain above the Seq of the head",
				key:     recordPrefix + string(appendName(nil, 3, []byte("a record of another process"))),
				headSeq: 2,
			},
			{
				name:    "keeps a snapshot above the Seq of the new one",
				key:     snapshotPrefix + string(appendName(nil, 4, []byte("a later snapshot"))),
				headSeq: 3,
			},
			{name: "keeps a key under snapshots/ that is not a name", key: snapshotPrefix + "a key", headSeq: 3},
			{name: "keeps a key under groups/ that is not a name", key: groupPrefix + "a key", headSeq: 3},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f, s := three(t)
				putObject(t, f.store, tt.key, []byte("an object"))

				w := &snapper{base: installed{seq: 2, objects: tt.base}, seq: 3}
				assert.NoError(t, s.collect(bounded(t), w, &snapshot{Objects: tt.objects}, "", tt.headSeq),
					"collect must walk the store")

				_, err := f.store.Stat(t.Context(), tt.key)
				assert.NoError(t, err, "collect must keep the object")
			})
		}

		t.Run("deletes a group below the Seq of the new snapshot", func(t *testing.T) {
			t.Parallel()
			f, s := three(t)
			group := groupPrefix + string(appendName(nil, 2, []byte("a group")))
			putObject(t, f.store, group, []byte("a group"))

			w := &snapper{base: installed{seq: 2}, seq: 3}
			assert.NoError(t, s.collect(bounded(t), w, &snapshot{}, "", 3), "collect must walk the store")

			_, err := f.store.Stat(t.Context(), group)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "collect must delete the group")
		})
	})
}
