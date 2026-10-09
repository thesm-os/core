// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fsm_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/fsm"
)

// The goroutines of a concurrent case, and the machines that each drives.
const (
	goroutines = 8
	rounds     = 20
)

// state is the job lifecycle that the tests declare.
type state uint8

const (
	pending state = iota
	running
	succeeded
	failed
	cancelled
	// outside is the first value beyond the states of the job lifecycle.
	outside
)

// String returns the name of s, or its number for a value beyond the job
// lifecycle.
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
	// unknown is the first value beyond the events of the job lifecycle.
	unknown
)

// String returns the name of e, or its number for a value beyond the job
// lifecycle.
func (e event) String() string {
	if e < unknown {
		return [...]string{"Start", "Finish", "Fail", "Cancel"}[e]
	}

	return "event(" + strconv.Itoa(int(e)) + ")"
}

// job is the data of a job's machine: the log of the actions that ran,
// and the attempts against their limit.
type job struct {
	log      []string
	attempts int
	limit    int
}

func TestSpec(t *testing.T) {
	t.Parallel()

	spec := jobSpec(t)

	t.Run("Initial", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the declared initial state", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, spec.Initial(), pending, "Initial must return the initial state of the Builder")
		})

		t.Run("returns an initial state other than the zero value", func(t *testing.T) {
			t.Parallel()
			late, err := fsm.NewBuilder[state, event, job](running).
				Edge(running, finish, succeeded).Terminal(succeeded).Build()
			assert.NoError(t, err, "the spec must build")
			assert.Equal(t, late.Initial(), running, "Initial must return the initial state of the Builder")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the destination of an unguarded edge", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, finish, nil)
			assert.True(t, ok, "an unguarded edge must accept its event")
			assert.Equal(t, to, succeeded, "Next must return the destination of the edge")
		})

		t.Run("returns the destination of the first edge whose guard holds", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, fail, &job{attempts: 1, limit: 3})
			assert.True(t, ok, "a guarded edge must accept its event")
			assert.Equal(t, to, pending, "Next must take the guarded edge while its guard holds")
		})

		t.Run("returns the destination of the next edge when a guard fails", func(t *testing.T) {
			t.Parallel()
			to, ok := spec.Next(running, fail, &job{attempts: 3, limit: 3})
			assert.True(t, ok, "the unguarded edge must accept the event")
			assert.Equal(t, to, failed, "Next must take the unguarded edge after the guard")
		})

		t.Run("runs no action", func(t *testing.T) {
			t.Parallel()
			j := job{}
			assert.Pure(t, func() job { return j }, func() { _, _ = spec.Next(pending, start, &j) },
				"Next must not run the action of the edge")
		})

		tests := []struct {
			name string
			from state
			on   event
		}{
			{name: "reports false for an event that no edge accepts", from: running, on: start},
			{name: "reports false for a state beyond the spec", from: outside, on: start},
			{name: "reports false for an event beyond the spec", from: pending, on: unknown},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				to, ok := spec.Next(tt.from, tt.on, nil)
				expect.False(t, ok, "Next must refuse the event")
				expect.Equal(t, to, tt.from, "a refused event must return the state as it is")
			})
		}

		t.Run("reports false for an event beyond the spec next to a cell with an edge", func(t *testing.T) {
			t.Parallel()
			// The toggle has one event, so Off on event 1 would index the
			// cell of On for event 0 if the bound were off by one.
			to, ok := toggle(t, false).Next(off, 1, nil)
			expect.False(t, ok, "Next must refuse an event beyond the spec")
			expect.Equal(t, to, off, "a refused event must return the state as it is")
		})
	})

	t.Run("Allows", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			from, to state
			want     bool
		}{
			{name: "reports true for a declared edge", from: running, to: cancelled, want: true},
			{name: "reports true for a guarded edge", from: running, to: pending, want: true},
			{name: "reports false for an undeclared transition", from: succeeded, to: running, want: false},
			{name: "reports false for a state to itself without a loop", from: pending, to: pending, want: false},
			{name: "reports false for a source beyond the spec", from: outside, to: running, want: false},
			{name: "reports false for a destination beyond the spec", from: running, to: outside, want: false},
			// Pending to Outside would index Running to Pending, which is
			// allowed, if the bound were off by one.
			{
				name: "reports false for a destination beyond the spec next to an allowed entry",
				from: pending, to: outside, want: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, spec.Allows(tt.from, tt.to), tt.want, "Allows must follow the declared edges")
			})
		}
	})

	t.Run("Terminal", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give state
			want bool
		}{
			{name: "reports true for a declared terminal state", give: failed, want: true},
			{name: "reports false for a state with edges", give: running, want: false},
			{name: "reports false for a state beyond the spec", give: outside, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, spec.Terminal(tt.give), tt.want, "Terminal must follow the declaration")
			})
		}
	})

	t.Run("Known", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give state
			want bool
		}{
			{name: "reports true for the initial state", give: pending, want: true},
			{name: "reports true for a terminal state", give: cancelled, want: true},
			{name: "reports false for a state beyond the spec", give: outside, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, spec.Known(tt.give), tt.want, "Known must follow the declaration")
			})
		}

		t.Run("reports false for a value that the Spec never declares", func(t *testing.T) {
			t.Parallel()
			gap, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, failed).Terminal(failed).Build()
			assert.NoError(t, err, "the spec must build")
			assert.False(t, gap.Known(running), "a value below the largest state that no call declares must be unknown")
		})
	})

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a machine in the initial state with the data", func(t *testing.T) {
			t.Parallel()
			m := spec.Start(job{limit: 2})
			expect.Equal(t, m.State(), pending, "Start must begin in the initial state")
			expect.Equal(t, m.Data().limit, 2, "Start must keep the data")
		})

		t.Run("returns machines that follow the Spec on goroutines that drive them at once", func(t *testing.T) {
			t.Parallel()

			// run is the final state and the data of one machine.
			type run struct {
				final state
				data  job
			}

			events := []event{start, fail, start, fail, start, finish}
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
				runs := make([]run, 0, rounds)
				for range rounds {
					m := spec.Start(job{limit: 3})
					for _, on := range events {
						if _, err := m.Fire(on); err != nil {
							return runs, err
						}
					}
					runs = append(runs, run{final: m.State(), data: *m.Data()})
				}

				return runs, nil
			})

			m := spec.Start(job{limit: 3})
			for _, on := range events {
				_, err := m.Fire(on)
				assert.NoError(t, err, "Fire must accept every event of the sequence")
			}
			assert.Equal(t, m.State(), succeeded, "the sequence must end in Succeeded")
			want := slices.Repeat([]run{{final: m.State(), data: *m.Data()}}, rounds)

			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
				assert.NoError(t, o.Error, "every machine must accept every event")
				runs, _ := o.Output.([]run)
				assert.Equal(t, runs, want, "every machine must end as a machine on one goroutine ends")
			}
		})
	})

	t.Run("Resume", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a machine in a declared state with the data", func(t *testing.T) {
			t.Parallel()
			m, err := spec.Resume(running, job{limit: 4})
			assert.NoError(t, err, "a declared state must resume")
			expect.Equal(t, m.State(), running, "Resume must begin in the given state")
			expect.Equal(t, m.Data().limit, 4, "Resume must keep the data")
		})

		t.Run("returns ErrState for a state beyond the spec", func(t *testing.T) {
			t.Parallel()
			_, err := spec.Resume(outside, job{})
			expect.ErrorIs(t, err, fsm.ErrState, "Resume must refuse a state beyond the spec")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrState must classify as Invalid")
		})

		t.Run("returns ErrState for a value that the Spec never declares", func(t *testing.T) {
			t.Parallel()
			gap, err := fsm.NewBuilder[state, event, job](pending).
				Edge(pending, finish, failed).Terminal(failed).Build()
			assert.NoError(t, err, "the spec must build")
			_, err = gap.Resume(running, job{})
			assert.ErrorIs(t, err, fsm.ErrState, "Resume must refuse a value that no call declares")
		})
	})

	t.Run("Mermaid", func(t *testing.T) {
		t.Parallel()

		t.Run("renders the declaration as a state diagram", func(t *testing.T) {
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
			assert.Equal(t, spec.Mermaid(), want, "Mermaid must render the initial state, the edges and the ends")
		})
	})
}

// TestSpecAllocs checks the allocation contract of the queries of a Spec.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestSpecAllocs(t *testing.T) {
	spec := jobSpec(t)
	j := job{limit: 3}

	t.Run("Initial", func(t *testing.T) {
		var got state
		expect.MaxAllocs(t, func() { got = spec.Initial() }, 0, "Initial must not allocate")
		assert.Equal(t, got, pending, "the test must measure the initial state")
	})

	t.Run("Next", func(t *testing.T) {
		var got state
		expect.MaxAllocs(t, func() { got, _ = spec.Next(running, fail, &j) }, 0, "Next must not allocate")
		assert.Equal(t, got, pending, "the test must measure a guarded edge")
	})

	t.Run("Allows", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = spec.Allows(running, cancelled) }, 0, "Allows must not allocate")
		assert.True(t, got, "the test must measure a declared edge")
	})

	t.Run("Terminal", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = spec.Terminal(failed) }, 0, "Terminal must not allocate")
		assert.True(t, got, "the test must measure a terminal state")
	})

	t.Run("Known", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = spec.Known(running) }, 0, "Known must not allocate")
		assert.True(t, got, "the test must measure a declared state")
	})
}

// BenchmarkSpec reports the cost of the queries of a Spec, and fails when
// one allocates.
func BenchmarkSpec(b *testing.B) {
	spec := jobSpec(b)
	j := job{limit: 3}

	b.Run("Initial", func(b *testing.B) {
		var got state

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = spec.Initial()
		}

		assert.Equal(b, got, pending, "the benchmark must measure the initial state")
	})

	b.Run("Next", func(b *testing.B) {
		var got state

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = spec.Next(running, fail, &j)
		}

		assert.Equal(b, got, pending, "the benchmark must measure a guarded edge")
	})

	b.Run("Allows", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = spec.Allows(running, cancelled)
		}

		assert.True(b, got, "the benchmark must measure a declared edge")
	})

	b.Run("Terminal", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = spec.Terminal(failed)
		}

		assert.True(b, got, "the benchmark must measure a terminal state")
	})

	b.Run("Known", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = spec.Known(running)
		}

		assert.True(b, got, "the benchmark must measure a declared state")
	})
}

// jobBuilder declares the job lifecycle. A failed attempt returns to
// Pending while attempts remain, and a job can be cancelled until it
// finishes. Each edge action and the actions of Running append to the
// log.
func jobBuilder() *fsm.Builder[state, event, job] {
	return fsm.NewBuilder[state, event, job](pending).
		Edge(pending, start, running, fsm.Do(func(j *job) {
			j.attempts++
			j.log = append(j.log, "start")
		})).
		Edge(running, finish, succeeded, fsm.Do(func(j *job) { j.log = append(j.log, "finish") })).
		Edge(running, fail, pending,
			fsm.If(func(j *job) bool { return j.attempts < j.limit }),
			fsm.Do(func(j *job) { j.log = append(j.log, "retry") })).
		Edge(running, fail, failed, fsm.Do(func(j *job) { j.log = append(j.log, "fail") })).
		Edges([]state{pending, running}, cancel, cancelled).
		OnExit(running, func(j *job) { j.log = append(j.log, "exit running") }).
		OnEnter(running, func(j *job) { j.log = append(j.log, "enter running") }).
		Terminal(succeeded, failed, cancelled)
}

// jobSpec builds the job lifecycle, and fails tb when it does not build.
func jobSpec(tb testing.TB) *fsm.Spec[state, event, job] {
	tb.Helper()

	spec, err := jobBuilder().Build()
	assert.NoError(tb, err, "the job lifecycle must build")

	return spec
}
