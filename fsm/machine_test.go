// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fsm"
)

// light is the state of a toggle, for the allocation tests and the
// benchmarks.
type light uint8

const (
	off light = iota
	on
)

// String returns the name of l.
func (l light) String() string { return [...]string{"Off", "On"}[l] }

// flip is the one event of a toggle.
type flip uint8

// String returns the name of the event.
func (flip) String() string { return "Flip" }

// count is the data of a toggle: the number of times its actions ran.
type count struct{ n int }

func TestMachine(t *testing.T) {
	t.Parallel()

	spec := jobSpec(t)

	t.Run("Fire", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the new state", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			to, err := m.Fire(start)
			assert.NoError(t, err, "Pending must accept Start")
			expect.Equal(t, to, running, "Fire must return the new state")
			expect.Equal(t, m.State(), running, "the machine must be in the new state")
		})

		t.Run("runs the actions of a transition in their documented order", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 3})
			assert.Total(t, func(e event) error {
				_, err := m.Fire(e)

				return err
			}, []event{start, fail}, "the machine must accept Start then Fail")
			assert.Equal(t, strings.Join(m.Data().log, ","), "start,enter running,exit running,retry",
				"Fire must run the exit action, then the edge action, then the entry action")
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
			assert.NoError(t, err, "the spec must build")

			m = s.Start(job{})
			_, err = m.Fire(finish)
			assert.NoError(t, err, "Pending must accept Finish")
			assert.Equal(t, seen, succeeded, "the entry action must see the new state")
		})

		t.Run("runs only the edge action on an edge back to its own state", func(t *testing.T) {
			t.Parallel()
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, pending, fsm.Do(func(j *job) { j.log = append(j.log, "loop") })).
				Edge(pending, finish, succeeded).
				OnExit(pending, func(j *job) { j.log = append(j.log, "exit") }).
				OnEnter(pending, func(j *job) { j.log = append(j.log, "enter") }).
				Terminal(succeeded).
				Build()
			assert.NoError(t, err, "the spec must build")

			m := s.Start(job{})
			_, err = m.Fire(start)
			assert.NoError(t, err, "Pending must accept Start")
			assert.Equal(t, m.Data().log, []string{"loop"}, "a loop must not run the exit or the entry action")
		})

		t.Run("takes the next edge when a guard fails", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 1})
			assert.Total(t, func(e event) error {
				_, err := m.Fire(e)

				return err
			}, []event{start, fail}, "the machine must accept Start then Fail")
			assert.Equal(t, m.State(), failed, "the unguarded edge must be taken once the attempts run out")
		})

		t.Run("returns ErrRejected for an event that no edge accepts", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			to, err := m.Fire(finish)
			expect.ErrorIs(t, err, fsm.ErrRejected, "Pending must refuse Finish")
			expect.Equal(t, errs.Classify(err), errs.Conflict, "ErrRejected must classify as Conflict")
			expect.Equal(t, to, pending, "Fire must return the current state")
			expect.Equal(t, m.State(), pending, "a refused event must not change the state")
			expect.Empty(t, m.Data().log, "a refused event must not run an action")
		})

		t.Run("returns ErrRejected when every guard fails", func(t *testing.T) {
			t.Parallel()
			m := guarded(t).Start(job{})
			_, err := m.Fire(finish)
			assert.ErrorIs(t, err, fsm.ErrRejected, "an event whose guards all fail must be refused")
		})

		t.Run("returns no error for the next event after every guard failed", func(t *testing.T) {
			t.Parallel()
			m := guarded(t).Start(job{})
			_, _ = m.Fire(finish)
			_, err := m.Fire(cancel)
			assert.NoError(t, err, "the machine must accept an event after a refusal")
		})

		t.Run("returns ErrRejected for an event beyond the spec", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, err := m.Fire(unknown)
			assert.ErrorIs(t, err, fsm.ErrRejected, "an event beyond the spec must be refused")
		})

		t.Run("returns no error for the next event after an event beyond the spec", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, _ = m.Fire(unknown)
			_, err := m.Fire(start)
			assert.NoError(t, err, "the machine must accept an event after a refusal")
		})

		t.Run("returns ErrRejected for an event beyond the spec next to a cell with an edge", func(t *testing.T) {
			t.Parallel()
			// The toggle has one event, so Off on event 1 would index the
			// cell of On for event 0 if the bound were off by one.
			m := toggle(t, false).Start(count{})
			_, err := m.Fire(1)
			expect.ErrorIs(t, err, fsm.ErrRejected, "an event beyond the spec must be refused")
			expect.Equal(t, m.State(), off, "a refused event must not change the state")
		})

		t.Run("returns ErrRejected for every event in a terminal state", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, err := m.Fire(cancel)
			assert.NoError(t, err, "Pending must accept Cancel")

			prop.ErrorIs(t, func(e event) error {
				_, err := m.Fire(e)

				return err
			}, fsm.ErrRejected, "a terminal state must refuse every event", prop.Using(prop.Integer[event](0, 0xff)))
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
			assert.NoError(t, err, "the spec must build")

			m = s.Start(job{})
			_, err = m.Fire(start)
			assert.NoError(t, err, "the outer Fire must succeed")
			expect.ErrorIs(t, inner, fsm.ErrReentrant, "the inner Fire must be refused")
			expect.Equal(t, errs.Classify(inner), errs.Invalid, "ErrReentrant must classify as Invalid")
			expect.Equal(t, m.State(), running, "the outer transition must complete")
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
			assert.NoError(t, err, "the spec must build")

			m = s.Start(job{})
			_, err = m.Fire(start)
			assert.NoError(t, err, "the outer Fire must succeed")
			expect.ErrorIs(t, inner, fsm.ErrReentrant, "the Fire from the guard must be refused")
			expect.Equal(t, m.State(), running, "the outer transition must complete")
		})

		t.Run("returns ErrReentrant after an action panicked", func(t *testing.T) {
			t.Parallel()
			s, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, start, running, fsm.Do(func(*job) {
					panic("fsm_test: action") //nolint:forbidigo // the test is about an action that panics.
				})).
				Edge(running, finish, succeeded).
				Terminal(succeeded).
				Build()
			assert.NoError(t, err, "the spec must build")

			m := s.Start(job{})
			assert.Panics(t, func() { _, _ = m.Fire(start) }, "the panic of the action must reach the caller")
			_, err = m.Fire(start)
			assert.ErrorIs(t, err, fsm.ErrReentrant, "a machine stopped in a transition must refuse every event")
		})

		t.Run("takes the next transitions after a rejected event", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, _ = m.Fire(finish)
			assert.Total(t, func(e event) error {
				_, err := m.Fire(e)

				return err
			}, []event{start, finish}, "the machine must accept Start then Finish after a refusal")
			assert.Equal(t, m.State(), succeeded, "the machine must take the transitions after a refusal")
		})
	})

	t.Run("Data", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the data that a guard reads", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 1})
			_, err := m.Fire(start)
			assert.NoError(t, err, "Pending must accept Start")

			m.Data().limit = 5
			_, err = m.Fire(fail)
			assert.NoError(t, err, "Running must accept Fail")
			assert.Equal(t, m.State(), pending, "the guard must see the limit that Data updated")
		})

		t.Run("returns the data that an action updates", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{})
			_, err := m.Fire(start)
			assert.NoError(t, err, "Pending must accept Start")
			assert.Equal(t, m.Data().attempts, 1, "Data must show the attempt that the action counted")
		})
	})
}

// TestMachineAllocs checks the allocation contract of a Machine.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestMachineAllocs(t *testing.T) {
	t.Run("Fire", func(t *testing.T) {
		t.Run("of an unguarded edge", func(t *testing.T) {
			m := toggle(t, false).Start(count{})

			var err error
			expect.MaxAllocs(t, func() { _, err = m.Fire(0) }, 0, "Fire must not allocate")
			assert.NoError(t, err, "the test must measure a transition")
		})

		t.Run("of a guarded edge with actions", func(t *testing.T) {
			m := toggle(t, true).Start(count{})

			var err error
			expect.MaxAllocs(t, func() { _, err = m.Fire(0) }, 0, "Fire must not allocate")
			assert.NoError(t, err, "the test must measure a transition")
		})
	})

	t.Run("State", func(t *testing.T) {
		m := toggle(t, false).Start(count{})

		var got light
		expect.MaxAllocs(t, func() { got = m.State() }, 0, "State must not allocate")
		assert.Equal(t, got, off, "the test must measure the initial state")
	})

	t.Run("Data", func(t *testing.T) {
		m := toggle(t, false).Start(count{n: 7})

		var got *count
		expect.MaxAllocs(t, func() { got = m.Data() }, 0, "Data must not allocate")
		assert.Equal(t, got.n, 7, "the test must measure the data of the machine")
	})
}

// BenchmarkMachine reports the cost of a Machine, and fails when a method
// allocates.
func BenchmarkMachine(b *testing.B) {
	b.Run("Fire", func(b *testing.B) {
		b.Run("of an unguarded edge", func(b *testing.B) {
			m := toggle(b, false).Start(count{})

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = m.Fire(0)
			}

			assert.NoError(b, err, "the benchmark must measure a transition")
		})

		b.Run("of a guarded edge with actions", func(b *testing.B) {
			m := toggle(b, true).Start(count{})

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = m.Fire(0)
			}

			assert.NoError(b, err, "the benchmark must measure a transition")
		})
	})

	b.Run("State", func(b *testing.B) {
		m := toggle(b, false).Start(count{})

		var got light

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = m.State()
		}

		assert.Equal(b, got, off, "the benchmark must measure the initial state")
	})

	b.Run("Data", func(b *testing.B) {
		m := toggle(b, false).Start(count{n: 7})

		var got *count

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = m.Data()
		}

		assert.Equal(b, got.n, 7, "the benchmark must measure the data of the machine")
	})
}

// guarded declares Pending with a Finish whose guard always fails and an
// unguarded Cancel, and fails tb when it does not build.
func guarded(tb testing.TB) *fsm.Spec[state, event, job] {
	tb.Helper()

	spec, err := fsm.NewBuilder[state, event, job](pending).
		Edge(pending, finish, succeeded, fsm.If(func(*job) bool { return false })).
		Edge(pending, cancel, cancelled).
		Terminal(succeeded, cancelled).
		Build()
	assert.NoError(tb, err, "the spec must build")

	return spec
}

// toggle declares Off and On, each flipping to the other, and fails tb
// when it does not build. With actions, the toggle adds a guard and an
// edge action to each edge, and an entry action to On.
func toggle(tb testing.TB, withActions bool) *fsm.Spec[light, flip, count] {
	tb.Helper()

	b := fsm.NewBuilder[light, flip, count](off)

	if withActions {
		always := fsm.If(func(c *count) bool { return c.n >= 0 })
		bump := fsm.Do(func(c *count) { c.n++ })
		b.Edge(off, 0, on, always, bump).
			Edge(on, 0, off, always, bump).
			OnEnter(on, func(c *count) { c.n++ })
	} else {
		b.Edge(off, 0, on).Edge(on, 0, off)
	}

	spec, err := b.Build()
	assert.NoError(tb, err, "the toggle must build")

	return spec
}
