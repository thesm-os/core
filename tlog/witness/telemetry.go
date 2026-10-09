// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"time"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
)

// The instruments of a Server.
const (
	// commitDuration is the duration of each commit, from its first check
	// to the results of its calls.
	commitDuration telemetry.InstrumentName = "tlog.witness.commit.duration"

	// commitCalls is the number of calls of each commit.
	commitCalls telemetry.InstrumentName = "tlog.witness.commit.calls"

	// updatesCount counts the updates that a commit checked or advanced, by
	// their outcome.
	updatesCount telemetry.InstrumentName = "tlog.witness.updates"

	// originsCount is the number of origins in the state.
	originsCount telemetry.InstrumentName = "tlog.witness.origins"
)

// The attributes of the instruments, and the values of outcomeKey.
const (
	// errorTypeKey is the attribute of a failed commit: the class of its
	// error, as the semantic conventions of OpenTelemetry name the error
	// type.
	errorTypeKey = "error.type"

	// outcomeKey is the attribute of an update.
	outcomeKey = "outcome"

	// outcomeCommitted is an update that a commit advanced and cosigned.
	outcomeCommitted = "committed"

	// outcomeUncosigned is an update that a commit advanced, and that a
	// failed signature left without a cosignature.
	outcomeUncosigned = "uncosigned"

	// outcomeConflict is an update whose old size was not the committed
	// size.
	outcomeConflict = "conflict"

	// outcomeInconsistent is an update that was not consistent with the
	// committed checkpoint.
	outcomeInconsistent = "inconsistent"
)

// seconds is the UCUM unit of a duration.
const seconds = "s"

// metrics are the instruments of a Server, bound once to every attribute
// set that they record, so that a record does not allocate.
//
// # Concurrency
//
// Safe for concurrent use, as the instruments are.
type metrics struct {
	// duration records the commits without an error, and failed the
	// commits with one, at the index of the class of the error: one
	// histogram for each of the eight classes of errs.
	duration telemetry.Histogram
	failed   [8]telemetry.Histogram

	// calls records the number of calls of each commit.
	calls telemetry.Histogram

	// committed, uncosigned, conflict and inconsistent count the updates
	// of each outcome.
	committed, uncosigned, conflict, inconsistent telemetry.Counter

	// origins reports the number of origins in the state.
	origins telemetry.Gauge
}

// newMetrics returns the instruments of r, bound to their attribute sets.
//
// # Allocation contract
//
// Allocates the instruments and their bindings.
func newMetrics(r telemetry.Reporter) metrics {
	duration := r.Histogram(telemetry.InstrumentSpec{
		Name:        commitDuration,
		Description: "The duration of the commits of a witness, from its checks to the results of its calls.",
		Unit:        seconds,
	})

	m := metrics{
		duration: duration,
		calls: r.Histogram(telemetry.InstrumentSpec{
			Name:        commitCalls,
			Description: "The number of calls of each commit of a witness.",
			Unit:        "{call}",
		}),
		origins: r.Gauge(telemetry.InstrumentSpec{
			Name:        originsCount,
			Description: "The number of origins in the state of a witness.",
			Unit:        "{origin}",
		}),
	}

	for c := range m.failed {
		m.failed[c] = duration.With([]telemetry.Attr{telemetry.AttrString(errorTypeKey, errs.Class(c).String())})
	}

	updates := r.Counter(telemetry.InstrumentSpec{
		Name:        updatesCount,
		Description: "The updates that the commits of a witness checked, by their outcome.",
		Unit:        "{update}",
	})

	m.committed = updates.With([]telemetry.Attr{telemetry.AttrString(outcomeKey, outcomeCommitted)})
	m.uncosigned = updates.With([]telemetry.Attr{telemetry.AttrString(outcomeKey, outcomeUncosigned)})
	m.conflict = updates.With([]telemetry.Attr{telemetry.AttrString(outcomeKey, outcomeConflict)})
	m.inconsistent = updates.With([]telemetry.Attr{telemetry.AttrString(outcomeKey, outcomeInconsistent)})

	return m
}

// recordCommit records the duration elapsed of a commit of calls calls,
// which ended with err.
func (m *metrics) recordCommit(ctx context.Context, elapsed time.Duration, calls int, err error) {
	h := m.duration
	if err != nil {
		c := errs.Classify(err)
		if int(c) >= len(m.failed) {
			c = errs.Unspecified
		}

		h = m.failed[c]
	}

	h.Record(ctx, elapsed.Seconds())
	m.calls.Record(ctx, float64(calls))
}
