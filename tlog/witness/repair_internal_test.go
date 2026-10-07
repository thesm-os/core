// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"sync"
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/errs"
)

func TestRepairInternal(t *testing.T) {
	t.Parallel()

	l := newInternalLog(t, "example.com/a")

	// unlined returns a server over a store with hooks, and the key of the
	// record of a commit of l that the store refused the lines of.
	unlined := func(tb testing.TB) (*Server, string) {
		tb.Helper()

		f := newInternalFixture(tb, l)
		st := &faulty{Store: f.store}
		cfg := f.config()
		cfg.State = st
		s := newInternalServer(tb, cfg)

		st.refuse(linesPrefix)
		l.advance(tb, s, 0, 5, nil)
		st.refuse("")

		s.mu.RLock()
		defer s.mu.RUnlock()

		return s, recordPrefix + s.st.head.Record
	}

	t.Run("repair", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves no repair of a record whose lines it stored", func(t *testing.T) {
			t.Parallel()
			s, key := unlined(t)
			assert.NoError(t, s.repair(bounded(t), key), "repair must store the lines")

			s.rmu.Lock()
			defer s.rmu.Unlock()

			assert.NotContains(t, s.repairs, key, "a repair that succeeded must leave the server")
		})
	})

	t.Run("repairRecord", func(t *testing.T) {
		t.Parallel()

		t.Run("adds no origin that left the state before the repair", func(t *testing.T) {
			t.Parallel()
			s, key := unlined(t)
			h := hashOrigin(l.origin)

			s.mu.Lock()
			s.st.origins.Delete(h)
			s.mu.Unlock()

			assert.NoError(t, s.repairRecord(bounded(t), key), "repairRecord must store the lines")
			assert.False(t, s.st.origins.Has(h), "the repair must not add the origin again")
		})

		t.Run("returns the error of the store for a record that it does not read", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			missing := recordPrefix + string(appendName(nil, 1, []byte("a missing record")))

			err := s.repairRecord(bounded(t), missing)
			assert.Equal(t, errs.Classify(err), errs.NotFound, "repairRecord must return the error of the store")
		})

		t.Run("returns ErrJournal for a record whose note is not a signed note", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			name := putRecord(t, f.store, &record{Seq: 1, Time: 1, Calls: []call{{Note: []byte("not a note")}}})

			err := f.server(t).repairRecord(bounded(t), recordPrefix+name)
			assert.ErrorIs(t, err, ErrJournal, "repairRecord must refuse the record")
		})

		t.Run("moves the served position of an origin to the later of two calls of the record", func(t *testing.T) {
			t.Parallel()
			other := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, other)
			st := &faulty{Store: f.store}
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)
			st.refuse(linesPrefix)

			var wg sync.WaitGroup

			returned := make(chan struct{})

			wg.Go(func() {
				defer close(returned)

				other.advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, returned, "the first commit must sign")

			// The next commit takes both calls of l in one record without lines.
			wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() { l.advance(t, s, 5, 6, nil) })
			waitQueue(t, s, 2)
			close(g.release)
			waitAll(t, &wg, "every call must return")
			st.refuse("")

			s.mu.RLock()
			key := recordPrefix + s.st.head.Record
			s.mu.RUnlock()

			assert.NoError(t, s.repairRecord(bounded(t), key), "repairRecord must store the lines")

			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()

			assert.Equal(t, o.served.call, 1, "the repair must serve the later call of the record")
		})
	})
}
