// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/version"
)

// maxTries bounds the starts of the checks of one commit, and the tries of
// the install of one snapshot.
const maxTries = 16

// errOvertaken reports a replacement of the head that another head came
// before: a failed precondition, or a head that a write of an unknown
// outcome left at another version.
var errOvertaken = errors.New("witness: another commit replaced the head first")

// batch is the memory of the running commit: its calls, its record and its
// lines. One commit runs at a time in a Server, and each commit starts the
// next one when it no longer uses the batch, so the batch has one user at
// a time.
type batch struct {
	// overlay maps the hash of each origin that a passed call of the
	// commit advances to its checkpoint in the commit.
	overlay map[originHash]committed

	// sign is the memory of the signatures.
	sign signing

	// recKey and linesKey are the keys of the record and of its lines once
	// every passed call has its lines, and linesKey is empty otherwise.
	recKey, linesKey string

	// calls are the calls that the commit took, and passed those that
	// passed the checks of the last try.
	calls, passed []*pending

	// texts are the texts of the notes of the passed calls.
	texts [][]byte

	// buf contains the encoding of the record and then of its lines, hbuf
	// the encoding of the head, and kbuf the keys of the record and its
	// lines.
	buf, hbuf, kbuf []byte

	// moves are the served positions that the lines of the record move.
	moves []servedUpdate

	// lines are the lines of the record.
	lines lines

	// rec is the record of the passed calls, whose calls refer to the
	// memory of the calls.
	rec record

	// seq is the Seq of the record once every passed call has its lines.
	seq uint64

	// advanced reports whether the record of the commit committed.
	advanced bool
}

// servedUpdate is an update of a record whose position becomes the served
// position of its origin once the lines of the record are stored.
type servedUpdate struct {
	// hash is the hash of the origin.
	hash originHash

	// call is the index of the call in the record, and update the index of
	// the update in the call.
	call, update int
}

// linesWrite is the write of the lines of one record, which a commit runs
// after its calls have their results. A commit locks the lmu of the Server
// before it takes the write. It unlocks the lmu once the write and its
// moves end, so one write runs at a time.
type linesWrite struct {
	// recKey and linesKey are the keys of the record and of its lines, and
	// linesKey is empty for a commit without lines.
	recKey, linesKey string

	// data is the encoding of the lines.
	data []byte

	// moves are the served positions that the write moves.
	moves []servedUpdate

	// seq is the Seq of the record.
	seq uint64
}

// commit runs one commit of the calls in the queue of s, on the goroutine
// that enqueue or the commit before started, as commitCalls describes. It
// then writes a snapshot when one is due, while the next commit runs.
func (s *Server) commit() {
	b := &s.batch

	s.qmu.Lock()
	b.calls = s.take(b.calls[:0])
	s.qmu.Unlock()

	if len(b.calls) > 0 {
		s.commitCalls(b)
	} else {
		s.startNext()
	}

	s.snapshotIfDue()
}

// commitCalls runs the commit of the calls of b. It delivers their results
// once the signatures exist and the write of the lines of the commit
// before has ended. It then starts the next commit when calls wait, and
// stores the lines of the record of b. A commit that it starts runs during
// the write. When a call returns, the lines of every earlier commit of s
// are stored or their write failed.
//
// The commit and the write of its lines run under one context of their
// own, with the deadline Timeout + SignTimeout, which no caller's context
// ends. The wait for the write of the commit before ends by the deadline
// of that commit, which comes first.
func (s *Server) commitCalls(b *batch) {
	start := s.clock.Time()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout+s.signTimeout)
	defer cancel()

	err := s.run(ctx, b)

	s.metrics.recordCommit(ctx, s.clock.Time().Sub(start), len(b.calls), err)
	s.account(ctx, b, err)

	s.lmu.Lock()
	s.deliver(b.calls)
	s.lines.take(b)
	b.release()
	s.startNext()
	s.storeLines(ctx)
}

// startNext starts the next commit on a goroutine of its own when calls
// wait in the queue of s, and records that no commit runs otherwise.
func (s *Server) startNext() {
	s.qmu.Lock()
	next := len(s.queue) > 0
	s.committing = next
	s.qmu.Unlock()

	if next {
		go s.committer()
	}
}

// take passes the lines of the record of b to w, and the buffers of w to b,
// so that neither copies: the encoding of the lines, the served positions
// that they move, and the keys and the Seq of the record. The caller has
// locked the lmu of the Server.
func (w *linesWrite) take(b *batch) {
	w.data, b.buf = b.buf, w.data
	w.moves, b.moves = b.moves, w.moves[:0]
	w.recKey, w.linesKey, w.seq = b.recKey, b.linesKey, b.seq
}

// storeLines creates the lines of the write in s.lines under ctx, the
// context of its commit, and moves the served positions of their origins.
// It then unlocks the lmu that the commit locked, so that the next commit
// can write its lines. It skips the write for a commit without lines.
//
// It logs a write that fails, and leaves the record without lines for a
// repair. On [version.ErrExists], another process or a repair created the
// lines first, and the route serves those.
func (s *Server) storeLines(ctx context.Context) {
	defer s.lmu.Unlock()

	w := &s.lines
	if w.linesKey == "" {
		return
	}

	err := create(ctx, s.store, w.linesKey, w.data)
	if err != nil && !errors.Is(err, version.ErrExists) {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: a commit did not store its lines",
			slog.String("record", w.recKey[len(recordPrefix):]), slog.Any("error", err))

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, m := range w.moves {
		o, ok := s.st.origins.Get(m.hash)
		if ok && (o.served.key == "" || o.served.seq < w.seq) {
			o.served = position{key: w.recKey, seq: w.seq, call: m.call, update: m.update}
			s.st.origins.Set(m.hash, o)
		}
	}
}

// run takes the steps of a commit of the calls of b, and sets the result
// of each call:
//
//  1. It checks each call against the state and the calls before it.
//  2. It checks the circuits of the cosigners, and reads the time of the
//     commit.
//  3. It creates the record of the passed calls.
//  4. It replaces the head. When another commit came first, it catches up
//     to the new head, and starts again at step 1, unless the chain of the
//     new head contains its record.
//  5. It applies the record to the state.
//  6. It signs the note of each passed call with every cosigner, and sets
//     the lines of each call. It leaves the write of the lines of the
//     record and the move of the served positions to commitCalls.
//
// Returns the error that the passed calls failed with, and nil when every
// call has its lines or its failures.
func (s *Server) run(ctx context.Context, b *batch) error {
	b.advanced, b.linesKey, b.moves = false, "", b.moves[:0]

	for range maxTries {
		bs, err := s.check(ctx, b)
		if err != nil {
			return failAll(b.calls, err)
		}

		if len(b.passed) == 0 {
			// A commit without a passed call writes nothing, so no failed
			// replacement of the head reveals a state that another process
			// overtook. The failures state the checkpoints that the witness
			// committed last only against the head of the store.
			_, v, herr := readHead(ctx, s.store)
			if herr != nil {
				return failAll(b.calls, herr)
			}

			if v == bs.version {
				return nil
			}

			if _, serr := s.sync(ctx, ""); serr != nil {
				return failAll(b.calls, serr)
			}

			continue
		}

		t, err := s.admit(bs)
		if err != nil {
			return failAll(b.passed, err)
		}

		recKey, linesKey, size, err := s.writeRecord(ctx, b, bs, t)
		if err != nil {
			return failAll(b.passed, err)
		}

		v, err := s.replaceHead(ctx, b, bs, recKey[len(recordPrefix):])
		if errors.Is(err, errOvertaken) {
			own, serr := s.sync(ctx, recKey[len(recordPrefix):])
			if serr != nil {
				return failAll(b.passed, serr)
			}

			if !own {
				continue
			}
		} else if err != nil {
			return failAll(b.passed, err)
		} else {
			s.applyCommit(ctx, b, bs, recKey, size, v)
		}

		b.advanced = true

		// The signatures run under a context of their own, which a slow store
		// cannot take the time of.
		return s.finish(b, t, recKey, linesKey, bs.seq+1) //nolint:contextcheck // see above
	}

	return failAll(b.calls, fmt.Errorf("%w: %d starts of the checks", ErrContention, maxTries))
}

// check runs checks 5 to 7 of the protocol on each call of b, in order,
// against the state of s and the updates of the calls before it that
// passed. It records the failures of each call, adds each call whose
// updates all pass to b.passed, and returns the base of the state that it
// checked against.
//
// It reads the state under the read lock. An origin that the state does
// not contain and whose prefix is among the retired origins costs a List,
// after the lock: the retired objects of an origin never change, so the
// List returns what the state of the base retired.
//
// Returns the errors of lookupRetired, after which the calls are unchecked.
func (s *Server) check(ctx context.Context, b *batch) (base, error) {
	if b.overlay == nil {
		b.overlay = make(map[originHash]committed)
	}

	clear(b.overlay)
	b.passed = b.passed[:0]

	s.mu.RLock()
	bs := base{
		record: s.st.head.Record, snapshot: s.st.snap.name, version: s.st.version,
		seq: s.st.seq, time: s.st.time, end: s.st.end,
	}
	count := s.st.origins.Len()

	for _, p := range b.calls {
		for i := range p.updates {
			u := &p.updates[i]

			o, ok := s.st.origins.Get(u.hash)
			u.last = committed{root: o.root, size: o.size, fresh: !ok}
			u.retired = !ok && s.st.retired.Has(binary.BigEndian.Uint64(u.hash[:]))
		}
	}

	s.mu.RUnlock()

	for _, p := range b.calls {
		for i := range p.updates {
			if u := &p.updates[i]; u.retired {
				c, err := s.lookupRetired(ctx, u.hash, u.origin)
				if err != nil {
					return base{}, err
				}

				u.last = c
			}
		}
	}

	fresh := 0

	for _, p := range b.calls {
		added := s.checkCall(b, p, count+fresh)
		if len(p.failures) > 0 {
			continue
		}

		fresh += added

		for i := range p.updates {
			u := &p.updates[i]
			b.overlay[u.hash] = committed{root: u.root, size: u.size}
		}

		b.passed = append(b.passed, p)
	}

	return bs, nil
}

// checkCall runs checks 5 to 7 on each update of p, against the overlay
// of b and the checkpoint that check read for it, in a state of count
// origins. It records a Failure for each update that fails, and returns
// the number of new origins of p.
func (s *Server) checkCall(b *batch, p *pending, count int) int {
	p.failures = p.failures[:0]
	added := 0

	for i := range p.updates {
		u := &p.updates[i]
		if c, ok := b.overlay[u.hash]; ok {
			u.last = c
		}

		if u.last.fresh && count+added >= s.maxOrigins {
			err := fmt.Errorf("%w: a new origin beyond %d origins", ErrUnknownOrigin, s.maxOrigins)
			p.failures = append(p.failures, Failure{Index: i, Err: err})

			continue
		}

		if u.last.fresh {
			added++
		}

		if u.oldSize != u.last.size {
			p.failures = append(p.failures, Failure{Index: i, Err: &SizeError{Size: u.last.size}})

			continue
		}

		if err := consistent(u.log.Hasher, u.oldSize, u.last.root, u.size, u.root, u.proof); err != nil {
			p.failures = append(p.failures, Failure{Index: i, Err: err})
		}
	}

	return added
}

// admit checks the circuits of the cosigners, reads the UTC source, and
// returns the time of a commit after the head of bs: the reading in whole
// seconds since the Unix epoch, or the time of the head's record when that
// is later.
//
// Returns an error that wraps [resilience.ErrOpen] when the circuit of a
// cosigner is open, and an error that wraps [checkpoint.ErrClock] for a
// reading that fails, that is not synchronised within MaxError, that is
// not after the Unix epoch, or that the time of the head is ahead of by
// more than twice MaxError.
func (s *Server) admit(bs base) (time.Time, error) {
	for j, target := range s.targets {
		if s.breaker.State(target) == resilience.Open {
			return time.Time{}, fmt.Errorf("witness: the circuit of %s: %w", s.lineKeys[j].Name, resilience.ErrOpen)
		}
	}

	r, err := s.utc.ReadUTC()
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: read the UTC source: %w", checkpoint.ErrClock, err)
	}

	if !r.Within(s.maxError) {
		return time.Time{}, fmt.Errorf("%w: a reading with Synced %t and MaxError %v, for the bound %v",
			checkpoint.ErrClock, r.Synced, r.MaxError, s.maxError)
	}

	sec := r.Time.Unix()
	if sec <= 0 {
		return time.Time{}, fmt.Errorf("%w: a reading at %v, not after the Unix epoch", checkpoint.ErrClock, r.Time)
	}

	headTime := time.Unix(int64(bs.time), 0) //nolint:gosec // the time of a record is the seconds of a reading
	if headTime.Sub(r.Time) > 2*s.maxError {
		return time.Time{}, fmt.Errorf("%w: the head's time %v is ahead of the reading %v by more than twice %v",
			checkpoint.ErrClock, headTime, r.Time, s.maxError)
	}

	return time.Unix(max(sec, int64(bs.time)), 0), nil //nolint:gosec // as above
}

// writeRecord creates the record of the passed calls of b after the head
// of bs, with the time t, and returns its key, the key of its lines and
// the length of its encoding. Both keys share one string, so the commit
// allocates one for them. A record of the same name that exists is the
// same record, so its creation succeeds.
//
// Returns the errors of the store.
func (s *Server) writeRecord(
	ctx context.Context, b *batch, bs base, t time.Time,
) (recKey, linesKey string, size uint64, err error) {
	seq := bs.seq + 1
	b.rec.Prev, b.rec.Seq, b.rec.Offset = bs.record, seq, bs.end
	b.rec.Time = uint64(t.Unix()) //nolint:gosec // admit returns a time after the Unix epoch

	b.rec.Calls = b.rec.Calls[:0]
	for _, p := range b.passed {
		b.rec.Calls = append(b.rec.Calls, call{Note: p.msg, Updates: p.entries})
	}

	// The encoding of a record fails only for an origin that is not Valid,
	// and the checks before the queue refuse such an origin.
	buf, _ := b.rec.AppendBinary(b.buf[:0])
	b.buf = buf

	b.kbuf = append(b.kbuf[:0], recordPrefix...)
	b.kbuf = appendName(b.kbuf, seq, buf)
	b.kbuf = append(b.kbuf, linesPrefix...)
	b.kbuf = append(b.kbuf, b.kbuf[len(recordPrefix):len(recordPrefix)+nameText]...)

	keys := string(b.kbuf)
	recKey, linesKey = keys[:len(recordPrefix)+nameText], keys[len(recordPrefix)+nameText:]

	if err := create(ctx, s.store, recKey, buf); err != nil && !errors.Is(err, version.ErrExists) {
		return "", "", 0, err
	}

	return recKey, linesKey, uint64(len(buf)), nil
}

// replaceHead replaces the head of bs with the head of the record named
// name, and returns the version of the new head.
//
// On [version.ErrOutcomeUnknown] it reads the head. A head that is still
// the head of bs means that the write did not apply, and it writes the
// head again. Any other head returns errOvertaken, and so does a failed
// precondition: the caller then catches up, which finds the record when
// the write applied.
//
// Returns errOvertaken, and the other errors of the store.
func (s *Server) replaceHead(ctx context.Context, b *batch, bs base, name string) (version.Version, error) {
	h := head{Record: name, Snapshot: bs.snapshot}

	for {
		v, buf, err := writeHead(ctx, s.store, &h, bs.version, b.hbuf)
		b.hbuf = buf

		if err == nil {
			return v, nil
		}

		if errors.Is(err, version.ErrMismatch) || errors.Is(err, version.ErrExists) {
			return version.Unspecified, errOvertaken
		}

		if !errors.Is(err, version.ErrOutcomeUnknown) {
			return version.Unspecified, err
		}

		_, cur, err := readHead(ctx, s.store)
		if err != nil {
			return version.Unspecified, err
		}

		if cur != bs.version {
			return version.Unspecified, errOvertaken
		}
	}
}

// applyCommit applies the record of b, which the head of version v
// committed after the head of bs, to the state of s, when the state still
// reflects the head of bs. A refresh that read the new head first applied
// the record already.
func (s *Server) applyCommit(ctx context.Context, b *batch, bs base, recKey string, size uint64, v version.Version) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.st.version != bs.version {
		return
	}

	s.st.apply(&b.rec, recKey, size)
	s.st.version, s.st.synced = v, s.clock.Time()
	s.metrics.origins.Set(ctx, float64(s.st.origins.Len()))
}

// finish takes step 6 of a commit whose record committed under recKey,
// with the Seq seq and the time t. It signs the note of each passed call
// of b with every cosigner and sets the lines of each call. For
// commitCalls, it keeps the encoding of the lines of the record in b.buf,
// the keys, the Seq, and the served positions that the lines move. The
// positions list the later calls first, so the latest update of an origin
// in the record becomes its served position.
//
// The signatures run under a context of their own, with the deadline
// SignTimeout, which the work on the store does not share.
//
// Returns the error of cosign, after which the passed calls advanced their
// origins without lines.
func (s *Server) finish(b *batch, t time.Time, recKey, linesKey string, seq uint64) error {
	b.texts = b.texts[:0]
	for _, p := range b.passed {
		b.texts = append(b.texts, p.text)
	}

	// A context of its own, which a slow store cannot take the time of.
	sctx, cancel := context.WithTimeout(context.Background(), s.signTimeout)
	err := s.cosign(sctx, b.texts, t, &b.sign)

	cancel()

	if err != nil {
		return failAll(b.passed, err)
	}

	b.lines.Calls = b.lines.Calls[:0]

	for i, p := range b.passed {
		p.lines = s.appendLines(p.lines[:0], &b.sign, i)
		b.lines.Calls = append(b.lines.Calls, p.lines)
	}

	for i, p := range slices.Backward(b.passed) {
		for j := range p.updates {
			b.moves = append(b.moves, servedUpdate{hash: p.updates[j].hash, call: i, update: j})
		}
	}

	// The encoding of lines has no error: it has only bytes.
	b.buf, _ = b.lines.AppendBinary(b.buf[:0])
	b.recKey, b.linesKey, b.seq = recKey, linesKey, seq

	return nil
}

// account records the outcome of the calls of b in the metrics and the
// log of s: each failed update, the updates that the commit advanced, and
// err, the error of the commit.
func (s *Server) account(ctx context.Context, b *batch, err error) {
	for _, p := range b.calls {
		for _, f := range p.failures {
			u := &p.updates[f.Index]

			if _, ok := errors.AsType[*SizeError](f.Err); ok {
				s.metrics.conflict.Add(ctx, 1)

				continue
			}

			if errors.Is(f.Err, ErrInconsistent) {
				s.metrics.inconsistent.Add(ctx, 1)
				s.logger.LogAttrs(ctx, slog.LevelError, "witness: an inconsistent checkpoint",
					slog.String("origin", string(u.origin)),
					slog.Uint64("committed_size", u.last.size), slog.String("committed_root", u.last.root.String()),
					slog.Uint64("size", u.size), slog.String("root", u.root.String()),
					slog.String("note", string(p.msg)), slog.Any("error", f.Err))

				continue
			}

			s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: a new origin beyond MaxOrigins",
				slog.String("origin", string(u.origin)), slog.Int("max_origins", s.maxOrigins))
		}
	}

	if b.advanced {
		counter := s.metrics.committed
		if err != nil {
			counter = s.metrics.uncosigned
		}

		for _, p := range b.passed {
			counter.Add(ctx, int64(len(p.updates)))
		}
	}

	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: a commit failed",
			slog.Int("calls", len(b.calls)), slog.Bool("advanced", b.advanced), slog.Any("error", err))
	}
}

// release drops the references of b to the memory of its calls, which
// their callers reuse, and keeps the capacity of its slices.
func (b *batch) release() {
	clear(b.calls)
	clear(b.passed)
	clear(b.texts)
	clear(b.lines.Calls)
	clear(b.rec.Calls)

	b.calls, b.passed, b.texts = b.calls[:0], b.passed[:0], b.texts[:0]
	b.lines.Calls, b.rec.Calls = b.lines.Calls[:0], b.rec.Calls[:0]
}

// failAll sets the error of each call of calls to err, and returns err.
func failAll(calls []*pending, err error) error {
	for _, p := range calls {
		p.err = err
		p.failures = p.failures[:0]
	}

	return err
}

// snapshotIfDue writes a snapshot when the records after the installed
// snapshot amount to its size, and to at least snapshotBytes, and no other
// snapshot of s runs.
func (s *Server) snapshotIfDue() {
	s.mu.RLock()
	due := s.st.end-s.st.snap.end >= max(uint64(s.st.snap.size), snapshotBytes) //nolint:gosec // a length
	s.mu.RUnlock()

	if !due || !s.snapshotting.CompareAndSwap(false, true) {
		return
	}

	defer s.snapshotting.Store(false)

	s.writeSnapshot()
}
