// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
)

func TestSyncInternal(t *testing.T) {
	t.Parallel()

	t.Run("sync", func(t *testing.T) {
		t.Parallel()

		l := newInternalLog(t, "example.com/a")

		faults := []struct {
			give func(tb testing.TB, st blob.Store)
			name string
		}{
			{
				name: "returns ErrJournal for a head that does not decode",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					putObject(tb, st, headKey, []byte{0xff})
				},
			},
			{
				name: "returns ErrJournal for a head above 1 KiB",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					putObject(tb, st, headKey, make([]byte, maxHeadBytes+1))
				},
			},
			{
				name: "returns ErrJournal for a head that names no record",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					putHead(tb, st, head{Record: "a record"})
				},
			},
			{
				name: "returns ErrJournal for a head that names no snapshot",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					putHead(tb, st, head{Record: putRecord(tb, st, &record{Seq: 1, Time: 1}), Snapshot: "a snapshot"})
				},
			},
			{
				name: "returns ErrJournal for a record whose bytes do not hash to its name",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					name := putRecord(tb, st, &record{Seq: 1, Time: 1})
					putObject(tb, st, recordPrefix+name, []byte("other bytes"))
					putHead(tb, st, head{Record: name})
				},
			},
			{
				name: "returns ErrJournal for a record that does not decode",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					data := []byte{0xff}
					name := string(appendName(nil, 1, data))
					putObject(tb, st, recordPrefix+name, data)
					putHead(tb, st, head{Record: name})
				},
			},
			{
				name: "returns ErrJournal for a record whose Seq is not the Seq of its name",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					data, _ := (&record{Seq: 5, Time: 1}).MarshalBinary()
					name := string(appendName(nil, 1, data))
					putObject(tb, st, recordPrefix+name, data)
					putHead(tb, st, head{Record: name})
				},
			},
			{
				name: "returns ErrJournal for a first record with a predecessor",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					prev := string(appendName(nil, 1, []byte("a predecessor")))
					putHead(tb, st, head{Record: putRecord(tb, st, &record{Seq: 1, Time: 1, Prev: prev})})
				},
			},
			{
				name: "returns ErrJournal for a record whose predecessor does not precede it",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					first := putRecord(tb, st, &record{Seq: 1, Time: 1})
					third := putRecord(tb, st, &record{Seq: 3, Time: 1, Prev: first})
					putHead(tb, st, head{Record: third})
				},
			},
			{
				name: "returns ErrJournal for a chain that ends before the record of its snapshot",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					first := putRecord(tb, st, &record{Seq: 1, Time: 1})
					later := string(appendName(nil, 3, []byte("a later record")))
					putHead(tb, st, head{Record: first, Snapshot: putSnapshot(tb, st, &snapshot{Record: later})})
				},
			},
			{
				name: "returns ErrJournal for a record of the chain that is missing",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					putHead(tb, st, head{Record: string(appendName(nil, 1, []byte("a missing record")))})
				},
			},
			{
				name: "returns ErrJournal for a snapshot that is missing",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					first := putRecord(tb, st, &record{Seq: 1, Time: 1})
					putHead(tb, st, head{Record: first, Snapshot: string(appendName(nil, 1, []byte("a snapshot")))})
				},
			},
			{
				name: "returns ErrJournal for a snapshot that does not decode",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					data := []byte{0xff}
					name := string(appendName(nil, 1, data))
					putObject(tb, st, snapshotPrefix+name, data)
					putHead(tb, st, head{Record: putRecord(tb, st, &record{Seq: 1, Time: 1}), Snapshot: name})
				},
			},
			{
				name: "returns ErrJournal for a snapshot whose objects are not records or groups",
				give: func(tb testing.TB, st blob.Store) {
					tb.Helper()
					first := putRecord(tb, st, &record{Seq: 1, Time: 1})
					snap := &snapshot{Record: first, Objects: []object{{Key: "other/" + first}}}
					putHead(tb, st, head{Record: first, Snapshot: putSnapshot(tb, st, snap)})
				},
			},
		}
		for _, tt := range faults {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newInternalFixture(t, l)
				tt.give(t, f.store)

				_, err := NewServer(bounded(t), f.config())
				testkit.ErrorIs(t, err, ErrJournal, "NewServer must refuse the journal")
				testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			})
		}

		t.Run("returns the error of the store for a head that it does not read", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			st := &faulty{Store: f.store, get: func(string) error { return errors.New("the store failed") }}
			cfg := f.config()
			cfg.State = st

			_, err := NewServer(bounded(t), cfg)
			testkit.Error(t, err, "NewServer must fail")
			testkit.ErrorIsNot(t, err, ErrJournal, "the error must be the error of the store")
		})

		t.Run("returns the cause of ctx when ctx ends", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("the caller left")
			cancel(cause)

			_, err := NewServer(ctx, newInternalFixture(t, l).config())
			testkit.ErrorIs(t, err, cause, "NewServer must return the cause of ctx")
		})

		t.Run("starts the walk again when a record of the chain is missing once", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			l.advance(t, f.server(t), 0, 5, nil)

			var missed atomic.Bool

			st := &faulty{Store: f.store, get: func(key string) error {
				if strings.HasPrefix(key, recordPrefix) && missed.CompareAndSwap(false, true) {
					return errs.WithClass(errors.New("the record is gone"), errs.NotFound)
				}

				return nil
			}}
			cfg := f.config()
			cfg.State = st

			s, err := NewServer(bounded(t), cfg)
			testkit.NoError(t, err, "NewServer must walk again")
			testkit.Equal(t, s.st.seq, uint64(1), "NewServer must read the record")
		})

		t.Run("ignores the records off the chain", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			s := f.server(t)
			l.advance(t, s, 0, 5, nil)
			putRecord(t, f.store, &record{Seq: 2, Time: 1})
			l.advance(t, s, 5, 6, nil)

			again := f.server(t)
			o, ok := again.st.origins.Get(hashOrigin(l.origin))
			testkit.True(t, ok, "NewServer must read the origin")
			testkit.Equal(t, o.size, uint64(6), "NewServer must read the chain")
		})

		t.Run("rebuilds the state from a snapshot and the records after it", func(t *testing.T) {
			t.Parallel()
			b := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, b)
			s := f.server(t)
			l.advance(t, s, 0, 5, nil)
			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
			b.advance(t, s, 5, 6, nil)
			testkit.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")
			b.advance(t, s, 6, 7, nil)

			again := f.server(t)
			for _, want := range []struct {
				origin checkpoint.Origin
				size   uint64
			}{{l.origin, 5}, {b.origin, 7}} {
				o, ok := again.st.origins.Get(hashOrigin(want.origin))
				testkit.True(t, ok, "NewServer must read "+string(want.origin))
				testkit.Equal(t, o.size, want.size, "NewServer must read the size of "+string(want.origin))
			}

			o, _ := again.st.origins.Get(hashOrigin(l.origin))
			testkit.True(t, o.served.group, "NewServer must serve the group of the idle origin")
		})

		t.Run("walks the retired objects for a snapshot whose base it did not take", func(t *testing.T) {
			t.Parallel()
			b := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, b)
			s := f.server(t)
			l.advance(t, s, 0, 5, nil)
			f.refuse(l.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(bounded(t)), "the retiring snapshot must install")
			b.advance(t, s, 5, 6, nil)
			testkit.NoError(t, s.snapshot(bounded(t)), "the next snapshot must install")

			again := f.server(t)
			h := hashOrigin(l.origin)
			testkit.False(t, again.st.origins.Has(h), "the state must not contain the retired origin")
			testkit.True(t, again.st.retired.Len() == 1, "the state must know the retired origin")
		})

		t.Run("reports a record below the record of the snapshot as its own", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			behind := f.server(t)
			s := f.server(t)

			names := make([]string, 0, 3)
			for size := uint64(1); size <= 3; size++ {
				l.advance(t, s, size-1, size, nil)
				names = append(names, s.st.head.Record)
			}

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			own, err := behind.sync(bounded(t), names[1])
			testkit.NoError(t, err, "sync must catch up to the head")
			testkit.True(t, own, "sync must find the record on the chain")

			other := f.server(t)
			other.mu.Lock()
			other.st = newState()
			other.mu.Unlock()

			own, err = other.sync(bounded(t), string(appendName(nil, 2, []byte("another record"))))
			testkit.NoError(t, err, "sync must catch up to the head")
			testkit.False(t, own, "sync must not find another record of the Seq")
		})

		t.Run("reports the record of the snapshot as its own", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			behind := f.server(t)
			s := f.server(t)

			for size := uint64(1); size <= 3; size++ {
				l.advance(t, s, size-1, size, nil)
			}

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			own, err := behind.sync(bounded(t), s.st.head.Record)
			testkit.NoError(t, err, "sync must catch up to the head")
			testkit.True(t, own, "sync must find the record of the snapshot on the chain")
		})

		t.Run("reports the head's record of a state that reflects the head as its own", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t, l).server(t)
			l.advance(t, s, 0, 5, nil)

			s.mu.RLock()
			name := s.st.head.Record
			s.mu.RUnlock()

			own, err := s.sync(bounded(t), name)
			testkit.NoError(t, err, "sync must read the head")
			testkit.True(t, own, "sync must find the record on the chain of the state")
		})

		t.Run("records the time of a sync of a state that reflects the head", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			s := f.server(t)
			f.clock.Advance(time.Minute)

			_, err := s.sync(bounded(t), "")
			testkit.NoError(t, err, "sync must read the head")
			testkit.Equal(t, s.st.synced, f.clock.Time(), "sync must record the time of the reading")
		})

		t.Run("catches up to a head whose snapshot changed and whose record did not", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			s := f.server(t)
			l.advance(t, s, 0, 5, nil)

			// Another process installs a snapshot at the record of the head.
			testkit.NoError(t, f.server(t).snapshot(bounded(t)), "the other process must install")

			_, err := s.sync(bounded(t), "")
			testkit.NoError(t, err, "sync must catch up to the head")

			h, _, err := readHead(t.Context(), f.store)
			testkit.NoError(t, err, "the head must read")
			testkit.Equal(t, s.st.snap.name, h.Snapshot, "the state must take the snapshot of the head")
		})

		t.Run("walks twice from one version of the head before it returns the error of a snapshot that it cannot take",
			func(t *testing.T) {
				t.Parallel()
				f := newInternalFixture(t, l)
				first := putRecord(t, f.store, &record{Seq: 1, Time: 1})
				snap := &snapshot{
					Record:  first,
					Origins: []snapOrigin{{Origin: l.origin, Object: 1, Root: l.update(t, 0, 5, nil).Body.Root}},
				}
				putHead(t, f.store, head{Record: first, Snapshot: putSnapshot(t, f.store, snap)})

				var reads atomic.Int64

				st := &faulty{Store: f.store, get: func(key string) error {
					if strings.HasPrefix(key, snapshotPrefix) {
						reads.Add(1)
					}

					return nil
				}}
				cfg := f.config()
				cfg.State = st

				_, err := NewServer(bounded(t), cfg)
				testkit.ErrorIs(t, err, ErrJournal, "NewServer must refuse the snapshot")
				testkit.Equal(t, reads.Load(), int64(2), "NewServer must walk twice from one version of the head")
			})

		t.Run("reports false for a record whose successor is missing", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			behind := f.server(t)
			s := f.server(t)

			names := make([]string, 0, 3)
			for size := uint64(1); size <= 3; size++ {
				l.advance(t, s, size-1, size, nil)
				names = append(names, s.st.head.Record)
			}

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			testkit.NoError(t, f.store.Delete(t.Context(), recordPrefix+names[2], ""), "the record must delete")

			own, err := behind.sync(bounded(t), names[1])
			testkit.NoError(t, err, "sync must catch up to the head")
			testkit.False(t, own, "sync must not know the record")
		})

		t.Run("returns the error of the store for the retired origins of a snapshot whose base it did not take",
			func(t *testing.T) {
				t.Parallel()
				b := newInternalLog(t, "example.com/b")
				f := newInternalFixture(t, l, b)
				s := f.server(t)
				l.advance(t, s, 0, 5, nil)
				testkit.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
				l.advance(t, s, 5, 6, nil)
				testkit.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

				st := &faulty{Store: f.store, list: func(prefix string) error {
					if prefix == retiredPrefix {
						return errors.New("the walk failed")
					}

					return nil
				}}
				cfg := f.config()
				cfg.State = st

				_, err := NewServer(bounded(t), cfg)
				testkit.Error(t, err, "NewServer must fail")
				testkit.ErrorIsNot(t, err, ErrJournal, "the error must be the error of the store")
			})

		t.Run("returns the error of the store for a record below the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			behind := newInternalServer(t, cfg)
			s := f.server(t)

			names := make([]string, 0, 3)
			for size := uint64(1); size <= 3; size++ {
				l.advance(t, s, size-1, size, nil)
				names = append(names, s.st.head.Record)
			}

			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			st.get = func(key string) error {
				if key == recordPrefix+names[2] {
					return errors.New("the read failed")
				}

				return nil
			}

			_, err := behind.sync(bounded(t), names[1])
			testkit.Error(t, err, "sync must fail")
		})

		t.Run("applies no walk to a state that changed during it", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)
			l.advance(t, f.server(t), 0, 5, nil)

			var once atomic.Bool

			st.mu.Lock()
			st.get = func(key string) error {
				if strings.HasPrefix(key, recordPrefix) && once.CompareAndSwap(false, true) {
					_, err := s.sync(bounded(t), "")
					testkit.NoError(t, err, "the inner sync must catch up to the head")
				}

				return nil
			}
			st.mu.Unlock()

			_, err := s.sync(bounded(t), "")
			testkit.NoError(t, err, "sync must catch up to the head")
			testkit.Equal(t, s.st.seq, uint64(1), "the state must contain the record once")
		})
	})

	t.Run("walk", func(t *testing.T) {
		t.Parallel()

		l := newInternalLog(t, "example.com/a")

		t.Run("returns ErrJournal for a head of another record at the Seq of the state", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			first := putRecord(t, f.store, &record{Seq: 1, Time: 1})
			ours := putRecord(t, f.store, &record{Seq: 2, Time: 1, Prev: first})
			theirs := putRecord(t, f.store, &record{Seq: 2, Time: 2, Prev: first})
			snap := putSnapshot(t, f.store, &snapshot{Record: theirs})
			s := f.server(t)

			// The state reflects the record ours of Seq 2, and the head names
			// theirs, another record of Seq 2, and a snapshot at it.
			_, err := s.walk(bounded(t), head{Record: theirs, Snapshot: snap}, base{record: ours, seq: 2}, "")
			testkit.ErrorIs(t, err, ErrJournal, "walk must refuse a chain without the record of the state")
		})
	})
}

// putObject stores data under key in st.
func putObject(tb testing.TB, st blob.Store, key string, data []byte) {
	tb.Helper()

	_, err := blob.PutBytes(tb.Context(), st, key, data, blob.PutOptions{})
	testkit.NoError(tb, err, "Put must store "+key)
}

// putHead stores h as the head of st.
func putHead(tb testing.TB, st blob.Store, h head) {
	tb.Helper()

	data, _ := h.MarshalBinary()
	putObject(tb, st, headKey, data)
}

// putRecord stores rec under its name in st, and returns the name.
func putRecord(tb testing.TB, st blob.Store, rec *record) string {
	tb.Helper()

	data, err := rec.MarshalBinary()
	testkit.NoError(tb, err, "the record must encode")

	name := string(appendName(nil, rec.Seq, data))
	putObject(tb, st, recordPrefix+name, data)

	return name
}

// putSnapshot stores snap under its name in st, and returns the name.
func putSnapshot(tb testing.TB, st blob.Store, snap *snapshot) string {
	tb.Helper()

	data, err := snap.MarshalBinary()
	testkit.NoError(tb, err, "the snapshot must encode")

	seq, _ := parseName(snap.Record)
	name := string(appendName(nil, seq, data))
	putObject(tb, st, snapshotPrefix+name, data)

	return name
}
