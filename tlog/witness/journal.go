// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

//go:generate go tool kanon -type=head,record,call,entry,group,lines,snapshot,object,snapOrigin -canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The keys of the journal in the State of a Server. Every object except
// the head is immutable, and its key ends in a name of nameText bytes.
const (
	// headKey is the key of the head, the one object that a commit
	// replaces.
	headKey = "head"

	// recordPrefix starts the key of a record.
	recordPrefix = "records/"

	// linesPrefix starts the key of the lines of a record, before the
	// name of the record.
	linesPrefix = "lines/"

	// snapshotPrefix starts the key of a snapshot.
	snapshotPrefix = "snapshots/"

	// groupPrefix starts the key of a group.
	groupPrefix = "groups/"

	// retiredPrefix starts the key of a retired origin.
	retiredPrefix = "retired/"

	// seqText is the length of a sequence number in a name: the decimal
	// of the largest uint64, padded with zeros.
	seqText = 20

	// nameText is the length of a name: the sequence number of seqText
	// digits, a hyphen and the 64 digits of the lowercase hexadecimal of a
	// SHA-256.
	nameText = 85
)

// head names the latest committed record and the installed snapshot.
type head struct {
	// Record is the name of the latest committed record.
	Record string

	// Snapshot is the name of the installed snapshot, empty before the
	// first.
	Snapshot string
}

// record is one commit of the journal.
type record struct {
	// Prev is the name of the predecessor, empty for the first record.
	Prev string

	// Calls are the calls of the commit that passed its checks.
	Calls []call

	// Seq is the Seq of the predecessor plus 1, and 1 for the first
	// record.
	Seq uint64

	// Time is the timestamp of every cosignature of the calls, in seconds
	// since the Unix epoch.
	Time uint64

	// Offset is the sum of the encoded sizes of the records before this
	// one on the chain.
	Offset uint64
}

// call is the note, the updates and, in a group, the cosignature lines of
// one Advance or add-checkpoint.
type call struct {
	// Note is the note that the witness cosigns, with the log's
	// signatures.
	Note []byte

	// Lines are the cosignature lines in a group, and empty in a record,
	// whose lines are an object of their own.
	Lines []byte

	// Updates are the new checkpoints of the call, in the order of the
	// call.
	Updates []entry
}

// entry is the new checkpoint of one origin.
type entry struct {
	// Origin is the origin of the checkpoint.
	Origin checkpoint.Origin

	// Prefix is what the monitor retrieval route serves for the origin
	// before the note.
	Prefix []byte

	// Root is the root of the tree of the checkpoint.
	Root crypto.Digest

	// Size is the size of the tree of the checkpoint.
	Size uint64
}

// group is the calls of the updates that one snapshot moved out of their
// records and out of other groups. Each call contains its lines and the
// moved updates only.
type group struct {
	// Calls are the calls of the moved updates.
	Calls []call
}

// lines is the cosignature lines of the calls of one record, which a
// commit or a repair writes after it signs.
type lines struct {
	// Calls are the lines of each call of the record, in the order of
	// its calls.
	Calls [][]byte
}

// snapshot is the state as of one committed record.
type snapshot struct {
	// Record is the name of the record.
	Record string

	// Base is the name of the snapshot that the writer started from,
	// empty for the first.
	Base string

	// Retired are the first 8 bytes of the SHA-256 of each origin that
	// the snapshot retired, read as a big-endian uint64.
	Retired []uint64

	// Objects are the records and groups that the positions refer to.
	Objects []object

	// Origins are the origins of the state, in the order of their
	// hashes.
	Origins []snapOrigin

	// End is the Offset of the record plus its encoded size.
	End uint64
}

// object is a record or a group that the positions of a snapshot refer
// to.
type object struct {
	// Key is the key of the record or the group.
	Key string

	// Updates is the number of updates of a group, and 0 for a record.
	Updates uint32
}

// snapOrigin is the latest committed checkpoint of one origin in a
// snapshot, and the position of its update.
type snapOrigin struct {
	// Origin is the origin.
	Origin checkpoint.Origin

	// Size is the size of the tree of the checkpoint.
	Size uint64

	// Time is the Time of the record of the update.
	Time uint64

	// Object is the index of the record or the group in Objects.
	Object uint32

	// Call is the index of the call in the object.
	Call uint32

	// Update is the index of the update in the call.
	Update uint32

	// Root is the root of the tree of the checkpoint.
	Root crypto.Digest
}

// appendName appends to dst the name of an object whose encoding is
// encoded: seq in seqText decimal digits, a hyphen, and the lowercase
// hexadecimal SHA-256 of encoded.
func appendName(dst []byte, seq uint64, encoded []byte) []byte {
	var digits [seqText]byte

	d := strconv.AppendUint(digits[:0], seq, 10)
	for range seqText - len(d) {
		dst = append(dst, '0')
	}

	dst = append(dst, d...)
	dst = append(dst, '-')
	sum := sha256.Sum256(encoded)

	return hex.AppendEncode(dst, sum[:])
}

// parseName returns the sequence number of the name of an object, and
// reports whether name is such a name: seqText decimal digits, a hyphen,
// and 64 lowercase hexadecimal digits. ParseUint refuses a sequence number
// of other characters than decimal digits.
func parseName(name string) (uint64, bool) {
	if len(name) != nameText || name[seqText] != '-' {
		return 0, false
	}

	for i := seqText + 1; i < len(name); i++ {
		if c := name[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, false
		}
	}

	seq, err := strconv.ParseUint(name[:seqText], 10, 64)

	return seq, err == nil
}

// checkName returns nil when name is the name of an object of the
// sequence number of name whose encoding is encoded.
//
// Returns an error that wraps [ErrJournal] for an encoding whose
// SHA-256 is not the hash of the name.
func checkName(name string, encoded []byte) error {
	sum := sha256.Sum256(encoded)

	var want [2 * sha256.Size]byte

	hex.Encode(want[:], sum[:])

	if len(name) != nameText || name[seqText+1:] != string(want[:]) {
		return fmt.Errorf("%w: the object %s does not hash to its name", ErrJournal, name)
	}

	return nil
}
