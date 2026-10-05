// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// picky is a gated cosigner whose signature of a text that contains refused
// fails while failing is set.
type picky struct {
	*gated

	refused []byte
	failing atomic.Bool
}

// AppendSignAt fails for a text that contains p.refused while p.failing is
// set, and signs as the gated cosigner of p otherwise.
func (p *picky) AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error) {
	if p.failing.Load() && (len(p.refused) == 0 || bytes.Contains(text, p.refused)) {
		return dst, errors.New("the cosigner refuses the text")
	}

	return p.gated.AppendSignAt(ctx, dst, text, t)
}

// overlapping is a cosigner that counts the signatures that run at once,
// and keeps the most. While wait is set, a signature waits until two run at
// once, for at most 5 seconds, and fails after that. Otherwise it takes 10
// ms, so that the signatures of concurrent goroutines overlap.
type overlapping struct {
	checkpoint.Cosigner

	// both closes when two signatures run at once.
	both chan struct{}
	once sync.Once

	// running and most are the signatures that run, and the most that ran
	// at once, which mu guards.
	running, most int
	mu            sync.Mutex

	wait bool
}

// AppendSignAt counts the signature among those that run, waits as o
// describes, and signs as the wrapped cosigner.
func (o *overlapping) AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error) {
	o.mu.Lock()
	o.running++
	o.most = max(o.most, o.running)
	n := o.running
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.running--
		o.mu.Unlock()
	}()

	if n == 2 {
		o.once.Do(func() { close(o.both) })
	}

	if !o.wait {
		time.Sleep(10 * time.Millisecond)

		return o.Cosigner.AppendSignAt(ctx, dst, text, t)
	}

	select {
	case <-o.both:
	case <-time.After(5 * time.Second):
		return dst, errors.New("the signatures do not run at once")
	}

	return o.Cosigner.AppendSignAt(ctx, dst, text, t)
}

func TestCosignInternal(t *testing.T) {
	t.Parallel()

	t.Run("cosign", func(t *testing.T) {
		t.Parallel()

		t.Run("fails every call of a commit when one signature fails, and a repair stores their lines",
			func(t *testing.T) {
				t.Parallel()
				a, b, c := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b"),
					newInternalLog(t, "example.com/c")
				f := newInternalFixture(t, a, b, c)
				p := &picky{
					gated:   &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})},
					refused: []byte(b.origin),
				}
				p.failing.Store(true)
				f.signer = p
				s := f.server(t)

				var wg sync.WaitGroup

				first := make(chan struct{})

				wg.Go(func() {
					defer close(first)

					c.advance(t, s, 0, 5, nil)
				})
				awaitBefore(t, p.started, first, "the commit of c must sign")

				errs := make([]error, 2)
				for i, l := range []*internalLog{a, b} {
					wg.Go(func() {
						_, _, errs[i] = s.Advance(t.Context(), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
					})
					waitQueue(t, s, i+1)
				}

				close(p.release)
				waitAll(t, &wg, "every call must return")

				for _, err := range errs {
					testkit.Error(t, err, "every call of the commit must fail")
				}

				testkit.Len(t, listKeys(t, f.store, linesPrefix), 1, "the commit must store no lines")

				p.failing.Store(false)
				f.clock.Advance(time.Minute)

				for _, l := range []*internalLog{a, b} {
					testkit.Equal(
						t,
						getRoute(t, s, l.origin).Code,
						http.StatusOK,
						"the route must serve the repaired call",
					)
				}
			})

		t.Run("lets every cosigner sign when one fails", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			p := &picky{gated: &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}}
			close(p.release)
			p.failing.Store(true)

			cfg := f.config()
			cfg.Cosigners = []checkpoint.Cosigner{p, secondCosigner(t, f)}
			s := newInternalServer(t, cfg)

			for size := uint64(1); size <= 2; size++ {
				_, _, err := s.Advance(t.Context(), a.note(t, size), []Update{a.update(t, size-1, size, nil)}, nil)
				testkit.Error(t, err, "the commit must fail at the failing cosigner")
			}

			testkit.Equal(
				t,
				f.breaker.State(s.targets[0]),
				resilience.Open,
				"the failing cosigner must open its circuit",
			)
			testkit.Equal(
				t,
				f.breaker.State(s.targets[1]),
				resilience.Closed,
				"the other cosigner must keep its circuit",
			)
		})

		// overlapped returns a server whose one cosigner is c over the
		// cosigner of a fixture, and that signs on signers goroutines.
		overlapped := func(tb testing.TB, signers int, wait bool) (*Server, *overlapping) {
			tb.Helper()

			f := newInternalFixture(tb)
			c := &overlapping{Cosigner: f.signer, both: make(chan struct{}), wait: wait}
			cfg := f.config()
			cfg.Cosigners = []checkpoint.Cosigner{c}
			s := newInternalServer(tb, cfg)
			s.signers = signers

			return s, c
		}

		texts := [][]byte{[]byte("a\n"), []byte("b\n"), []byte("c\n"), []byte("d\n"), []byte("e\n"), []byte("f\n")}

		t.Run("signs two texts of one cosigner at once", func(t *testing.T) {
			t.Parallel()
			s, c := overlapped(t, 2, true)

			var g signing
			testkit.NoError(t, s.cosign(t.Context(), texts[:2], internalTime, &g), "the signatures must run at once")

			for i, text := range texts[:2] {
				testkit.True(t, c.Verify(text, g.values[0][i]), "the value of each text must verify")
			}
		})

		tests := []struct {
			name    string
			signers int
		}{
			{name: "signs at most two texts of one cosigner at once for two signers", signers: 2},
			{name: "signs one text of one cosigner at a time for one signer", signers: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, c := overlapped(t, tt.signers, false)

				var g signing
				testkit.NoError(t, s.cosign(t.Context(), texts, internalTime, &g), "the cosigner must sign every text")
				testkit.True(t, c.most <= tt.signers, "the cosigner must sign at most signers texts at once")

				for i, text := range texts {
					testkit.True(t, c.Verify(text, g.values[0][i]), "the value of each text must verify")
				}
			})
		}
	})
}

func BenchmarkCosignInternal(b *testing.B) {
	b.Run("cosign", func(b *testing.B) {
		// Each iteration creates the context of the signatures of a commit,
		// as finish does: 4 objects. One cosigner signs one text on the
		// calling goroutine. Each task.Each costs 5 objects on Go 1.27.1,
		// and 3 more as the first under its parent context: 8 over the texts
		// of one cosigner, 8 over two cosigners, and 21 over two cosigners
		// and the texts of each.
		tests := []struct {
			name      string
			texts     [][]byte
			cosigners int
			want      uint64
		}{
			{name: "of one text", texts: [][]byte{[]byte("a\n")}, cosigners: 1, want: 4},
			{name: "of two texts", texts: [][]byte{[]byte("a\n"), []byte("b\n")}, cosigners: 1, want: 12},
			{name: "of one text by two cosigners", texts: [][]byte{[]byte("a\n")}, cosigners: 2, want: 12},
			{
				name: "of two texts by two cosigners", texts: [][]byte{[]byte("a\n"), []byte("b\n")}, cosigners: 2,
				want: 25,
			},
		}
		for _, tt := range tests {
			b.Run(tt.name, func(b *testing.B) {
				f := newInternalFixture(b)

				cfg := f.config()
				if tt.cosigners == 2 {
					cfg.Cosigners = []checkpoint.Cosigner{f.signer, secondCosigner(b, f)}
				}

				s := newInternalServer(b, cfg)

				var g signing
				testkit.NoError(b, s.cosign(b.Context(), tt.texts, internalTime, &g), "the first cosign must sign")

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				var err error
				for c.Loop() {
					sctx, cancel := context.WithTimeout(b.Context(), time.Minute)
					err = s.cosign(sctx, tt.texts, internalTime, &g)

					cancel()
				}

				testkit.NoError(b, err, "the benchmark must measure signatures")
			})
		}
	})
}

// secondCosigner returns an Ed25519 cosigner of the name
// example.com/second over the clock of f, whose key is the Ed25519 key of
// the SHA-256 of that name.
func secondCosigner(tb testing.TB, f *internalFixture) checkpoint.Cosigner {
	tb.Helper()

	seed := sha256.Sum256([]byte("example.com/second"))

	k, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	testkit.NoError(tb, err, "the seed must give a key")

	c, err := checkpoint.NewCosignatureV1Signer("example.com/second", checkpoint.TypeEd25519Cosignature, k,
		f.clock, time.Second)
	testkit.NoError(tb, err, "NewCosignatureV1Signer must accept the key")

	return c
}
