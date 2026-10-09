// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"encoding/binary"
	"hash/maphash"
	"log/slog"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock"
)

// RateLimitConfig configures a [RateLimitHandler]. Clock, Suppressed, Every
// and Keys are required, and Subject is optional.
type RateLimitConfig struct {
	// Clock reads the time of each record that the handler limits.
	Clock clock.Clock

	// Suppressed counts the records that the handler drops, one per
	// record.
	Suppressed Counter

	// Subject is the key of the attribute whose value tells apart the
	// events of one message, such as the identifier of a tenant or a
	// device. Empty, the message alone names an event.
	Subject string

	// Every is the interval within which the handler passes one record of
	// an event. It must be positive.
	Every time.Duration

	// Keys is the number of events whose last passed record the handler
	// remembers. It must be positive. An event that the handler no longer
	// remembers passes its next record.
	Keys int
}

// RateLimitHandler is a [slog.Handler] that passes at most one record of
// an event per interval to the handler that it wraps. An event is a
// message and the value of the configuration's Subject attribute. The
// handler passes every record at [slog.LevelError] and above, and adds
// each record that it drops to the configuration's Suppressed counter.
//
// The handler remembers the time of the last passed record of up to Keys
// events, in a [cache.Cache] of S3-FIFO eviction, so a burst of distinct
// events costs bounded memory. It keys each event by a 64-bit hash of the
// message and the Subject value, so two events whose hashes collide share
// one interval, with a probability of about one in 2^64 per pair.
//
// The Subject value is that of the first attribute of the Subject key
// outside every group: from a WithAttrs call before any WithGroup, or else
// from the record, when the handler has no group. A record without one
// names its event by its message alone.
//
// Two records of an event that the handler does not remember, handled at
// once, may both pass. A clock that steps backward delays an event's
// records until it passes the time of the event's last record again.
//
// # Concurrency
//
// Safe for concurrent use. Handle takes no lock for an event that the
// handler remembers. The handlers that WithAttrs and WithGroup return share
// the remembered events of the handler that they derive from.
//
// # Allocation contract
//
// Handle does not allocate for an event that the handler remembers whose
// Subject value is a string, an int64 or a uint64, apart from what the
// wrapped handler allocates. The first passed record of an event allocates
// its entry in the cache. A Subject value of another kind allocates its
// string form for each record.
type RateLimitHandler struct {
	next       slog.Handler
	clock      clock.Clock
	suppressed Counter

	// last maps the hash of an event to the Unix time, in nanoseconds, of
	// its last passed record.
	last *cache.Cache[uint64, *atomic.Int64]

	// bound is the Subject value of a WithAttrs call, and isBound reports
	// whether a WithAttrs call had one.
	bound slog.Value

	subject string

	seed  maphash.Seed
	every time.Duration

	isBound bool

	// grouped reports whether a WithGroup call put the attributes of later
	// calls and of the records into a group.
	grouped bool
}

var _ slog.Handler = (*RateLimitHandler)(nil)

// NewRateLimitHandler returns a RateLimitHandler of cfg that passes the
// records that it does not drop to next.
//
// Error modes: a nil next, Clock or Suppressed, an Every that is not
// positive, and Keys below 1, return [ErrConfig], classified Invalid.
//
// # Allocation contract
//
// The handler and its cache.
func NewRateLimitHandler(next slog.Handler, cfg RateLimitConfig) (*RateLimitHandler, error) {
	if next == nil || cfg.Clock == nil || cfg.Suppressed == nil || cfg.Every <= 0 || cfg.Keys < 1 {
		return nil, ErrConfig
	}

	// cache.New refuses only a nil Clock and a Capacity below 1, which the
	// check above refuses first.
	last, _ := cache.New(cache.Config[uint64, *atomic.Int64]{
		Clock: cfg.Clock, Capacity: int64(cfg.Keys),
	})

	return &RateLimitHandler{
		next:       next,
		clock:      cfg.Clock,
		suppressed: cfg.Suppressed,
		last:       last,
		subject:    cfg.Subject,
		seed:       maphash.MakeSeed(),
		every:      cfg.Every,
	}, nil
}

// Enabled reports whether the wrapped handler handles records at level.
func (h *RateLimitHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle passes r to the wrapped handler when its level is
// [slog.LevelError] or above, when the handler does not remember its
// event, and when its event's last passed record is at least the interval
// old. It drops r otherwise, adds 1 to the Suppressed counter, and returns
// nil.
//
// Error modes: the error of the wrapped handler, as it returns it.
//
//nolint:gocritic // hugeParam: slog.Handler passes the Record by value
func (h *RateLimitHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		return h.next.Handle(ctx, r) //nolint:wrapcheck // a handler returns the error of the handler that it wraps
	}

	key := h.key(&r)
	now := h.clock.Time().UnixNano()

	if last, ok := h.last.Get(key); ok {
		prev := last.Load()
		if now-prev < int64(h.every) || !last.CompareAndSwap(prev, now) {
			h.suppressed.Add(ctx, 1)

			return nil
		}
	} else {
		last = new(atomic.Int64)
		last.Store(now)
		h.last.Set(key, last, time.Time{})
	}

	return h.next.Handle(ctx, r) //nolint:wrapcheck // a handler returns the error of the handler that it wraps
}

// WithAttrs returns a RateLimitHandler that wraps the handler of the
// wrapped handler's WithAttrs, and shares the remembered events of h. When
// h has no group and no Subject value yet, the first attribute of attrs
// whose key is the Subject gives the Subject value of the returned
// handler's records.
func (h *RateLimitHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)

	if !h.grouped && !h.isBound && h.subject != "" {
		for _, a := range attrs {
			if a.Key == h.subject {
				c.bound, c.isBound = a.Value, true

				break
			}
		}
	}

	return &c
}

// WithGroup returns a RateLimitHandler that wraps the handler of the
// wrapped handler's WithGroup, and shares the remembered events of h. The
// attributes of the returned handler's later WithAttrs calls and records
// are in the group, so they give no Subject value. An empty name returns h,
// as [slog.Handler] requires.
func (h *RateLimitHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	c := *h
	c.next = h.next.WithGroup(name)
	c.grouped = true

	return &c
}

// key returns the hash of the event of r: the length of its message, the
// message, and the kind and the value of its Subject value when it has
// one. The length keeps a message and a value from reading as another
// split of the same octets.
func (h *RateLimitHandler) key(r *slog.Record) uint64 {
	var (
		m   maphash.Hash
		buf [8]byte
	)

	m.SetSeed(h.seed)

	binary.LittleEndian.PutUint64(buf[:], uint64(len(r.Message)))
	_, _ = m.Write(buf[:])          // maphash.Hash.Write returns no error
	_, _ = m.WriteString(r.Message) // maphash.Hash.WriteString returns no error

	v, ok := h.bound, h.isBound
	if !ok && !h.grouped && h.subject != "" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == h.subject {
				v, ok = a.Value, true
			}

			return !ok
		})
	}

	if !ok {
		return m.Sum64()
	}

	v = v.Resolve()
	_ = m.WriteByte(byte(v.Kind())) //nolint:gosec // G115: a slog.Kind is below 9, and WriteByte returns no error

	switch v.Kind() {
	case slog.KindInt64:
		binary.LittleEndian.PutUint64(buf[:], uint64(v.Int64())) //nolint:gosec // G115: the hash takes the bits
		_, _ = m.Write(buf[:])
	case slog.KindUint64:
		binary.LittleEndian.PutUint64(buf[:], v.Uint64())
		_, _ = m.Write(buf[:])
	default:
		_, _ = m.WriteString(v.String())
	}

	return m.Sum64()
}
