// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"maps"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The fixture values of the cases of Verifier.
const (
	// exampleCheckpoint is the text of a checkpoint of exampleLog, of the
	// size and the root of the example of tlog-checkpoint.
	exampleCheckpoint = exampleLog + "\n20852163\n" + exampleRoot + "\n"

	// newVerifierAllocs is the number of allocations of NewVerifier for the
	// policy of oneGroupPolicyText, with one log and three witnesses: the
	// Verifier, its Keyring, rules, order and trees, two for the Verifier of
	// each key, and four for the sign.Policy of the origin.
	newVerifierAllocs = 18
)

// The labels under which the machine of the Resets counts a case that
// verifies a checkpoint that the last policy accepts and a case that
// verifies one that it refuses.
const (
	acceptedCheckpoint = "an accepted checkpoint"
	refusedCheckpoint  = "a refused checkpoint"
)

func TestVerifier(t *testing.T) {
	t.Parallel()

	t.Run("NewVerifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier of a quorum of 62 nested groups", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewVerifier(nestedPolicy(t, 62), resolver)
			assert.NoError(t, err, "NewVerifier must accept a tree of 64 rules of depth")
		})

		t.Run("returns the error of sign.Policy.Reset for a quorum of 63 nested groups", func(t *testing.T) {
			t.Parallel()
			v, err := checkpoint.NewVerifier(nestedPolicy(t, 63), resolver)
			expect.ErrorIs(t, err, sign.ErrPolicy, "NewVerifier must return the error of sign.Policy.Reset")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Nil(t, v, "NewVerifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrPolicy for a policy without logs", func(t *testing.T) {
			t.Parallel()
			text := "witness a " + witness(t, "example.com/w").Key().String() + "\nquorum a\n"
			v, err := checkpoint.NewVerifier(mustParsePolicy(t, []byte(text)), resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse a policy without logs")
			expect.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+": a verifier needs a log",
				"the error must state the missing log")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Nil(t, v, "NewVerifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrPolicy for a Policy that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewVerifier(&checkpoint.Policy{}, resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse the zero Policy")
		})

		t.Run("returns ErrPolicy for a log that is not Valid", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = append(p.Logs, checkpoint.Log{})
			_, err := checkpoint.NewVerifier(p, resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse a log without a key")
			assert.Equal(t, err.Error(), checkpoint.ErrPolicy.Error()+": a log with an invalid key",
				"the error must name the rule")
		})

		t.Run("returns the error of the resolver for a log key", func(t *testing.T) {
			t.Parallel()
			r := maps.Clone(resolver)
			delete(r, note.TypeEd25519)
			_, err := checkpoint.NewVerifier(examplePolicy(t), r)
			assert.ErrorIs(t, err, note.ErrUnknownType, "NewVerifier must return the error of the resolver")
		})

		t.Run("returns the error of the resolver for a witness key", func(t *testing.T) {
			t.Parallel()
			r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
			_, err := checkpoint.NewVerifier(examplePolicy(t), r)
			assert.ErrorIs(t, err, note.ErrUnknownType, "NewVerifier must return the error of the resolver")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets v to the trees of another policy", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			assert.NoError(t, v.Reset(twoLogs(t), resolver), "Reset must accept the policy")
			_, err := verify(v, checkpointNote(t, "example.com/b", "X1", "X2", "Y1"))
			assert.NoError(t, err, "Verify must accept a checkpoint of the new policy")
		})

		t.Run("removes the tree of an origin of the policy before", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			assert.NoError(t, v.Reset(twoLogs(t), resolver), "Reset must accept the policy")
			_, err := verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			assert.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse an origin of the policy before")
		})

		t.Run("sets a zero Verifier to the Verifier of a policy", func(t *testing.T) {
			t.Parallel()
			var v checkpoint.Verifier
			assert.NoError(t, v.Reset(examplePolicy(t), resolver), "Reset must accept the policy")
			_, err := verify(&v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			assert.NoError(t, err, "Verify must accept the checkpoint")
		})

		t.Run("builds the Verifier of each key once over the Resets of one policy", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			r := countingResolver(calls)
			v := &checkpoint.Verifier{}
			for range 3 {
				assert.NoError(t, v.Reset(examplePolicy(t), r), "Reset must accept the policy")
			}
			expect.Equal(t, calls.Load(), int64(7), "the resolver must build the Verifier of each of 7 keys once")
			_, err := verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			expect.NoError(t, err, "Verify must accept the checkpoint")
		})

		t.Run("removes every tree with an error", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			err := v.Reset(&checkpoint.Policy{}, resolver)
			assert.ErrorIs(t, err, checkpoint.ErrPolicy, "Reset must refuse the zero Policy")
			_, err = verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			assert.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse every origin after an error")
		})

		// The machine resets one Verifier to the policies in any order, the
		// zero Policy among them, and verifies checkpoints with it. Each
		// Verify must return what a new Verifier of the last policy returns,
		// and what the zero Verifier returns after a policy that Reset
		// refuses.
		t.Run("sets v to the Verifier of the last policy under any sequence of Resets", func(t *testing.T) {
			t.Parallel()
			policies := []*checkpoint.Policy{
				examplePolicy(t),
				twoLogs(t),
				changed(t, func(p *checkpoint.Policy) { p.Quorum = checkpoint.QuorumNone }),
				mustParsePolicy(t, []byte(oneGroupPolicyText(t))),
				{},
			}
			models := make([]*checkpoint.Verifier, len(policies))
			refusals := make([]error, len(policies))
			for i, p := range policies {
				models[i], refusals[i] = checkpoint.NewVerifier(p, resolver)
				if refusals[i] != nil {
					models[i] = &checkpoint.Verifier{}
				}
			}
			logOnly, err := note.Sign(t.Context(), []byte(exampleCheckpoint), logSigner(t, exampleLog))
			assert.NoError(t, err, "note.Sign must sign the checkpoint")
			notes := []*note.Note{
				checkpointNote(t, exampleLog, "X1", "X2", "Y1"),
				checkpointNote(t, exampleLog, "Y1", "Y2", "Y3", "X1"),
				checkpointNote(t, "example.com/a", "X1", "X2", "Y1"),
				checkpointNote(t, "example.com/b", "X3"),
				&logOnly,
			}

			prop.ForAll(t, "Verify must return what a new Verifier of the last policy returns", func(c *prop.Case) {
				v := &checkpoint.Verifier{}
				model := &checkpoint.Verifier{}
				var body checkpoint.Body

				stateful.Steps(c, stateful.Machine[struct{}]{
					Actions: []stateful.Action[struct{}]{
						{
							Name: "reset",
							Input: func(c *prop.Case, _ struct{}) any {
								return c.Draw(prop.Integer(0, len(policies)-1), "policy")
							},
							Run: func(c *prop.Case, _ int, in any) {
								i := in.(int)
								assert.Equal(c, v.Reset(policies[i], resolver), refusals[i],
									"Reset must return the error of NewVerifier")
								model = models[i]
							},
						},
						{
							Name: "verify",
							Input: func(c *prop.Case, _ struct{}) any {
								return c.Draw(prop.Integer(0, len(notes)-1), "note")
							},
							Run: func(c *prop.Case, _ int, in any) {
								var want checkpoint.Body
								wantErr := model.Verify(notes[in.(int)], &want)
								assert.Equal(c, v.Verify(notes[in.(int)], &body), wantErr,
									"Verify must return the error of a new Verifier of the last policy")
								if wantErr != nil {
									c.Classify(refusedCheckpoint)

									return
								}
								c.Classify(acceptedCheckpoint)
								assert.Equal(c, body, want, "Verify must set the body that a new Verifier sets")
							},
						},
					},
				})
			}, prop.Require(acceptedCheckpoint, 0.1), prop.Require(refusedCheckpoint, 0.1))
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("sets b to the body of the checkpoint that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, mustParsePolicy(t, readFile(t, policyFile)))
			got, err := verify(v, mustParse(t, readFile(t, cosignedFile)))
			assert.NoError(t, err, "Verify must accept the checkpoint of the vectors")
			assert.Equal(t, got, checkpoint.Body{Origin: exampleLog, Size: exampleSize, Root: exampleDigest(t)},
				"Verify must set the body")
		})

		t.Run("sets the extension lines of a checkpoint that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			text := "log " + vectorKey(t, vectorLog).String() + "\nwitness W1 " + vectorKey(t, vectorW1).String() +
				"\nquorum W1\n"
			v := mustVerifier(t, mustParsePolicy(t, []byte(text)))
			got, err := verify(v, mustParse(t, readFile(t, extensionFile)))
			assert.NoError(t, err, "Verify must accept the checkpoint with an extension line")
			assert.Equal(t, got.Extensions, []checkpoint.Extension{"an extension line"}, "Verify must set the line")
		})

		t.Run("returns nil for the example of tlog-policy with two X witnesses beside one Y witness",
			func(t *testing.T) {
				t.Parallel()
				_, err := verify(mustVerifier(t, examplePolicy(t)), checkpointNote(t, exampleLog, "X1", "X2", "Y3"))
				assert.NoError(t, err, "Verify must accept the log and the quorum")
			})

		t.Run("returns ErrThreshold for three Y witnesses beside one X witness", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			_, err := verify(v, checkpointNote(t, exampleLog, "Y1", "Y2", "Y3", "X1"))
			expect.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a quorum without two X witnesses")
			expect.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})

		t.Run("returns ErrThreshold for a checkpoint without the signature of the log", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			_, err := verify(v, cosign(t, exampleCheckpoint, "X1", "X2", "Y1"))
			assert.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a checkpoint that the log did not sign")
		})

		t.Run("sets one Body to the checkpoints of two origins in turn", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, twoLogs(t))
			var b checkpoint.Body
			for _, origin := range []note.Name{"example.com/a", "example.com/b", "example.com/a"} {
				assert.NoError(t, v.Verify(checkpointNote(t, origin, "X1", "X2", "Y1"), &b),
					"Verify must accept the checkpoint of "+string(origin))
				expect.Equal(t, b.Origin, checkpoint.Origin(origin), "Verify must set the origin")
			}
		})

		t.Run("returns nil for the checkpoint of each origin of logs that the policy lists out of order",
			func(t *testing.T) {
				t.Parallel()
				p := examplePolicy(t)
				p.Logs = []checkpoint.Log{
					{Key: logSigner(t, "example.com/c").Key()},
					{Key: logSigner(t, "example.com/a").Key()},
					{Key: logSigner(t, "example.com/b").Key()},
				}
				v := mustVerifier(t, p)
				for _, origin := range []note.Name{"example.com/a", "example.com/b", "example.com/c"} {
					_, err := verify(v, checkpointNote(t, origin, "X1", "X2", "Y1"))
					expect.NoError(t, err, "Verify must accept the checkpoint of "+string(origin))
				}
			})

		unknown := []struct {
			name   string
			origin note.Name
		}{
			{name: "returns ErrOrigin for an origin before the first origin of the policy", origin: "example.com/"},
			{name: "returns ErrOrigin for an origin between two origins of the policy", origin: "example.com/ab"},
			{name: "returns ErrOrigin for an origin after the last origin of the policy", origin: "example.com/c"},
		}
		for _, tt := range unknown {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := verify(mustVerifier(t, twoLogs(t)), checkpointNote(t, tt.origin, "X1", "X2", "Y1"))
				assert.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse the origin")
				expect.Equal(t, err.Error(), checkpoint.ErrOrigin.Error()+": "+strconv.Quote(string(tt.origin)),
					"the error must name the origin")
				expect.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			})
		}

		t.Run("returns ErrOrigin for the zero Verifier", func(t *testing.T) {
			t.Parallel()
			var v checkpoint.Verifier
			_, err := verify(&v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			assert.ErrorIs(t, err, checkpoint.ErrOrigin, "the zero Verifier must refuse every origin")
		})

		t.Run("returns ErrThreshold for the checkpoint of one origin that the log of another signed",
			func(t *testing.T) {
				t.Parallel()
				n := checkpointNote(t, "example.com/a", "X1", "X2", "Y1")
				n.Text = []byte("example.com/b\n20852163\n" + exampleRoot + "\n")
				_, err := verify(mustVerifier(t, twoLogs(t)), n)
				assert.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a log signature of another origin")
			})

		t.Run("returns nil for the checkpoint of the first of two logs of one origin", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = append(p.Logs, checkpoint.Log{Key: rotatedSigner(t).Key()})
			_, err := verify(mustVerifier(t, p), checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			assert.NoError(t, err, "Verify must accept the signature of the first log")
		})

		t.Run("returns nil for the checkpoint of the second of two logs of one origin", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = append(p.Logs, checkpoint.Log{Key: rotatedSigner(t).Key()})
			n, err := note.Sign(t.Context(), []byte(exampleCheckpoint), rotatedSigner(t),
				witness(t, exampleWitnesses["X1"]), witness(t, exampleWitnesses["X2"]),
				witness(t, exampleWitnesses["Y1"]))
			assert.NoError(t, err, "note.Sign must sign the checkpoint")
			_, err = verify(mustVerifier(t, p), &n)
			assert.NoError(t, err, "Verify must accept the signature of the second log")
		})

		t.Run("returns nil for a checkpoint with the signature of the log alone under the quorum none",
			func(t *testing.T) {
				t.Parallel()
				p := examplePolicy(t)
				p.Quorum = checkpoint.QuorumNone
				n, err := note.Sign(t.Context(), []byte(exampleCheckpoint), logSigner(t, exampleLog))
				assert.NoError(t, err, "note.Sign must sign the checkpoint")
				_, err = verify(mustVerifier(t, p), &n)
				assert.NoError(t, err, "Verify must accept the log alone under the quorum none")
			})

		t.Run("returns nil for a checkpoint of a policy of one log without witnesses", func(t *testing.T) {
			t.Parallel()
			p := mustParsePolicy(t, []byte("log "+logSigner(t, exampleLog).Key().String()+"\nquorum none\n"))
			n, err := note.Sign(t.Context(), []byte(exampleCheckpoint), logSigner(t, exampleLog))
			assert.NoError(t, err, "note.Sign must sign the checkpoint")
			_, err = verify(mustVerifier(t, p), &n)
			assert.NoError(t, err, "Verify must accept the log of a policy without witnesses")
		})

		t.Run("returns ErrThreshold without the signature of the log under the quorum none", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Quorum = checkpoint.QuorumNone
			_, err := verify(mustVerifier(t, p), cosign(t, exampleCheckpoint, "X1", "X2", "Y1"))
			assert.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a checkpoint that the log did not sign")
		})

		t.Run("returns ErrBody for a note whose text is not a body", func(t *testing.T) {
			t.Parallel()
			n, err := note.Sign(t.Context(), []byte("not a checkpoint\n"), logSigner(t, exampleLog))
			assert.NoError(t, err, "note.Sign must sign the text")
			_, err = verify(mustVerifier(t, examplePolicy(t)), &n)
			assert.ErrorIs(t, err, checkpoint.ErrBody, "Verify must refuse a text that is not a body")
			expect.Equal(t, err.Error(), checkpoint.ErrBody.Error()+
				": a body has three lines or more, and ends in a newline", "Verify must return the error of ParseBody")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		unchanged := []struct {
			give func(t *testing.T) *note.Note
			name string
		}{
			{
				name: "leaves b unchanged for a text that is not a body",
				give: func(t *testing.T) *note.Note {
					t.Helper()
					n, err := note.Sign(t.Context(), []byte("not a checkpoint\n"), logSigner(t, exampleLog))
					assert.NoError(t, err, "note.Sign must sign the text")

					return &n
				},
			},
			{
				name: "leaves b unchanged for an origin of no log",
				give: func(t *testing.T) *note.Note {
					t.Helper()

					return checkpointNote(t, "example.com/c", "X1", "X2", "Y1")
				},
			},
			{
				name: "leaves b unchanged for signatures that do not satisfy the tree",
				give: func(t *testing.T) *note.Note {
					t.Helper()

					return checkpointNote(t, exampleLog, "X1")
				},
			},
		}
		for _, tt := range unchanged {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				want := checkpoint.Body{
					Origin: "before", Size: 1, Root: exampleDigest(t), Extensions: []checkpoint.Extension{"line"},
				}
				b := want
				err := mustVerifier(t, examplePolicy(t)).Verify(tt.give(t), &b)
				assert.HasError(t, err, "Verify must refuse the note")
				assert.Equal(t, b, want, "Verify must leave b unchanged with an error")
			})
		}

		// The goroutines share the Verifier and the pooled buffers of the
		// cosignature Verifiers of its keys.
		t.Run("returns the result of each checkpoint to goroutines that verify at once", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, twoLogs(t))
			accepted := []*note.Note{
				checkpointNote(t, "example.com/a", "X1", "X2", "Y1"),
				checkpointNote(t, "example.com/b", "X2", "X3", "Y3"),
			}
			refused := checkpointNote(t, "example.com/a", "Y1", "Y2", "Y3", "X1")
			outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
				var b checkpoint.Body
				for i := range rounds {
					expect.NoError(t, v.Verify(accepted[(client+i)%len(accepted)], &b),
						"Verify must accept the checkpoint on every goroutine")
					expect.ErrorIs(t, v.Verify(refused, &b), sign.ErrThreshold,
						"Verify must refuse a quorum without two X witnesses on every goroutine")
				}

				return b.Origin, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every goroutine must finish")
			}
		})
	})
}

// TestVerifierAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
// A Verifier of CosignatureV1 keys builds each message in a pooled buffer.
// Each measurement of Verify starts with two collections, which empty the
// pool. The warm-up call of MaxAllocs then grows a buffer, and the measured
// calls reuse it only when the Verifier of each key keeps the growth of the
// buffer and returns it to the pool.
//
//nolint:paralleltest // see above
func TestVerifierAllocs(t *testing.T) {
	p := mustParsePolicy(t, []byte(oneGroupPolicyText(t)))
	v := mustVerifier(t, p)
	n := checkpointNote(t, exampleLog, "X1", "X2")

	t.Run("NewVerifier", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = checkpoint.NewVerifier(p, resolver) }, newVerifierAllocs,
			"NewVerifier must allocate the memory of the Verifier at the size of the policy")
		assert.NoError(t, err, "the test must measure a policy that NewVerifier accepts")
	})

	t.Run("Reset", func(t *testing.T) {
		t.Run("of the policy of the Reset before", func(t *testing.T) {
			reused := mustVerifier(t, p)

			var err error
			expect.MaxAllocs(t, func() { err = reused.Reset(p, resolver) }, 0,
				"Reset must not allocate for the policy of the Reset before")
			assert.NoError(t, err, "the test must measure a policy that Reset accepts")
		})

		t.Run("of the policy of a new Verifier", func(t *testing.T) {
			var err error
			expect.MaxAllocsWithSetup(t, func() *checkpoint.Verifier { return mustVerifier(t, p) },
				func(fresh *checkpoint.Verifier) { err = fresh.Reset(p, resolver) }, 0,
				"Reset must not allocate for the policy that NewVerifier sized the Verifier for")
			assert.NoError(t, err, "the test must measure a policy that Reset accepts")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Run("into a Body of the checkpoint", func(t *testing.T) {
			var body checkpoint.Body
			assert.NoError(t, v.Verify(n, &body), "Verify must accept the checkpoint")

			var err error
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { err = v.Verify(n, &body) }, 0, "Verify must not allocate")
			assert.NoError(t, err, "the test must measure a checkpoint that Verify accepts")
		})

		t.Run("into a new Body", func(t *testing.T) {
			var err error
			runtime.GC()
			runtime.GC()
			expect.MaxAllocsWithSetup(t, func() *checkpoint.Body { return &checkpoint.Body{} },
				func(b *checkpoint.Body) { err = v.Verify(n, b) }, 0,
				"Verify must set the origin of the tree in a new Body without an allocation")
			assert.NoError(t, err, "the test must measure a checkpoint that Verify accepts")
		})

		t.Run("of ML-DSA-44 cosignatures", func(t *testing.T) {
			pq, cosigned := pqCheckpoint(t)

			var body checkpoint.Body
			assert.NoError(t, pq.Verify(cosigned, &body), "Verify must accept the checkpoint")

			var err error
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { err = pq.Verify(cosigned, &body) }, 0, "Verify must not allocate")
			assert.NoError(t, err, "the test must measure a checkpoint that Verify accepts")
		})
	})
}

// BenchmarkVerifier reports the cost of each function and method, and
// fails above the allocations that their contracts state.
func BenchmarkVerifier(b *testing.B) {
	p := mustParsePolicy(b, []byte(oneGroupPolicyText(b)))
	v := mustVerifier(b, p)
	n := checkpointNote(b, exampleLog, "X1", "X2")

	b.Run("NewVerifier", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(newVerifierAllocs)
		defer c.End()

		for c.Loop() {
			_, err = checkpoint.NewVerifier(p, resolver)
		}

		assert.NoError(b, err, "the benchmark must measure a policy that NewVerifier accepts")
	})

	b.Run("Reset", func(b *testing.B) {
		b.Run("of the policy of the Reset before", func(b *testing.B) {
			reused := mustVerifier(b, p)

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Reset(p, resolver)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that Reset accepts")
		})

		b.Run("of the policy of a new Verifier", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				var fresh *checkpoint.Verifier
				c.Excluding(func() { fresh = mustVerifier(b, p) })
				err = fresh.Reset(p, resolver)
			}

			assert.NoError(b, err, "the benchmark must measure a policy that Reset accepts")
		})
	})

	b.Run("Verify", func(b *testing.B) {
		b.Run("into a Body of the checkpoint", func(b *testing.B) {
			var body checkpoint.Body
			assert.NoError(b, v.Verify(n, &body), "Verify must accept the checkpoint")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = v.Verify(n, &body)
			}

			assert.NoError(b, err, "the benchmark must measure a checkpoint that Verify accepts")
		})

		b.Run("into a new Body", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				var body *checkpoint.Body
				c.Excluding(func() { body = &checkpoint.Body{} })
				err = v.Verify(n, body)
			}

			assert.NoError(b, err, "the benchmark must measure a checkpoint that Verify accepts")
		})

		b.Run("of ML-DSA-44 cosignatures", func(b *testing.B) {
			pq, cosigned := pqCheckpoint(b)

			var body checkpoint.Body
			assert.NoError(b, pq.Verify(cosigned, &body), "Verify must accept the checkpoint")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = pq.Verify(cosigned, &body)
			}

			assert.NoError(b, err, "the benchmark must measure a checkpoint that Verify accepts")
		})
	})
}

// verify returns the body that v.Verify sets for n in a new Body, and the
// error of Verify.
func verify(v *checkpoint.Verifier, n *note.Note) (checkpoint.Body, error) {
	var b checkpoint.Body
	err := v.Verify(n, &b)

	return b, err
}

// mustVerifier returns the Verifier of p with resolver, and fails the test
// when NewVerifier refuses p.
func mustVerifier(tb testing.TB, p *checkpoint.Policy) *checkpoint.Verifier {
	tb.Helper()

	v, err := checkpoint.NewVerifier(p, resolver)
	assert.NoError(tb, err, "NewVerifier must accept the policy")

	return v
}

// countingResolver returns the entries of resolver, each of which adds the
// Verifier that it builds to calls.
func countingResolver(calls *atomic.Int64) note.Resolver {
	r := make(note.Resolver, len(resolver))
	for typ, entry := range resolver {
		r[typ] = func(k note.Key) (note.Verifier, error) {
			calls.Add(1)

			return entry(k)
		}
	}

	return r
}

// checkpointNote returns the checkpoint of origin, of the size and the
// root of the example of tlog-checkpoint, signed by the log of that name
// and cosigned by each witness of the example of tlog-policy that names
// lists.
func checkpointNote(tb testing.TB, origin note.Name, names ...checkpoint.PolicyName) *note.Note {
	tb.Helper()

	n := cosign(tb, string(origin)+"\n20852163\n"+exampleRoot+"\n", names...)

	signed, err := note.Sign(tb.Context(), n.Text, logSigner(tb, origin))
	assert.NoError(tb, err, "note.Sign must sign the checkpoint")

	n.Signatures = append(signed.Signatures, n.Signatures...)

	return n
}

// twoLogs returns the example of tlog-policy with the logs example.com/a
// and example.com/b in place of its log.
func twoLogs(tb testing.TB) *checkpoint.Policy {
	tb.Helper()

	p := examplePolicy(tb)
	p.Logs = []checkpoint.Log{
		{Key: logSigner(tb, "example.com/a").Key()},
		{Key: logSigner(tb, "example.com/b").Key()},
	}

	return p
}

// rotatedSigner returns the note Signer of type 0x01 of a second log named
// exampleLog, over another Ed25519 key, as a log has after a key rotation.
func rotatedSigner(tb testing.TB) *note.TextSigner {
	tb.Helper()

	s, err := note.NewTextSigner(exampleLog, note.TypeEd25519, ed25519Signer(tb, exampleLog+" rotated"))
	assert.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

	return s
}

// nestedPolicy returns a policy of the log of the example of tlog-policy
// whose quorum is a chain of depth groups: the first group lists the
// witness X1, and each later group lists the group before it.
func nestedPolicy(tb testing.TB, depth int) *checkpoint.Policy {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logSigner(tb, exampleLog).Key().String() + "\n")
	s.WriteString("witness g0 " + witness(tb, exampleWitnesses["X1"]).Key().String() + "\n")

	for i := range depth {
		s.WriteString("group g" + strconv.Itoa(i+1) + " 1 g" + strconv.Itoa(i) + "\n")
	}

	s.WriteString("quorum g" + strconv.Itoa(depth) + "\n")

	return mustParsePolicy(tb, []byte(s.String()))
}

// pqCheckpoint returns the Verifier of a policy of the log of the example
// of tlog-policy and a quorum of 2 of the ML-DSA-44 witnesses P1, P2 and
// P3 of type 0x06, and a checkpoint of that log that P1 and P2 cosigned.
func pqCheckpoint(tb testing.TB) (*checkpoint.Verifier, *note.Note) {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logSigner(tb, exampleLog).Key().String() + "\n")

	names := []checkpoint.PolicyName{"P1", "P2", "P3"}
	signers := make([]note.Signer, 0, 1+len(names))
	signers = append(signers, logSigner(tb, exampleLog))

	for _, name := range names {
		w, err := checkpoint.NewSubtreeV1Signer(note.Name("example.com/"+name), checkpoint.TypeMLDSA44Cosignature,
			mldsaSigner(tb, mldsa.MLDSA44, string(name), ""), fake.New(clockTime), time.Second)
		assert.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")
		s.WriteString("witness " + string(name) + " " + w.Key().String() + "\n")
		signers = append(signers, w)
	}

	s.WriteString("group P 2 P1 P2 P3\nquorum P\n")

	n, err := note.Sign(tb.Context(), []byte(exampleCheckpoint), signers[:3]...)
	assert.NoError(tb, err, "note.Sign must sign the checkpoint")

	return mustVerifier(tb, mustParsePolicy(tb, []byte(s.String()))), &n
}

// oneGroupPolicyText returns a policy of the log of the example of
// tlog-policy and a quorum of 2 of the witnesses X1, X2 and X3.
func oneGroupPolicyText(tb testing.TB) string {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logSigner(tb, exampleLog).Key().String() + "\n")

	for _, n := range xNames {
		s.WriteString("witness " + string(n) + " " + witness(tb, exampleWitnesses[n]).Key().String() + "\n")
	}

	s.WriteString("group X 2 X1 X2 X3\nquorum X\n")

	return s.String()
}
