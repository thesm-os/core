// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"
)

var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeKernel is a kernel call that returns a set status or error and
// counts its calls, over a time that the test moves.
type fakeKernel struct {
	err   error
	now   time.Time
	st    status
	mu    sync.Mutex
	calls atomic.Int32
}

func newFakeKernel(st status) *fakeKernel {
	return &fakeKernel{now: origin, st: st}
}

func (k *fakeKernel) read() (status, error) {
	k.calls.Add(1)

	k.mu.Lock()
	defer k.mu.Unlock()

	return k.st, k.err
}

func (k *fakeKernel) time() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.now
}

func (k *fakeKernel) advance(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.now = k.now.Add(d)
}

func (k *fakeKernel) set(st status, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.st, k.err = st, err
}

func mustRead(t *testing.T, s *Source) (maxError time.Duration, synced bool) {
	t.Helper()

	r, err := s.ReadUTC()
	testkit.NoError(t, err, "ReadUTC must succeed")

	return r.MaxError, r.Synced
}

func TestSource(t *testing.T) {
	t.Parallel()

	t.Run("reports the kernel's reading at the time of the call", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
		s := newSource(time.Minute, k.read, k.time)

		r, err := s.ReadUTC()
		testkit.NoError(t, err, "ReadUTC must succeed")
		testkit.Equal(t, r.Time, origin, "Time must be the current time")
		testkit.Equal(t, r.MaxError, time.Millisecond, "MaxError must be the kernel's at the call")
		testkit.True(t, r.Synced, "Synced must be the kernel's")
	})

	t.Run("adds 500 µs to MaxError for every second since the call", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
		s := newSource(time.Minute, k.read, k.time)

		k.advance(2 * time.Second)
		maxError, _ := mustRead(t, s)
		testkit.Equal(t, maxError, 2*time.Millisecond, "two seconds must add one millisecond")

		k.advance(time.Millisecond)
		maxError, _ = mustRead(t, s)
		testkit.Equal(t, maxError, 2*time.Millisecond+500*time.Nanosecond,
			"a millisecond must add 500 nanoseconds")
		testkit.Equal(t, k.calls.Load(), int32(1), "reads within the refresh interval must not call the kernel")
	})

	t.Run("calls the kernel again once the refresh interval has passed", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
		s := newSource(time.Minute, k.read, k.time)
		k.set(status{maxError: 100 * time.Microsecond, synced: true}, nil)

		k.advance(time.Minute - time.Nanosecond)
		_, _ = mustRead(t, s)
		testkit.Equal(t, k.calls.Load(), int32(1), "a read before the interval ends must not call the kernel")

		k.advance(time.Nanosecond)
		maxError, _ := mustRead(t, s)
		testkit.Equal(t, k.calls.Load(), int32(2), "a read at the end of the interval must call the kernel")
		testkit.Equal(t, maxError, 100*time.Microsecond, "the new call's maximum error must replace the old one")
	})

	t.Run("reports an unsynchronised clock as the kernel does", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: time.Millisecond})
		_, synced := mustRead(t, newSource(time.Minute, k.read, k.time))
		testkit.False(t, synced, "an unsynchronised kernel must give an unsynchronised reading")
	})

	t.Run("keeps a reading at the 16 s phase limit synchronised", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: 15*time.Second + 900*time.Millisecond, synced: true})
		s := newSource(time.Hour, k.read, k.time)

		k.advance(200 * time.Second)
		maxError, synced := mustRead(t, s)
		testkit.Equal(t, maxError, 16*time.Second, "200 s must add 100 ms")
		testkit.True(t, synced, "a reading at the phase limit must stay synchronised, as in the kernel")
	})

	t.Run("marks the reading unsynchronised past the 16 s phase limit", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: 15*time.Second + 900*time.Millisecond, synced: true})
		s := newSource(time.Hour, k.read, k.time)

		k.advance(202 * time.Second)
		maxError, synced := mustRead(t, s)
		testkit.Equal(t, maxError, 16*time.Second, "MaxError must stop at the phase limit")
		testkit.False(t, synced, "a reading past the phase limit must be unsynchronised")
	})

	t.Run("returns the kernel call's error until a later call succeeds", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{})
		k.set(status{}, errBoom)
		s := newSource(time.Minute, k.read, k.time)

		_, err := s.ReadUTC()
		testkit.ErrorIs(t, err, errBoom, "a failed call must fail the read")

		k.set(status{maxError: time.Millisecond, synced: true}, nil)
		k.advance(time.Minute)
		maxError, synced := mustRead(t, s)
		testkit.Equal(t, maxError, time.Millisecond, "a later successful call must restore readings")
		testkit.True(t, synced, "a later successful call must restore readings")
	})

	t.Run("lets one reader call the kernel while the others use the last result", func(t *testing.T) {
		t.Parallel()
		k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
		s := newSource(time.Minute, k.read, k.time)

		var entries, returned atomic.Int32
		release := make(chan struct{})
		s.read = func() (status, error) {
			if entries.Add(1) == 1 {
				<-release
			}

			return k.read()
		}
		k.advance(time.Minute)

		const readers = 50
		var wg sync.WaitGroup
		wg.Add(readers)
		for range readers {
			go func() {
				defer wg.Done()
				_, _ = s.ReadUTC()
				returned.Add(1)
			}()
		}

		deadline := time.Now().Add(time.Second)
		for returned.Load() < readers-1 && time.Now().Before(deadline) {
			runtime.Gosched()
		}

		testkit.Equal(t, returned.Load(), int32(readers-1), "every reader but the caller of the kernel must return")
		testkit.Equal(t, entries.Load(), int32(1), "only one reader may call the kernel")
		close(release)
		wg.Wait()
		testkit.Equal(t, k.calls.Load(), int32(2), "fifty stale reads must call the kernel once")
	})
}

var errBoom = testkit.TestError("adjtimex failed")
