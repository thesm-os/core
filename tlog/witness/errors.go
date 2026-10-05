// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors, wrapped with the
// detail of the failure. [ErrConfig] and [ErrRequest] classify as
// [errs.Invalid]: the same input fails the same way on every call.
// [ErrUnknownOrigin] classifies as [errs.NotFound], and [ErrSignature] as
// [errs.Denied]. [ErrInconsistent], [ErrCosignature] and [ErrJournal]
// classify as [errs.Integrity]: data failed verification, and a retry
// reads the same data. [ErrContention] classifies as [errs.Transient]: a
// later commit catches up.
var (
	// ErrConfig reports a configuration that NewClient or NewServer
	// refuses.
	ErrConfig = errs.WithClass(errors.New("witness: invalid configuration"), errs.Invalid)

	// ErrRequest reports a request that is malformed or that exceeds a
	// bound of the protocol, and a checkpoint that a cosigner does not
	// sign.
	ErrRequest = errs.WithClass(errors.New("witness: invalid request"), errs.Invalid)

	// ErrUnknownOrigin reports an origin that the witness does not
	// accept: an origin that Logs refuses, and a new origin beyond
	// MaxOrigins.
	ErrUnknownOrigin = errs.WithClass(errors.New("witness: unknown origin"), errs.NotFound)

	// ErrSignature reports a note with an invalid line of a key of its
	// log, or without a valid line of every key of one key set.
	ErrSignature = errs.WithClass(errors.New("witness: invalid log signature"), errs.Denied)

	// ErrInconsistent reports a checkpoint that is not consistent with the
	// checkpoint that the witness committed last for its origin.
	ErrInconsistent = errs.WithClass(errors.New("witness: inconsistent checkpoint"), errs.Integrity)

	// ErrCosignature reports a response of a witness without a valid line
	// of each of its keys, with an invalid line of one, or with a line
	// whose timestamp is 0.
	ErrCosignature = errs.WithClass(errors.New("witness: invalid cosignature"), errs.Integrity)

	// ErrJournal reports a journal that the server cannot read: an object
	// that does not decode or whose hash is not its name, or a chain that
	// ends before its snapshot.
	ErrJournal = errs.WithClass(errors.New("witness: corrupt journal"), errs.Integrity)

	// ErrContention reports a commit that the commits of other processes
	// overtook 16 times.
	ErrContention = errs.WithClass(errors.New("witness: commit contention"), errs.Transient)
)
