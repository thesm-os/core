// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The configuration of the RateLimitHandlers of the cases.
const (
	// every is the interval of the cases.
	every = time.Minute

	// subjectKey is the key of the Subject attribute of the cases.
	subjectKey = "tenant"

	// message is the message of the records of the cases.
	message = "anchor failed"
)

// errWrapped is the error of a failingHandler.
var errWrapped = errors.New("the wrapped handler failed")

// failingHandler is a slog.Handler that returns errWrapped for every
// record.
type failingHandler struct{ slog.Handler }

// Handle returns errWrapped.
func (failingHandler) Handle(context.Context, slog.Record) error {
	return errWrapped
}

func TestRateLimitHandler(t *testing.T) {
	t.Parallel()

	t.Run("NewRateLimitHandler", func(t *testing.T) {
		t.Parallel()

		next := slog.DiscardHandler
		tests := []struct {
			name string
			next slog.Handler
			edit func(*telemetry.RateLimitConfig)
		}{
			{name: "returns ErrConfig for a nil handler", edit: func(*telemetry.RateLimitConfig) {}},
			{
				name: "returns ErrConfig for a nil Clock",
				next: next,
				edit: func(c *telemetry.RateLimitConfig) { c.Clock = nil },
			},
			{
				name: "returns ErrConfig for a nil Suppressed counter",
				next: next,
				edit: func(c *telemetry.RateLimitConfig) { c.Suppressed = nil },
			},
			{
				name: "returns ErrConfig for an interval of zero",
				next: next,
				edit: func(c *telemetry.RateLimitConfig) { c.Every = 0 },
			},
			{
				name: "returns ErrConfig for zero Keys",
				next: next,
				edit: func(c *telemetry.RateLimitConfig) { c.Keys = 0 },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := rateLimitConfig(fake.New(origin), &summingCounter{})
				tt.edit(&cfg)

				_, err := telemetry.NewRateLimitHandler(tt.next, cfg)
				testkit.ErrorIs(t, err, telemetry.ErrConfig, "NewRateLimitHandler must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a handler that remembers one event", func(t *testing.T) {
			t.Parallel()
			cfg := rateLimitConfig(fake.New(origin), &summingCounter{})
			cfg.Keys = 1

			_, err := telemetry.NewRateLimitHandler(next, cfg)
			testkit.NoError(t, err, "one key must be valid")
		})
	})

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the wrapped handler handles the level", func(t *testing.T) {
			t.Parallel()
			next := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
			h, _, _ := newRateLimit(t, next)

			testkit.False(t, h.Enabled(t.Context(), slog.LevelInfo), "Enabled must report the wrapped handler's answer")
			testkit.True(t, h.Enabled(t.Context(), slog.LevelWarn), "Enabled must report the wrapped handler's answer")
		})
	})

	t.Run("Handle", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the first record of an event", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, suppressed := newRateLimit(t, textHandler(out))

			slog.New(h).Info(message, subjectKey, "a")
			testkit.Equal(t, lines(out), 1, "the first record must pass")
			testkit.Equal(t, suppressed.sum.Load(), int64(0), "a passed record must not count")
		})

		t.Run("drops a second record of the event within the interval", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, clk, suppressed := newRateLimit(t, textHandler(out))
			logger := slog.New(h)

			logger.Info(message, subjectKey, "a")
			clk.Advance(every - time.Nanosecond)
			logger.Info(message, subjectKey, "a")

			testkit.Equal(t, lines(out), 1, "the second record must not pass")
			testkit.Equal(t, suppressed.sum.Load(), int64(1), "the dropped record must count")
		})

		t.Run("passes a record of the event once the interval has passed", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, clk, _ := newRateLimit(t, textHandler(out))
			logger := slog.New(h)

			logger.Info(message, subjectKey, "a")
			clk.Advance(every)
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "a")

			testkit.Equal(t, lines(out), 2, "one record per interval must pass")
		})

		t.Run("passes every record at LevelError", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, suppressed := newRateLimit(t, textHandler(out))
			logger := slog.New(h)

			logger.Error(message, subjectKey, "a")
			logger.Error(message, subjectKey, "a")

			testkit.Equal(t, lines(out), 2, "no record at LevelError may be dropped")
			testkit.Equal(t, suppressed.sum.Load(), int64(0), "no record was dropped")
		})

		distinct := []struct {
			name          string
			first, second []any
			message       string
		}{
			{
				name:   "passes the records of two string subjects",
				first:  []any{subjectKey, "a"},
				second: []any{subjectKey, "b"},
			},
			{
				name:   "passes the records of two int64 subjects",
				first:  []any{subjectKey, 1},
				second: []any{subjectKey, 2},
			},
			{
				name:   "passes the records of two uint64 subjects",
				first:  []any{subjectKey, uint64(1)},
				second: []any{subjectKey, uint64(2)},
			},
			{
				name:   "passes the records of two subjects of another kind",
				first:  []any{subjectKey, true},
				second: []any{subjectKey, false},
			},
			{name: "passes the records of a subject and of none", first: []any{subjectKey, "a"}},
			{
				name:    "passes the records of two messages",
				first:   []any{subjectKey, "a"},
				second:  []any{subjectKey, "a"},
				message: "another",
			},
		}
		for _, tt := range distinct {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				out := &bytes.Buffer{}
				h, _, _ := newRateLimit(t, textHandler(out))
				logger := slog.New(h)

				second := message
				if tt.message != "" {
					second = tt.message
				}

				logger.Info(message, tt.first...)
				logger.Info(second, tt.second...)
				testkit.Equal(t, lines(out), 2, "the records of two events must both pass")
			})
		}

		t.Run("names the event by its message alone for an empty Subject", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			cfg := rateLimitConfig(fake.New(origin), &summingCounter{})
			cfg.Subject = ""
			h, err := telemetry.NewRateLimitHandler(textHandler(out), cfg)
			testkit.NoError(t, err, "NewRateLimitHandler must accept the configuration")

			logger := slog.New(h)
			logger.Info(message, "", "a")
			logger.Info(message, "", "b")
			testkit.Equal(t, lines(out), 1, "the records of one message must be one event")
		})

		t.Run("forgets the oldest event beyond Keys", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			cfg := rateLimitConfig(fake.New(origin), &summingCounter{})
			cfg.Keys = 1
			h, err := telemetry.NewRateLimitHandler(textHandler(out), cfg)
			testkit.NoError(t, err, "NewRateLimitHandler must accept the configuration")

			logger := slog.New(h)
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "b")
			logger.Info(message, subjectKey, "a")
			testkit.Equal(t, lines(out), 3, "a forgotten event must pass again")
		})

		t.Run("returns the error of the wrapped handler", func(t *testing.T) {
			t.Parallel()
			h, _, _ := newRateLimit(t, failingHandler{slog.DiscardHandler})

			err := h.Handle(t.Context(), slog.NewRecord(origin, slog.LevelInfo, message, 0))
			testkit.ErrorIs(t, err, errWrapped, "Handle must return the wrapped handler's error")

			err = h.Handle(t.Context(), slog.NewRecord(origin, slog.LevelError, message, 0))
			testkit.ErrorIs(t, err, errWrapped, "Handle must return the wrapped handler's error at LevelError")
		})
	})

	t.Run("WithAttrs", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the Subject value of the attributes", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, textHandler(out))

			a := slog.New(h.WithAttrs([]slog.Attr{slog.String("region", "eu"), slog.String(subjectKey, "a")}))
			b := slog.New(h.WithAttrs([]slog.Attr{slog.String("region", "eu"), slog.String(subjectKey, "b")}))
			a.Info(message)
			b.Info(message)
			a.Info(message, subjectKey, "c")

			testkit.Equal(t, lines(out), 2, "the bound Subject value must name the event")
			testkit.True(t, strings.Contains(out.String(), "region=eu tenant=a"),
				"the wrapped handler must receive the attributes")
			testkit.True(t, strings.Contains(out.String(), "tenant=b"),
				"the record of the second Subject value must pass")
		})

		t.Run("keeps the first Subject value", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, textHandler(out))

			a := h.WithAttrs([]slog.Attr{slog.String(subjectKey, "a")})
			slog.New(a).Info(message)
			slog.New(a.WithAttrs([]slog.Attr{slog.String(subjectKey, "b")})).Info(message)

			testkit.Equal(t, lines(out), 1, "a later Subject value must not rename the event")
		})

		t.Run("takes no Subject value inside a group", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, textHandler(out))

			g := h.WithGroup("request")
			slog.New(g.WithAttrs([]slog.Attr{slog.String(subjectKey, "a")})).Info(message)
			slog.New(g.WithAttrs([]slog.Attr{slog.String(subjectKey, "b")})).Info(message)

			testkit.Equal(t, lines(out), 1, "an attribute in a group must not name the event")
		})
	})

	t.Run("WithGroup", func(t *testing.T) {
		t.Parallel()

		t.Run("puts the attributes of the records into the group", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, textHandler(out))

			logger := slog.New(h.WithGroup("request"))
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "b")

			testkit.Equal(t, lines(out), 1, "a record's attribute in a group must not name the event")
			testkit.True(
				t,
				strings.Contains(out.String(), "request.tenant=a"),
				"the wrapped handler must open the group",
			)
		})

		t.Run("returns the handler for an empty name", func(t *testing.T) {
			t.Parallel()
			h, _, _ := newRateLimit(t, slog.DiscardHandler)
			testkit.True(t, h.WithGroup("") == slog.Handler(h), "WithGroup of an empty name must return the receiver")
		})
	})
}

func BenchmarkRateLimitHandler(b *testing.B) {
	suppressed := noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"})
	h, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, rateLimitConfig(fake.New(origin), suppressed))
	testkit.NoError(b, err, "NewRateLimitHandler must accept the configuration")

	subjects := []struct {
		name  string
		value slog.Attr
	}{
		{name: "Handle of a dropped record of a string subject", value: slog.String(subjectKey, "a")},
		{name: "Handle of a dropped record of an int64 subject", value: slog.Int64(subjectKey, 7)},
	}
	for _, s := range subjects {
		b.Run(s.name, func(b *testing.B) {
			r := slog.NewRecord(origin, slog.LevelInfo, message, 0)
			r.AddAttrs(s.value)
			errBench := h.Handle(b.Context(), r)
			testkit.NoError(b, errBench, "the first record must pass")

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				errBench = h.Handle(b.Context(), r)
			}

			testkit.NoError(b, errBench, "a dropped record must return nil")
		})
	}

	b.Run("Handle of a record at LevelError", func(b *testing.B) {
		r := slog.NewRecord(origin, slog.LevelError, message, 0)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var errBench error
		for c.Loop() {
			errBench = h.Handle(b.Context(), r)
		}

		testkit.NoError(b, errBench, "the discarding handler must return nil")
	})
}

// rateLimitConfig returns the configuration of the cases on clk with
// suppressed.
func rateLimitConfig(clk clock.Clock, suppressed telemetry.Counter) telemetry.RateLimitConfig {
	return telemetry.RateLimitConfig{Clock: clk, Suppressed: suppressed, Subject: subjectKey, Every: every, Keys: 16}
}

// newRateLimit returns a RateLimitHandler of next with the configuration
// of the cases, its fake clock and its Suppressed counter, and fails tb
// when NewRateLimitHandler refuses them.
func newRateLimit(tb testing.TB, next slog.Handler) (*telemetry.RateLimitHandler, *fake.Clock, *summingCounter) {
	tb.Helper()

	clk, suppressed := fake.New(origin), &summingCounter{}
	h, err := telemetry.NewRateLimitHandler(next, rateLimitConfig(clk, suppressed))
	testkit.NoError(tb, err, "NewRateLimitHandler must accept the configuration")

	return h, clk, suppressed
}

// textHandler returns a text handler that writes to out without the time
// of a record.
func textHandler(out *bytes.Buffer) slog.Handler {
	return slog.NewTextHandler(out, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	})
}

// lines returns the number of records in out, one per line.
func lines(out *bytes.Buffer) int {
	return strings.Count(out.String(), "\n")
}
