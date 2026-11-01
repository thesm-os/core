// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by [Instant.UnmarshalBinary] and [Clock.Update].
var (
	// ErrInstantSize is returned when an encoded [Instant] is not
	// exactly [InstantSize] bytes long. It classifies as
	// [go.thesmos.sh/core/errs.Invalid].
	ErrInstantSize = errs.WithClass(errors.New("clock: instant encoding must be 16 bytes"), errs.Invalid)

	// ErrOffset is returned by [Clock.Update] for a peer instant whose
	// wall is further ahead of the physical time of the clock than its
	// max offset. It classifies as [go.thesmos.sh/core/errs.Integrity],
	// because the instant of the peer fails a check of its
	// plausibility.
	ErrOffset = errs.WithClass(errors.New("clock: peer instant beyond the max offset"), errs.Integrity)
)
