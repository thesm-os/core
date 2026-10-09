// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"context"
	"time"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/telemetry"
)

// The duration instruments of the semantic conventions for HTTP.
const (
	// ServerDuration is the duration of the requests that a server serves.
	ServerDuration telemetry.InstrumentName = "http.server.request.duration"

	// ClientDuration is the duration of the requests that a client sends.
	ClientDuration telemetry.InstrumentName = "http.client.request.duration"
)

// seconds is the UCUM unit of a duration instrument.
const seconds = "s"

// maxSeries is the number of attribute sets that a [Durations] keeps
// bound at once. It is the default cardinality limit of an instrument in
// the OpenTelemetry SDK, 2,000, rounded up to a power of two.
const maxSeries = 2048

// Durations records durations in seconds into one histogram instrument,
// bound once per attribute set. A key K identifies an attribute set, and
// the function that [NewDurations] receives builds its attributes.
//
// A [cache.Cache] keeps at most 2,048 bound sets, and evicts with S3-FIFO.
// Durations releases a set that the cache evicts, so that an adapter can
// forget it, and binds it again at the next record of its key. A set is
// pinned while a record uses it, so a concurrent eviction cannot release
// it during the record.
//
// # Concurrency
//
// Safe for concurrent use. A record of a bound set takes no lock.
// Goroutines that record a new set at once may each bind it. The cache
// keeps the last binding and releases the others, as a Set of a key that
// it contains releases the value that it replaces. The set receives every
// record, because releasing one bound instrument does not end another
// instrument of the same set.
//
// # Allocation contract
//
// [Durations.Record] does not allocate for a bound set, apart from what
// the histogram allocates. The binding of a new set allocates its
// attributes, the bound histogram that With returns, and the entry of the
// cache.
type Durations[K comparable] struct {
	histogram telemetry.Histogram
	attrs     func(K) []telemetry.Attr
	bound     *cache.Cache[K, telemetry.Histogram]
}

// NewDurations returns a Durations over the histogram name of r, with
// description and the buckets that the semantic conventions advise for
// an HTTP duration, from 5 ms to 10 s. attrs returns the attributes of the
// set of a key. The cache of the bound sets reads c, which no entry uses,
// because a bound set does not expire.
//
// NewDurations returns no error: it constructs the cache with a clock and
// a positive capacity, which [cache.New] accepts. The caller passes a
// non-nil r, c and attrs.
func NewDurations[K comparable](
	r telemetry.Reporter, name telemetry.InstrumentName, description string,
	c clock.Clock, attrs func(K) []telemetry.Attr,
) *Durations[K] {
	d := &Durations[K]{
		histogram: r.Histogram(telemetry.InstrumentSpec{
			Name:        name,
			Description: description,
			Unit:        seconds,
			Bounds:      []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10},
		}),
		attrs: attrs,
	}

	// cache.New refuses only a nil Clock and a Capacity below 1. The cache
	// releases the bound histogram of every set that leaves it.
	d.bound, _ = cache.New(cache.Config[K, telemetry.Histogram]{
		Clock:    c,
		Capacity: maxSeries,
		Evicted:  func(_ K, h telemetry.Histogram) { h.Release() },
	})

	return d
}

// Record records elapsed, in seconds, into the set of k. It records into
// the bound set of k, pinned for the record, when the cache contains one.
// Otherwise it binds the set, records into it, and then adds it to the
// cache.
func (d *Durations[K]) Record(ctx context.Context, k K, elapsed time.Duration) {
	if p, ok := d.bound.Pin(k); ok {
		p.Value().Record(ctx, elapsed.Seconds())
		p.Unpin()

		return
	}

	h := d.histogram.With(d.attrs(k))
	h.Record(ctx, elapsed.Seconds())
	d.bound.Set(k, h, time.Time{})
}
