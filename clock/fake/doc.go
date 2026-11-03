// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package fake provides a [clock.Clock] in virtual time for tests.
//
// The virtual time moves only at [Clock.Advance] and [Clock.Set]. A
// goroutine that blocks in [clock.Sleep], [clock.After] or [clock.Wait],
// or on the channel of a [clock.Timer], wakes when the virtual time is
// at or after its deadline.
//
// # Goroutine synchronisation
//
// A test that starts a goroutine which blocks on the clock, and then
// advances the clock, races with that goroutine. The Advance can come
// before the goroutine registers its waiter. [Clock.AwaitWaiters]
// blocks until the clock has at least n waiters, so the test advances
// the clock after the goroutine blocks:
//
//	clk := fake.New(time.Unix(0, 0))
//	go func() { clock.Sleep(clk, 5*time.Second) }()
//	clk.AwaitWaiters(1)
//	clk.Advance(6 * time.Second)
//
// AwaitWaiters panics after two seconds of real time. [Clock.Waiters]
// returns the number of waiters, so a test polls it with a bound of its
// own for a goroutine that works before its first blocking clock call.
// A test also reads the drop of the count after a waiter leaves, such
// as the timer of a [clock.Wait] whose context ended.
//
// # HLC behaviour
//
// [Clock.Update] merges the instant of a peer as a hybrid logical clock
// does. For a peer ahead, it takes the wall of the peer and the Logical
// after the Logical of the peer. For a peer at the same wall, it takes
// the larger Logical plus 1. For a peer behind, it takes the next local
// Logical. It refuses a peer more than
// [go.thesmos.sh/core/clock.DefaultMaxOffset] ahead of the virtual time
// with [go.thesmos.sh/core/clock.ErrOffset]. [Clock.Now] adds 1 to the
// Logical on every call, so two calls of Now between two moves of the
// virtual time return distinct instants that
// [clock.Instant.HappensBefore] orders.
//
// # Durations
//
// [Clock.Elapsed] is the sum of the durations of [Clock.Advance].
// [Clock.Set] models a step of a wall clock, so a test steps the wall
// time with Set and moves durations forward with Advance.
//
// # Waiter lifecycle
//
// Each pending timer of [Clock.NewTimer], [clock.Sleep], [clock.After]
// and [clock.Wait] is a waiter. The clock removes the waiter when it
// sends the tick of the timer, at an Advance or a Set to a time at or
// after the deadline, and when the timer stops. A timer that a test
// abandons before its deadline keeps its waiter. Code on a long-lived
// clock of a simulation stops each timer on the paths that cancel it,
// so the list of waiters remains bounded.
package fake
