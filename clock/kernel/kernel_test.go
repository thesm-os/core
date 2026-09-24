// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kernel_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/kernel"
	"go.thesmos.sh/core/errs"
)

func TestNew(t *testing.T) {
	t.Parallel()

	for _, refresh := range []time.Duration{0, -time.Second} {
		t.Run("refuses a refresh of "+refresh.String(), func(t *testing.T) {
			t.Parallel()
			s, err := kernel.New(refresh)
			testkit.ErrorIs(t, err, kernel.ErrRefresh, "a refresh that is not positive must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrRefresh must classify as Invalid")
			testkit.True(t, s == nil, "a refused call must return no Source")
		})
	}
}

// TestZeroAlloc enforces the allocation contract of ReadUTC between
// kernel calls. testing.AllocsPerRun reads a process-global malloc
// counter, so this test does not call t.Parallel. The refresh is an
// hour, so no read in the test calls the kernel.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	s, err := kernel.New(time.Hour)
	testkit.NoError(t, err, "New must accept a positive refresh")

	t.Run("ReadUTC", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() { _, _ = s.ReadUTC() })
		testkit.Equal(t, allocs, float64(0), "ReadUTC must not allocate between kernel calls")
	})
}

func BenchmarkReadUTC(b *testing.B) {
	s, err := kernel.New(100 * time.Millisecond)
	testkit.NoError(b, err, "New must accept a positive refresh")
	b.ReportAllocs()

	for b.Loop() {
		_, _ = s.ReadUTC()
	}
}
