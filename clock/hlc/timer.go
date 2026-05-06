// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc

import (
	"time"

	"go.thesmos.sh/core/clock"
)

// Compile-time interface check.
var _ clock.Timer = (*timer)(nil)

// NewTimer creates a [clock.Timer] that fires after at least d has
// elapsed, using the standard-library [time.NewTimer] under the
// hood. Production callers (event loops, dispatchers) read from the
// returned Timer's channel to drive timer-fired wakes; routing
// through this method instead of calling [time.NewTimer] directly
// is what lets test runs swap in a virtual-time clock for
// fast-forward determinism.
//
// # Allocation contract
//
// Cold path. Allocates the wrapper struct and the underlying
// [time.Timer].
func (*Clock) NewTimer(d time.Duration) clock.Timer {
	return &timer{t: time.NewTimer(d)}
}

// timer wraps a standard-library [time.Timer] to satisfy
// [clock.Timer]. Pure pass-through — every method delegates to the
// embedded [time.Timer].
type timer struct {
	t *time.Timer
}

// C returns the underlying timer's tick channel.
func (s *timer) C() <-chan time.Time { return s.t.C }

// Reset reschedules the underlying timer to fire after d.
func (s *timer) Reset(d time.Duration) bool { return s.t.Reset(d) }

// Stop prevents the underlying timer from firing.
func (s *timer) Stop() bool { return s.t.Stop() }
