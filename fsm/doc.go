// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package fsm runs finite state machines over small integer states and
// events.
//
// A state machine written by hand spreads its transitions across the
// branches of the functions that change it, and nothing checks that a
// state can be reached or left. A [Spec] declares the transitions in
// one table, built once by a [Builder] and validated at construction.
// A [Machine] is one instance of a Spec: a current state and the data
// that guards and actions read and update.
//
// # Two uses
//
//   - A caller that stores a status uses the Spec alone. [Spec.Allows]
//     checks a transition before the caller swaps the stored status,
//     and [Spec.Terminal] reports whether a status is final.
//   - A caller that handles events in memory holds a Machine and calls
//     [Machine.Fire] for each event.
//
// # Semantics
//
// A Machine handles one event at a time. [Machine.Fire] tries the
// edges out of the current state for the event in declaration order,
// and takes the first whose guard holds or that has no guard. A
// transition runs the exit action of the old state, the edge's action
// and the entry action of the new state, in that order. An edge that
// leads back to its own state runs only its edge action.
//
// Guards read the machine's data and must not change it, because
// [Spec.Next] evaluates guards without running actions.
//
// # Concurrency
//
// A Spec is immutable and safe for concurrent use. Machines are not:
// the owner of each serialises its calls, typically under the lock that
// already guards the data around it.
//
// # Allocation contract
//
// [Spec.Next], [Spec.Allows], [Spec.Terminal] and [Machine.Fire] do
// not allocate. [Builder.Build] allocates the tables once.
package fsm
