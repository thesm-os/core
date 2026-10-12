// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// The instruments of a witness.
const (
	durationName = "tlog.witness.commit.duration"
	callsName    = "tlog.witness.commit.calls"
	updatesName  = "tlog.witness.updates"
	originsName  = "tlog.witness.origins"
)

// The attributes of the instruments of a witness.
const (
	errorTypeKey = "error.type"
	outcomeKey   = "outcome"
)

// oddClassError is an error whose class is outside the classes of errs.
type oddClassError struct {
	class errs.Class
}

func TestTelemetry(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		t.Run("records the duration and the calls of a commit", func(t *testing.T) {
			t.Parallel()
			m, s, _ := metered(t, l)
			advance(t, s, l, l.update(t, 0, 5))

			assert.Length(t, m.Values(durationName), 1, "the commit must record its duration")
			assert.Equal(t, m.Values(callsName), []float64{1}, "the commit must record its calls")
		})

		t.Run("records the class of the error of a failed commit", func(t *testing.T) {
			t.Parallel()
			m, s, f := metered(t, l)
			f.clock.SetUTCError(0, false)

			_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			assert.ErrorIs(t, err, checkpoint.ErrClock, "the commit must fail")
			assert.Length(t, m.Values(durationName, telemetry.AttrString(errorTypeKey, "Transient")), 1,
				"the commit must record its class")
		})

		t.Run("records the error of every class of errs under the name of its class", func(t *testing.T) {
			t.Parallel()

			for c := range errs.Integrity + 1 {
				m, s, f := metered(t, l)
				f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
					return errs.WithClass(errors.New("the store failed"), c)
				}})

				_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				assert.HasError(t, err, "the commit must fail")
				assert.Length(t, m.Values(durationName, telemetry.AttrString(errorTypeKey, c.String())), 1,
					"the commit must record "+c.String())
			}
		})

		odd := []struct {
			name string
			give errs.Class
		}{
			{
				name: "records the error of the first class after the classes of errs as Unspecified",
				give: errs.Integrity + 1,
			},
			{name: "records the error of a class far after the classes of errs as Unspecified", give: 200},
		}
		for _, tt := range odd {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m, s, f := metered(t, l)
				f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
					return oddClassError{class: tt.give}
				}})

				_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				assert.HasError(t, err, "the commit must fail")
				assert.Length(t, m.Values(durationName, telemetry.AttrString(errorTypeKey, "Unspecified")), 1,
					"the commit must record Unspecified")
			})
		}

		t.Run("counts the updates of a commit whose signatures fail as uncosigned and none as committed",
			func(t *testing.T) {
				t.Parallel()
				m := telemetrytest.NewRecorder()
				f := newFixture(t, l)
				c := &flaky{Cosigner: f.cosigners[0]}
				c.fail.Store(true)
				f.cosigners = []checkpoint.Cosigner{c}
				cfg := f.config()
				cfg.Reporter = m

				_, _, err := newServer(t, cfg).Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				assert.HasError(t, err, "the signature must fail")
				assert.Equal(t, m.Values(updatesName, telemetry.AttrString(outcomeKey, "uncosigned")), []float64{1},
					"the recorder must count the update")
				assert.Empty(t, m.Values(updatesName, telemetry.AttrString(outcomeKey, "committed")),
					"the recorder must count no committed update")
			})

		t.Run("counts the updates of each outcome", func(t *testing.T) {
			t.Parallel()
			m, s, f := metered(t, l)
			advance(t, s, l, l.update(t, 0, 5))

			_, _, err := s.Advance(bounded(t), l.notes[6], []witness.Update{l.update(t, 0, 6)}, nil)
			assert.NoError(t, err, "the conflict must be a failure")

			proof := l.update(t, 5, 9).Proof
			proof[0] = l.leaves[0]
			u := l.update(t, 5, 9)
			u.Proof = proof
			_, _, err = s.Advance(bounded(t), l.notes[9], []witness.Update{u}, nil)
			assert.NoError(t, err, "the inconsistency must be a failure")

			c := &flaky{Cosigner: f.cosigners[0]}
			c.fail.Store(true)
			f.cosigners = []checkpoint.Cosigner{c}
			cfg := f.config()
			cfg.Reporter = m
			_, _, err = newServer(t, cfg).Advance(bounded(t), l.notes[6], []witness.Update{l.update(t, 5, 6)}, nil)
			assert.HasError(t, err, "the signature must fail")

			for _, want := range []string{"committed", "conflict", "inconsistent", "uncosigned"} {
				assert.Equal(t, m.Values(updatesName, telemetry.AttrString(outcomeKey, want)), []float64{1},
					"the recorder must count one "+want)
			}

			assert.Length(t, f.logger.messages(slog.LevelError), 1, "the server must log the inconsistent checkpoint")
		})

		t.Run("reports the number of origins after a commit", func(t *testing.T) {
			t.Parallel()
			other := newTestLog(t, "example.com/other")
			m, s, _ := metered(t, l, other)
			advance(t, s, l, l.update(t, 0, 5))
			advance(t, s, other, other.update(t, 0, 5))

			got := m.Values(originsName)
			assert.NotEmpty(t, got, "the gauge must report the origins")
			assert.Equal(t, got[len(got)-1], float64(2), "the gauge must report both origins")
		})
	})
}

// metered returns a recorder, and a server of a fixture of logs that
// reports to it.
func metered(tb testing.TB, logs ...*testLog) (*telemetrytest.Recorder, *witness.Server, *fixture) {
	tb.Helper()

	m := telemetrytest.NewRecorder()
	f := newFixture(tb, logs...)
	cfg := f.config()
	cfg.Reporter = m

	return m, newServer(tb, cfg), f
}

// Error returns the text of the error.
func (oddClassError) Error() string {
	return "an error of an odd class"
}

// Class returns the class of e, which is outside the classes of errs.
func (e oddClassError) Class() errs.Class {
	return e.class
}
