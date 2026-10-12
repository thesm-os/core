// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"
	"log/slog"
	"slices"
	"sync"
)

// Records is a [slog.Handler] that keeps every record at or above its
// level. A test passes a Records to [slog.New] for the logger of a package,
// and reads the records with [Records.All].
//
// # Attributes and groups
//
// Records keeps each record with the attributes that the handlers of
// log/slog write for it:
//
//   - The attributes of each WithAttrs come before the attributes of the
//     record.
//   - A group of WithGroup contains the attributes after it: those of a
//     later WithAttrs, and those of the record.
//   - Records resolves each value, drops an empty attribute and a group
//     without attributes, and puts the attributes of a group with an
//     empty key in place of the group.
//
// # Concurrency
//
// A Records and the handlers that it derives are safe for concurrent use.
//
// The zero Records is not usable. [NewRecords] returns one.
type Records struct {
	log *recordLog

	// ops are the WithAttrs and the WithGroup that derived the handler, in
	// order.
	ops []recordsOp
}

var _ slog.Handler = (*Records)(nil)

// recordLog is the log that a [Records] and the handlers that it derives
// share: the minimum level and the records in order.
type recordLog struct {
	records []slog.Record
	mu      sync.Mutex
	level   slog.Level
}

// recordsOp is one WithAttrs or WithGroup of a [Records]: attrs, or a
// group when group is not empty.
type recordsOp struct {
	group string
	attrs []slog.Attr
}

// NewRecords returns a Records that keeps each record at level or above.
//
// # Allocation contract
//
// NewRecords allocates the handler and its log.
func NewRecords(level slog.Level) *Records {
	return &Records{log: &recordLog{level: level}}
}

// Enabled reports whether level is at or above the level of h.
func (h *Records) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.log.level
}

// Handle keeps a copy of r with the attributes of h and of r. It returns
// no error.
//
//nolint:gocritic // hugeParam: slog.Handler passes the Record by value
func (h *Records) Handle(_ context.Context, r slog.Record) error {
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)

		return true
	})

	kept := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	kept.AddAttrs(nest(h.ops, attrs)...)

	h.log.mu.Lock()
	defer h.log.mu.Unlock()

	h.log.records = append(h.log.records, kept)

	return nil
}

// WithAttrs returns a handler of the log of h that adds attrs to each
// record. It returns h for no attributes.
func (h *Records) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	return &Records{log: h.log, ops: append(slices.Clip(h.ops), recordsOp{attrs: slices.Clone(attrs)})}
}

// WithGroup returns a handler of the log of h that puts the attributes
// after it into the group name. It returns h for an empty name.
func (h *Records) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &Records{log: h.log, ops: append(slices.Clip(h.ops), recordsOp{group: name})}
}

// All returns every record that h and the handlers derived from h kept,
// in order.
//
// # Allocation contract
//
// All allocates the slice that it returns.
func (h *Records) All() []slog.Record {
	h.log.mu.Lock()
	defer h.log.mu.Unlock()

	return slices.Clone(h.log.records)
}

// nest returns the attributes of a record under ops: the attributes of
// each WithAttrs in order, and the attributes after each WithGroup inside
// that group. attrs, the attributes of the record, come last.
func nest(ops []recordsOp, attrs []slog.Attr) []slog.Attr {
	var out []slog.Attr

	for i, op := range ops {
		if op.group != "" {
			if inner := nest(ops[i+1:], attrs); len(inner) > 0 {
				out = append(out, slog.Attr{Key: op.group, Value: slog.GroupValue(inner...)})
			}

			return out
		}

		out = append(out, clean(op.attrs)...)
	}

	return append(out, clean(attrs)...)
}

// clean returns attrs with each value resolved and without the empty
// attributes. A group without attributes is dropped, and a group with an
// empty key gives its attributes in its place.
func clean(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))

	for _, a := range attrs {
		a.Value = a.Value.Resolve()
		if a.Equal(slog.Attr{}) {
			continue
		}

		if a.Value.Kind() != slog.KindGroup {
			out = append(out, a)

			continue
		}

		inner := clean(a.Value.Group())
		if len(inner) == 0 {
			continue
		}

		if a.Key == "" {
			out = append(out, inner...)

			continue
		}

		out = append(out, slog.Attr{Key: a.Key, Value: slog.GroupValue(inner...)})
	}

	return out
}
