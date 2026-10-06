// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"sync"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

func TestRepairInternal(t *testing.T) {
	t.Parallel()

	t.Run("repairRecord", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the store for a record that it does not read", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			missing := recordPrefix + string(appendName(nil, 1, []byte("a missing record")))

			err := s.repairRecord(bounded(t), missing)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "repairRecord must return the error of the store")
		})

		t.Run("returns ErrJournal for a record whose note is not a signed note", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			name := putRecord(t, f.store, &record{Seq: 1, Time: 1, Calls: []call{{Note: []byte("not a note")}}})

			err := f.server(t).repairRecord(bounded(t), recordPrefix+name)
			testkit.ErrorIs(t, err, ErrJournal, "repairRecord must refuse the record")
		})

		t.Run("moves the served position of an origin to the later of two calls of the record", func(t *testing.T) {
			t.Parallel()
			l, other := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
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

			testkit.NoError(t, s.repairRecord(bounded(t), key), "repairRecord must store the lines")

			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()

			testkit.Equal(t, o.served.call, 1, "the repair must serve the later call of the record")
		})
	})
}
