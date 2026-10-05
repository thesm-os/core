// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"context"
	"fmt"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/version"
)

// The read limits of the objects of the journal. A server writes no
// object above its limit, so a larger object is corrupt.
const (
	// maxHeadBytes bounds the head: two names of nameText bytes, their
	// tags and their lengths.
	maxHeadBytes = 1024

	// maxObjectBytes bounds a record, a group and the lines of a record.
	// A record contains at most maxCommitBytes of calls. A group contains
	// at most maxGroupBytes, or one call of a record. The lines of a record
	// contain at most maxUpdates calls, each with one line of every
	// cosigner: 16 KiB per call, more than two lines of ML-DSA-87 and one
	// of Ed25519.
	maxObjectBytes = 67108864

	// snapshotOriginBytes is the room of one origin in the read limit of a
	// snapshot: its origin, its root, its numbers and its object, and the
	// prefix of a retired origin.
	snapshotOriginBytes = 1024
)

// readers contains the readers over the bodies that a Server writes, so
// that a write does not allocate one.
var readers = pool.NewPool(func() *bytes.Reader { return new(bytes.Reader) })

// readHead returns the head of the journal in st and its version, and the
// zero head with the zero version for a store without a head.
//
// Returns an error that wraps [ErrJournal] for a head that does not decode,
// that is larger than any head, or whose names are not names of the
// journal, and the error of st for a read that fails.
//
// # Allocation contract
//
// Allocates the bytes of the head and the string of its names.
func readHead(ctx context.Context, st blob.Store) (head, version.Version, error) {
	data, info, err := blob.GetBytes(ctx, st, headKey, maxHeadBytes)

	class := errs.Classify(err)
	if class == errs.NotFound {
		return head{}, version.Unspecified, nil
	}

	if class == errs.Invalid {
		// The key of the head is valid, so GetBytes refuses only a head
		// above the limit.
		return head{}, version.Unspecified, fmt.Errorf("%w: a head above %d bytes: %w", ErrJournal, maxHeadBytes, err)
	}

	if err != nil {
		return head{}, version.Unspecified, fmt.Errorf("witness: read the head: %w", err)
	}

	var h head
	if err := h.UnmarshalBinary(data); err != nil {
		return head{}, version.Unspecified, fmt.Errorf("%w: the head does not decode: %w", ErrJournal, err)
	}

	if _, ok := parseName(h.Record); !ok {
		return head{}, version.Unspecified, fmt.Errorf("%w: the head names the record %q", ErrJournal, h.Record)
	}

	if _, ok := parseName(h.Snapshot); h.Snapshot != "" && !ok {
		return head{}, version.Unspecified, fmt.Errorf("%w: the head names the snapshot %q", ErrJournal, h.Snapshot)
	}

	return h, info.Version, nil
}

// writeHead replaces the head of the journal in st with h, when the
// stored head has the version ifMatch, and returns the version of the new
// head. For the zero ifMatch, it creates the head where st has none. It
// encodes h into buf, and returns buf with its capacity for the next
// write.
//
// Returns [version.ErrMismatch] for a stored head of another version,
// [version.ErrExists] for a head where ifMatch is zero, and the other
// errors of st, such as [version.ErrOutcomeUnknown].
//
// # Allocation contract
//
// Allocates what st allocates, when buf has room for the head.
func writeHead(
	ctx context.Context, st blob.Store, h *head, ifMatch version.Version, buf []byte,
) (version.Version, []byte, error) {
	// The encoding of a head has no error: it has only strings.
	buf, _ = h.AppendBinary(buf[:0])

	cond := version.WriteOptions{IfMatch: ifMatch}
	if ifMatch.IsZero() {
		cond = version.WriteOptions{IfNoneMatch: version.Wildcard}
	}

	info, err := put(ctx, st, headKey, buf, cond)

	return info.Version, buf, err
}

// create writes data to st under key, when st has no object under key.
//
// Returns [version.ErrExists] when st has one, and the other errors of st.
//
// # Allocation contract
//
// Allocates what st allocates.
func create(ctx context.Context, st blob.Store, key string, data []byte) error {
	_, err := put(ctx, st, key, data, version.WriteOptions{IfNoneMatch: version.Wildcard})

	return err
}

// put writes data to st under key with the precondition cond, through a
// pooled reader. A Store reads the reader only until Put returns.
//
// Returns the errors of st, wrapped with the key.
func put(ctx context.Context, st blob.Store, key string, data []byte, cond version.WriteOptions) (blob.Info, error) {
	r := readers.Get()
	r.Reset(data)

	info, err := st.Put(ctx, key, r, blob.PutOptions{Write: cond})

	r.Reset(nil)
	readers.Put(r)

	if err != nil {
		return blob.Info{}, fmt.Errorf("witness: write %s: %w", key, err)
	}

	return info, nil
}

// read returns the bytes of the object under key in st, read with the
// limit of limit bytes.
//
// Returns the error of st, whose class is [errs.NotFound] for an absent
// object, and an error that wraps [ErrJournal] for an object above limit.
//
// # Allocation contract
//
// Allocates the bytes of the object.
func read(ctx context.Context, st blob.Store, key string, limit int64) ([]byte, error) {
	data, _, err := blob.GetBytes(ctx, st, key, limit)
	if errs.Classify(err) == errs.Invalid {
		// The keys of the journal are valid, so GetBytes refuses only an
		// object above the limit.
		return nil, fmt.Errorf("%w: the object %s is above %d bytes: %w", ErrJournal, key, limit, err)
	}

	if err != nil {
		return nil, fmt.Errorf("witness: read %s: %w", key, err)
	}

	return data, nil
}

// readNamed returns the bytes of the object under prefix and name in st,
// read with the limit of limit bytes, after it checks that their hash is
// the hash of name.
//
// Returns the errors of read, and an error that wraps [ErrJournal] for
// bytes whose hash is not the hash of name.
//
// # Allocation contract
//
// Allocates the key and the bytes of the object.
func readNamed(ctx context.Context, st blob.Store, prefix, name string, limit int64) ([]byte, error) {
	data, err := read(ctx, st, prefix+name, limit)
	if err != nil {
		return nil, err
	}

	if err := checkName(name, data); err != nil {
		return nil, err
	}

	return data, nil
}
