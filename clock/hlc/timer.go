// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc

import (
	"time"

	"go.thesmos.sh/core/clock"
)

// Compile-time interface check.
var _ clock.Timer = (*timer)(nil)

// NewTimer returns the [clock.Timer] of the physical clock, which sends
// its tick once [Clock.Elapsed] has advanced by at least d. For [New] and
// [NewWithSource] it is a timer of [time.NewTimer]. A caller such as an
// event loop waits on the channel of the Timer in place of a [time.Timer],
// so a test can run the caller on a virtual clock.
//
// # Allocation contract
//
// Cold path. Allocates the Timer of the physical clock: for New and
// NewWithSource the wrapper struct and the underlying [time.Timer].
func (c *Clock) NewTimer(d time.Duration) clock.Timer {
	return c.physical.NewTimer(d)
}

// timer is a [clock.Timer] over a [time.Timer]. Each method calls the
// method of the same name of the time.Timer.
type timer struct {
	t *time.Timer
}

// C returns the channel of the time.Timer.
func (s *timer) C() <-chan time.Time { return s.t.C }

// Reset calls Reset of the time.Timer with d.
func (s *timer) Reset(d time.Duration) bool { return s.t.Reset(d) }

// Stop calls Stop of the time.Timer.
func (s *timer) Stop() bool { return s.t.Stop() }
