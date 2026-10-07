// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fsm"
)

// The texts that follow the name of a state in the problems that Build
// reports, pinned once for the cases that count them.
const (
	deadEnd      = " has no outgoing edge and is not terminal"
	unreachable  = " is unreachable from "
	unfinishable = " has no path to a terminal state"
	nilEntry     = " has a nil entry action"
	twoEntries   = " has two entry actions"
)

// wide is a state or event type that uses all 256 values, for the tests
// that fill the transition table.
type wide uint8

// String returns the name of w.
func (w wide) String() string { return "w" + strconv.Itoa(int(w)) }

func TestBuilder(t *testing.T) {
	t.Parallel()

	t.Run("Build", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Spec for a valid declaration", func(t *testing.T) {
			t.Parallel()
			_, err := jobBuilder().Build()
			assert.NoError(t, err, "a valid declaration must build")
		})

		t.Run("returns the same Spec when called again", func(t *testing.T) {
			t.Parallel()
			b := jobBuilder()
			first, err := b.Build()
			assert.NoError(t, err, "the first Build must succeed")
			second, err := b.Build()
			assert.NoError(t, err, "the second Build must succeed")
			assert.Equal(t, second.Mermaid(), first.Mermaid(), "both builds must describe the same machine")
		})

		tests := []struct {
			name string
			give *fsm.Builder[state, event, job]
		}{
			{
				name: "returns ErrSpec for a state that is unreachable",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Edge(running, finish, succeeded).
					Terminal(succeeded),
			},
			{
				// The largest state is only a source, so the tables must be
				// sized from the sources as well as the destinations.
				name: "returns ErrSpec for an unreachable source beyond every other state",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Edge(cancelled, finish, succeeded).
					Terminal(succeeded),
			},
			{
				// The initial state is the largest value and is in no edge,
				// so the tables must be sized from it too.
				name: "returns ErrSpec for an initial state beyond every edge",
				give: fsm.NewBuilder[state, event, job](cancelled).
					Edge(pending, finish, succeeded).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for a state without an outgoing edge that is not terminal",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, start, running),
			},
			{
				name: "returns ErrSpec for a terminal state with an outgoing edge",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Edge(succeeded, start, pending).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for an edge after an unguarded edge",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Edge(pending, finish, failed).
					Terminal(succeeded, failed),
			},
			{
				name: "returns ErrSpec for an edge with two guards",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded,
						fsm.If(func(*job) bool { return true }),
						fsm.If(func(*job) bool { return true })).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for an edge with two actions",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded, fsm.Do(func(*job) {}), fsm.Do(func(*job) {})).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for a nil guard",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded, fsm.If[job](nil)).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for a nil action",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded, fsm.Do[job](nil)).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for a zero option",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded, fsm.Option[job]{}).
					Terminal(succeeded),
			},
			{
				name: "returns ErrSpec for a state with two entry actions",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnEnter(succeeded, func(*job) {}).
					OnEnter(succeeded, func(*job) {}),
			},
			{
				name: "returns ErrSpec for a state with two exit actions",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnExit(pending, func(*job) {}).
					OnExit(pending, func(*job) {}),
			},
			{
				name: "returns ErrSpec for a nil entry action",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnEnter(succeeded, nil),
			},
			{
				name: "returns ErrSpec for a nil exit action",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnExit(pending, nil),
			},
			{
				name: "returns ErrSpec for an entry action on a state beyond every edge",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnEnter(cancelled, func(*job) {}),
			},
			{
				name: "returns ErrSpec for an exit action on a state beyond every edge",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded).
					OnExit(cancelled, func(*job) {}),
			},
			{
				name: "returns ErrSpec for a terminal state beyond every edge",
				give: fsm.NewBuilder[state, event, job](pending).
					Edge(pending, finish, succeeded).
					Terminal(succeeded, cancelled),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tt.give.Build()
				expect.ErrorIs(t, err, fsm.ErrSpec, "Build must refuse the declaration")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrSpec must classify as Invalid")
			})
		}

		t.Run("returns one problem for each problem it finds", func(t *testing.T) {
			t.Parallel()
			// Running and Succeeded are unreachable, and neither Succeeded
			// nor Failed has an outgoing edge or is terminal.
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(running, finish, succeeded).
				Edge(pending, start, failed).
				Build()
			assert.ErrorIs(t, err, fsm.ErrSpec, "Build must refuse the declaration")
			expect.That(t, err.Error()).
				Contains(succeeded.String()+deadEnd, "Build must report the dead end at Succeeded").
				Contains(failed.String()+deadEnd, "Build must report the dead end at Failed").
				Contains(running.String()+unreachable, "Build must report that Running is unreachable").
				Contains(succeeded.String()+unreachable, "Build must report that Succeeded is unreachable")
			assert.Equal(t, strings.Count(err.Error(), fsm.ErrSpec.Error()), 4, "Build must report each problem once")
		})

		t.Run("returns ErrSpec for each state without a path to a terminal state", func(t *testing.T) {
			t.Parallel()
			// Running and Failed lead only to each other, and Pending can
			// still be cancelled.
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(pending, cancel, cancelled).
				Edge(running, fail, failed).
				Edge(failed, start, running).
				Terminal(cancelled).
				Build()
			assert.ErrorIs(t, err, fsm.ErrSpec, "a cycle without an exit must be refused")
			expect.That(t, err.Error()).
				Contains(running.String()+unfinishable, "Build must report Running").
				Contains(failed.String()+unfinishable, "Build must report Failed")
			assert.Equal(t, strings.Count(err.Error(), fsm.ErrSpec.Error()), 2,
				"Build must report Running and Failed alone")
		})

		t.Run("returns ErrSpec for an initial state without an edge", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).Build()
			expect.ErrorIs(t, err, fsm.ErrSpec, "an initial state that no edge leaves must be refused")
			expect.Contains(t, err.Error(), pending.String()+deadEnd, "Build must report the dead end at Pending")
		})

		t.Run("returns a Spec for an action before a guard", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.Do(func(*job) {}), fsm.If(func(*job) bool { return true })).
				Terminal(succeeded).
				Build()
			assert.NoError(t, err, "an edge must take one action and one guard in either order")
		})

		t.Run("returns every problem of the entry actions around a nil one", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnEnter(succeeded, func(*job) {}).
				OnEnter(succeeded, nil).
				OnEnter(succeeded, func(*job) {}).
				Build()
			assert.ErrorIs(t, err, fsm.ErrSpec, "Build must refuse the declaration")
			expect.That(t, err.Error()).
				Contains(succeeded.String()+nilEntry, "Build must report the nil entry action").
				Contains(succeeded.String()+twoEntries, "Build must report the two entry actions")
		})

		t.Run("returns a Spec for a cycle whose only exit is guarded", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(running, fail, pending).
				Edge(running, finish, succeeded, fsm.If(func(*job) bool { return false })).
				Terminal(succeeded).
				Build()
			assert.NoError(t, err, "a guarded exit must count as a path to a terminal state")
		})

		t.Run("returns a Spec for a cycle without an exit when no state is terminal", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(running, fail, pending).
				Build()
			assert.NoError(t, err, "a Spec without terminal states must not need one")
		})

		t.Run("returns one problem for a state without an outgoing edge", func(t *testing.T) {
			t.Parallel()
			_, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running).
				Edge(pending, cancel, cancelled).
				Terminal(cancelled).
				Build()
			assert.ErrorIs(t, err, fsm.ErrSpec, "a state without an outgoing edge must be refused")
			expect.NotContains(t, err.Error(), running.String()+unfinishable,
				"a dead end must not also be reported as unable to finish")
			expect.Equal(t, strings.Count(err.Error(), fsm.ErrSpec.Error()), 1, "Build must report the state once")
		})

		t.Run("returns a Spec for as many edges as 16-bit offsets address", func(t *testing.T) {
			t.Parallel()
			// 65535 edges fill every cell but the last. The last state still
			// has edges on the other events, and every state leads to the
			// next, so the declaration is valid.
			_, err := fullTable(1).Build()
			assert.NoError(t, err, "65535 edges must build")
		})

		t.Run("returns ErrSpec for more edges than 16-bit offsets address", func(t *testing.T) {
			t.Parallel()
			_, err := fullTable(0).Build()
			assert.ErrorIs(t, err, fsm.ErrSpec, "65536 edges must be refused")
		})
	})

	t.Run("Edges", func(t *testing.T) {
		t.Parallel()

		t.Run("declares the edge from every listed state", func(t *testing.T) {
			t.Parallel()
			spec := jobSpec(t)
			expect.True(t, spec.Allows(pending, cancelled), "Pending must lead to Cancelled")
			expect.True(t, spec.Allows(running, cancelled), "Running must lead to Cancelled")
		})
	})
}

// fullTable declares an unguarded edge for every state and event, from s
// on e to s+1, and leaves out skip cells from the end.
func fullTable(skip int) *fsm.Builder[wide, wide, struct{}] {
	b := fsm.NewBuilder[wide, wide, struct{}](0)

	cells := 256*256 - skip
	for i := range cells {
		from, on := wide(i/256), wide(i%256)
		b.Edge(from, on, from+1)
	}

	return b
}
