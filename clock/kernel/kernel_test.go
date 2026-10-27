// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package kernel_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/kernel"
	"go.thesmos.sh/core/errs"
)

func TestNew(t *testing.T) {
	t.Parallel()

	for _, refresh := range []time.Duration{0, -time.Second} {
		t.Run("returns ErrRefresh for a refresh of "+refresh.String(), func(t *testing.T) {
			t.Parallel()
			s, err := kernel.New(refresh)
			assert.ErrorIs(t, err, kernel.ErrRefresh, "New refuses a refresh that is not positive")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrRefresh classifies as Invalid")
			expect.Nil(t, s, "a refused call returns no Source")
		})
	}
}

// TestSourceAllocs checks that ReadUTC does not allocate between kernel calls.
// The refresh is an hour, so no read of the test calls the kernel. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestSourceAllocs(t *testing.T) {
	s, err := kernel.New(time.Hour)
	assert.NoError(t, err, "New accepts a positive refresh")

	t.Run("ReadUTC", func(t *testing.T) {
		expect.MaxAllocs(t, func() { _, _ = s.ReadUTC() }, 0, "ReadUTC does not allocate between kernel calls")
	})
}

func BenchmarkReadUTC(b *testing.B) {
	s, err := kernel.New(100 * time.Millisecond)
	assert.NoError(b, err, "New accepts a positive refresh")
	b.ReportAllocs()

	for b.Loop() {
		_, _ = s.ReadUTC()
	}
}
