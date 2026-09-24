// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// UTCReading is one reading of UTC with a bound on its error.
//
// # Allocation contract
//
// Value type. Its methods do not allocate.
type UTCReading struct {
	// Time is the reading.
	Time time.Time

	// MaxError bounds the distance between Time and UTC at the moment
	// of the reading: UTC lies within [Time-MaxError, Time+MaxError].
	// It is a bound only when Synced is true.
	MaxError time.Duration

	// Synced reports whether the source considers the clock
	// synchronised to UTC.
	Synced bool
}

// Within reports whether the reading is synchronised and its error
// bound is at most limit.
func (r UTCReading) Within(limit time.Duration) bool {
	return r.Synced && r.MaxError <= limit
}

// Earliest returns Time minus MaxError, the earliest UTC the reading
// allows.
func (r UTCReading) Earliest() time.Time {
	return r.Time.Add(-r.MaxError)
}

// Latest returns Time plus MaxError, the latest UTC the reading
// allows.
func (r UTCReading) Latest() time.Time {
	return r.Time.Add(r.MaxError)
}

// UTCSource reads UTC with a bound on its error. It is separate from
// [Clock], so a caller that needs the bound asks for a UTCSource and
// every Clock keeps its contract. [go.thesmos.sh/core/clock/kernel.Source]
// reads the bound from the Linux kernel, and
// [go.thesmos.sh/core/clock/fake.Clock] reports one a test sets.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type UTCSource interface {
	// ReadUTC returns a reading. An error means the source could not
	// be read at all. An unsynchronised clock is a reading with Synced
	// false and a nil error.
	//
	//testkit:nondeterministic
	ReadUTC() (UTCReading, error)
}
