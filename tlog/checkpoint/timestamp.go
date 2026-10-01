// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
)

// timestampSize is the length of the timestamp that starts the value of
// a timestamped signature: a big-endian uint64.
const timestampSize = 8

// unixEpoch is the earliest time that a cosigner writes.
var unixEpoch = time.Unix(0, 0)

// Timestamp returns the time of the value of a timestamped signature: the
// big-endian uint64 in its first 8 bytes, as seconds since the Unix epoch,
// in UTC. It returns the zero Time for a timestamp of 0, which states no
// time. Read the timestamp only of a signature that verified, because the
// signature covers the timestamp.
//
// Returns [ErrTimestamp], classified [errs.Invalid], for a value shorter
// than 8 bytes, and for a timestamp above 2^63 − 1, which
// tlog-cosignature forbids.
//
// # Allocation contract
//
// Zero-alloc, apart from the error of a value that it refuses.
func Timestamp(value []byte) (time.Time, error) {
	t, _, ok := splitValue(value)
	if !ok {
		return time.Time{}, fmt.Errorf("%w: a value of %d bytes, or a timestamp above 2^63 − 1",
			ErrTimestamp, len(value))
	}

	if t == 0 {
		return time.Time{}, nil
	}

	return time.Unix(int64(t), 0).UTC(), nil //nolint:gosec // splitValue refuses a timestamp above 2^63 − 1
}

// splitValue splits the value of a timestamped signature into its
// timestamp and its signature. It reports false for a value shorter than
// 8 bytes, and for a timestamp above 2^63 − 1.
func splitValue(value []byte) (uint64, []byte, bool) {
	if len(value) < timestampSize {
		return 0, nil, false
	}

	t := binary.BigEndian.Uint64(value)
	if t > math.MaxInt64 {
		return 0, nil, false
	}

	return t, value[timestampSize:], true
}

// readTimestamp returns the timestamp of a cosignature: 0 for a nil utc,
// and otherwise the whole seconds since the Unix epoch of a reading of
// utc whose error bound is within maxError.
//
// Returns an error that wraps [ErrClock], classified [errs.Transient],
// when utc returns an error, when the reading is not within maxError, and
// when the reading is before the Unix epoch.
func readTimestamp(utc clock.UTCSource, maxError time.Duration) (uint64, error) {
	if utc == nil {
		return 0, nil
	}

	r, err := utc.ReadUTC()
	if err != nil {
		return 0, errs.WithClass(fmt.Errorf("%w: read the UTC source: %w", ErrClock, err), errs.Transient)
	}

	if !r.Within(maxError) {
		return 0, fmt.Errorf("%w: a reading with Synced %t and MaxError %v, for the bound %v",
			ErrClock, r.Synced, r.MaxError, maxError)
	}

	if r.Time.Before(unixEpoch) {
		return 0, fmt.Errorf("%w: a reading at %v, before the Unix epoch", ErrClock, r.Time)
	}

	return uint64(r.Time.Unix()), nil //nolint:gosec // a time at or after the Unix epoch has no negative seconds
}
