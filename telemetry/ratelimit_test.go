// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

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

// limits is the configuration of the cases without the clock and the
// Suppressed counter, which each case sets.
var limits = telemetry.RateLimitConfig{Subject: subjectKey, Every: every, Keys: 16}

// withoutTime configures the text handlers of the cases, which write each
// record without its time on a line of its own.
var withoutTime = &slog.HandlerOptions{
	ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.TimeKey {
			return slog.Attr{}
		}

		return a
	},
}

// kindString is the octet of slog.KindString, which the key of an event
// writes between its message and a string Subject value.
var kindString = string([]byte{byte(slog.KindString)})

// failingHandler is a slog.Handler that returns errWrapped for every
// record.
type failingHandler struct{ slog.Handler }

// Handle returns errWrapped.
func (failingHandler) Handle(context.Context, slog.Record) error { return errWrapped }

// tenantValuer is a slog.LogValuer whose value is the string of its tenant.
type tenantValuer struct{ tenant string }

// LogValue returns the string of the tenant.
func (v tenantValuer) LogValue() slog.Value { return slog.StringValue(v.tenant) }

// entry is the message and the attributes of a record that a case logs.
type entry struct {
	message string
	args    []any
}

// handledRecord is a record of the allocation ceilings of Handle.
type handledRecord struct {
	record *slog.Record
	name   string
}

func TestRateLimitHandler(t *testing.T) {
	t.Parallel()

	t.Run("NewRateLimitHandler", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			next slog.Handler
			edit func(*telemetry.RateLimitConfig)
			name string
		}{
			{name: "returns ErrConfig for a nil handler", edit: func(*telemetry.RateLimitConfig) {}},
			{
				name: "returns ErrConfig for a nil Clock",
				next: slog.DiscardHandler,
				edit: func(c *telemetry.RateLimitConfig) { c.Clock = nil },
			},
			{
				name: "returns ErrConfig for a nil Suppressed counter",
				next: slog.DiscardHandler,
				edit: func(c *telemetry.RateLimitConfig) { c.Suppressed = nil },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := limits
				cfg.Clock, cfg.Suppressed = fake.New(origin), &summingCounter{}
				tt.edit(&cfg)

				_, err := telemetry.NewRateLimitHandler(tt.next, cfg)
				expect.ErrorIs(t, err, telemetry.ErrConfig, "NewRateLimitHandler must refuse the configuration")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns ErrConfig for an interval that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(interval time.Duration) error {
				cfg := limits
				cfg.Clock, cfg.Suppressed, cfg.Every = fake.New(origin), &summingCounter{}, interval
				_, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, cfg)

				return err
			}, telemetry.ErrConfig, "NewRateLimitHandler must refuse an interval that is not positive",
				prop.Using(prop.Duration(math.MinInt64, 0)), prop.Example(time.Duration(0)))
		})

		t.Run("returns ErrConfig for fewer than one key", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(keys int) error {
				cfg := limits
				cfg.Clock, cfg.Suppressed, cfg.Keys = fake.New(origin), &summingCounter{}, keys
				_, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, cfg)

				return err
			}, telemetry.ErrConfig, "NewRateLimitHandler must refuse fewer than one key",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns a handler that remembers one event", func(t *testing.T) {
			t.Parallel()
			cfg := limits
			cfg.Clock, cfg.Suppressed, cfg.Keys = fake.New(origin), &summingCounter{}, 1

			_, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, cfg)
			assert.NoError(t, err, "NewRateLimitHandler must accept one key")
		})
	})

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			level slog.Level
			want  bool
		}{
			{name: "reports false for a level below that of the wrapped handler", level: slog.LevelInfo},
			{name: "reports true for the level of the wrapped handler", level: slog.LevelWarn, want: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				next := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
				h, _, _ := newRateLimit(t, next)
				assert.Equal(t, h.Enabled(t.Context(), tt.level), tt.want,
					"Enabled must report what the wrapped handler reports")
			})
		}
	})

	t.Run("Handle", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the first record of an event", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, suppressed := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			slog.New(h).Info(message, subjectKey, "a")
			expect.Equal(t, strings.Count(out.String(), "\n"), 1, "the first record must pass")
			expect.Equal(t, suppressed.sum.Load(), int64(0), "a passed record must not count")
		})

		t.Run("drops a second record of the event within the interval", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, clk, suppressed := newRateLimit(t, slog.NewTextHandler(out, withoutTime))
			logger := slog.New(h)

			logger.Info(message, subjectKey, "a")
			clk.Advance(every - time.Nanosecond)
			logger.Info(message, subjectKey, "a")

			expect.Equal(t, strings.Count(out.String(), "\n"), 1, "the second record must not pass")
			expect.Equal(t, suppressed.sum.Load(), int64(1), "the dropped record must count")
		})

		t.Run("passes a record of the event once the interval has passed", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, clk, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))
			logger := slog.New(h)

			logger.Info(message, subjectKey, "a")
			clk.Advance(every)
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "a")

			assert.Equal(t, strings.Count(out.String(), "\n"), 2, "one record per interval must pass")
		})

		t.Run("passes every record at LevelError", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, suppressed := newRateLimit(t, slog.NewTextHandler(out, withoutTime))
			logger := slog.New(h)

			logger.Error(message, subjectKey, "a")
			logger.Error(message, subjectKey, "a")

			expect.Equal(t, strings.Count(out.String(), "\n"), 2, "no record at LevelError may be dropped")
			expect.Equal(t, suppressed.sum.Load(), int64(0), "no record was dropped")
		})

		distinct := []struct {
			name          string
			first, second entry
		}{
			{
				name:   "passes the records of two string subjects",
				first:  entry{message: message, args: []any{subjectKey, "a"}},
				second: entry{message: message, args: []any{subjectKey, "b"}},
			},
			{
				name:   "passes the records of two int64 subjects",
				first:  entry{message: message, args: []any{subjectKey, 1}},
				second: entry{message: message, args: []any{subjectKey, 2}},
			},
			{
				name:   "passes the records of two uint64 subjects",
				first:  entry{message: message, args: []any{subjectKey, uint64(1)}},
				second: entry{message: message, args: []any{subjectKey, uint64(2)}},
			},
			{
				name:   "passes the records of two subjects of another kind",
				first:  entry{message: message, args: []any{subjectKey, true}},
				second: entry{message: message, args: []any{subjectKey, false}},
			},
			{
				name:   "passes the records of a subject and of none",
				first:  entry{message: message, args: []any{subjectKey, "a"}},
				second: entry{message: message},
			},
			{
				name:   "passes the records of a nil subject and of none",
				first:  entry{message: message, args: []any{subjectKey, nil}},
				second: entry{message: message},
			},
			{
				name:   "passes the records of two messages",
				first:  entry{message: message, args: []any{subjectKey, "a"}},
				second: entry{message: "another", args: []any{subjectKey, "a"}},
			},
			{
				name:   "passes the records of two messages without a subject",
				first:  entry{message: message},
				second: entry{message: "another"},
			},
			{
				name:   "passes the records of two subjects after another attribute",
				first:  entry{message: message, args: []any{"region", "eu", subjectKey, "a"}},
				second: entry{message: message, args: []any{"region", "eu", subjectKey, "b"}},
			},
			{
				name:   "passes the records of a message and a value that join to the same octets",
				first:  entry{message: "a", args: []any{subjectKey, kindString + "b"}},
				second: entry{message: "a" + kindString, args: []any{subjectKey, "b"}},
			},
		}
		for _, tt := range distinct {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				out := &bytes.Buffer{}
				h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))
				logger := slog.New(h)

				logger.Info(tt.first.message, tt.first.args...)
				logger.Info(tt.second.message, tt.second.args...)
				assert.Equal(t, strings.Count(out.String(), "\n"), 2, "the records of two events must both pass")
			})
		}

		same := []struct {
			name          string
			first, second entry
		}{
			{
				name:   "takes the first Subject value of the record",
				first:  entry{message: message, args: []any{subjectKey, "a", subjectKey, "b"}},
				second: entry{message: message, args: []any{subjectKey, "a", subjectKey, "c"}},
			},
			{
				name:   "takes the resolved value of a LogValuer subject",
				first:  entry{message: message, args: []any{subjectKey, tenantValuer{tenant: "a"}}},
				second: entry{message: message, args: []any{subjectKey, "a"}},
			},
		}
		for _, tt := range same {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				out := &bytes.Buffer{}
				h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))
				logger := slog.New(h)

				logger.Info(tt.first.message, tt.first.args...)
				logger.Info(tt.second.message, tt.second.args...)
				assert.Equal(t, strings.Count(out.String(), "\n"), 1, "the records of one event must pass once")
			})
		}

		t.Run("names the event by its message alone for an empty Subject", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			cfg := limits
			cfg.Clock, cfg.Suppressed, cfg.Subject = fake.New(origin), &summingCounter{}, ""
			h, err := telemetry.NewRateLimitHandler(slog.NewTextHandler(out, withoutTime), cfg)
			assert.NoError(t, err, "NewRateLimitHandler must accept the configuration")

			logger := slog.New(h)
			logger.Info(message, "", "a")
			logger.Info(message, "", "b")
			assert.Equal(t, strings.Count(out.String(), "\n"), 1, "the records of one message must be one event")
		})

		t.Run("forgets the oldest event beyond Keys", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			cfg := limits
			cfg.Clock, cfg.Suppressed, cfg.Keys = fake.New(origin), &summingCounter{}, 1
			h, err := telemetry.NewRateLimitHandler(slog.NewTextHandler(out, withoutTime), cfg)
			assert.NoError(t, err, "NewRateLimitHandler must accept the configuration")

			logger := slog.New(h)
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "b")
			logger.Info(message, subjectKey, "a")
			assert.Equal(t, strings.Count(out.String(), "\n"), 3, "a forgotten event must pass again")
		})

		levels := []struct {
			name  string
			level slog.Level
		}{
			{name: "returns the error of the wrapped handler", level: slog.LevelInfo},
			{name: "returns the error of the wrapped handler at LevelError", level: slog.LevelError},
		}
		for _, tt := range levels {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h, _, _ := newRateLimit(t, failingHandler{slog.DiscardHandler})
				assert.ErrorIs(t, h.Handle(t.Context(), slog.NewRecord(origin, tt.level, message, 0)), errWrapped,
					"Handle must return the error of the wrapped handler")
			})
		}
	})

	t.Run("WithAttrs", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the Subject value of the attributes", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			region := slog.String("region", "eu")
			slog.New(h.WithAttrs([]slog.Attr{region, slog.String(subjectKey, "a")})).Info(message)
			slog.New(h.WithAttrs([]slog.Attr{region, slog.String(subjectKey, "b")})).Info(message)

			assert.Equal(t, strings.Count(out.String(), "\n"), 2, "the bound Subject values must name two events")
		})

		t.Run("prefers the Subject value of the attributes to that of the record", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			logger := slog.New(h.WithAttrs([]slog.Attr{slog.String(subjectKey, "a")}))
			logger.Info(message)
			logger.Info(message, subjectKey, "b")

			assert.Equal(t, strings.Count(out.String(), "\n"), 1, "the bound Subject value must name the event")
		})

		t.Run("passes the attributes to the wrapped handler", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			slog.New(h.WithAttrs([]slog.Attr{slog.String("region", "eu"), slog.String(subjectKey, "a")})).Info(message)
			assert.Contains(t, out.String(), "region=eu tenant=a", "the wrapped handler must receive the attributes")
		})

		t.Run("keeps the first Subject value", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			a := h.WithAttrs([]slog.Attr{slog.String(subjectKey, "a")})
			slog.New(a).Info(message)
			slog.New(a.WithAttrs([]slog.Attr{slog.String(subjectKey, "b")})).Info(message)

			assert.Equal(t, strings.Count(out.String(), "\n"), 1, "a later Subject value must not rename the event")
		})

		t.Run("takes no Subject value inside a group", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			g := h.WithGroup("request")
			slog.New(g.WithAttrs([]slog.Attr{slog.String(subjectKey, "a")})).Info(message)
			slog.New(g.WithAttrs([]slog.Attr{slog.String(subjectKey, "b")})).Info(message)

			assert.Equal(t, strings.Count(out.String(), "\n"), 1, "an attribute in a group must not name the event")
		})

		t.Run("takes no Subject value for an empty Subject", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			cfg := limits
			cfg.Clock, cfg.Suppressed, cfg.Subject = fake.New(origin), &summingCounter{}, ""
			h, err := telemetry.NewRateLimitHandler(slog.NewTextHandler(out, withoutTime), cfg)
			assert.NoError(t, err, "NewRateLimitHandler must accept the configuration")

			slog.New(h.WithAttrs([]slog.Attr{slog.String("", "a")})).Info(message)
			slog.New(h.WithAttrs([]slog.Attr{slog.String("", "b")})).Info(message)
			assert.Equal(t, strings.Count(out.String(), "\n"), 1, "an attribute without a key must not name the event")
		})
	})

	t.Run("WithGroup", func(t *testing.T) {
		t.Parallel()

		t.Run("takes no Subject value of a record in the group", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			logger := slog.New(h.WithGroup("request"))
			logger.Info(message, subjectKey, "a")
			logger.Info(message, subjectKey, "b")

			assert.Equal(t, strings.Count(out.String(), "\n"), 1,
				"an attribute of a record in a group must not name the event")
		})

		t.Run("passes the group to the wrapped handler", func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h, _, _ := newRateLimit(t, slog.NewTextHandler(out, withoutTime))

			slog.New(h.WithGroup("request")).Info(message, subjectKey, "a")
			assert.Contains(t, out.String(), "request.tenant=a", "the wrapped handler must open the group")
		})

		t.Run("returns the handler for an empty name", func(t *testing.T) {
			t.Parallel()
			h, _, _ := newRateLimit(t, slog.DiscardHandler)
			assert.True(t, h.WithGroup("") == slog.Handler(h), "WithGroup of an empty name must return the receiver")
		})
	})
}

// TestRateLimitHandlerAllocs checks the allocation contract of Enabled and
// Handle over a handler that discards every record and the no-op counter.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestRateLimitHandlerAllocs(t *testing.T) {
	ctx := t.Context()
	cfg := limits
	cfg.Clock, cfg.Suppressed = fake.New(origin), noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"})
	h, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, cfg)
	assert.NoError(t, err, "NewRateLimitHandler must accept the configuration")

	t.Run("Enabled", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = h.Enabled(ctx, slog.LevelInfo) }, 0, "Enabled must not allocate")
		assert.False(t, got, "the test must measure the level of the discarding handler")
	})

	t.Run("Handle", func(t *testing.T) {
		for _, tt := range handled() {
			t.Run(tt.name, func(t *testing.T) {
				assert.NoError(t, h.Handle(ctx, *tt.record), "the first record must pass")

				var got error
				expect.MaxAllocs(t, func() { got = h.Handle(ctx, *tt.record) }, 0, "Handle must not allocate")
				assert.NoError(t, got, "the test must measure a record that Handle drops or passes")
			})
		}
	})
}

// BenchmarkRateLimitHandler reports the cost of Enabled and Handle over a
// handler that discards every record and the no-op counter, and fails when
// one allocates.
func BenchmarkRateLimitHandler(b *testing.B) {
	ctx := b.Context()
	cfg := limits
	cfg.Clock, cfg.Suppressed = fake.New(origin), noop.Reporter{}.Counter(telemetry.InstrumentSpec{Name: "bench"})
	h, err := telemetry.NewRateLimitHandler(slog.DiscardHandler, cfg)
	assert.NoError(b, err, "NewRateLimitHandler must accept the configuration")

	b.Run("Enabled", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = h.Enabled(ctx, slog.LevelInfo)
		}

		assert.False(b, got, "the benchmark must measure the level of the discarding handler")
	})

	b.Run("Handle", func(b *testing.B) {
		for _, tt := range handled() {
			b.Run(tt.name, func(b *testing.B) {
				assert.NoError(b, h.Handle(ctx, *tt.record), "the first record must pass")

				var got error

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					got = h.Handle(ctx, *tt.record)
				}

				assert.NoError(b, got, "the benchmark must measure a record that Handle drops or passes")
			})
		}
	})
}

// handled returns the records of the allocation ceilings of Handle: a
// dropped record of a string subject and of an int64 subject, and a record
// at LevelError, which Handle passes.
func handled() []handledRecord {
	text := slog.NewRecord(origin, slog.LevelInfo, message, 0)
	text.AddAttrs(slog.String(subjectKey, "a"))

	number := slog.NewRecord(origin, slog.LevelInfo, message, 0)
	number.AddAttrs(slog.Int64(subjectKey, 7))

	failure := slog.NewRecord(origin, slog.LevelError, message, 0)

	return []handledRecord{
		{name: "of a dropped record of a string subject", record: &text},
		{name: "of a dropped record of an int64 subject", record: &number},
		{name: "of a record at LevelError", record: &failure},
	}
}

// newRateLimit returns a RateLimitHandler of next with the configuration
// of the cases, its fake clock and its Suppressed counter, and fails tb
// when NewRateLimitHandler refuses them.
func newRateLimit(tb testing.TB, next slog.Handler) (*telemetry.RateLimitHandler, *fake.Clock, *summingCounter) {
	tb.Helper()

	cfg := limits
	clk, suppressed := fake.New(origin), &summingCounter{}
	cfg.Clock, cfg.Suppressed = clk, suppressed
	h, err := telemetry.NewRateLimitHandler(next, cfg)
	assert.NoError(tb, err, "NewRateLimitHandler must accept the configuration")

	return h, clk, suppressed
}
