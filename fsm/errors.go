// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
var (
	// ErrSpec is returned by [Builder.Build], joined with one error for
	// every problem in the declaration. It classifies as Invalid under
	// [go.thesmos.sh/core/errs.Classify].
	ErrSpec = errs.WithClass(errors.New("fsm: invalid spec"), errs.Invalid)

	// ErrRejected is returned by [Machine.Fire] when no edge accepts the
	// event in the current state. It classifies as Conflict, because the
	// caller acted on a state that the machine is not in.
	ErrRejected = errs.WithClass(errors.New("fsm: event not accepted in the current state"), errs.Conflict)

	// ErrReentrant is returned by [Machine.Fire] when it is called from
	// a guard or action of the same machine, or after an action of the
	// machine panicked. It classifies as Invalid.
	ErrReentrant = errs.WithClass(errors.New("fsm: fire called during a transition of the same machine"), errs.Invalid)

	// ErrState is returned by [Spec.Resume] for a state that the Spec
	// does not declare. It classifies as Invalid.
	ErrState = errs.WithClass(errors.New("fsm: state not in the spec"), errs.Invalid)
)
