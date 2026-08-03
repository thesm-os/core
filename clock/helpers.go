// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"context"
	"time"
)

// Sleep blocks until at least d has elapsed against c. With a
// virtual clock, Sleep does not block real wall-clock time — it
// returns when the test harness advances virtual time past the
// deadline.
//
// A non-positive d delegates to [Clock.NewTimer], which by contract
// returns a timer that fires immediately; Sleep returns on the
// next channel receive without further waiting.
func Sleep(c Clock, d time.Duration) {
	<-c.NewTimer(d).C()
}

// After returns a channel that delivers the firing time once at
// least d has elapsed against c. With a virtual clock, the channel
// does not deliver until the test harness advances virtual time
// past the deadline.
//
// After always allocates a [Timer]. Callers that drop the channel
// without draining it leak the underlying timer; prefer [Wait],
// which stops its timer on every exit path.
func After(c Clock, d time.Duration) <-chan time.Time {
	return c.NewTimer(d).C()
}

// Wait blocks until at least d has elapsed against c, or until ctx
// is done, whichever happens first. It returns nil if the wait
// completed and ctx.Err() if the context ended first.
//
// Wait is the cancellable counterpart to [Sleep] and the safe
// counterpart to [After]: it stops the underlying [Timer] on every
// exit path, so a cancelled wait leaves nothing armed. With a
// virtual clock it consumes no real wall-clock time.
//
// Wait deliberately does not implement a cadence loop. Whether the
// first tick fires immediately, whether the interval runs from
// start or from completion, whether failures back off, and whether
// the interval is jittered are independent policy decisions with
// no defensible default. Wait is the primitive beneath all of them.
//
// # Allocation contract
//
// Allocates one [Timer] via [Clock.NewTimer], as [Sleep] and
// [After] do.
func Wait(ctx context.Context, c Clock, d time.Duration) error {
	t := c.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
