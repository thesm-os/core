// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

// base is what a walk of the chain starts from: the head and the
// snapshot that the state reflects.
type base struct {
	// record is the name of the head's record of the state, and snapshot
	// the name of the snapshot that the state took last. Both are empty
	// for an empty state.
	record, snapshot string

	// version is the version of the head that the state reflects.
	version version.Version

	// seq and time are the Seq and the Time of the record, and end its
	// Offset plus the length of its encoding.
	seq, time, end uint64
}

// walked is a record that a walk read, with the length of its encoding.
type walked struct {
	rec  *record
	key  string
	size uint64
}

// plan is the result of a walk from a head of the store to a base: the
// snapshot that the state takes, and the records that it applies after
// it.
type plan struct {
	// snap is the snapshot of the head, and nil when the state took it
	// already.
	snap *snapshot

	// retired is the set of the retired origins that a walk of the store
	// read, and nil when the base of snap is the snapshot of the state.
	retired *btree.Set[uint64]

	// records are the records after the base, or after the record of
	// snap, newest first.
	records []walked

	// size is the length of the encoding of snap.
	size int64

	// own reports whether the walk found the record that it looked for on
	// the chain.
	own bool
}

// sync brings the state of s to the head of its store, and reports
// whether the record named own is on the chain of that head. A commit
// whose replacement of the head failed passes the name of its record, and
// a refresh passes the empty name.
//
// sync reads the head. When the state reflects that head, it records the
// time of the reading and returns. Otherwise it walks the chain from the
// head back to the record of the state, as walk describes, and applies
// what it read under the mutex of s, when the state still reflects the
// head that the walk started from. A state that changed meanwhile, and a
// walk that found an object missing, start again from a new reading of
// the head.
//
// Error modes:
//   - An error that wraps [ErrJournal] when two walks from one version of
//     the head find an object that does not decode or whose hash is not its
//     name, a chain that ends before its snapshot or before the record of
//     the state, or a record missing from the chain after the record of the
//     snapshot.
//   - The cause of ctx when ctx ends, and the errors of the store.
//
// # Allocation contract
//
// Allocates what it reads: the head, and for a state behind the head the
// records and the snapshot that the walk reads.
func (s *Server) sync(ctx context.Context, own string) (bool, error) {
	var faulted version.Version

	for {
		if err := ctx.Err(); err != nil {
			return false, context.Cause(ctx)
		}

		h, v, err := readHead(ctx, s.store)
		if err != nil {
			return false, err
		}

		ownSeq, _ := parseName(own)

		s.mu.RLock()
		b := base{
			record: s.st.head.Record, snapshot: s.st.snap.name, version: s.st.version,
			seq: s.st.seq, time: s.st.time, end: s.st.end,
		}
		chained, _ := s.st.chain.Get(ownSeq)
		s.mu.RUnlock()

		// A refresh can apply the record named own before this call reads
		// the head. A refresh, which passes the empty name, ignores the
		// result.
		seen := chained == own

		// A state that a commit moved since the read reflects a later head,
		// so it is as current as the reading.
		//dokimi:mutate-skip sbr-delete,ror-false: a state that reflects the head walks no object, and applyPlan of the empty plan sets the same head, version and count of origins and the time of the reading, or reads the head again after a commit
		if v == b.version {
			s.mu.Lock()
			s.st.synced = s.clock.Time()
			s.mu.Unlock()

			return seen, nil
		}

		p, err := s.walk(ctx, h, b, own)
		if errors.Is(err, ErrJournal) && v != faulted {
			faulted = v

			continue
		}

		if err != nil {
			return false, err
		}

		applied, err := s.applyPlan(ctx, &p, h, v, b.version)
		if err != nil && v != faulted {
			faulted = v

			continue
		}

		if err != nil {
			return false, err
		}

		if applied {
			return p.own || seen, nil
		}
	}
}

// applyPlan applies p, the plan of a walk from the head h of version v,
// to the state of s, when the state still reflects the head of version
// from. It reports whether it applied p.
//
// Returns the error of [state.take], which wraps [ErrJournal], after which
// the state is unchanged.
func (s *Server) applyPlan(ctx context.Context, p *plan, h head, v, from version.Version) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.st.version != from {
		return false, nil
	}

	if p.snap != nil {
		if err := s.st.take(p.snap, h.Snapshot, p.size, p.retired); err != nil {
			return false, err
		}
	}

	for _, w := range slices.Backward(p.records) {
		s.st.apply(w.rec, w.key, w.size)
	}

	s.st.head, s.st.version, s.st.synced = h, v, s.clock.Time()
	s.metrics.origins.Set(ctx, float64(s.st.origins.Len()))

	return true, nil
}

// walk reads what the state of base b needs to catch up to the head h:
//
//   - The snapshot of h, when b has not taken it, and the retired origins
//     of the store, when the base of that snapshot is not the snapshot of
//     b.
//   - The records from the record of h back to the record of b. A state
//     before the record of the snapshot reads the records back to that
//     record, and takes the snapshot instead of the records before it.
//
// When own is not empty, walk reports whether own is on the chain. A walk
// that stops at the record of the snapshot then reads the records before
// it back to the Seq of own, without keeping them. A record that it does
// not find there leaves own unknown, and walk reports false.
//
// Returns an error that wraps [ErrJournal] for an object that does not
// decode or whose hash is not its name, for a record whose Seq is not the
// Seq of its name or whose predecessor does not precede it, for a chain
// that ends before the record that the walk stops at, and for a record of
// the chain that is missing. Returns the errors of the store.
func (s *Server) walk(ctx context.Context, h head, b base, own string) (plan, error) {
	var p plan

	stop, stopSeq := b.record, b.seq

	if h.Snapshot != b.snapshot {
		snap, size, err := s.readSnapshot(ctx, h.Snapshot)
		if errs.Classify(err) == errs.NotFound {
			return plan{}, fmt.Errorf("%w: the snapshot %s of the head is missing: %w", ErrJournal, h.Snapshot, err)
		}

		if err != nil {
			return plan{}, err
		}

		p.snap, p.size = snap, size

		if snap.Base != b.snapshot {
			if p.retired, err = s.listRetired(ctx); err != nil {
				return plan{}, err
			}
		}

		if seq, _ := parseName(snap.Record); seq > b.seq {
			stop, stopSeq = snap.Record, seq
		}
	}

	name, err := s.walkTo(ctx, h.Record, stop, stopSeq, own, &p)
	if err != nil {
		return plan{}, err
	}

	// A record that walkTo found is after stopSeq, and the state of the
	// caller decides about a record at or before the record of b.
	ownSeq, ok := parseName(own)
	if !ok || ownSeq > stopSeq || stopSeq == b.seq {
		return p, nil
	}

	// The walk stopped at the record of the snapshot, after own. The
	// records from there back to the Seq of own decide whether own is on
	// the chain.
	for seq := stopSeq; seq > ownSeq; seq-- {
		rec, _, err := s.readRecord(ctx, name)
		if errs.Classify(err) == errs.NotFound {
			return p, nil
		}

		if err != nil {
			return plan{}, err
		}

		name = rec.Prev
	}

	p.own = name == own

	return p, nil
}

// walkTo reads the records of the chain from the record named from back
// to the record named stop, of Seq stopSeq, without it, and adds them to
// p, newest first. It reports in p whether it read the record named own.
// It returns the name at which it stopped, which is stop.
//
// The chain has one record of each Seq. The walk reads one record for each
// Seq from that of from down to stopSeq, and then requires the name stop.
// An empty stop walks back to the first record of the chain. A from below
// stopSeq reads nothing, and fails that requirement. readHead refuses a
// from that is not a name, and the loop refuses a predecessor that is not
// the name of the next Seq, so the first record of the chain is the one
// whose predecessor is empty.
//
// Returns the errors of walk.
func (s *Server) walkTo(ctx context.Context, from, stop string, stopSeq uint64, own string, p *plan) (string, error) {
	seq, _ := parseName(from)
	name := from

	for ; seq > stopSeq; seq-- {
		// A name that does not parse has the Seq 0, below every Seq of the
		// loop.
		if got, _ := parseName(name); got != seq {
			return "", fmt.Errorf("%w: the chain from %s does not contain the record %q", ErrJournal, from, stop)
		}

		rec, size, err := s.readRecord(ctx, name)
		if errs.Classify(err) == errs.NotFound {
			return "", fmt.Errorf("%w: the record %s of the chain is missing: %w", ErrJournal, name, err)
		}

		if err != nil {
			return "", err
		}

		if rec.Seq != seq {
			return "", fmt.Errorf("%w: the record %s has the Seq %d", ErrJournal, name, rec.Seq)
		}

		p.own = p.own || name == own
		p.records = append(p.records, walked{rec: rec, key: recordPrefix + name, size: size})
		name = rec.Prev
	}

	if name != stop {
		return "", fmt.Errorf("%w: the chain from %s does not contain the record %q", ErrJournal, from, stop)
	}

	return name, nil
}

// readRecord reads and decodes the record named name, and returns it with
// the length of its encoding.
//
// Returns the errors of readNamed, and an error that wraps [ErrJournal]
// for a record that does not decode.
func (s *Server) readRecord(ctx context.Context, name string) (*record, uint64, error) {
	data, err := readNamed(ctx, s.store, recordPrefix, name, maxObjectBytes)
	if err != nil {
		return nil, 0, err
	}

	rec := new(record)
	if err := rec.DecodeKanon(data, kanon.Options{}); err != nil {
		return nil, 0, fmt.Errorf("%w: the record %s does not decode: %w", ErrJournal, name, err)
	}

	return rec, uint64(len(data)), nil
}

// readSnapshot reads and decodes the snapshot named name, and returns it
// with the length of its encoding.
//
// Returns the errors of readNamed, and an error that wraps [ErrJournal]
// for a snapshot that does not decode.
func (s *Server) readSnapshot(ctx context.Context, name string) (*snapshot, int64, error) {
	data, err := readNamed(ctx, s.store, snapshotPrefix, name, s.snapshotLimit)
	if err != nil {
		return nil, 0, err
	}

	snap := new(snapshot)
	if err := snap.DecodeKanon(data, kanon.Options{}); err != nil {
		return nil, 0, fmt.Errorf("%w: the snapshot %s does not decode: %w", ErrJournal, name, err)
	}

	return snap, int64(len(data)), nil
}
