// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/version"
)

// position locates an update of the journal: a record or a group, the
// index of a call in it, and the index of the update in the call. The
// zero position locates no update.
type position struct {
	// key is the key of the record or the group, and empty for the zero
	// position.
	key string

	// seq is the Seq of the record, and for a group the Seq of the record
	// of the snapshot whose writer created the group. An update at a
	// position of a group is at or before that record.
	seq uint64

	// call is the index of the call in the object, and update the index
	// of the update in the call.
	call, update int

	// group reports whether key is the key of a group, whose calls contain
	// their lines.
	group bool
}

// originState is the state of one origin in the memory of a Server: its
// latest committed checkpoint, and the two positions of the monitor
// retrieval route.
type originState struct {
	// origin is the origin. It shares no memory with an object that the
	// state read.
	origin checkpoint.Origin

	// latest locates the update of the latest committed checkpoint, which
	// a snapshot records.
	latest position

	// served locates the latest update whose lines the process stored or
	// found, which the monitor retrieval route serves. It is the zero
	// position before the process knows such an update.
	served position

	// size and root are the size and the root of the latest committed
	// checkpoint, and time the Time of its record.
	size uint64
	time uint64
	root crypto.Digest

	// snapshot reports whether the process read the origin from a
	// snapshot. The route responds with 503 for such an origin without a
	// served position, and with 404 for an origin that the process added
	// from a commit or a record.
	snapshot bool
}

// installed is the snapshot that a state took last.
type installed struct {
	// name is the name of the snapshot, and empty before the first.
	name string

	// objects are the records and groups that the snapshot refers to,
	// with keys that share no memory with the snapshot.
	objects []object

	// seq is the Seq of the record of the snapshot, end its End, and size
	// the length of its encoding.
	seq, end uint64
	size     int64
}

// state is the state of a Server in memory: each origin, the retired
// origins, the names of the records on the chain, and the head and the
// snapshot that it reflects.
//
// # Concurrency
//
// Not safe for concurrent use. The mutex of the Server guards it.
type state struct {
	// synced is the time of the clock of the Server at which the state
	// last matched the head of the store.
	synced time.Time

	// origins maps the hash of each origin to its state.
	origins *btree.MapFunc[originHash, originState]

	// retired contains the first 8 bytes of the hash of each origin that a
	// snapshot retired, read as a big-endian uint64.
	retired *btree.Set[uint64]

	// head is the head that the state reflects, and version the version
	// of that head in the store, which is empty for an empty journal.
	head    head
	version version.Version

	// chain maps the Seq of each record on the chain to its name, from
	// the record of the base of the installed snapshot, so that garbage
	// collection finds the records that are off the chain.
	chain btree.Map[uint64, string]

	// snap is the installed snapshot that the state took last.
	snap installed

	// seq and time are the Seq and the Time of the head's record, and end
	// its Offset plus the length of its encoding. All three are 0 for an
	// empty journal.
	seq, time, end uint64
}

// newState returns the state of an empty journal.
func newState() *state {
	return &state{
		origins: btree.NewMapFunc[originHash, originState](func(a, b originHash) int {
			return bytes.Compare(a[:], b[:])
		}),
		retired: new(btree.Set[uint64]),
	}
}

// apply adds rec, the record under key that follows the head of st on the
// chain, to st: each update becomes the latest position of its origin,
// with its size, its root and the Time of rec. The served position and
// the snapshot flag of an origin do not change, and a new origin starts
// without a served position. size is the length of the encoding of rec.
//
// The state keeps key, and a copy of the origin of each new origin, so it
// keeps no other memory of rec.
//
// # Allocation contract
//
// Allocates the copy of each new origin, and the nodes of the maps as
// [btree.MapFunc.Set] documents them.
func (st *state) apply(rec *record, key string, size uint64) {
	for i, c := range rec.Calls {
		for j, e := range c.Updates {
			h := hashOrigin(e.Origin)

			o, ok := st.origins.Get(h)
			if !ok {
				o.origin = checkpoint.Origin(strings.Clone(string(e.Origin)))
			}

			o.root, o.size, o.time = e.Root, e.Size, rec.Time
			o.latest = position{key: key, seq: rec.Seq, call: i, update: j}
			st.origins.Set(h, o)
		}
	}

	name := key[len(recordPrefix):]
	st.chain.Set(rec.Seq, name)
	st.head.Record = name
	st.seq, st.time, st.end = rec.Seq, rec.Time, rec.Offset+size
}

// take adopts snap, the snapshot under name with an encoding of size
// bytes, which the head of the store names:
//
//   - The position of an origin in snap becomes its latest position, with
//     its size, its root and its time, unless st has an update of the
//     origin after the record of snap.
//   - A position in a group becomes the served position of an origin whose
//     served update is at or before the record of snap, because a group
//     contains its lines.
//   - An origin that snap does not contain, and whose latest update is at
//     or before the record of snap, leaves st, because snap retired it.
//   - An origin that st did not contain starts with the snapshot flag.
//
// retired is the set of the retired origins that a walk of the store read,
// and nil when the base of snap is the snapshot that st took last. take
// then adds the prefixes that snap retired to the set of st. Either way
// the set has every retired origin before the origins leave st.
//
// A state before the record of snap moves its head to that record, so that
// the records after it apply. take drops the names of the chain before the
// record of the base of snap, which garbage collection after snap no
// longer needs.
//
// Returns an error that wraps [ErrJournal], and leaves st unchanged, for a
// snapshot whose record or objects are not names of the journal, whose
// origins are not in the strict order of their hashes, or whose positions
// name an object that it does not list.
//
// # Allocation contract
//
// Allocates the hashes of the origins of snap for the length of the call,
// the copies of the keys of its objects and of each new origin, and the
// nodes of the maps.
func (st *state) take(snap *snapshot, name string, size int64, retired *btree.Set[uint64]) error {
	seq, ok := parseName(snap.Record)
	if !ok {
		return fmt.Errorf("%w: the snapshot %s names the record %q", ErrJournal, name, snap.Record)
	}

	objects := make([]object, len(snap.Objects))
	refs := make([]position, len(snap.Objects))

	for i, obj := range snap.Objects {
		ref, err := objectPosition(obj)
		if err != nil {
			return fmt.Errorf("%w: in the snapshot %s", err, name)
		}

		objects[i] = object{Key: ref.key, Updates: obj.Updates}
		refs[i] = ref
	}

	hashes := make([]originHash, len(snap.Origins))

	for i, o := range snap.Origins {
		if int(o.Object) >= len(refs) {
			return fmt.Errorf("%w: the snapshot %s names the object %d of %d", ErrJournal, name, o.Object, len(refs))
		}

		hashes[i] = hashOrigin(o.Origin)
		if i > 0 && bytes.Compare(hashes[i-1][:], hashes[i][:]) >= 0 {
			return fmt.Errorf("%w: the origins of the snapshot %s are not in the order of their hashes", ErrJournal,
				name)
		}
	}

	if retired != nil {
		st.retired = retired
	} else {
		for _, prefix := range snap.Retired {
			st.retired.Add(prefix)
		}
	}

	st.removeRetired(hashes, seq)

	maxTime := uint64(0)

	for i, so := range snap.Origins {
		p := refs[so.Object]
		p.call, p.update = int(so.Call), int(so.Update)

		o, ok := st.origins.Get(hashes[i])
		if !ok {
			o.origin = checkpoint.Origin(strings.Clone(string(so.Origin)))
			o.snapshot = true
		}

		if !ok || o.latest.seq <= seq {
			o.root, o.size, o.time, o.latest = so.Root, so.Size, so.Time, p
		}

		if p.group && (o.served.key == "" || o.served.seq <= seq) {
			o.served = p
		}

		maxTime = max(maxTime, so.Time)
		st.origins.Set(hashes[i], o)
	}

	if base, ok := parseName(snap.Base); ok {
		st.chain.DeleteRange(0, base)
	}

	if seq > st.seq {
		record := strings.Clone(snap.Record)
		st.chain.Set(seq, record)
		st.head.Record = record
		st.seq, st.time, st.end = seq, maxTime, snap.End
	}

	st.head.Snapshot = name
	st.snap = installed{name: name, objects: objects, seq: seq, end: snap.End, size: size}

	return nil
}

// removeRetired removes from st every origin whose latest update is at or
// before the record of Seq seq, and whose hash hashes does not contain.
// hashes is in strict ascending order, so one walk of both finds them.
func (st *state) removeRetired(hashes []originHash, seq uint64) {
	i := 0

	for h, o := range st.origins.All() {
		for i < len(hashes) && bytes.Compare(hashes[i][:], h[:]) < 0 {
			i++
		}

		listed := i < len(hashes) && hashes[i] == h
		if !listed && o.latest.seq <= seq {
			st.origins.Delete(h)
		}
	}
}

// objectPosition returns the position of the first update of obj, an
// object that a snapshot lists: a record with no count of updates, or a
// group with one. Its key does not share memory with obj.
//
// Returns an error that wraps [ErrJournal] for an object that is neither.
func objectPosition(obj object) (position, error) {
	key := obj.Key

	group := strings.HasPrefix(key, groupPrefix)
	if !group && !strings.HasPrefix(key, recordPrefix) {
		return position{}, fmt.Errorf("%w: the object %q is not a record or a group", ErrJournal, key)
	}

	name := key[len(recordPrefix):]
	if group {
		name = key[len(groupPrefix):]
	}

	seq, ok := parseName(name)
	if !ok || group != (obj.Updates > 0) {
		return position{}, fmt.Errorf("%w: the object %q with %d updates", ErrJournal, key, obj.Updates)
	}

	return position{key: strings.Clone(key), seq: seq, group: group}, nil
}
