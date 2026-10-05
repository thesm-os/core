// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/version"
)

// repairing is the repair of one record, which the GETs of the record and
// the snapshot writer wait for.
type repairing struct {
	// err is the result of the repair, which is set before done closes.
	err error

	// done closes when the repair ends.
	done chan struct{}
}

// repair runs the repair of the record under key on a goroutine of its own,
// or joins the repair of the record that runs, and waits for it or for the
// end of ctx. A process runs one repair of a record at a time. A repair
// that fails remains, so the GETs of the record start no other repair of
// it until a snapshot installs.
//
// The repair runs under the two contexts of a commit: the store's, with
// the deadline Timeout + SignTimeout, and the signatures', with the
// deadline SignTimeout.
//
// Returns the error of repairRecord, and the cause of ctx when ctx ends
// first, after which the repair still runs.
func (s *Server) repair(ctx context.Context, key string) error {
	r, run := s.startRepair(key)
	if run {
		// The repair outlives the GET that starts it, because the GETs of the
		// record wait for it.
		go func() { //nolint:gosec,contextcheck // G118: see above
			rctx, cancel := context.WithTimeout(context.Background(), s.timeout+s.signTimeout)
			defer cancel()

			s.endRepair(key, r, s.repairRecord(rctx, key))
		}()
	}

	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// startRepair returns the repair of the record under key, and reports
// whether the caller runs it: a record without a repair in s gets a new
// one.
func (s *Server) startRepair(key string) (*repairing, bool) {
	s.rmu.Lock()
	defer s.rmu.Unlock()

	if r, ok := s.repairs[key]; ok {
		return r, false
	}

	r := &repairing{done: make(chan struct{})}
	s.repairs[key] = r

	return r, true
}

// endRepair ends r, the repair of the record under key, with err. A
// repair that succeeded leaves s, because the lines that it stored end
// every later repair of the record.
func (s *Server) endRepair(key string, r *repairing, err error) {
	r.err = err

	if err == nil {
		s.rmu.Lock()
		delete(s.repairs, key)
		s.rmu.Unlock()
	}

	close(r.done)
}

// repairRecord stores the lines of the record under key, which a commit
// left without them: it signs the note of every call of the record at the
// record's time with every cosigner, as cosign signs, and creates the
// lines. It then moves the served position of each origin of the record to
// its latest update in the record, unless the origin has a served update
// after it.
//
// It signs at the time of the record, so its cosignatures state what the
// commit's would have stated. The signatures run under a context of their
// own with the deadline SignTimeout. When the lines exist, another process
// stored them first, and the repair succeeds with them.
//
// Returns the errors of the store, the error of cosign, and an error that
// wraps [ErrJournal] for a record that does not decode or whose notes are
// not signed notes.
func (s *Server) repairRecord(ctx context.Context, key string) error {
	name := key[len(recordPrefix):]

	rec, _, err := s.readRecord(ctx, name)
	if err != nil {
		return err
	}

	texts := make([][]byte, len(rec.Calls))
	for i, c := range rec.Calls {
		if texts[i], err = note.TextOf(c.Note); err != nil {
			return fmt.Errorf("%w: a note of the record %s: %w", ErrJournal, name, err)
		}
	}

	var g signing

	// A context of its own, which a slow store cannot take the time of.
	sctx, cancel := context.WithTimeout(context.Background(), s.signTimeout)
	at := time.Unix(int64(rec.Time), 0) //nolint:gosec // the time of a record is the seconds of a reading
	err = s.cosign(sctx, texts, at, &g) //nolint:contextcheck // see above

	cancel()

	if err != nil {
		return err
	}

	ls := lines{Calls: make([][]byte, len(rec.Calls))}
	for i := range rec.Calls {
		ls.Calls[i] = s.appendLines(nil, &g, i)
	}

	// The encoding of lines has no error: it has only bytes.
	data, _ := ls.MarshalBinary()

	if err := create(ctx, s.store, linesPrefix+name, data); err != nil && !errors.Is(err, version.ErrExists) {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// The later calls first, so the latest update of an origin in the
	// record becomes its served position.
	for i, c := range slices.Backward(rec.Calls) {
		for j, e := range slices.Backward(c.Updates) {
			h := hashOrigin(e.Origin)

			o, ok := s.st.origins.Get(h)
			if ok && (o.served.key == "" || o.served.seq < rec.Seq) {
				o.served = position{key: key, seq: rec.Seq, call: i, update: j}
				s.st.origins.Set(h, o)
			}
		}
	}

	return nil
}
