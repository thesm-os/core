// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest_test

import (
	"log/slog"
	"testing"
	"testing/slogtest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/coretest/telemetrytest"
)

// The messages of the records of the cases.
const (
	messageFirst  = "first"
	messageSecond = "second"
)

func TestRecords(t *testing.T) {
	t.Parallel()

	t.Run("NewRecords", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a handler that log/slog accepts", func(t *testing.T) {
			t.Parallel()
			handlers := map[string]*telemetrytest.Records{}
			slogtest.Run(t, func(t *testing.T) slog.Handler {
				h := telemetrytest.NewRecords(slog.LevelDebug)
				handlers[t.Name()] = h

				return h
			}, func(t *testing.T) map[string]any {
				records := handlers[t.Name()].All()
				assert.Length(t, records, 1, "the case must log one record")

				return recordMap(records[0])
			})
		})
	})

	t.Run("Enabled", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false below its level", func(t *testing.T) {
			t.Parallel()
			assert.False(t, telemetrytest.NewRecords(slog.LevelWarn).Enabled(t.Context(), slog.LevelInfo),
				"Enabled must refuse a level below the level of the handler")
		})

		t.Run("reports true at its level", func(t *testing.T) {
			t.Parallel()
			assert.True(t, telemetrytest.NewRecords(slog.LevelWarn).Enabled(t.Context(), slog.LevelWarn),
				"Enabled must accept the level of the handler")
		})
	})

	t.Run("Handle", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the message, the level and the attributes of a record", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			slog.New(h).Warn(messageFirst, "status", 503)
			records := h.All()
			assert.Length(t, records, 1, "the handler must keep the record")
			expect.Equal(t, records[0].Message, messageFirst, "the record must have its message")
			expect.Equal(t, records[0].Level, slog.LevelWarn, "the record must have its level")
			expect.Equal(t, recordMap(records[0])["status"], any(int64(503)), "the record must have its attribute")
		})

		t.Run("drops a group whose attributes are all empty", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			slog.New(h).Info(messageFirst, slog.Group("request", slog.Attr{}))
			assert.Equal(t, h.All()[0].NumAttrs(), 0, "the record must have no group without attributes")
		})
	})

	t.Run("WithAttrs", func(t *testing.T) {
		t.Parallel()

		t.Run("adds its attributes to the records of the derived handler", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			slog.New(h).With("service", "ledger").Info(messageFirst)
			slog.New(h).Info(messageSecond)
			records := h.All()
			assert.Length(t, records, 2, "the log must keep the records of both handlers")
			expect.Equal(t, recordMap(records[0])["service"], any("ledger"),
				"the derived handler must add its attribute")
			expect.Equal(t, records[1].NumAttrs(), 0, "the first handler must add no attribute")
		})

		t.Run("returns the handler for no attributes", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			assert.Equal(t, h.WithAttrs(nil), slog.Handler(h), "WithAttrs must return the handler itself",
				assert.ByIdentity())
		})
	})

	t.Run("WithGroup", func(t *testing.T) {
		t.Parallel()

		t.Run("puts the attributes after it into the group", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			logger := slog.New(h).With("service", "ledger").WithGroup("http").With("method", "GET")
			logger.Info(messageFirst, "status", 200)
			assert.Equal(t, recordMap(h.All()[0]), map[string]any{
				slog.LevelKey:   slog.LevelInfo,
				slog.MessageKey: messageFirst,
				slog.TimeKey:    h.All()[0].Time,
				"service":       "ledger",
				"http":          map[string]any{"method": "GET", "status": int64(200)},
			}, "the group must contain the attributes after it")
		})

		t.Run("returns the handler for an empty name", func(t *testing.T) {
			t.Parallel()
			h := telemetrytest.NewRecords(slog.LevelDebug)
			assert.Equal(t, h.WithGroup(""), slog.Handler(h), "WithGroup must return the handler itself",
				assert.ByIdentity())
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the records of every handler on many goroutines", func(t *testing.T) {
			t.Parallel()
			const clients, logs = 4, 50
			h := telemetrytest.NewRecords(slog.LevelDebug)
			outcomes := history.Concurrently(clients, time.Minute, func(client int) (any, error) {
				logger := slog.New(h).With("client", client)
				for range logs {
					logger.Info(messageFirst)
				}

				return nil, nil
			})
			for _, o := range outcomes {
				expect.True(t, o.Finished, "every client must finish")
			}
			assert.Length(t, h.All(), clients*logs, "the log must keep every record")
		})
	})
}

// recordMap returns r as a map of the form that slogtest reads: the time,
// the level, the message and each attribute, with a map for each group.
// A zero time has no key.
func recordMap(r slog.Record) map[string]any {
	m := map[string]any{slog.LevelKey: r.Level, slog.MessageKey: r.Message}
	if !r.Time.IsZero() {
		m[slog.TimeKey] = r.Time
	}

	r.Attrs(func(a slog.Attr) bool {
		addAttr(m, a)

		return true
	})

	return m
}

// addAttr adds a to m, as a map for a group.
func addAttr(m map[string]any, a slog.Attr) {
	if a.Value.Kind() != slog.KindGroup {
		m[a.Key] = a.Value.Any()

		return
	}

	group := map[string]any{}
	for _, inner := range a.Value.Group() {
		addAttr(group, inner)
	}

	m[a.Key] = group
}
