// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"go.thesmos.sh/testkit"
)

func TestFromTimex(t *testing.T) {
	t.Parallel()

	t.Run("converts maxerror from microseconds", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(0, &syscall.Timex{Maxerror: 1500})
		testkit.Equal(t, st.maxError, 1500*time.Microsecond, "Maxerror must be read as microseconds")
		testkit.True(t, st.synced, "TIME_OK without STA_UNSYNC must be synchronised")
	})

	t.Run("reports STA_UNSYNC as unsynchronised", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(0, &syscall.Timex{Status: staUnsync})
		testkit.False(t, st.synced, "STA_UNSYNC must be unsynchronised")
	})

	t.Run("reports TIME_ERROR as unsynchronised", func(t *testing.T) {
		t.Parallel()
		st := fromTimex(timeError, &syscall.Timex{})
		testkit.False(t, st.synced, "TIME_ERROR must be unsynchronised")
	})
}

func TestReadWith(t *testing.T) {
	t.Parallel()

	t.Run("wraps the error of a failed call", func(t *testing.T) {
		t.Parallel()
		_, err := readWith(func(*syscall.Timex) (int, error) { return 0, syscall.EPERM })
		testkit.ErrorIs(t, err, syscall.EPERM, "the system call's error must be returned")
		testkit.True(t, strings.HasPrefix(err.Error(), "kernel: adjtimex: "), "the error must name the call")
	})

	t.Run("reads the running kernel", func(t *testing.T) {
		t.Parallel()
		s, err := New(time.Second)
		testkit.NoError(t, err, "New must accept a positive refresh")

		r, err := s.ReadUTC()
		testkit.NoError(t, err, "reading the kernel must succeed on Linux")
		testkit.True(t, time.Since(r.Time).Abs() < time.Second, "Time must be the current time")
		testkit.True(t, r.MaxError >= 0, "MaxError must not be negative")
	})
}
