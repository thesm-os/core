// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

const (
	// snapshotBytes is the least amount of records after the installed
	// snapshot at which the next snapshot is due.
	snapshotBytes = 67108864

	// maxGroupBytes bounds the notes, the lines and the prefixes of a
	// group, apart from a group of one call above it.
	maxGroupBytes = 1048576

	// warnShare is the share of MaxOrigins, in tenths, above which a
	// snapshot logs a warning.
	warnShare = 9
)

// move is an update that a snapshot moves into a new group.
type move struct {
	// from is the latest position of the origin: a record before the
	// record of the base, or a group that the snapshot compacts.
	from position

	// hash is the hash of the origin.
	hash originHash
}

// placed is the position of a moved update in the new groups: the index
// of its group, of its call in the group, and of the update in the call.
type placed struct {
	group, call, update int
}

// snapper is the memory of the writer of one snapshot.
type snapper struct {
	// origins is the clone of the state that the snapshot reads.
	origins *btree.MapFunc[originHash, originState]

	// placed maps the hash of each moved origin to its new position.
	placed map[originHash]placed

	// retired contains the hashes of the origins that the snapshot
	// retires.
	retired map[originHash]struct{}

	// record is the name of the head's record of the clone, the record of
	// the snapshot.
	record string

	// keys are the keys of the new groups, and counts their numbers of
	// updates, in the order of their creation.
	keys   []string
	counts []uint32

	// group is the group that the writer fills.
	group group

	// prefixes are the first 8 bytes of the hashes of the retired
	// origins.
	prefixes []uint64

	// base is the installed snapshot when the writer started.
	base installed

	// seq, time and end are the Seq, the Time and the end of the record
	// of the snapshot.
	seq, time, end uint64

	// bytes are the notes, the lines and the prefixes of group.
	bytes int

	// repaired reports whether the snapshot tried its one repair.
	repaired bool
}

// writeSnapshot writes a snapshot of the state of s, installs it, and
// collects the garbage that it leaves, under a context of its own with the
// deadline Timeout + SignTimeout. It logs a snapshot that fails.
func (s *Server) writeSnapshot() {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout+s.signTimeout)
	defer cancel()

	if err := s.snapshot(ctx); err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: a snapshot failed", slog.Any("error", err))
	}
}

// snapshot takes the steps of a snapshot. Let B be the installed snapshot
// when it starts:
//
//  1. It retires every origin that Logs refuses and whose latest update is
//     older than Retention at the time of the snapshot's record, and
//     creates its retired object.
//  2. It moves into new groups the latest update of every other origin
//     whose position refers to a record before B's record.
//  3. It moves into new groups the current updates of every group of B in
//     which fewer than half of the updates are still the latest of their
//     origins.
//  4. It repairs at most one record without lines, older than the repair
//     age, before it moves the record's updates. A record without lines
//     keeps its updates otherwise.
//  5. It creates the groups, then the snapshot.
//  6. It installs the snapshot while the head's snapshot is still B, up to
//     maxTries times.
//  7. It collects garbage.
//
// Returns the errors of the store, and an error that wraps [ErrJournal]
// for an object that does not decode, whose hash is not its name, or that
// does not contain the update of a position.
func (s *Server) snapshot(ctx context.Context) error {
	w := &snapper{placed: make(map[originHash]placed), retired: make(map[originHash]struct{})}

	s.mu.Lock()
	w.origins, w.base = s.st.origins.Clone(), s.st.snap
	w.record, w.seq, w.time, w.end = s.st.head.Record, s.st.seq, s.st.time, s.st.end
	s.mu.Unlock()

	if w.seq == w.base.seq {
		return nil
	}

	if err := s.retire(ctx, w); err != nil {
		return err
	}

	if err := s.moveAll(ctx, w, w.moves()); err != nil {
		return err
	}

	if err := s.flush(ctx, w); err != nil {
		return err
	}

	snap := w.build()

	// The encodings of the objects of a snapshot fail only for an origin
	// that is not Valid, and the state contains only Valid origins.
	data, _ := snap.MarshalBinary()

	name := string(appendName(nil, w.seq, data))
	if err := create(ctx, s.store, snapshotPrefix+name, data); err != nil && !errors.Is(err, version.ErrExists) {
		return err
	}

	headSeq, ok, err := s.install(ctx, w, snap, name, int64(len(data)))
	if err != nil || !ok {
		return err
	}

	return s.collect(ctx, w, snap, name, headSeq)
}

// retire creates the retired object of every origin of the clone of w that
// Logs refuses and whose latest update is older than Retention at the time
// of the record of the snapshot, and adds it to the retired origins of w.
//
// Returns the errors of the store.
func (s *Server) retire(ctx context.Context, w *snapper) error {
	at := time.Unix(int64(w.time), 0) //nolint:gosec // the time of a record is the seconds of a reading

	for h, o := range w.origins.All() {
		latest := time.Unix(int64(o.time), 0) //nolint:gosec // as above
		if _, ok := s.logs(o.origin); ok || at.Sub(latest) <= s.retention {
			continue
		}

		// The state contains only Valid origins, as snapshot states.
		data, _ := (&entry{Origin: o.origin, Root: o.root, Size: o.size}).MarshalBinary()

		key := retiredKey(h, o.size)
		if err := create(ctx, s.store, key, data); err != nil && !errors.Is(err, version.ErrExists) {
			return err
		}

		w.retired[h] = struct{}{}
		w.prefixes = append(w.prefixes, binary.BigEndian.Uint64(h[:]))
	}

	return nil
}

// moves returns the updates that the snapshot of w moves, in the order of
// their positions: the latest update of every origin that w does not
// retire and whose position is a record before the record of the base,
// and the latest updates in every group of the base in which fewer than
// half of the updates are current.
func (w *snapper) moves() []move {
	current := make(map[string]uint32)

	for h, o := range w.origins.All() {
		if _, gone := w.retired[h]; !gone && o.latest.group {
			current[o.latest.key]++
		}
	}

	compacted := make(map[string]bool)

	for _, obj := range w.base.objects {
		if obj.Updates > 0 && 2*current[obj.Key] < obj.Updates {
			compacted[obj.Key] = true
		}
	}

	var moves []move

	for h, o := range w.origins.All() {
		if _, gone := w.retired[h]; gone {
			continue
		}

		if (!o.latest.group && o.latest.seq < w.base.seq) || compacted[o.latest.key] {
			moves = append(moves, move{from: o.latest, hash: h})
		}
	}

	slices.SortFunc(moves, func(a, b move) int {
		return cmp.Or(strings.Compare(a.from.key, b.from.key), cmp.Compare(a.from.call, b.from.call),
			cmp.Compare(a.from.update, b.from.update))
	})

	return moves
}

// moveAll moves each update of moves into the groups of w, one object at a
// time: it reads the object of the updates, and adds each call with moved
// updates to the group that w fills.
//
// A record without lines keeps its updates, unless it is the one record
// that the snapshot repairs: the first such record older than the repair
// age, which the snapshot repairs under its own context.
//
// Returns the errors of the store, and an error that wraps [ErrJournal]
// for an object that does not decode or that does not contain the update
// of a position.
func (s *Server) moveAll(ctx context.Context, w *snapper, moves []move) error {
	for len(moves) > 0 {
		n := 1
		for n < len(moves) && moves[n].from.key == moves[0].from.key {
			n++
		}

		l, err := s.source(ctx, w, moves[0])
		if err != nil {
			return err
		}

		if l != nil {
			if err := s.place(ctx, w, l, moves[:n]); err != nil {
				return err
			}
		}

		moves = moves[n:]
	}

	return nil
}

// source reads the object of m, the first move of an object, for the
// snapshot of w, and returns nil for a record whose lines are missing and
// that the snapshot does not repair.
//
// Returns the errors of moveAll.
func (s *Server) source(ctx context.Context, w *snapper, m move) (*loaded, error) {
	p := m.from

	l, err := s.readLoaded(ctx, p)
	if p.group || errs.Classify(err) != errs.NotFound {
		return l, err
	}

	if w.repaired {
		return nil, nil //nolint:nilnil // a record that keeps its updates is no error
	}

	// The origins of the updates of one record have the time of the record.
	if o, _ := w.origins.Get(m.hash); !s.repairDue(o.time) {
		return nil, nil //nolint:nilnil // as above
	}

	w.repaired = true

	r, run := s.startRepair(p.key)
	if run {
		s.endRepair(p.key, r, s.repairRecord(ctx, p.key))
	}

	select {
	case <-r.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}

	if r.err == nil {
		return s.readLoaded(ctx, p)
	}

	return nil, nil //nolint:nilnil // a record whose repair fails keeps its updates
}

// place adds the calls of the updates of moves, which are in l, to the
// group that w fills, with one copy of the note and the lines of each
// call, and records the new position of each update. A group that would
// exceed maxGroupBytes with a call is created first, so a call above it
// takes a group of its own.
//
// Returns the errors of flush, and an error that wraps [ErrJournal] for a
// position that l does not contain an update of the origin at.
func (s *Server) place(ctx context.Context, w *snapper, l *loaded, moves []move) error {
	for len(moves) > 0 {
		at := moves[0].from.call

		n := 1
		for n < len(moves) && moves[n].from.call == at {
			n++
		}

		if at >= len(l.calls) {
			return fmt.Errorf("%w: the object %s has no call %d", ErrJournal, moves[0].from.key, at)
		}

		c := &l.calls[at]
		gc := call{Note: c.Note, Lines: l.lines[at]}
		size := len(c.Note) + len(gc.Lines)

		for _, m := range moves[:n] {
			if m.from.update >= len(c.Updates) || hashOrigin(c.Updates[m.from.update].Origin) != m.hash {
				return fmt.Errorf("%w: the object %s has no update of the origin at %d.%d", ErrJournal, m.from.key,
					m.from.call, m.from.update)
			}

			gc.Updates = append(gc.Updates, c.Updates[m.from.update])
			size += len(c.Updates[m.from.update].Prefix)
		}

		if len(w.group.Calls) > 0 && w.bytes+size > maxGroupBytes {
			if err := s.flush(ctx, w); err != nil {
				return err
			}
		}

		for i, m := range moves[:n] {
			w.placed[m.hash] = placed{group: len(w.keys), call: len(w.group.Calls), update: i}
		}

		w.group.Calls = append(w.group.Calls, gc)
		w.bytes += size
		moves = moves[n:]
	}

	return nil
}

// flush creates the group that w fills, when it has a call, and starts the
// next group.
//
// Returns the errors of the store.
func (s *Server) flush(ctx context.Context, w *snapper) error {
	if len(w.group.Calls) == 0 {
		return nil
	}

	// The updates of a group come from records and groups that decoded,
	// and their decode refuses an origin that is not Valid.
	data, _ := w.group.MarshalBinary()

	key := groupPrefix + string(appendName(nil, w.seq, data))
	if err := create(ctx, s.store, key, data); err != nil && !errors.Is(err, version.ErrExists) {
		return err
	}

	updates := 0
	for _, c := range w.group.Calls {
		updates += len(c.Updates)
	}

	w.keys = append(w.keys, key)
	w.counts = append(w.counts, uint32(updates)) //nolint:gosec // a group has at most maxUpdates updates per call
	w.group, w.bytes = group{}, 0

	return nil
}

// build returns the snapshot of w: every origin that it does not retire,
// in the order of their hashes, at its new position or its latest one,
// with the objects that the positions refer to.
func (w *snapper) build() *snapshot {
	snap := &snapshot{Record: w.record, Base: w.base.name, End: w.end, Retired: w.prefixes}
	index := make(map[string]uint32)
	counts := make(map[string]uint32)

	for _, obj := range w.base.objects {
		counts[obj.Key] = obj.Updates
	}

	for i, key := range w.keys {
		counts[key] = w.counts[i]
	}

	for h, o := range w.origins.All() {
		if _, gone := w.retired[h]; gone {
			continue
		}

		key, at, upd := o.latest.key, o.latest.call, o.latest.update
		if p, ok := w.placed[h]; ok {
			key, at, upd = w.keys[p.group], p.call, p.update
		}

		i, ok := index[key]
		if !ok {
			i = uint32(len(snap.Objects)) //nolint:gosec // the objects are at most the origins
			index[key] = i
			snap.Objects = append(snap.Objects, object{Key: key, Updates: counts[key]})
		}

		snap.Origins = append(snap.Origins, snapOrigin{
			Origin: o.origin, Root: o.root, Size: o.size, Time: o.time, Object: i,
			Call: uint32(at), Update: uint32(upd), //nolint:gosec // positions in an object of at most maxUpdates
		})
	}

	return snap
}

// install replaces the head with the head's record and the snapshot under
// name, while the head's snapshot is still the base of w, up to maxTries
// times. It reports whether it installed the snapshot, and returns the Seq
// of the head's record that it installed it with. A head with another
// snapshot means that another process installed one first, and the
// snapshot is abandoned.
//
// After the install, the state of s takes the snapshot when it reflects
// the head that the install replaced, and the failed repairs leave s.
//
// Returns the errors of the store.
func (s *Server) install(
	ctx context.Context, w *snapper, snap *snapshot, name string, size int64,
) (uint64, bool, error) {
	for range maxTries {
		h, v, err := readHead(ctx, s.store)
		if err != nil {
			return 0, false, err
		}

		if h.Snapshot != w.base.name {
			return 0, false, nil
		}

		next := head{Record: h.Record, Snapshot: name}

		nv, _, err := writeHead(ctx, s.store, &next, v, nil)
		if errors.Is(err, version.ErrMismatch) {
			continue
		}

		if err != nil {
			return 0, false, err
		}

		seq, _ := parseName(h.Record)
		s.installed(ctx, snap, name, size, v, nv, next)

		return seq, true, nil
	}

	return 0, false, fmt.Errorf("%w: %d tries of the install of a snapshot", ErrContention, maxTries)
}

// installed makes the state of s take snap, the snapshot under name of size
// bytes that the head of version nv installed in place of the head of
// version v, when the state reflects the head of version v. It clears the
// failed repairs, so a GET can repair their records again, and logs a
// state above 90% of MaxOrigins.
func (s *Server) installed(
	ctx context.Context, snap *snapshot, name string, size int64, v, nv version.Version, h head,
) {
	s.mu.Lock()

	if s.st.version == v && s.st.take(snap, name, size, nil) == nil {
		s.st.head, s.st.version, s.st.synced = h, nv, s.clock.Time()
	}

	count := s.st.origins.Len()
	s.mu.Unlock()

	s.metrics.origins.Set(ctx, float64(count))

	if count*10 > s.maxOrigins*warnShare {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: the state is above 90% of MaxOrigins",
			slog.Int("origins", count), slog.Int("max_origins", s.maxOrigins))
	}

	s.rmu.Lock()
	for key, r := range s.repairs {
		select {
		case <-r.done:
			delete(s.repairs, key)
		default:
		}
	}
	s.rmu.Unlock()
}
