// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"syscall"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

func TestFromTimex(t *testing.T) {
	t.Parallel()

	t.Run("converts maxerror from microseconds", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(0, &syscall.Timex{Maxerror: 1500})
		expect.Equal(t, st.maxError, 1500*time.Microsecond, "fromTimex reads Maxerror as microseconds")
		expect.True(t, st.synced, "TIME_OK without STA_UNSYNC is synchronised")
	})

	t.Run("reports STA_UNSYNC as unsynchronised", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(0, &syscall.Timex{Status: staUnsync})
		assert.False(t, st.synced, "STA_UNSYNC is unsynchronised")
	})

	t.Run("reports TIME_ERROR as unsynchronised", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(timeError, &syscall.Timex{})
		assert.False(t, st.synced, "TIME_ERROR is unsynchronised")
	})
}

func TestReadKernel(t *testing.T) {
	t.Parallel()

	t.Run("returns the status that adjtimex reports", func(t *testing.T) {
		t.Parallel()
		var tx syscall.Timex
		state, err := syscall.Adjtimex(&tx)
		assert.NoError(t, err, "adjtimex succeeds on Linux")
		before := fromTimex(state, &tx)

		got, err := readKernel()
		assert.NoError(t, err, "readKernel succeeds on Linux")

		state, err = syscall.Adjtimex(&tx)
		assert.NoError(t, err, "adjtimex succeeds on Linux")
		after := fromTimex(state, &tx)

		assert.InRange(t, got.maxError, float64(before.maxError), float64(after.maxError),
			"readKernel returns the maximum error that adjtimex reports around the call")
	})
}

func TestReadWith(t *testing.T) {
	t.Parallel()

	t.Run("returns the status of a call that succeeds", func(t *testing.T) {
		t.Parallel()
		st, err := readWith(func(tx *syscall.Timex) (int, error) {
			tx.Maxerror = 1500

			return 0, nil
		})
		assert.NoError(t, err, "readWith returns no error for a call that succeeds")
		assert.Equal(t, st, status{maxError: 1500 * time.Microsecond, synced: true},
			"readWith returns the status of the Timex of the call")
	})

	t.Run("wraps the error of a failed call", func(t *testing.T) {
		t.Parallel()
		_, err := readWith(func(*syscall.Timex) (int, error) { return 0, syscall.EPERM })
		assert.ErrorIs(t, err, syscall.EPERM, "readWith returns the error of the system call")
		assert.HasPrefix(t, err.Error(), "kernel: adjtimex: ", "the error names the call")
	})

	t.Run("reads the running kernel", func(t *testing.T) {
		t.Parallel()
		s, err := New(time.Second)
		assert.NoError(t, err, "New accepts a positive refresh")

		r, err := s.ReadUTC()
		assert.NoError(t, err, "the read of the kernel succeeds on Linux")
		expect.InRange(t, time.Since(r.Time), float64(-time.Second), float64(time.Second),
			"the reading is within a second of the current time")
		expect.InRange(t, r.MaxError, 0, float64(16*time.Second),
			"the MaxError of the reading is between 0 and the phase limit of 16 s")
	})
}
