// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/tlog"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
	"go.thesmos.sh/core/version"
)

// maxHeadWrites bounds the writes of the head that a test of two servers
// blocks.
const maxHeadWrites = 16

// patience bounds each wait of a case: a call into a Server, a value that
// a hook sends, and the goroutines that a case starts.
const patience = 5 * time.Second

// flaky is a cosigner whose signatures fail while fail is set, and that
// runs before, when it is not nil, before each signature.
type flaky struct {
	checkpoint.Cosigner

	before func(ctx context.Context) error
	fail   atomic.Bool
}

// brokenUTC is a UTC source whose readings fail.
type brokenUTC struct{}

func TestCommit(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		t.Run("returns ErrClock for a reading outside MaxError and creates no record", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			f.clock.SetUTCError(time.Second, true)

			_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the reading")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
			testkit.Len(t, keys(t, f.store, "records/"), 0, "the commit must create no record")
		})

		t.Run("returns ErrClock for a reading that is not synchronised", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			f.clock.SetUTCError(0, false)

			_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the reading")
		})

		t.Run("returns ErrClock for a UTC source whose reading fails", func(t *testing.T) {
			t.Parallel()
			cfg := newFixture(t, l).config()
			cfg.UTC = brokenUTC{}

			_, _, err := newServer(t, cfg).Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the reading")
		})

		t.Run("returns ErrClock for a reading at the Unix epoch", func(t *testing.T) {
			t.Parallel()
			cfg := newFixture(t, l).config()
			cfg.UTC = fake.New(time.Unix(0, 0))

			_, _, err := newServer(t, cfg).Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the reading")
		})

		t.Run("returns ErrClock for a head ahead of the reading by more than twice MaxError", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			ahead := f.config()
			ahead.UTC = fake.New(clockTime.Add(10 * time.Second))
			advance(t, newServer(t, ahead), l, l.update(t, 0, 5))

			_, _, err := newServer(
				t,
				f.config(),
			).Advance(bounded(t), l.notes[6], []witness.Update{l.update(t, 5, 6)}, nil)
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the head's time")
			testkit.Len(t, keys(t, f.store, "records/"), 1, "the commit must create no record")
		})

		t.Run("commits after a head ahead of the reading by exactly twice MaxError", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			ahead := f.config()
			ahead.UTC = fake.New(clockTime.Add(2 * time.Second))
			advance(t, newServer(t, ahead), l, l.update(t, 0, 5))

			cfg := f.config()
			cfg.MaxError = time.Second
			testkit.NotEqual(t, len(advance(t, newServer(t, cfg), l, l.update(t, 5, 6))), 0,
				"Advance must commit after the head")
		})

		t.Run("commits readings a millisecond apart around a second boundary under a MaxError of 1 ms",
			func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, l)
				late := f.config()
				late.MaxError = time.Millisecond
				late.UTC = fake.New(clockTime.Add(time.Second + 500*time.Microsecond))
				first := advance(t, newServer(t, late), l, l.update(t, 0, 5))

				early := f.config()
				early.MaxError = time.Millisecond
				early.UTC = fake.New(clockTime.Add(time.Second - 500*time.Microsecond))
				second := advance(t, newServer(t, early), l, l.update(t, 5, 6))

				testkit.Equal(t, lineTime(t, l, 6, second), lineTime(t, l, 5, first),
					"the later commit must take the time of the head")
			})

		t.Run("gives the cosignatures of a commit its time, which never decreases along the chain", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			f.cosigners = append(f.cosigners, mldsaCosigner(t, "example.com/pq", nil))

			behind := f.config()
			behind.UTC = fake.New(clockTime.Add(-5 * time.Millisecond))
			a, b := newServer(t, f.config()), newServer(t, behind)

			last := time.Time{}

			for size := uint64(1); size <= 6; size++ {
				s := a
				if size%2 == 0 {
					s = b
				}

				lines := advance(t, s, l, l.update(t, size-1, size))
				text := append([]byte(nil), noteText(t, l.notes[size])...)
				n := mustParse(t, append(append(text, '\n'), lines...))
				testkit.Len(t, n.Signatures, 2, "Advance must return a line of each cosigner")

				for _, sig := range n.Signatures {
					ts, err := checkpoint.Timestamp(sig.Value)
					testkit.NoError(t, err, "the line must have a timestamp")
					testkit.False(t, ts.IsZero(), "no line may have the timestamp 0")
					testkit.False(t, ts.Before(last), "the times must never decrease")
					testkit.Equal(t, ts, lineTime(t, l, size, lines), "every line must have the time of the commit")
				}

				last = lineTime(t, l, size, lines)
			}
		})

		t.Run("returns ErrOpen while the circuit of a cosigner is open and creates no record", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0]}
			c.fail.Store(true)
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())

			for size := uint64(1); size <= 2; size++ {
				resend(t, s, l, size)
			}

			records := len(keys(t, f.store, "records/"))
			_, _, err := s.Advance(bounded(t), l.notes[3], []witness.Update{l.update(t, 2, 3)}, nil)
			testkit.ErrorIs(t, err, resilience.ErrOpen, "Advance must refuse the commit")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
			testkit.Len(t, keys(t, f.store, "records/"), records, "the commit must create no record")
		})

		t.Run("admits one commit after the open interval whose signatures close the circuit", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0]}
			c.fail.Store(true)
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())

			for size := uint64(1); size <= 2; size++ {
				resend(t, s, l, size)
			}

			c.fail.Store(false)
			f.clock.Advance(time.Minute)

			testkit.NotEqual(t, len(advance(t, s, l, l.update(t, 2, 3))), 0, "the probe must commit")
			testkit.Equal(
				t,
				f.breaker.State(target(f.cosigners[0])),
				resilience.Closed,
				"the probe must close the circuit",
			)
		})

		t.Run("opens the circuit again when the signatures of the probe fail", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0]}
			c.fail.Store(true)
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())

			for size := uint64(1); size <= 2; size++ {
				resend(t, s, l, size)
			}

			f.clock.Advance(time.Minute)
			resend(t, s, l, 3)
			testkit.Equal(
				t,
				f.breaker.State(target(f.cosigners[0])),
				resilience.Open,
				"the probe must open the circuit",
			)
		})

		t.Run("counts a signature that SignTimeout ends as a failure of the cosigner", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0], before: func(ctx context.Context) error {
				<-ctx.Done()

				return context.Cause(ctx)
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			cfg := f.config()
			cfg.SignTimeout = 20 * time.Millisecond
			s := newServer(t, cfg)

			for size := uint64(1); size <= 2; size++ {
				_, _, err := s.Advance(bounded(t), l.notes[size], []witness.Update{l.update(t, size-1, size)}, nil)
				testkit.ErrorIs(t, err, context.DeadlineExceeded, "the signature must end at SignTimeout")
			}

			testkit.Equal(t, f.breaker.State(target(f.cosigners[0])), resilience.Open, "the circuit must count both")
		})

		t.Run("fails a commit whose store is too slow without a failure of a circuit", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			cfg := f.config()
			cfg.Timeout, cfg.SignTimeout = 20*time.Millisecond, 20*time.Millisecond
			s := newServer(t, cfg)
			f.store.intercept(hook{op: opPut, prefix: "records/", before: func(ctx context.Context, _ string) error {
				<-ctx.Done()

				return context.Cause(ctx)
			}})

			for range 3 {
				_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				testkit.ErrorIs(t, err, context.DeadlineExceeded, "the commit must end at its deadline")
			}

			testkit.Equal(
				t,
				f.breaker.State(target(f.cosigners[0])),
				resilience.Closed,
				"no circuit may count the store",
			)
		})

		t.Run("stores the lines of a commit whose writes and signature together exceed Timeout", func(t *testing.T) {
			t.Parallel()

			// The three writes take 360 ms, above a Timeout of 300 ms, so a
			// store whose deadline were Timeout alone would end the commit
			// before its lines. The deadline of Timeout + SignTimeout leaves 840
			// ms after the writes and the signature for the stalls of a loaded
			// machine.
			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				time.Sleep(100 * time.Millisecond)

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			cfg := f.config()
			cfg.Timeout, cfg.SignTimeout = 300*time.Millisecond, time.Second
			s := newServer(t, cfg)
			f.store.intercept(hook{op: opPut, before: func(context.Context, string) error {
				time.Sleep(120 * time.Millisecond)

				return nil
			}})

			testkit.NotEqual(t, len(advance(t, s, l, l.update(t, 0, 5))), 0, "the commit must return its lines")
			settle(t, s, l)
			testkit.Len(t, keys(t, f.store, "lines/"), 1, "the commit must store its lines")
		})

		t.Run("returns ErrContention when the head moves 16 times", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			f.store.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
				return version.ErrMismatch
			}})

			_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			testkit.ErrorIs(t, err, witness.ErrContention, "Advance must give up")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
		})

		t.Run("writes the head again without a second record after an unknown outcome that did not apply",
			func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, l)
				s := newServer(t, f.config())

				var (
					once    atomic.Bool
					records atomic.Int64
				)

				f.store.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
					if once.CompareAndSwap(false, true) {
						return version.ErrOutcomeUnknown
					}

					return nil
				}})
				f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
					records.Add(1)

					return nil
				}})

				testkit.NotEqual(t, len(advance(t, s, l, l.update(t, 0, 5))), 0, "the commit must commit")
				testkit.Equal(t, records.Load(), int64(1), "the commit must create one record")
			})

		t.Run("returns ErrUnknownOrigin for the second of two new origins of a call beyond MaxOrigins",
			func(t *testing.T) {
				t.Parallel()
				batch := newTestLog(t, "example.com/batch")
				f := newFixture(t)
				cfg := f.config()
				cfg.MaxOrigins = 1

				root := batch.body(5).Root
				updates := make([]witness.Update, 2)

				for i := range updates {
					origin := checkpoint.Origin("example.com/log/" + strconv.Itoa(i))
					f.accepted[origin] = batch.log
					updates[i] = witness.Update{Body: checkpoint.Body{Origin: origin, Size: 5, Root: root}}
				}

				_, failures, err := newServer(t, cfg).Advance(bounded(t), batch.notes[5], updates, nil)
				testkit.NoError(t, err, "Advance must check the updates")
				testkit.Len(t, failures, 1, "Advance must refuse one update")
				testkit.Equal(t, failures[0].Index, 1, "the failure must name the second update")
				testkit.ErrorIs(t, failures[0].Err, witness.ErrUnknownOrigin, "the failure must be ErrUnknownOrigin")
			})

		t.Run("returns the cause of ctx to a caller whose context ends while the commit completes", func(t *testing.T) {
			t.Parallel()
			a, b := newTestLog(t, "example.com/a"), newTestLog(t, "example.com/b")
			f := newFixture(t, a, b)
			s := newServer(t, f.config())
			release := make(chan struct{})
			blocked := make(chan struct{})

			var once sync.Once

			f.store.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
				once.Do(func() {
					close(blocked)
					<-release
				})

				return nil
			}})

			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("the caller left")

			var wg sync.WaitGroup

			var errA error

			returned := make(chan struct{})

			wg.Go(func() {
				defer close(returned)

				_, _, errA = s.Advance(ctx, a.notes[5], []witness.Update{a.update(t, 0, 5)}, nil)
			})
			awaitBefore(t, blocked, returned, "the commit must write the head")
			cancel(cause)
			waitAll(t, &wg, "the caller must return")
			close(release)

			testkit.ErrorIs(t, errA, cause, "the caller must receive the cause of its context")
			settle(t, s, a)
			testkit.Equal(t, get(t, s, a.origin).Code, 200, "the commit must complete")
			testkit.NotEqual(t, len(advance(t, s, b, b.update(t, 0, 5))), 0, "the next call must commit")
		})

		t.Run("commits one of two inconsistent checkpoints of two Servers that read one head", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			a, b := newServer(t, f.config()), newServer(t, f.config())
			arrived := make(chan struct{}, maxHeadWrites)
			release := make(chan struct{})

			f.store.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
				arrived <- struct{}{}
				<-release

				return nil
			}})

			fork := l.body(5)
			fork.Root = crypto.NewDigest256(sha256.Sum256([]byte("a fork")))

			var wg sync.WaitGroup

			results := make([]error, 2)
			lines := make([][]byte, 2)
			returned := make(chan struct{}, 2)

			wg.Go(func() {
				defer func() { returned <- struct{}{} }()

				lines[0], _, results[0] = a.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			})
			wg.Go(func() {
				defer func() { returned <- struct{}{} }()

				lines[1], _, results[1] = b.Advance(bounded(t), signBody(t, fork, l.signer),
					[]witness.Update{{Body: fork}}, nil)
			})

			awaitBefore(t, arrived, returned, "the first Server must write the head")
			awaitBefore(t, arrived, returned, "the second Server must write the head")
			close(release)
			waitAll(t, &wg, "both Servers must return")

			cosigned := 0
			for i := range lines {
				if results[i] == nil && len(lines[i]) > 0 {
					cosigned++
				}
			}

			testkit.Equal(t, cosigned, 1, "exactly one of the two checkpoints must be cosigned")
		})

		t.Run("cosigns at most one of two inconsistent checkpoints of two Servers in each of 1,000 rounds",
			func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				cfg := f.config()
				cfg.MaxOrigins = 1000
				a, b := newServer(t, cfg), newServer(t, cfg)

				for i := range 1000 {
					origin := checkpoint.Origin("example.com/round/" + strconv.Itoa(i))

					f.mu.Lock()
					f.accepted[origin] = l.log
					f.mu.Unlock()

					bodies := []checkpoint.Body{
						{Origin: origin, Size: 5, Root: crypto.NewDigest256(sha256.Sum256([]byte("x" + origin)))},
						{Origin: origin, Size: 5, Root: crypto.NewDigest256(sha256.Sum256([]byte("y" + origin)))},
					}

					var (
						wg    sync.WaitGroup
						lines [2][]byte
					)

					for j, s := range []*witness.Server{a, b} {
						msg := signBody(t, bodies[j], l.signer)
						wg.Go(func() {
							lines[j], _, _ = s.Advance(bounded(t), msg, []witness.Update{{Body: bodies[j]}}, nil)
						})
					}

					waitAll(t, &wg, "both Servers must return")

					if len(lines[0]) > 0 && len(lines[1]) > 0 {
						t.Fatalf("round %d cosigned two inconsistent checkpoints", i)
					}
				}
			})

		t.Run("creates one record for the same calls of two Servers from one head in one second", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			other := &store{Store: f.store.Store}
			cfg := f.config()
			cfg.State = other
			a, b := newServer(t, f.config()), newServer(t, cfg)

			recorded, replaced := make(chan struct{}), make(chan struct{})

			var onceRecorded, onceReplaced sync.Once

			f.store.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
				<-recorded

				return nil
			}, after: func(_ string, err error) error {
				onceReplaced.Do(func() { close(replaced) })

				return err
			}})
			other.intercept(hook{op: opPut, prefix: "records/", after: func(_ string, err error) error {
				onceRecorded.Do(func() { close(recorded) })

				return err
			}})
			other.intercept(hook{op: opPut, prefix: "head", before: func(context.Context, string) error {
				<-replaced

				return nil
			}})

			var (
				wg     sync.WaitGroup
				la, lb []byte
			)

			wg.Go(func() { la = advance(t, a, l, l.update(t, 0, 5)) })
			wg.Go(func() { lb = advance(t, b, l, l.update(t, 0, 5)) })
			waitAll(t, &wg, "both Servers must return")

			testkit.Len(t, keys(t, f.store, "records/"), 1, "the two commits must create one record")
			testkit.Equal(t, string(lb), string(la), "both Servers must sign the same lines with Ed25519")
		})

		t.Run("never cosigns inconsistent checkpoints when the writes fail from any write on", func(t *testing.T) {
			t.Parallel()
			fork := newForkLog(t, logName)

			for k := range 40 {
				f := newFixture(t, l)
				var writes atomic.Int64
				var failing atomic.Bool
				failing.Store(true)
				f.store.intercept(hook{op: opPut, before: func(context.Context, string) error {
					if failing.Load() && writes.Add(1) > int64(k) {
						return errors.New("the store fails")
					}

					return nil
				}})

				var cosigned []checkpoint.Body

				s := newServer(t, f.config())
				for _, step := range []struct {
					log  *testLog
					size uint64
				}{
					{l, 3}, {fork, 3}, {l, 5}, {fork, 6}, {l, 8}, {fork, 9}, {l, 9},
				} {
					body, err := sendLike(t, s, step.log, step.size)
					if err != nil && failing.Load() {
						failing.Store(false)
						s = newServer(t, f.config())
						body, err = sendLike(t, s, step.log, step.size)
					}

					if err == nil {
						cosigned = append(cosigned, body)
					}
				}

				main, forked := 0, 0
				for _, b := range cosigned {
					if b.Root == l.body(b.Size).Root {
						main++
					} else {
						forked++
					}
				}

				testkit.True(t, main == 0 || forked == 0, "the run with writes failing after "+strconv.Itoa(k)+
					" must not cosign both trees")
			}
		})

		outcomes := []struct {
			name    string
			prefix  string
			applied bool
		}{
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of an applied record",
				prefix: "records/", applied: true,
			},
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of a record not applied",
				prefix: "records/",
			},
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of an applied head",
				prefix: "head", applied: true,
			},
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of a head not applied",
				prefix: "head",
			},
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of applied lines",
				prefix: "lines/", applied: true,
			},
			{
				name:   "leaves a state that a new Server reads after an unknown outcome of lines not applied",
				prefix: "lines/",
			},
		}
		for _, tt := range outcomes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, l)
				s := newServer(t, f.config())
				advance(t, s, l, l.update(t, 0, 5))
				settle(t, s, l)
				testkit.Len(t, keys(t, f.store, "lines/"), 1, "the first commit must store its lines")

				var once atomic.Bool

				h := hook{op: opPut, prefix: tt.prefix}
				if tt.applied {
					h.after = func(_ string, err error) error {
						if err == nil && once.CompareAndSwap(false, true) {
							return version.ErrOutcomeUnknown
						}

						return err
					}
				} else {
					h.before = func(context.Context, string) error {
						if once.CompareAndSwap(false, true) {
							return version.ErrOutcomeUnknown
						}

						return nil
					}
				}

				// The commit writes its lines after Advance returns.
				f.store.intercept(h)
				_, _, _ = s.Advance(bounded(t), l.notes[6], []witness.Update{l.update(t, 5, 6)}, nil)
				settle(t, s, l)
				testkit.True(t, once.Load(), "the store must return an unknown outcome for the write")
				f.store.reset()

				again := newServer(t, f.config())
				_, err := sendLike(t, again, l, 7)
				testkit.NoError(t, err, "a new Server must commit the next checkpoint")
			})
		}
	})
}

// newForkLog returns a test log of origin with the key of the test log of
// origin and a tree of other leaves, whose checkpoints are inconsistent
// with those of that log at every size.
func newForkLog(tb testing.TB, origin string) *testLog {
	tb.Helper()

	l := newTestLog(tb, origin)
	for i := range l.leaves {
		l.leaves[i] = tlog.LeafHash(l.log.Hasher, []byte("fork/"+strconv.Itoa(i)))
	}

	for size := range l.notes {
		l.notes[size] = signBody(tb, l.body(uint64(size)), l.signer)
	}

	return l
}

// sendLike sends the checkpoint of size of l to s as a log sends it: from
// the size that it believes s committed, and again from the size of a 409.
// It returns the body that s cosigned.
//
// Returns the error of Advance, and an error for a checkpoint that s does
// not cosign.
func sendLike(tb testing.TB, s *witness.Server, l *testLog, size uint64) (checkpoint.Body, error) {
	tb.Helper()

	oldSize := uint64(0)

	for range 3 {
		_, failures, err := s.Advance(bounded(tb), l.notes[size], []witness.Update{l.update(tb, oldSize, size)}, nil)
		if err != nil {
			return checkpoint.Body{}, err
		}

		if len(failures) == 0 {
			return l.body(size), nil
		}

		se, ok := errors.AsType[*witness.SizeError](failures[0].Err)
		if !ok || se.Size > size {
			return checkpoint.Body{}, failures[0].Err
		}

		oldSize = se.Size
	}

	return checkpoint.Body{}, errors.New("the witness did not cosign the checkpoint")
}

// AppendSignAt fails while c.fail is set, and runs c.before first.
func (c *flaky) AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error) {
	if c.before != nil {
		if err := c.before(ctx); err != nil {
			return dst, err
		}
	}

	if c.fail.Load() {
		return dst, errors.New("the cosigner is down")
	}

	return c.Cosigner.AppendSignAt(ctx, dst, text, t)
}

// ReadUTC returns an error.
func (brokenUTC) ReadUTC() (clock.UTCReading, error) {
	return clock.UTCReading{}, errors.New("the UTC source failed")
}

// resend sends the checkpoint of size of l to s from the size that s
// committed last, as a log does after a failed signature, and fails the
// test unless the commit fails at its signature.
func resend(tb testing.TB, s *witness.Server, l *testLog, size uint64) {
	tb.Helper()

	_, failures, err := s.Advance(bounded(tb), l.notes[size], []witness.Update{l.update(tb, size-1, size)}, nil)
	if len(failures) > 0 {
		se, _ := errors.AsType[*witness.SizeError](failures[0].Err)
		_, _, err = s.Advance(bounded(tb), l.notes[size], []witness.Update{l.update(tb, se.Size, size)}, nil)
	}

	testkit.Error(tb, err, "the commit must fail at its signature")
}

// target returns the target of the circuit of cosigner c: its key name,
// a plus sign, and its key ID in eight hexadecimal digits.
func target(c checkpoint.Cosigner) string {
	k := c.Key()

	return fmt.Sprintf("%s+%08x", k.Name, k.ID())
}

// lineTime returns the timestamp of the first line of lines over the note
// of size of l.
func lineTime(tb testing.TB, l *testLog, size uint64, lines []byte) time.Time {
	tb.Helper()

	text := append([]byte(nil), noteText(tb, l.notes[size])...)
	n := mustParse(tb, append(append(text, '\n'), lines...))

	ts, err := checkpoint.Timestamp(n.Signatures[0].Value)
	testkit.NoError(tb, err, "the line must have a timestamp")

	return ts
}

// bounded returns a context of tb that ends after patience. A call into a
// Server under it returns the error of the context when a defect stalls
// the call, so the case fails instead of waiting for the end of the test.
func bounded(tb testing.TB) context.Context {
	tb.Helper()

	ctx, cancel := context.WithTimeout(tb.Context(), patience)
	tb.Cleanup(cancel)

	return ctx
}

// awaitBefore returns the next value of ch, and fails tb at once when end
// delivers first while ch has no value, or when nothing arrives within
// patience. A test passes the end of the call that leads to the value, so
// it fails as soon as that call returns without the value.
func awaitBefore[T, E any](tb testing.TB, ch <-chan T, end <-chan E, what string) T {
	tb.Helper()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	var zero T

	select {
	case v := <-ch:
		return v
	case <-end:
		// A value that came before the end of the call is in ch.
		select {
		case v := <-ch:
			return v
		default:
		}

		tb.Fatalf("%s: the call returned first", what)
	case <-timer.C:
		tb.Fatalf("%s: nothing arrived within %s", what, patience)
	}

	return zero
}

// waitAll waits for wg, and fails tb when wg does not finish within
// patience.
func waitAll(tb testing.TB, wg *sync.WaitGroup, what string) {
	tb.Helper()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		tb.Fatal(what)
	}
}

// settle returns once the write of the lines of every earlier commit of s
// has ended. A call returns only after the write of the lines of the commit
// before its own, so settle sends a call whose commit fails its update: a
// checkpoint of l from the old size 1, which no test commits.
func settle(tb testing.TB, s *witness.Server, l *testLog) {
	tb.Helper()

	_, failures, err := s.Advance(bounded(tb), l.notes[2], []witness.Update{l.update(tb, 1, 2)}, nil)
	testkit.NoError(tb, err, "the call that settles the writes must return")
	testkit.Len(tb, failures, 1, "the commit must fail the call that settles the writes")
}
