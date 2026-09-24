// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"fmt"
	"syscall"
	"time"
)

// The kernel's status bit and clock state for an unsynchronised clock,
// from include/uapi/linux/timex.h.
const (
	staUnsync = 0x0040
	timeError = 5
)

// readKernel calls adjtimex(2) without modifying the clock.
func readKernel() (status, error) {
	return readWith(syscall.Adjtimex)
}

// readWith is readKernel with the system call passed in, so a test can
// exercise a failed call.
func readWith(adjtimex func(*syscall.Timex) (int, error)) (status, error) {
	var tx syscall.Timex

	state, err := adjtimex(&tx)
	if err != nil {
		return status{}, fmt.Errorf("kernel: adjtimex: %w", err)
	}

	return fromTimex(state, &tx), nil
}

// fromTimex returns the status in one adjtimex result. Maxerror is in
// microseconds.
func fromTimex(state int, tx *syscall.Timex) status {
	return status{
		maxError: time.Duration(int64(tx.Maxerror)) * time.Microsecond,
		synced:   state != timeError && tx.Status&staUnsync == 0,
	}
}
