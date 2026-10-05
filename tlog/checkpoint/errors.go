// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors, wrapped with the
// detail of the failure. [ErrBody], [ErrPolicy] and [ErrTimestamp]
// classify as [errs.Invalid]: the same input fails the same way on every
// call. [ErrOrigin] classifies as [errs.Integrity]: the note names a log
// that the policy does not list. [ErrClock] classifies as
// [errs.Transient]: a cosigner whose clock recovers its error bound signs
// on a later call.
var (
	// ErrBody reports a malformed checkpoint body, and a body that a
	// [SubtreeV1] signature cannot cover.
	ErrBody = errs.WithClass(errors.New("checkpoint: malformed body"), errs.Invalid)

	// ErrPolicy reports a policy that breaks a rule of tlog-policy, and a
	// policy from which [Verifier.Reset] or [Policy.QuorumRule] cannot
	// build a rule.
	ErrPolicy = errs.WithClass(errors.New("checkpoint: invalid policy"), errs.Invalid)

	// ErrOrigin reports a checkpoint whose origin is not the key name of a
	// log of the policy.
	ErrOrigin = errs.WithClass(errors.New("checkpoint: unknown origin"), errs.Integrity)

	// ErrTimestamp reports a malformed timestamp, a cosigner without a UTC
	// source or with a negative error bound, and a time of
	// [Cosigner.AppendSignAt] whose whole seconds since the Unix epoch are
	// not positive.
	ErrTimestamp = errs.WithClass(errors.New("checkpoint: invalid timestamp"), errs.Invalid)

	// ErrClock reports a cosigner whose UTC source returns no reading
	// within the cosigner's error bound.
	ErrClock = errs.WithClass(errors.New("checkpoint: clock outside its error bound"), errs.Transient)
)
