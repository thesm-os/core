// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package kernel reads UTC and a bound on its error from the Linux
// kernel's time discipline, as a [clock.UTCSource].
//
// The kernel keeps a maximum error in microseconds and a flag that
// reports whether the clock is unsynchronised, and adjtimex(2) returns
// both. The kernel adds 500 µs to the maximum error every second, and
// marks the clock unsynchronised once the error passes 16 s. The
// synchronisation daemon resets the error each time it adjusts the
// clock.
//
// # Refresh
//
// A [Source] calls adjtimex(2) at most once per refresh interval, which
// costs about 550 ns. Between calls it adds the kernel's own 500 µs
// per second to the last maximum error, so a read costs [time.Now] plus
// an atomic load. A change the daemon makes becomes visible within one
// refresh interval.
//
// # The daemon behind the bound
//
// The kernel reports what the synchronisation daemon told it:
//
//   - chrony sets the maximum error to half the root delay plus the
//     root dispersion, a bound that includes the error of its time
//     sources.
//   - systemd-timesyncd sets it to zero at each adjustment, so the
//     kernel's bound excludes the error of the time source.
//
// A Source cannot tell which daemon runs. A deployment that relies on
// the bound runs chrony or ntpd and checks that it does.
//
// # Platforms
//
// On systems other than Linux, [Source.ReadUTC] returns an error
// wrapping [errors.ErrUnsupported].
//
// # Dependency position
//
// Imports errors, fmt, sync/atomic, syscall and time from the standard
// library, and go.thesmos.sh/core/clock and go.thesmos.sh/core/errs from
// this module. It calls one system call and opens no file or socket.
package kernel
