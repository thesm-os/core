// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// advanceCeilings are the allocation contracts of a commit of Advance.
// Each commit allocates 9 objects of the package: 4 for each of its two
// contexts with a deadline, and the string of the keys of its record.
// blob/memory allocates 6: a copy of each of the three objects of the
// commit, and the text of each version. A second cosigner adds the 7
// objects of task.Each and the closure of the cosigners.
var advanceCeilings = []struct {
	name  string
	extra []note.Name
	want  uint64
}{
	{name: "of one cosigner", want: 15},
	{name: "of two cosigners", extra: []note.Name{"example.com/second"}, want: 23},
}

// counted is a note.Verifier that counts its verifications.
type counted struct {
	note.Verifier

	n *atomic.Int64
}

func TestAdvance(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a line of the cosigner over the note at the time of the commit", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			f := newFixture(t, l)
			s := newServer(t, f.config())

			text := append([]byte(nil), noteText(t, l.notes[5])...)
			n := mustParse(t, append(append(text, '\n'), advance(t, s, l, l.update(t, 0, 5))...))
			assert.Length(t, n.Signatures, 1, "Advance must return one line per cosigner")
			assert.True(t, f.cosigners[0].Verify(n.Text, n.Signatures[0].Value), "the line must verify")

			ts, err := checkpoint.Timestamp(n.Signatures[0].Value)
			assert.NoError(t, err, "the line must have a timestamp")
			assert.Equal(t, ts, clockTime, "the line must have the time of the commit")
		})

		t.Run("appends the lines to dst", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			s := newServer(t, newFixture(t, l).config())

			got, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, []byte("prefix:"))
			assert.NoError(t, err, "Advance must commit the update")
			assert.Equal(t, string(got[:len("prefix:")]), "prefix:", "Advance must keep dst")
		})

		t.Run("returns a SizeError with the committed size after a restart", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			f := newFixture(t, l)
			s := newServer(t, f.config())

			advance(t, s, l, l.update(t, 0, 5))
			advance(t, s, l, l.update(t, 5, 8))

			again := newServer(t, f.config())
			_, failures, err := again.Advance(bounded(t), l.notes[9], []witness.Update{l.update(t, 5, 9)}, nil)
			assert.NoError(t, err, "Advance must check the update")
			assert.Length(t, failures, 1, "Advance must return the failure of the update")

			se := assert.ErrorAs[*witness.SizeError](t, failures[0].Err, "the failure must be a SizeError")
			assert.Equal(t, se.Size, uint64(8), "the SizeError must contain the committed size")
		})

		t.Run("commits a checkpoint of an equal size and root again", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			s := newServer(t, newFixture(t, l).config())

			advance(t, s, l, l.update(t, 0, 5))
			assert.NotEmpty(t, advance(t, s, l, l.update(t, 5, 5)), "Advance must cosign the equal checkpoint")
		})

		t.Run("returns ErrInconsistent for another root at an equal size", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			s := newServer(t, newFixture(t, l).config())

			advance(t, s, l, l.update(t, 0, 5))

			u := l.update(t, 5, 5)
			u.Body.Root = l.body(6).Root
			msg := signBody(t, u.Body, l.signer)

			_, failures, err := s.Advance(bounded(t), msg, []witness.Update{u}, nil)
			assert.NoError(t, err, "Advance must check the update")
			assert.Length(t, failures, 1, "Advance must return the failure")
			assert.ErrorIs(t, failures[0].Err, witness.ErrInconsistent, "the failure must be ErrInconsistent")
		})

		t.Run("returns a Failure of each update that fails and commits none", func(t *testing.T) {
			t.Parallel()
			a, b := newTestLog(t, "example.com/a"), newTestLog(t, "example.com/b")
			s := newServer(t, newFixture(t, a, b).config())
			msg := signBody(t, a.body(5), a.signer, b.signer)

			_, failures, err := s.Advance(bounded(t), msg, []witness.Update{a.update(t, 0, 5), b.update(t, 3, 5)}, nil)
			assert.NoError(t, err, "Advance must check the updates")
			assert.Length(t, failures, 1, "Advance must return the failure of the second update")
			assert.Equal(t, failures[0].Index, 1, "the failure must name the second update")

			_, failures, err = s.Advance(bounded(t), a.notes[6], []witness.Update{a.update(t, 5, 6)}, nil)
			assert.NoError(t, err, "Advance must check the update")
			assert.Length(t, failures, 1, "the first update must not have committed")
		})

		t.Run("commits 1,000 updates under one note and verifies its line once", func(t *testing.T) {
			t.Parallel()
			batch := newTestLog(t, "example.com/batch")

			var verified atomic.Int64

			f := newFixture(t)
			cfg := f.config()
			cfg.MaxOrigins = 2000
			cfg.Resolver = countingResolver(&verified)

			updates := make([]witness.Update, 1000)
			for i := range updates {
				origin := checkpoint.Origin("example.com/log/" + strconv.Itoa(i))
				f.accepted[origin] = batch.log
				updates[i] = witness.Update{Body: checkpoint.Body{Origin: origin, Size: 5, Root: batch.body(5).Root}}
			}

			s := newServer(t, cfg)
			lines, failures, err := s.Advance(bounded(t), batch.notes[5], updates, nil)
			assert.NoError(t, err, "Advance must commit the updates")
			assert.Empty(t, failures, "Advance must return no failure")
			assert.NotEmpty(t, lines, "Advance must return the lines")
			assert.Equal(t, verified.Load(), int64(1), "Advance must verify the line of the note once")
		})

		t.Run("commits none of 1,000 updates when one fails", func(t *testing.T) {
			t.Parallel()
			batch := newTestLog(t, "example.com/batch")
			f := newFixture(t)
			cfg := f.config()
			cfg.MaxOrigins = 2000

			updates := make([]witness.Update, 1000)
			for i := range updates {
				origin := checkpoint.Origin("example.com/log/" + strconv.Itoa(i))
				f.accepted[origin] = batch.log
				updates[i] = witness.Update{Body: checkpoint.Body{Origin: origin, Size: 5, Root: batch.body(5).Root}}
			}

			updates[999].OldSize = 2

			s := newServer(t, cfg)
			_, failures, err := s.Advance(bounded(t), batch.notes[5], updates, nil)
			assert.NoError(t, err, "Advance must check the updates")
			assert.Length(t, failures, 1, "Advance must return the failure of the last update")

			updates[999].OldSize = 0
			_, failures, err = s.Advance(bounded(t), batch.notes[5], updates, nil)
			assert.NoError(t, err, "Advance must commit the updates")
			assert.Empty(t, failures, "the first call must have committed none of the updates")
		})

		t.Run("returns ErrRequest for a note of 65 lines without a verification", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)

			var verified atomic.Int64

			cfg := newFixture(t, l).config()
			cfg.Resolver = countingResolver(&verified)
			s := newServer(t, cfg)

			signers := make([]note.Signer, 65)
			for i := range signers {
				signers[i] = l.signer
			}

			msg := signBody(t, l.body(5), signers...)
			_, _, err := s.Advance(bounded(t), msg, []witness.Update{l.update(t, 0, 5)}, nil)
			assert.ErrorIs(t, err, witness.ErrRequest, "Advance must refuse a note of 65 lines")
			assert.Equal(t, verified.Load(), int64(0), "Advance must verify no line")
		})

		t.Run("commits a note of 64 signature lines", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			s := newServer(t, newFixture(t, l).config())

			// The line of the log, and 63 lines of keys that the log does not
			// have.
			msg := append([]byte(nil), l.notes[5]...)
			for i := range 63 {
				other := ed25519Cosigner(t, note.Name("example.com/other/"+strconv.Itoa(i)))
				msg = append(msg, garbageLine(other.Key())...)
			}

			lines, failures, err := s.Advance(bounded(t), msg, []witness.Update{l.update(t, 0, 5)}, nil)
			assert.NoError(t, err, "Advance must accept a note of 64 lines")
			assert.Empty(t, failures, "Advance must return no failure")
			assert.NotEmpty(t, lines, "Advance must cosign the note")
		})

		t.Run(
			"returns ErrRequest for a root of another size than the log's digests and commits nothing",
			func(t *testing.T) {
				t.Parallel()
				l := newTestLog(t, logName)
				s := newServer(t, newFixture(t, l).config())

				u := l.update(t, 0, 5)
				u.Body.Root = crypto.NewDigest384([48]byte{1})
				msg := signBody(t, u.Body, l.signer)

				_, _, err := s.Advance(bounded(t), msg, []witness.Update{u}, nil)
				assert.ErrorIs(t, err, witness.ErrRequest, "Advance must refuse the root")

				_, failures, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
				assert.NoError(t, err, "Advance must commit the update")
				assert.Empty(t, failures, "the first call must have committed nothing")
			},
		)

		t.Run("returns ErrRequest for a note that a cosigner does not sign and commits nothing", func(t *testing.T) {
			t.Parallel()
			l := newTestLog(t, logName)
			f := newFixture(t, l)
			f.cosigners = append(f.cosigners, mldsaCosigner(t, "example.com/hybrid", f.clock))
			s := newServer(t, f.config())

			body := l.body(5)
			body.Extensions = []checkpoint.Extension{"an extension line"}
			msg := signBody(t, body, l.signer)

			_, _, err := s.Advance(bounded(t), msg, []witness.Update{l.update(t, 0, 5)}, nil)
			assert.ErrorIs(t, err, witness.ErrRequest, "Advance must refuse the note")
			assert.ErrorIs(t, err, checkpoint.ErrBody, "the error must wrap the error of the cosigner")

			_, failures, err := s.Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			assert.NoError(t, err, "Advance must commit the update")
			assert.Empty(t, failures, "the first call must have committed nothing")
		})

		l := newTestLog(t, logName)
		big := witness.Update{Body: l.body(5), Prefix: make([]byte, 16<<20)}
		tests := []struct {
			give    func(tb testing.TB) ([]byte, []witness.Update)
			name    string
			class   errs.Class
			wantErr error
		}{
			{
				name: "returns ErrRequest for no update", wantErr: witness.ErrRequest, class: errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) { return l.notes[5], nil },
			},
			{
				name: "returns ErrRequest for 4,097 updates", wantErr: witness.ErrRequest, class: errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) {
					updates := make([]witness.Update, 4097)
					for i := range updates {
						updates[i].Body = l.body(5)
						updates[i].Body.Origin = checkpoint.Origin("example.com/" + strconv.Itoa(i))
					}

					return l.notes[5], updates
				},
			},
			{
				name:    "returns ErrRequest for two updates of one origin around another",
				wantErr: witness.ErrRequest,
				class:   errs.Invalid,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					other := witness.Update{Body: l.body(5)}
					other.Body.Origin = "example.com/other"

					return l.notes[5], []witness.Update{l.update(tb, 0, 5), other, l.update(tb, 0, 5)}
				},
			},
			{
				name:    "returns ErrRequest for a body that is not Valid",
				wantErr: witness.ErrRequest,
				class:   errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) {
					return l.notes[5], []witness.Update{{Body: checkpoint.Body{Origin: l.origin, Size: 5}}}
				},
			},
			{
				name:    "returns ErrRequest for a body with an extension that is not a line",
				wantErr: witness.ErrRequest,
				class:   errs.Invalid,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					u := l.update(tb, 0, 5)
					u.Body.Extensions = []checkpoint.Extension{"two\nlines"}

					return l.notes[5], []witness.Update{u}
				},
			},
			{
				name: "returns ErrRequest for a msg that is not a signed note", wantErr: witness.ErrRequest,
				class: errs.Invalid,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					return []byte("not a note"), []witness.Update{l.update(tb, 0, 5)}
				},
			},
			{
				name:    "returns ErrRequest for an old size above the size",
				wantErr: witness.ErrRequest,
				class:   errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) {
					return l.notes[5], []witness.Update{{Body: l.body(5), OldSize: 6}}
				},
			},
			{
				name:    "returns ErrRequest for a proof hash of another size than the log's digests",
				wantErr: witness.ErrRequest, class: errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) {
					u := witness.Update{
						Body:    l.body(5),
						OldSize: 3,
						Proof:   []crypto.Digest{crypto.NewDigest512([64]byte{1})},
					}

					return l.notes[5], []witness.Update{u}
				},
			},
			{
				name: "returns ErrRequest for a call above 16 MiB", wantErr: witness.ErrRequest, class: errs.Invalid,
				give: func(testing.TB) ([]byte, []witness.Update) { return l.notes[5], []witness.Update{big} },
			},
			{
				name: "returns ErrUnknownOrigin for an origin that Logs refuses", wantErr: witness.ErrUnknownOrigin,
				class: errs.NotFound,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					other := newTestLog(tb, "example.com/other")

					return other.notes[5], []witness.Update{other.update(tb, 0, 5)}
				},
			},
			{
				name: "returns ErrSignature for a msg without a line of the log", wantErr: witness.ErrSignature,
				class: errs.Denied,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					other := newTestLog(tb, "example.com/other")

					return signBody(tb, l.body(5), other.signer), []witness.Update{l.update(tb, 0, 5)}
				},
			},
			{
				name: "returns ErrSignature for a msg with an invalid line of the log", wantErr: witness.ErrSignature,
				class: errs.Denied,
				give: func(tb testing.TB) ([]byte, []witness.Update) {
					tb.Helper()

					msg := append(append([]byte(nil), l.notes[5]...), garbageLine(l.signer.Key())...)

					return msg, []witness.Update{l.update(tb, 0, 5)}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := newServer(t, newFixture(t, l).config())
				msg, updates := tt.give(t)
				got, failures, err := s.Advance(bounded(t), msg, updates, []byte("prefix:"))
				assert.ErrorIs(t, err, tt.wantErr, "Advance must refuse the call")
				assert.Equal(t, errs.Classify(err), tt.class, "the error must classify as "+tt.class.String())
				assert.Empty(t, failures, "Advance must return no failure with an error")
				assert.Equal(t, string(got), "prefix:", "Advance must return dst unchanged")
			})
		}

		t.Run("returns ErrConfig for a Log without a hasher", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{Keys: l.log.Keys}
			_, _, err := newServer(
				t,
				f.config(),
			).Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			assert.ErrorIs(t, err, witness.ErrConfig, "Advance must refuse the Log")
		})

		t.Run("returns the error of the Resolver for a key of a log that it does not resolve", func(t *testing.T) {
			t.Parallel()
			cfg := newFixture(t, l).config()
			cfg.Resolver = note.Resolver{}
			_, _, err := newServer(t, cfg).Advance(bounded(t), l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			assert.ErrorIs(t, err, note.ErrUnknownType, "Advance must return the error of the Resolver")
		})

		t.Run("returns the cause of ctx when ctx ends before the result", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			release := make(chan struct{})
			f.store.intercept(hook{op: opPut, prefix: "records/", before: func(context.Context, string) error {
				<-release

				return nil
			}})

			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("the caller stopped waiting")
			cancel(cause)

			_, _, err := s.Advance(ctx, l.notes[5], []witness.Update{l.update(t, 0, 5)}, nil)
			close(release)
			assert.ErrorIs(t, err, cause, "Advance must return the cause of ctx")
		})

		// The two calls run through one pooled call, whose verification of
		// the first note must not decide about the second.
		t.Run("returns ErrSignature for a forged line of the key that verified the call before it", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			advance(t, s, l, l.update(t, 0, 5))

			text, err := note.TextOf(l.notes[6])
			assert.NoError(t, err, "TextOf must accept the note")
			forged := append(append(append([]byte(nil), text...), '\n'), garbageLine(l.signer.Key())...)

			_, _, err = s.Advance(bounded(t), forged, []witness.Update{l.update(t, 5, 6)}, nil)
			assert.ErrorIs(t, err, witness.ErrSignature, "Advance must verify the line of the second call")
		})

		t.Run("copies msg and the updates before it returns", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			msg := append([]byte(nil), l.notes[5]...)
			u := l.update(t, 0, 5)
			u.Prefix = []byte("a prefix")

			_, _, err := s.Advance(bounded(t), msg, []witness.Update{u}, nil)
			assert.NoError(t, err, "Advance must commit the update")
			clear(msg)
			clear(u.Prefix)

			_, failures, err := s.Advance(bounded(t), l.notes[6], []witness.Update{l.update(t, 5, 6)}, nil)
			assert.NoError(t, err, "Advance must commit the next update")
			assert.Empty(t, failures, "the committed state must not depend on the memory of the caller")
		})
	})
}

// TestAdvanceAllocs checks the allocation contract of Advance that
// BenchmarkAdvance states, in the ordinary build that CI and the
// confirmation of a mutation run test. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestAdvanceAllocs(t *testing.T) {
	t.Run("Advance", func(t *testing.T) {
		for _, tt := range advanceCeilings {
			t.Run(tt.name, func(t *testing.T) {
				s, l := committedServer(t, tt.extra)
				updates := []witness.Update{l.update(t, 5, 5)}
				dst := make([]byte, 0, 1024)

				var (
					lines []byte
					err   error
				)

				expect.MaxAllocs(t, func() { lines, _, err = s.Advance(t.Context(), l.notes[5], updates, dst[:0]) },
					tt.want, "a commit must allocate the objects of its contract alone")
				assert.NoError(t, err, "the test must measure a commit")
				assert.NotEmpty(t, lines, "the test must measure a cosigned commit")
			})
		}
	})
}

func BenchmarkAdvance(b *testing.B) {
	b.Run("Advance", func(b *testing.B) {
		for _, tt := range advanceCeilings {
			b.Run(tt.name, func(b *testing.B) {
				s, l := committedServer(b, tt.extra)
				updates := []witness.Update{l.update(b, 5, 5)}
				dst := make([]byte, 0, 1024)

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				var (
					lines []byte
					err   error
				)

				for c.Loop() {
					lines, _, err = s.Advance(b.Context(), l.notes[5], updates, dst[:0])
				}

				assert.NoError(b, err, "the benchmark must measure a commit")
				assert.NotEmpty(b, lines, "the benchmark must measure a cosigned commit")
			})
		}
	})
}

// committedServer returns a Server of a log that committed the size 5, and
// the log. The Server has a cosigner of each name of extra beside the
// cosigner of the fixture. A checkpoint of the committed size and root
// commits again, so each later call of the update from 5 to 5 commits a
// record. blob/memory formats a version below 100 without an allocation, so
// committedServer commits 40 records first, past it.
func committedServer(tb testing.TB, extra []note.Name) (*witness.Server, *testLog) {
	tb.Helper()

	l := newTestLog(tb, logName)
	f := newFixture(tb, l)

	for _, name := range extra {
		f.cosigners = append(f.cosigners, ed25519Cosigner(tb, name))
	}

	s := newServer(tb, f.config())
	advance(tb, s, l, l.update(tb, 0, 5))

	for range 40 {
		advance(tb, s, l, l.update(tb, 5, 5))
	}

	return s, l
}

// countingResolver returns the Resolver of the tests, whose Verifiers of
// note keys of type 0x01 count each verification in n.
func countingResolver(n *atomic.Int64) note.Resolver {
	r := resolver()
	text := r[note.TypeEd25519]
	r[note.TypeEd25519] = func(k note.Key) (note.Verifier, error) {
		v, err := text(k)
		if err != nil {
			return nil, err
		}

		return counted{Verifier: v, n: n}, nil
	}

	return r
}

// Verify counts the verification, and reports what the wrapped Verifier
// reports.
func (c counted) Verify(text, value []byte) bool {
	c.n.Add(1)

	return c.Verifier.Verify(text, value)
}
