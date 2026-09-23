// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"errors"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fsm"
)

// state is the job lifecycle the tests declare.
type state uint8

const (
	pending state = iota
	running
	succeeded
	failed
	cancelled
	// outside is the first value beyond the job lifecycle's states.
	outside
)

func (s state) String() string {
	if s < outside {
		return [...]string{"Pending", "Running", "Succeeded", "Failed", "Cancelled"}[s]
	}

	return "state(" + strconv.Itoa(int(s)) + ")"
}

// event is what happens to a job.
type event uint8

const (
	start event = iota
	finish
	fail
	cancel
	// unknown is the first value beyond the job lifecycle's events.
	unknown
)

func (e event) String() string {
	if e < unknown {
		return [...]string{"Start", "Finish", "Fail", "Cancel"}[e]
	}

	return "event(" + strconv.Itoa(int(e)) + ")"
}

// job is the data a job's machine holds.
type job struct {
	log      []string
	attempts int
	limit    int
}

func (j *job) record(entry string) { j.log = append(j.log, entry) }

// jobBuilder declares the job lifecycle: a failed attempt returns to
// pending while attempts remain, and a job can be cancelled until it
// finishes.
func jobBuilder() *fsm.Builder[state, event, job] {
	return fsm.NewBuilder[state, event, job](pending).
		Edge(pending, start, running, fsm.Do(func(j *job) { j.attempts++; j.record("start") })).
		Edge(running, finish, succeeded, fsm.Do(func(j *job) { j.record("finish") })).
		Edge(running, fail, pending,
			fsm.If(func(j *job) bool { return j.attempts < j.limit }),
			fsm.Do(func(j *job) { j.record("retry") })).
		Edge(running, fail, failed, fsm.Do(func(j *job) { j.record("fail") })).
		Edges([]state{pending, running}, cancel, cancelled).
		OnExit(running, func(j *job) { j.record("exit running") }).
		OnEnter(running, func(j *job) { j.record("enter running") }).
		Terminal(succeeded, failed, cancelled)
}

func jobSpec(tb testing.TB) *fsm.Spec[state, event, job] {
	tb.Helper()

	spec, err := jobBuilder().Build()
	testkit.NoError(tb, err, "the job lifecycle must build")

	return spec
}

func TestSpec(t *testing.T) {
	t.Parallel()

	spec := jobSpec(t)

	t.Run("Initial", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the declared initial state", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, spec.Initial(), pending, "Initial must return the builder's initial state")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("follows an unguarded edge", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, finish, nil)
			testkit.True(t, ok, "an unguarded edge must accept its event")
			testkit.Equal(t, to, succeeded, "Next must return the edge's destination")
		})

		t.Run("takes the first edge whose guard holds", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, fail, &job{attempts: 1, limit: 3})
			testkit.True(t, ok, "a guarded edge must accept its event")
			testkit.Equal(t, to, pending, "Next must take the guarded edge while its guard holds")
		})

		t.Run("falls through to the next edge when a guard fails", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, fail, &job{attempts: 3, limit: 3})
			testkit.True(t, ok, "the unguarded edge must accept the event")
			testkit.Equal(t, to, failed, "Next must fall through to the unguarded edge")
		})

		t.Run("runs no action", func(t *testing.T) {
			t.Parallel()
			j := job{}
			_, _ = spec.Next(pending, start, &j)
			testkit.Equal(t, j.attempts, 0, "Next must not run the edge's action")
		})

		t.Run("rejects an event that no edge accepts", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(pending, finish, nil)
			testkit.False(t, ok, "an event without an edge must be rejected")
			testkit.Equal(t, to, pending, "a rejected event must leave the state as it is")
		})

		t.Run("rejects a state beyond the spec", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(outside, start, nil)
			testkit.False(t, ok, "a state beyond the spec must be rejected")
			testkit.Equal(t, to, outside, "a rejected state must be returned as it is")
		})

		t.Run("rejects an event beyond the spec", func(t *testing.T) {
			t.Parallel()
			_, ok := spec.Next(pending, unknown, nil)
			testkit.False(t, ok, "an event beyond the spec must be rejected")
		})

		t.Run("rejects an event beyond the spec where the next cell holds an edge", func(t *testing.T) {
			t.Parallel()
			// The toggle has one event, so off on event 1 would index on's
			// cell for event 0 if the bound were off by one.
			to, ok := toggle(t, false).Next(off, 1, nil)
			testkit.False(t, ok, "an event beyond the spec must be rejected")
			testkit.Equal(t, to, off, "a rejected event must leave the state as it is")
		})
	})

	t.Run("Allows", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			from, to state
			want     bool
		}{
			"a declared edge":                  {running, cancelled, true},
			"a guarded edge":                   {running, pending, true},
			"an undeclared transition":         {succeeded, running, false},
			"a state to itself without a loop": {pending, pending, false},
			"a source beyond the spec":         {outside, running, false},
			"a destination beyond the spec":    {running, outside, false},
			// pending to outside would index running to pending, which
			// is allowed, if the bound were off by one.
			"a destination beyond the spec where the next entry is allowed": {pending, outside, false},
		}
		for name, tc := range cases {
			t.Run("reports "+name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, spec.Allows(tc.from, tc.to), tc.want, "Allows must follow the declared edges")
			})
		}
	})

	t.Run("Terminal", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			s    state
			want bool
		}{
			"a declared terminal state": {failed, true},
			"a state with edges":        {running, false},
			"a state beyond the spec":   {outside, false},
		}
		for name, tc := range cases {
			t.Run("reports "+name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, spec.Terminal(tc.s), tc.want, "Terminal must follow the declaration")
			})
		}
	})

	t.Run("Known", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			s    state
			want bool
		}{
			"the initial state":       {pending, true},
			"a terminal state":        {cancelled, true},
			"a state beyond the spec": {outside, false},
		}
		for name, tc := range cases {
			t.Run("reports "+name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, spec.Known(tc.s), tc.want, "Known must follow the declaration")
			})
		}

		t.Run("reports a gap in the values as unknown", func(t *testing.T) {
			t.Parallel()
			gap, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, failed).Terminal(failed).Build()
			testkit.NoError(t, err, "the spec must build")
			testkit.False(t, gap.Known(running), "a value below the largest state but never declared must be unknown")
		})
	})

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a machine in the initial state with the data", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 2})
			testkit.Equal(t, m.State(), pending, "Start must begin in the initial state")
			testkit.Equal(t, m.Data().limit, 2, "Start must hold the data")
		})
	})

	t.Run("Resume", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a machine in a declared state", func(t *testing.T) {
			t.Parallel()
			m, err := spec.Resume(running, job{limit: 4})
			testkit.NoError(t, err, "a declared state must resume")
			testkit.Equal(t, m.State(), running, "Resume must begin in the given state")
			testkit.Equal(t, m.Data().limit, 4, "Resume must hold the data")
		})

		t.Run("refuses a state beyond the spec", func(t *testing.T) {
			t.Parallel()
			_, err := spec.Resume(outside, job{})
			testkit.ErrorIs(t, err, fsm.ErrState, "a state beyond the spec must be refused")
		})

		t.Run("refuses a value the spec never declares", func(t *testing.T) {
			t.Parallel()
			gap, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, failed).Terminal(failed).Build()
			testkit.NoError(t, err, "the spec must build")
			_, err = gap.Resume(running, job{})
			testkit.True(t, errors.Is(err, fsm.ErrState), "an undeclared value must be refused")
		})
	})

	t.Run("Mermaid", func(t *testing.T) {
		t.Parallel()

		t.Run("renders the initial state, every edge and every terminal state", func(t *testing.T) {
			t.Parallel()
			want := "stateDiagram-v2\n" +
				"    [*] --> Pending\n" +
				"    Pending --> Running : Start\n" +
				"    Running --> Succeeded : Finish\n" +
				"    Running --> Pending : Fail [guarded]\n" +
				"    Running --> Failed : Fail\n" +
				"    Pending --> Cancelled : Cancel\n" +
				"    Running --> Cancelled : Cancel\n" +
				"    Succeeded --> [*]\n" +
				"    Failed --> [*]\n" +
				"    Cancelled --> [*]\n"
			testkit.Equal(t, spec.Mermaid(), want, "Mermaid must render the declaration")
		})
	})
}

func BenchmarkNext(b *testing.B) {
	spec := jobSpec(b)
	j := job{limit: 3}

	for b.Loop() {
		_, _ = spec.Next(running, fail, &j)
	}
}

func BenchmarkAllows(b *testing.B) {
	spec := jobSpec(b)

	for b.Loop() {
		_ = spec.Allows(running, cancelled)
	}
}
