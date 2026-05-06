// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

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
// without draining it leak the underlying timer; for cancellation
// support, use [Clock.NewTimer] directly and call [Timer.Stop].
func After(c Clock, d time.Duration) <-chan time.Time {
	return c.NewTimer(d).C()
}
