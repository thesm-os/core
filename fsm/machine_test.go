// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fsm"
)

// fire delivers every event in order and fails the test on the first
// error.
func fire(tb testing.TB, m *fsm.Machine[state, event, job], events ...event) {
	tb.Helper()

	for _, e := range events {
		_, err := m.Fire(e)
		testkit.NoError(tb, err, "Fire("+e.String()+") must be accepted")
	}
}

func TestMachine(t *testing.T) {
	t.Parallel()

	spec := jobSpec(t)

	t.Run("Fire", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the new state", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			to, err := m.Fire(start)
			testkit.NoError(t, err, "Start must be accepted in pending")
			testkit.Equal(t, to, running, "Fire must return the new state")
			testkit.Equal(t, m.State(), running, "the machine must be in the new state")
		})

		t.Run("runs the exit, edge and entry actions in that order", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 3})
			fire(t, &m, start, fail)
			testkit.Equal(t, strings.Join(m.Data().log, ","),
				"start,enter running,exit running,retry",
				"actions must run exit, then edge, then entry")
		})

		t.Run("enters the new state before its entry action runs", func(t *testing.T) {
			t.Parallel()
			var m fsm.Machine[state, event, job]
			var seen state
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded).
				Terminal(succeeded).
				OnEnter(succeeded, func(*job) { seen = m.State() }).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m = s.Start(job{})
			fire(t, &m, finish)
			testkit.Equal(t, seen, succeeded, "the entry action must see the new state")
		})

		t.Run("runs only the edge action on an edge back to its own state", func(t *testing.T) {
			t.Parallel()
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, pending, fsm.Do(func(j *job) { j.record("loop") })).
				Edge(pending, finish, succeeded).
				OnExit(pending, func(j *job) { j.record("exit") }).
				OnEnter(pending, func(j *job) { j.record("enter") }).
				Terminal(succeeded).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m := s.Start(job{})
			fire(t, &m, start)
			testkit.Equal(t, strings.Join(m.Data().log, ","), "loop", "a loop must not run exit or entry actions")
		})

		t.Run("falls through to the next edge when a guard fails", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 1})
			fire(t, &m, start, fail)
			testkit.Equal(t, m.State(), failed, "the unguarded edge must be taken once attempts run out")
		})

		t.Run("rejects an event without changing state or data", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			to, err := m.Fire(finish)
			testkit.ErrorIs(t, err, fsm.ErrRejected, "an event without an edge must be rejected")
			testkit.Equal(t, to, pending, "Fire must return the current state")
			testkit.Equal(t, m.State(), pending, "a rejected event must not change the state")
			testkit.Len(t, m.Data().log, 0, "a rejected event must not run an action")
		})

		t.Run("rejects an event when every guard fails", func(t *testing.T) {
			t.Parallel()
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, succeeded, fsm.If(func(*job) bool { return false })).
				Edge(pending, cancel, cancelled).
				Terminal(succeeded, cancelled).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m := s.Start(job{})
			_, err = m.Fire(finish)
			testkit.ErrorIs(t, err, fsm.ErrRejected, "an event whose guards all fail must be rejected")
			fire(t, &m, cancel)
		})

		t.Run("rejects an event beyond the spec", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, err := m.Fire(unknown)
			testkit.ErrorIs(t, err, fsm.ErrRejected, "an event beyond the spec must be rejected")
			fire(t, &m, start)
		})

		t.Run("rejects an event beyond the spec where the next cell holds an edge", func(t *testing.T) {
			t.Parallel()
			// The toggle has one event, so off on event 1 would index on's
			// cell for event 0 if the bound were off by one.
			m := toggle(t, false).Start(count{})
			_, err := m.Fire(1)
			testkit.ErrorIs(t, err, fsm.ErrRejected, "an event beyond the spec must be rejected")
			testkit.Equal(t, m.State(), off, "a rejected event must not change the state")
		})

		t.Run("rejects every event in a terminal state", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			fire(t, &m, cancel)
			for e := range unknown {
				_, err := m.Fire(e)
				testkit.ErrorIs(t, err, fsm.ErrRejected, "a terminal state must reject "+e.String())
			}
		})

		t.Run("returns ErrReentrant to an action that fires its own machine", func(t *testing.T) {
			t.Parallel()
			var m fsm.Machine[state, event, job]
			var inner error
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running, fsm.Do(func(*job) { _, inner = m.Fire(finish) })).
				Edge(running, finish, succeeded).
				Terminal(succeeded).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m = s.Start(job{})
			fire(t, &m, start)
			testkit.ErrorIs(t, inner, fsm.ErrReentrant, "the inner Fire must be refused")
			testkit.Equal(t, m.State(), running, "the outer transition must complete")
			fire(t, &m, finish)
		})

		t.Run("returns ErrReentrant to a guard that fires its own machine", func(t *testing.T) {
			t.Parallel()
			var m fsm.Machine[state, event, job]
			var inner error
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running, fsm.If(func(*job) bool {
					_, inner = m.Fire(cancel)

					return true
				})).
				Edge(pending, cancel, cancelled).
				Edge(running, finish, succeeded).
				Terminal(succeeded, cancelled).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m = s.Start(job{})
			fire(t, &m, start)
			testkit.ErrorIs(t, inner, fsm.ErrReentrant, "the Fire from the guard must be refused")
			testkit.Equal(t, m.State(), running, "the outer transition must complete")
		})

		t.Run("refuses every event after an action panicked", func(t *testing.T) {
			t.Parallel()
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running, fsm.Do(func(*job) {
					panic("fsm_test: action") //nolint:forbidigo // the test is about an action that panics.
				})).
				Edge(running, finish, succeeded).
				Terminal(succeeded).
				Build()
			testkit.NoError(t, err, "the spec must build")
			m := s.Start(job{})
			testkit.Panics(t, func() { _, _ = m.Fire(start) }, "the action's panic must reach the caller")
			_, err = m.Fire(start)
			testkit.ErrorIs(t, err, fsm.ErrReentrant, "a machine stopped mid-transition must refuse events")
		})

		t.Run("leaves the machine usable after a rejected event", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, err := m.Fire(finish)
			testkit.ErrorIs(t, err, fsm.ErrRejected, "the event must be rejected")
			fire(t, &m, start, finish)
			testkit.Equal(t, m.State(), succeeded, "the machine must accept events after a rejection")
		})
	})

	t.Run("Data", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the data that guards and actions see", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 1})
			fire(t, &m, start)
			m.Data().limit = 5
			fire(t, &m, fail)
			testkit.Equal(t, m.State(), pending, "a guard must see an update made through Data")
			testkit.Equal(t, m.Data().attempts, 1, "Data must show what the action recorded")
		})
	})
}

// light is a two-state toggle for the allocation test and benchmarks.
type light uint8

const (
	off light = iota
	on
)

func (l light) String() string { return [...]string{"Off", "On"}[l] }

type flip uint8

func (flip) String() string { return "Flip" }

type count struct{ n int }

// toggle declares off and on, each flipping to the other. Guarded
// toggles add a guard, an edge action and an entry action.
func toggle(tb testing.TB, guarded bool) *fsm.Spec[light, flip, count] {
	tb.Helper()

	b := fsm.NewBuilder[light, flip, count](off)

	if guarded {
		always := fsm.If(func(c *count) bool { return c.n >= 0 })
		bump := fsm.Do(func(c *count) { c.n++ })
		b.Edge(off, 0, on, always, bump).
			Edge(on, 0, off, always, bump).
			OnEnter(on, func(c *count) { c.n++ })
	} else {
		b.Edge(off, 0, on).Edge(on, 0, off)
	}

	spec, err := b.Build()
	testkit.NoError(tb, err, "the toggle must build")

	return spec
}

// TestZeroAlloc enforces the allocation contract of Fire, Next and
// Allows. testing.AllocsPerRun reads a process-global malloc counter,
// so this test does not call t.Parallel.
func TestZeroAlloc(t *testing.T) {
	spec := toggle(t, true)
	m := spec.Start(count{})
	c := count{}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Machine.Fire", func() { _, _ = m.Fire(0) }},
		{"Spec.Next", func() { _, _ = spec.Next(on, 0, &c) }},
		{"Spec.Allows", func() { _ = spec.Allows(on, off) }},
		{"Spec.Terminal", func() { _ = spec.Terminal(on) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(1000, tc.fn), float64(0), tc.name+" must not allocate")
		})
	}
}

func BenchmarkFire(b *testing.B) {
	m := toggle(b, false).Start(count{})

	for b.Loop() {
		_, _ = m.Fire(0)
	}
}

func BenchmarkFireGuardedWithActions(b *testing.B) {
	m := toggle(b, true).Start(count{})

	for b.Loop() {
		_, _ = m.Fire(0)
	}
}
