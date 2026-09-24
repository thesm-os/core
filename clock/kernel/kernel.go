// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// maxDrift is the rate at which the kernel grows its maximum error:
// 500 µs per second, its MAXFREQ of 500,000 ns/s.
const maxDrift = 500 * time.Microsecond

// phaseLimit is the maximum error past which the kernel caps the error
// and sets STA_UNSYNC: its NTP_PHASE_LIMIT of 16 s.
const phaseLimit = 16 * time.Second

// status is the part of one kernel call that a reading uses.
type status struct {
	maxError time.Duration
	synced   bool
}

// snapshot is the result of one kernel call and the time of the call.
type snapshot struct {
	at       time.Time
	err      error
	maxError time.Duration
	synced   bool
}

// Source is a [clock.UTCSource] over the kernel's clock. It calls the
// kernel at most once per refresh interval, and a read between calls
// costs [time.Now] plus an atomic load.
//
// # Concurrency
//
// Safe for concurrent use. When the last call is older than the
// refresh interval, one reader calls the kernel and publishes the
// result. Other readers use the previous result meanwhile, so at most
// one caller waits on the kernel at a time.
type Source struct {
	// read calls the kernel. Tests replace it.
	read func() (status, error)

	// now reads the time. Tests replace it.
	now func() time.Time

	snap    atomic.Pointer[snapshot]
	refresh time.Duration

	// busy reports whether a reader is calling the kernel.
	busy atomic.Bool
}

// Compile-time interface check.
var _ clock.UTCSource = (*Source)(nil)

// New returns a Source that calls the kernel's adjtimex(2) at most once
// per refresh, and makes the first call before it returns.
//
// Returns [ErrRefresh] for a refresh that is not positive. A failed
// kernel call does not fail New: [Source.ReadUTC] returns its error
// until a later call succeeds.
func New(refresh time.Duration) (*Source, error) {
	if refresh <= 0 {
		return nil, ErrRefresh
	}

	return newSource(refresh, readKernel, time.Now), nil
}

// newSource returns a Source over read and now, after one call to read.
func newSource(refresh time.Duration, read func() (status, error), now func() time.Time) *Source {
	s := &Source{read: read, now: now, refresh: refresh}
	s.snap.Store(s.call())

	return s
}

// call calls the kernel and returns the result. It reads the time
// before the call, so the growth computed from it errs on the large
// side.
func (s *Source) call() *snapshot {
	at := s.now()
	st, err := s.read()

	return &snapshot{at: at, err: err, maxError: st.maxError, synced: st.synced}
}

// ReadUTC returns a reading of the kernel's clock.
//
// Time is the current time. MaxError is the kernel's maximum error at
// the last call, plus 500 µs for every second since that call on the
// monotonic clock, which is the rate at which the kernel itself grows
// the error. Synced is false when the last call reported STA_UNSYNC or
// TIME_ERROR, and when MaxError passes the kernel's 16 s phase limit,
// where the kernel also marks the clock unsynchronised.
//
// A change the synchronisation daemon makes becomes visible within one
// refresh interval.
//
// Returns the error of the last kernel call when it failed. On systems
// other than Linux every call fails with an error wrapping
// [errors.ErrUnsupported], which classifies as
// [go.thesmos.sh/core/errs.Unsupported].
//
// # Allocation contract
//
// Zero-alloc. A reader that calls the kernel allocates one snapshot.
func (s *Source) ReadUTC() (clock.UTCReading, error) {
	now := s.now()
	snap := s.snap.Load()

	if now.Sub(snap.at) >= s.refresh && s.busy.CompareAndSwap(false, true) {
		snap = s.call()
		s.snap.Store(snap)
		s.busy.Store(false)
	}

	if snap.err != nil {
		return clock.UTCReading{}, snap.err
	}

	maxError := snap.maxError + max(now.Sub(snap.at), 0)/(time.Second/maxDrift)
	synced := snap.synced

	if maxError > phaseLimit {
		maxError, synced = phaseLimit, false
	}

	return clock.UTCReading{Time: now, MaxError: maxError, Synced: synced}, nil
}
