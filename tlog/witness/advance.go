// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"go.thesmos.sh/kanon/wire"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// Update advances one origin.
type Update struct {
	// Prefix is what the monitor retrieval route serves for the origin
	// before the note, such as the proof that links the body to the note.
	// It is empty for a checkpoint whose note is the note of the call.
	Prefix []byte

	// Proof is the consistency proof from OldSize to Body.Size.
	Proof []crypto.Digest

	// Body is the checkpoint: the origin, the size and the root.
	Body checkpoint.Body

	// OldSize is the size that the caller last saw at the witness.
	OldSize uint64
}

// Failure is an update that did not pass its checks against the state.
type Failure struct {
	// Err is ErrUnknownOrigin for a new origin beyond MaxOrigins, a
	// *SizeError, or an error that wraps ErrInconsistent.
	Err error

	// Index is the index of the update in the call.
	Index int
}

// Advance commits updates, the checkpoints of distinct origins that msg
// covers, in one commit, cosigns msg with every cosigner, and appends the
// cosignature lines to dst, one per cosigner in the order of the
// configuration. When an update fails, Advance commits nothing and signs
// nothing, and returns one Failure for each update that failed, with dst
// unchanged and a nil error.
//
// Advance returns once the signatures exist. The commit then stores the
// lines, and the monitor retrieval route serves the updates only after
// that. When the store refuses the lines, the route serves the earlier
// update of each origin until a repair stores them.
//
// Advance verifies the signatures of msg for the log of each update, as
// add-checkpoint verifies them. The caller verifies that msg covers each
// update: that the text of msg is the update's body, or that the note
// commits to the body by the rules of its format. Advance copies msg and
// the updates, so the caller reuses their memory once Advance returns.
//
// Error modes, each with dst unchanged:
//   - ErrRequest, classified Invalid, for no update, more than 4,096
//     updates, two updates of one origin, a body that is not Valid, an old
//     size above the size of its update, a root or a proof hash of another
//     size than the log's digests, a msg that is not a signed note, a msg of
//     more than 64 signature lines, a msg that a cosigner does not sign,
//     and a call whose encoding in a record exceeds 16 MiB.
//   - ErrUnknownOrigin, classified NotFound, for an origin that Logs
//     refuses.
//   - ErrSignature, classified Denied, for a msg without the signatures of
//     an update's log, and ErrConfig, classified Invalid, for a Log that
//     Logs returns without a hasher or a key set.
//   - resilience.ErrOpen, classified Transient, while the circuit of a
//     cosigner is open. The call committed nothing.
//   - An error that wraps checkpoint.ErrClock, classified Transient, for a
//     reading of UTC outside MaxError, and for a head whose time is ahead
//     of the reading by more than twice MaxError. The call committed
//     nothing.
//   - ErrContention, classified Transient, when other processes commit
//     first 16 times.
//   - The errors of State and of the cosigners, after which the call may
//     have committed without a cosignature, and the error of the Resolver
//     for a key of a log that it does not resolve.
//   - The cause of ctx when ctx ends first, after which the call may still
//     commit.
//
// # Allocation contract
//
// Allocates the failures of a call that has them, and what the commit
// allocates, which the calls of the commit share, when dst has room for
// the lines. A commit of one call with one Ed25519 cosigner over
// blob/memory allocates 15 objects on Go 1.27.1: the 9 of the commit that
// [Server] lists, and 6 of blob/memory. The concurrent signatures of a
// commit cost what [Server] lists. The call copies msg, the prefixes and
// the proofs into pooled memory, and waits on a pooled result. It
// parses msg into the note of a pooled call, under the allocation contract
// of [note.Note.UnmarshalText].
func (s *Server) Advance(ctx context.Context, msg []byte, updates []Update, dst []byte) ([]byte, []Failure, error) {
	if len(updates) == 0 || len(updates) > maxUpdates {
		return dst, nil, fmt.Errorf("%w: %d updates, outside 1 to %d", ErrRequest, len(updates), maxUpdates)
	}

	p := s.pendings.Get()
	owned := true

	defer func() {
		if owned {
			p.reset()
			s.pendings.Put(p)
		}
	}()

	if err := p.fill(msg, updates); err != nil {
		return dst, nil, err
	}

	if err := s.prepare(p); err != nil {
		return dst, nil, err
	}

	// The commit runs under a context of its own, which no caller's
	// context ends and whose values are no caller's.
	s.enqueue(p) //nolint:contextcheck // see above

	owned, err := s.wait(ctx, p)
	if err != nil {
		return dst, nil, err
	}

	if p.err != nil {
		return dst, nil, p.err
	}

	if len(p.failures) > 0 {
		return dst, slices.Clone(p.failures), nil
	}

	return append(dst, p.lines...), nil, nil
}

// fill copies msg and updates into p, and runs the checks of the form of
// a call that need no configuration: the count of the updates, their
// distinct origins, their bodies, and the note.
//
// Returns an error that wraps [ErrRequest] for a call that fails one.
func (p *pending) fill(msg []byte, updates []Update) error {
	for _, u := range updates {
		if !u.Body.Valid() {
			return fmt.Errorf("%w: the body of %q is not valid", ErrRequest, u.Body.Origin)
		}
	}

	// A later append that grows p.buf leaves msg and the earlier prefixes
	// in the memory that they refer to.
	p.buf = append(p.buf, msg...)
	p.msg = p.buf[:len(msg)]

	for _, u := range updates {
		start, from := len(p.buf), len(p.proofs)
		p.buf = append(p.buf, u.Prefix...)
		p.proofs = append(p.proofs, u.Proof...)

		p.updates = append(p.updates, update{
			origin:  u.Body.Origin,
			hash:    hashOrigin(u.Body.Origin),
			proof:   p.proofs[from:len(p.proofs):len(p.proofs)],
			prefix:  p.buf[start:len(p.buf):len(p.buf)],
			root:    u.Body.Root,
			size:    u.Body.Size,
			oldSize: u.OldSize,
		})
	}

	if err := p.distinct(); err != nil {
		return err
	}

	if err := p.note.UnmarshalText(p.msg); err != nil {
		return fmt.Errorf("%w: %w", ErrRequest, err)
	}

	p.text = p.note.Text

	return nil
}

// distinct returns nil when the updates of p have distinct origins. It
// sorts the hashes of the origins in the memory of p.
//
// Returns an error that wraps [ErrRequest] for two updates of one origin.
func (p *pending) distinct() error {
	p.hashes = p.hashes[:0]
	for i := range p.updates {
		p.hashes = append(p.hashes, p.updates[i].hash)
	}

	slices.SortFunc(p.hashes, func(a, b originHash) int { return bytes.Compare(a[:], b[:]) })

	for i := 1; i < len(p.hashes); i++ {
		if p.hashes[i] == p.hashes[i-1] {
			return fmt.Errorf("%w: two updates of one origin", ErrRequest)
		}
	}

	return nil
}

// prepare runs checks 2 to 4 of the protocol on p, a call whose note and
// updates its caller parsed, in the order of the protocol, each over every
// update before the next:
//
//   - Logs accepts the origin of every update.
//   - Every line of a key of the log of an update verifies, and every key
//     of one key set of the log signed. A note of more than maxLines lines
//     fails before any line verifies, and each line verifies at most once.
//   - The old size is at most the size, the root and each proof hash have
//     the size of the log's digests, and every cosigner signs the text.
//
// It then computes the size of the encoding of the call in a record.
//
// Returns an error that wraps [ErrUnknownOrigin], [ErrSignature],
// [ErrRequest] or [ErrConfig] for a call that fails, and the error of the
// Resolver for a key of a log that it does not resolve.
func (s *Server) prepare(p *pending) error {
	if len(p.note.Signatures) > maxLines {
		return fmt.Errorf("%w: a note of %d signature lines, above %d", ErrRequest, len(p.note.Signatures), maxLines)
	}

	for i := range p.updates {
		u := &p.updates[i]

		log, ok := s.logs(u.origin)
		if !ok {
			return fmt.Errorf("%w: the witness does not accept %s", ErrUnknownOrigin, u.origin)
		}

		if err := log.check(); err != nil {
			return err
		}

		u.log = log
	}

	p.states = slices.Grow(p.states[:0], len(p.note.Signatures))[:len(p.note.Signatures)]
	clear(p.states)

	for i := range p.updates {
		if err := s.keys.verifyLog(&p.note, &p.updates[i].log, p.states); err != nil {
			return err
		}
	}

	for i := range p.updates {
		u := &p.updates[i]
		if u.oldSize > u.size {
			return fmt.Errorf("%w: the old size %d of %s is above its size %d", ErrRequest, u.oldSize, u.origin,
				u.size)
		}

		size := tlog.Root(u.log.Hasher, nil).Size()
		if u.root.Size() != size ||
			slices.ContainsFunc(u.proof, func(h crypto.Digest) bool { return h.Size() != size }) {

			return fmt.Errorf("%w: a root or a proof hash of %s of another size than the %d bytes of its log",
				ErrRequest, u.origin, size)
		}
	}

	for j, c := range s.cosigners {
		if err := c.CheckText(p.text); err != nil {
			return fmt.Errorf("%w: the cosigner %s does not sign the note: %w", ErrRequest, s.lineKeys[j].Name, err)
		}
	}

	// The reset of p before its return to the pool emptied p.entries.
	for i := range p.updates {
		u := &p.updates[i]
		p.entries = append(p.entries, entry{Origin: u.origin, Prefix: u.prefix, Root: u.root, Size: u.size})
	}

	c := call{Note: p.msg, Updates: p.entries}

	p.size = wire.SizeBytes(c.SizeKanon())
	if p.size > maxCommitBytes {
		return fmt.Errorf("%w: a call of %d bytes in a record, above %d", ErrRequest, p.size, maxCommitBytes)
	}

	return nil
}
