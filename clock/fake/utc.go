// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"time"

	"go.thesmos.sh/core/clock"
)

// ReadUTC returns a [clock.UTCReading] whose Time is the virtual time
// and whose MaxError and Synced are what [Clock.SetUTCError] last set.
// A new Clock reports a synchronised reading with no error. ReadUTC
// never returns an error.
//
// ReadUTC does not advance the logical counter.
//
// # Allocation contract
//
// Zero alloc.
func (c *Clock) ReadUTC() (clock.UTCReading, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return clock.UTCReading{Time: c.now, MaxError: c.utcError, Synced: !c.utcUnsynced}, nil
}

// SetUTCError sets the MaxError and Synced fields of the readings that
// [Clock.ReadUTC] returns from now on.
func (c *Clock) SetUTCError(maxError time.Duration, synced bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.utcError = maxError
	c.utcUnsynced = !synced
}
