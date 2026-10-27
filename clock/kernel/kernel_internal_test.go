// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// errBoom is the error of a kernel call that a test fails.
var errBoom = errors.New("kernel: adjtimex failed")

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

// mustRead returns the MaxError and the Synced of a reading of s, and stops
// the test when ReadUTC fails.
func mustRead(t *testing.T, s *Source) (maxError time.Duration, synced bool) {
	t.Helper()

	r, err := s.ReadUTC()
	assert.NoError(t, err, "ReadUTC succeeds")

	return r.MaxError, r.Synced
}

func TestSource(t *testing.T) {
	t.Parallel()

	t.Run("ReadUTC", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the reading of the kernel at the time of the call", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
			s := newSource(time.Minute, k.read, k.time)

			r, err := s.ReadUTC()
			assert.NoError(t, err, "ReadUTC succeeds")
			expect.Equal(t, r.Time, origin, "the reading has the current time")
			expect.Equal(t, r.MaxError, time.Millisecond, "the reading has the MaxError of the kernel at the call")
			expect.True(t, r.Synced, "the reading has the Synced of the kernel")
		})

		t.Run("adds 500 µs to MaxError for every second since the call", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
			s := newSource(time.Minute, k.read, k.time)

			k.advance(2 * time.Second)
			maxError, _ := mustRead(t, s)
			assert.Equal(t, maxError, 2*time.Millisecond, "two seconds add a millisecond")

			k.advance(time.Millisecond)
			maxError, _ = mustRead(t, s)
			expect.Equal(t, maxError, 2*time.Millisecond+500*time.Nanosecond, "a millisecond adds 500 nanoseconds")
			expect.Equal(t, k.calls.Load(), int32(1), "the reads within the refresh interval do not call the kernel")
		})

		t.Run("calls the kernel again once the refresh interval has passed", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: time.Millisecond, synced: true})
			s := newSource(time.Minute, k.read, k.time)
			k.set(status{maxError: 100 * time.Microsecond, synced: true}, nil)

			k.advance(time.Minute - time.Nanosecond)
			_, _ = mustRead(t, s)
			assert.Equal(t, k.calls.Load(), int32(1), "a read before the end of the interval does not call the kernel")

			k.advance(time.Nanosecond)
			maxError, _ := mustRead(t, s)
			expect.Equal(t, k.calls.Load(), int32(2), "a read at the end of the interval calls the kernel")
			expect.Equal(t, maxError, 100*time.Microsecond, "the MaxError of the new call replaces the old one")
		})

		t.Run("returns an unsynchronised reading of an unsynchronised kernel", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: time.Millisecond})
			_, synced := mustRead(t, newSource(time.Minute, k.read, k.time))
			assert.False(t, synced, "an unsynchronised kernel gives an unsynchronised reading")
		})

		t.Run("keeps a reading at the 16 s phase limit synchronised", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: 15*time.Second + 900*time.Millisecond, synced: true})
			s := newSource(time.Hour, k.read, k.time)

			k.advance(200 * time.Second)
			maxError, synced := mustRead(t, s)
			expect.Equal(t, maxError, 16*time.Second, "200 s add 100 ms")
			expect.True(t, synced, "a reading at the phase limit stays synchronised, as in the kernel")
		})

		t.Run("returns an unsynchronised reading past the 16 s phase limit", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{maxError: 15*time.Second + 900*time.Millisecond, synced: true})
			s := newSource(time.Hour, k.read, k.time)

			k.advance(202 * time.Second)
			maxError, synced := mustRead(t, s)
			expect.Equal(t, maxError, 16*time.Second, "MaxError stops at the phase limit")
			expect.False(t, synced, "a reading past the phase limit is unsynchronised")
		})

		t.Run("returns the error of the kernel call until a later call succeeds", func(t *testing.T) {
			t.Parallel()
			k := newFakeKernel(status{})
			k.set(status{}, errBoom)
			s := newSource(time.Minute, k.read, k.time)

			_, err := s.ReadUTC()
			assert.ErrorIs(t, err, errBoom, "a failed call fails the read")

			k.set(status{maxError: time.Millisecond, synced: true}, nil)
			k.advance(time.Minute)
			maxError, synced := mustRead(t, s)
			expect.Equal(t, maxError, time.Millisecond, "a later call that succeeds restores the MaxError")
			expect.True(t, synced, "a later call that succeeds restores the Synced")
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
			for range readers {
				wg.Go(func() {
					_, _ = s.ReadUTC()
					returned.Add(1)
				})
			}

			assert.EventuallyTrue(t, time.Second, func() bool { return returned.Load() == readers-1 },
				"every reader but the caller of the kernel returns")
			assert.Equal(t, entries.Load(), int32(1), "one reader calls the kernel")
			close(release)
			wg.Wait()
			assert.Equal(t, k.calls.Load(), int32(2), "fifty stale reads call the kernel once")
		})
	})
}
