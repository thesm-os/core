// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"slices"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The bounds of a commit.
const (
	// maxCommitBytes bounds the encodings of the calls of one commit, and
	// of one call. A commit stops taking calls at this many bytes.
	maxCommitBytes = 16777216

	// maxUpdates bounds the updates of one Advance and of one commit. A
	// commit stops taking calls at this many updates, so the lines of its
	// record cover at most this many calls.
	maxUpdates = 4096
)

// update is one update of a pending call, in the memory of the call.
type update struct {
	// origin is the origin.
	origin checkpoint.Origin

	// log is the log of the origin, which [ServerConfig.Logs] returned.
	log Log

	// proof is the consistency proof from oldSize to size, and prefix what
	// the route serves before the note. Both are in the memory of the
	// call.
	proof  []crypto.Digest
	prefix []byte

	// last is the checkpoint that the latest commit checked the update
	// against.
	last committed

	// size and root are the size and the root of the checkpoint, and
	// oldSize the size that the caller saw at the witness last.
	size    uint64
	oldSize uint64
	root    crypto.Digest

	// hash is the hash of the origin.
	hash originHash

	// retired reports whether the state of the latest commit contained the
	// prefix of hash among the retired origins.
	retired bool
}

// pending is a call that waits for a commit: the note of an Advance or of
// an add-checkpoint request, its updates, and its result.
//
// A caller takes a pending from the pool of its Server, fills it, enqueues
// it, and waits. The commit that takes it is its only user until it
// delivers the result. A caller whose context ends first removes a
// pending that no commit took from the queue, and abandons one that a
// commit took. The commit then returns the pending to the pool.
type pending struct {
	// err is the error of the call.
	err error

	// done receives one value when the call has its result. It has a
	// buffer of one, so the commit never waits for the caller.
	done chan struct{}

	// note is the parse of msg.
	note note.Note

	// failures are the updates that failed their checks.
	failures []Failure

	// msg is the note that the call cosigns, with the log's signatures,
	// and text its text, a subslice of msg.
	msg, text []byte

	// buf contains msg and the prefixes of the updates for an Advance,
	// and the body of the request for add-checkpoint.
	buf []byte

	// updates are the updates of the call, and entries their entries in
	// the record of the call.
	updates []update
	entries []entry

	// proofs contains the proofs of the updates.
	proofs []crypto.Digest

	// hashes contains the hashes of the origins of the updates, sorted, to
	// find two updates of one origin.
	hashes []originHash

	// lines are the cosignature lines of the call, one per cosigner in the
	// order of the cosigners.
	lines []byte

	// states are the results of the verification of each signature line
	// of note, at the index of the line.
	states []lineState

	// req is the parse of the body of an add-checkpoint request, and body
	// the parse of the text of its note.
	req  request
	body checkpoint.Body

	// size is the length of the encoding of the call in a record.
	size int

	// taken, abandoned and delivered are the progress of the call in the
	// queue, which the mutex of the queue guards: a commit took it, its
	// caller stopped waiting after that, and the commit delivered its
	// result.
	taken, abandoned, delivered bool
}

// reset prepares p for a new call, and keeps the capacity of its memory.
func (p *pending) reset() {
	clear(p.updates)
	clear(p.entries)

	p.err, p.failures = nil, p.failures[:0]
	p.msg, p.text, p.buf = nil, nil, p.buf[:0]
	p.updates, p.entries, p.proofs, p.lines = p.updates[:0], p.entries[:0], p.proofs[:0], p.lines[:0]
	p.size, p.taken, p.abandoned, p.delivered = 0, false, false, false
}

// enqueue adds p to the queue of s, and starts a commit on a goroutine of
// its own when no commit runs.
func (s *Server) enqueue(p *pending) {
	s.qmu.Lock()
	s.queue = append(s.queue, p)
	start := !s.committing
	s.committing = true
	s.qmu.Unlock()

	if start {
		go s.committer()
	}
}

// wait waits for the result of p, or for the end of ctx, and reports
// whether p is still the caller's, which then returns it to the pool. A
// pending that a commit took and the caller abandons passes to that
// commit.
//
// Returns the cause of ctx when ctx ends before the result, after which a
// call that a commit took may still commit.
func (s *Server) wait(ctx context.Context, p *pending) (bool, error) {
	select {
	case <-p.done:
		return true, nil
	case <-ctx.Done():
	}

	s.qmu.Lock()

	if p.delivered {
		s.qmu.Unlock()
		<-p.done

		return true, nil
	}

	if p.taken {
		p.abandoned = true
		s.qmu.Unlock()

		return false, context.Cause(ctx)
	}

	if i := slices.Index(s.queue, p); i >= 0 {
		s.queue = slices.Delete(s.queue, i, i+1)
	}

	s.qmu.Unlock()

	return true, context.Cause(ctx)
}

// take moves the calls at the front of the queue into calls, up to the
// bounds of a commit, and marks them taken. It takes the first call
// whatever its size, which the checks before the queue bound. The caller
// has locked the mutex of the queue.
func (s *Server) take(calls []*pending) []*pending {
	size, updates, n := 0, 0, 0

	for _, p := range s.queue {
		if n > 0 && (size+p.size > maxCommitBytes || updates+len(p.updates) > maxUpdates) {
			break
		}

		p.taken = true
		size, updates, n = size+p.size, updates+len(p.updates), n+1
		calls = append(calls, p)
	}

	m := copy(s.queue, s.queue[n:])
	clear(s.queue[m:])
	s.queue = s.queue[:m]

	return calls
}

// deliver hands the result of each call of calls to its caller, and
// returns each call that its caller abandoned to the pool. The result is
// in the fields of the call.
func (s *Server) deliver(calls []*pending) {
	s.qmu.Lock()

	for _, p := range calls {
		if p.abandoned {
			continue
		}

		p.delivered = true
	}

	s.qmu.Unlock()

	for _, p := range calls {
		if p.delivered {
			p.done <- struct{}{}

			continue
		}

		p.reset()
		s.pendings.Put(p)
	}
}
