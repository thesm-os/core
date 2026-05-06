// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package fake provides a deterministic virtual-time [clock.Clock]
// for tests.
//
// Time does not advance on its own — only [Clock.Advance] (or [Clock.Set])
// moves the clock forward. Goroutines blocked in
// [clock.Sleep] / [clock.After] or waiting on a [clock.Timer] are
// woken when virtual time reaches their deadline.
//
// # Goroutine synchronisation
//
// Because virtual time and real time are decoupled, tests that
// schedule a sleep on a goroutine and then advance the clock may
// race: the test may call [Clock.Advance] before the goroutine has
// registered its waiter. Use [Clock.AwaitWaiters] to block until at
// least N waiters have registered, eliminating the race without
// real-time sleeps:
//
//	clk := fake.New(time.Unix(0, 0))
//	go func() { clock.Sleep(clk, 5*time.Second) }()
//	clk.AwaitWaiters(1)            // deterministic; no real-time
//	clk.Advance(6 * time.Second)   // wakes the goroutine
//
// # HLC behaviour
//
// [Clock.Update] performs the standard HLC merge against the
// observed peer instant — adopting the peer's wall and stepping
// past its logical when the peer is ahead, otherwise stepping the
// local logical. [Clock.Now] increments the logical counter on
// every call so two consecutive Now() invocations between Advance
// calls return distinct instants ordered by HappensBefore.
//
// # Waiter lifecycle
//
// Each pending [Clock.NewTimer], [clock.Sleep], or [clock.After]
// registers an internal waiter that is removed when the timer
// fires (via [Clock.Advance] / [Clock.Set]) or is explicitly
// stopped. Tests that abandon timers without [clock.Timer.Stop]
// and without draining the channel after firing leave waiter
// entries in the [Clock]'s internal slice. For the typical
// per-test [Clock] this is harmless — the [Clock] itself is
// short-lived. For long-lived simulation clocks, prefer to call
// [clock.Timer.Stop] on cancellation paths to keep the slice
// bounded.
package fake
