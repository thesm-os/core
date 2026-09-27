// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"strconv"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fsm"
)

// unfinishable pins the text that follows the name of a state in the
// error Build reports for a state with no path to a terminal state.
const unfinishable = " has no path to a terminal state"

// wide is a state or event type that uses all 256 values, for the
// tests that fill the transition table.
type wide uint8

func (w wide) String() string { return "w" + strconv.Itoa(int(w)) }

// fullTable declares an unguarded edge for every state and event, from
// s on e to s+1, and leaves out skip cells from the end.
func fullTable(skip int) *fsm.Builder[wide, wide, struct{}] {
	b := fsm.NewBuilder[wide, wide, struct{}](0)

	cells := 256*256 - skip
	for i := range cells {
		from, on := wide(i/256), wide(i%256)
		b.Edge(from, on, from+1)
	}

	return b
}

func TestBuilder(t *testing.T) {
	t.Parallel()

	t.Run("Build", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a spec for a valid declaration", func(t *testing.T) {
			t.Parallel()
			_, err := jobBuilder().Build()
			testkit.NoError(t, err, "a valid declaration must build")
		})

		t.Run("can be called again on the same builder", func(t *testing.T) {
			t.Parallel()
			b := jobBuilder()
			first, err := b.Build()
			testkit.NoError(t, err, "the first Build must succeed")
			second, err := b.Build()
			testkit.NoError(t, err, "the second Build must succeed")
			testkit.Equal(t, second.Mermaid(), first.Mermaid(), "both builds must describe the same machine")
		})

		rejected := map[string]*fsm.Builder[state, event, job]{
			"a state that is unreachable": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Edge(running, finish, succeeded).
				Terminal(succeeded),
			// The largest state appears only as a source, so the tables
			// must be sized from sources as well as destinations.
			"an unreachable source beyond every other state": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Edge(cancelled, finish, succeeded).
				Terminal(succeeded),
			// The initial state is the largest value and appears in no
			// edge, so the tables must be sized from it too.
			"an initial state beyond every edge": fsm.NewBuilder[state, event, job](cancelled).
				Edge(pending, finish, succeeded).
				Terminal(succeeded),
			"a state without an outgoing edge that is not terminal": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running),
			"a terminal state with an outgoing edge": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Edge(succeeded, start, pending).
				Terminal(succeeded),
			"an edge after an unguarded edge": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Edge(pending, finish, failed).
				Terminal(succeeded, failed),
			"an edge with two guards": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded,
					fsm.If(func(*job) bool { return true }),
					fsm.If(func(*job) bool { return true })).
				Terminal(succeeded),
			"an edge with two actions": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.Do(func(*job) {}), fsm.Do(func(*job) {})).
				Terminal(succeeded),
			"a nil guard": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.If[job](nil)).
				Terminal(succeeded),
			"a nil action": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.Do[job](nil)).
				Terminal(succeeded),
			"a zero option": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.Option[job]{}).
				Terminal(succeeded),
			"a state with two entry actions": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnEnter(succeeded, func(*job) {}).
				OnEnter(succeeded, func(*job) {}),
			"a state with two exit actions": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnExit(pending, func(*job) {}).
				OnExit(pending, func(*job) {}),
			"a nil entry action": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnEnter(succeeded, nil),
			"a nil exit action": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnExit(pending, nil),
			"an entry action on a state beyond every edge": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnEnter(cancelled, func(*job) {}),
			"an exit action on a state beyond every edge": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnExit(cancelled, func(*job) {}),
			"a terminal state beyond every edge": fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded, cancelled),
		}
		for name, b := range rejected {
			t.Run("rejects "+name, func(t *testing.T) {
				t.Parallel()
				_, err := b.Build()
				testkit.ErrorIs(t, err, fsm.ErrSpec, "the declaration must be rejected")
			})
		}

		t.Run("reports every problem in one error", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(running, finish, succeeded).
				Edge(pending, start, failed).
				Build()
			testkit.Error(t, err, "the declaration must be rejected")
			// running is unreachable, and pending's only edge leads to a
			// failed state that is not terminal and has no edge.
			testkit.True(t, strings.Count(err.Error(), "fsm: invalid spec") >= 3,
				"every problem must be reported: "+err.Error())
		})

		t.Run("rejects every state with no path to a terminal state", func(t *testing.T) {
			t.Parallel()
			// Running and Failed lead only to each other. Pending can still
			// be cancelled.
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(pending, cancel, cancelled).
				Edge(running, fail, failed).
				Edge(failed, start, running).
				Terminal(cancelled).
				Build()
			testkit.ErrorIs(t, err, fsm.ErrSpec, "a cycle without an exit must be rejected")
			testkit.Contains(t, err.Error(), running.String()+unfinishable, "Running must be reported")
			testkit.Contains(t, err.Error(), failed.String()+unfinishable, "Failed must be reported")
			testkit.Equal(t, strings.Count(err.Error(), fsm.ErrSpec.Error()), 2,
				"only Running and Failed must be reported: "+err.Error())
		})

		t.Run("accepts a cycle whose only exit is guarded", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(running, fail, pending).
				Edge(running, finish, succeeded, fsm.If(func(*job) bool { return false })).
				Terminal(succeeded).
				Build()
			testkit.NoError(t, err, "a guarded exit must count as a path to a terminal state")
		})

		t.Run("accepts a cycle without an exit when no state is terminal", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(running, fail, pending).
				Build()
			testkit.NoError(t, err, "a Spec without terminal states must not need one")
		})

		t.Run("reports a state without an outgoing edge once", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(pending, cancel, cancelled).
				Terminal(cancelled).
				Build()
			testkit.ErrorIs(t, err, fsm.ErrSpec, "a state without an outgoing edge must be rejected")
			testkit.NotContains(t, err.Error(), running.String()+unfinishable,
				"a state without an outgoing edge must not also be reported as unable to finish")
			testkit.Equal(t, strings.Count(err.Error(), fsm.ErrSpec.Error()), 1,
				"the state must be reported once: "+err.Error())
		})

		t.Run("accepts as many edges as 16-bit offsets address", func(t *testing.T) {
			t.Parallel()
			// 65,535 edges: every cell but the last. The last state still
			// has edges on the other events, and every state leads to the
			// next, so the declaration is valid.
			_, err := fullTable(1).Build()
			testkit.NoError(t, err, "65,535 edges must build")
		})

		t.Run("rejects more edges than 16-bit offsets address", func(t *testing.T) {
			t.Parallel()
			_, err := fullTable(0).Build()
			testkit.ErrorIs(t, err, fsm.ErrSpec, "65,536 edges must be rejected")
		})
	})

	t.Run("Edges", func(t *testing.T) {
		t.Parallel()

		t.Run("declares the edge from every listed state", func(t *testing.T) {
			t.Parallel()
			spec := jobSpec(t)
			testkit.True(t, spec.Allows(pending, cancelled), "pending must lead to cancelled")
			testkit.True(t, spec.Allows(running, cancelled), "running must lead to cancelled")
		})
	})
}
