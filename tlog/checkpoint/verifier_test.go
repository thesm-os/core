// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// newVerifierAllocs is the number of allocations of NewVerifier for the
// policy of oneGroupPolicyText, with one log and three witnesses: the
// Verifier, its Keyring, rules, order and trees, two for the Verifier of
// each key, and four for the sign.Policy of the origin.
const newVerifierAllocs = 18

func TestVerifier(t *testing.T) {
	t.Parallel()

	t.Run("NewVerifier", func(t *testing.T) {
		t.Parallel()

		t.Run("accepts a quorum of 62 nested groups", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewVerifier(nestedPolicy(t, 62), resolver())
			testkit.NoError(t, err, "NewVerifier must accept a tree of 64 rules of depth")
		})

		t.Run("returns the error of sign.Policy.Reset for a quorum of 63 nested groups", func(t *testing.T) {
			t.Parallel()
			v, err := checkpoint.NewVerifier(nestedPolicy(t, 63), resolver())
			testkit.ErrorIs(t, err, sign.ErrPolicy, "NewVerifier must return the error of sign.Policy.Reset")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, v == nil, "NewVerifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrPolicy for a policy without logs", func(t *testing.T) {
			t.Parallel()
			text := "witness a " + witnessKey(t, "example.com/w").String() + "\nquorum a\n"
			v, err := checkpoint.NewVerifier(mustParsePolicy(t, []byte(text)), resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse a policy without logs")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, v == nil, "NewVerifier must return a nil Verifier with an error")
		})

		t.Run("returns ErrPolicy for a Policy that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewVerifier(&checkpoint.Policy{}, resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse the zero Policy")
		})

		t.Run("returns ErrPolicy for a log that is not Valid", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = append(p.Logs, checkpoint.Log{})
			_, err := checkpoint.NewVerifier(p, resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "NewVerifier must refuse a log without a key")
			testkit.Contains(t, err.Error(), "a log with an invalid key", "the error must name the rule")
		})

		t.Run("returns the error of the resolver for a log key", func(t *testing.T) {
			t.Parallel()
			r := resolver()
			delete(r, note.TypeEd25519)
			_, err := checkpoint.NewVerifier(examplePolicy(t), r)
			testkit.ErrorIs(t, err, note.ErrUnknownType, "NewVerifier must return the error of the resolver")
		})

		t.Run("returns the error of the resolver for a witness key", func(t *testing.T) {
			t.Parallel()
			r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
			_, err := checkpoint.NewVerifier(examplePolicy(t), r)
			testkit.ErrorIs(t, err, note.ErrUnknownType, "NewVerifier must return the error of the resolver")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets v to the Verifier of another policy", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			testkit.NoError(t, v.Reset(twoLogs(t), resolver()), "Reset must accept the policy")
			_, err := verify(v, checkpointNote(t, "example.com/b", "X1", "X2", "Y1"))
			testkit.NoError(t, err, "Verify must accept a checkpoint of the new policy")
			_, err = verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse an origin of the policy before")
		})

		t.Run("sets a zero Verifier to the Verifier of a policy", func(t *testing.T) {
			t.Parallel()
			var v checkpoint.Verifier
			testkit.NoError(t, v.Reset(examplePolicy(t), resolver()), "Reset must accept the policy")
			_, err := verify(&v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.NoError(t, err, "Verify must accept the checkpoint")
		})

		t.Run("builds the Verifier of each key once over the Resets of one policy", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			r := countingResolver(calls)
			v := &checkpoint.Verifier{}
			for range 3 {
				testkit.NoError(t, v.Reset(examplePolicy(t), r), "Reset must accept the policy")
			}
			testkit.Equal(t, calls.Load(), int64(7), "the resolver must build the Verifier of each of 7 keys once")
			_, err := verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.NoError(t, err, "Verify must accept the checkpoint")
		})

		t.Run("removes every tree with an error", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			err := v.Reset(&checkpoint.Policy{}, resolver())
			testkit.ErrorIs(t, err, checkpoint.ErrPolicy, "Reset must refuse the zero Policy")
			_, err = verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse every origin after an error")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("sets b to the body of the checkpoint that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, mustParsePolicy(t, readFile(t, policyFile)))
			got, err := verify(v, mustParse(t, readFile(t, cosignedFile)))
			testkit.NoError(t, err, "Verify must accept the checkpoint of the vectors")
			testkit.Equal(t, got, checkpoint.Body{Origin: exampleLog, Size: exampleSize, Root: exampleDigest(t)},
				"Verify must set the body")
		})

		t.Run("sets the extension lines of a checkpoint that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			text := "log " + vectorKey(t, vectorLog).String() + "\nwitness W1 " + vectorKey(t, vectorW1).String() +
				"\nquorum W1\n"
			got, err := verify(
				mustVerifier(t, mustParsePolicy(t, []byte(text))),
				mustParse(t, readFile(t, extensionFile)),
			)
			testkit.NoError(t, err, "Verify must accept the checkpoint with an extension line")
			testkit.Equal(t, got.Extensions, []checkpoint.Extension{"an extension line"}, "Verify must set the line")
		})

		t.Run("accepts the example of tlog-policy with two X witnesses and one Y witness", func(t *testing.T) {
			t.Parallel()
			_, err := verify(mustVerifier(t, examplePolicy(t)), checkpointNote(t, exampleLog, "X1", "X2", "Y3"))
			testkit.NoError(t, err, "Verify must accept the log and the quorum")
		})

		t.Run("returns ErrThreshold for three Y witnesses and one X witness", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			_, err := verify(v, checkpointNote(t, exampleLog, "Y1", "Y2", "Y3", "X1"))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a quorum without two X witnesses")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})

		t.Run("returns ErrThreshold for a checkpoint without the signature of the log", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, examplePolicy(t))
			_, err := verify(v, cosign(t, checkpointText(exampleLog), "X1", "X2", "Y1"))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a checkpoint that the log did not sign")
		})

		t.Run("sets one Body to the checkpoints of two origins in turn", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, twoLogs(t))
			var b checkpoint.Body
			for _, origin := range []note.Name{"example.com/a", "example.com/b", "example.com/a"} {
				err := v.Verify(checkpointNote(t, origin, "X1", "X2", "Y1"), &b)
				testkit.NoError(t, err, "Verify must accept the checkpoint of "+string(origin))
				testkit.Equal(t, b.Origin, checkpoint.Origin(origin), "Verify must set the origin")
			}
		})

		t.Run("accepts the checkpoint of each origin of logs that the policy lists out of order", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = []checkpoint.Log{
				{Key: logKey(t, "example.com/c")}, {Key: logKey(t, "example.com/a")}, {Key: logKey(t, "example.com/b")},
			}
			v := mustVerifier(t, p)
			for _, origin := range []note.Name{"example.com/a", "example.com/b", "example.com/c"} {
				_, err := verify(v, checkpointNote(t, origin, "X1", "X2", "Y1"))
				testkit.NoError(t, err, "Verify must accept the checkpoint of "+string(origin))
			}
		})

		t.Run("returns ErrOrigin for a checkpoint of a third origin", func(t *testing.T) {
			t.Parallel()
			v := mustVerifier(t, twoLogs(t))
			for _, origin := range []note.Name{"example.com/", "example.com/ab", "example.com/c"} {
				_, err := verify(v, checkpointNote(t, origin, "X1", "X2", "Y1"))
				testkit.ErrorIs(t, err, checkpoint.ErrOrigin, "Verify must refuse the origin "+string(origin))
				testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			}
		})

		t.Run("returns ErrOrigin for the zero Verifier", func(t *testing.T) {
			t.Parallel()
			var v checkpoint.Verifier
			_, err := verify(&v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.ErrorIs(t, err, checkpoint.ErrOrigin, "the zero Verifier must refuse every origin")
		})

		t.Run(
			"returns ErrThreshold for the checkpoint of one origin that the log of another signed",
			func(t *testing.T) {
				t.Parallel()
				v := mustVerifier(t, twoLogs(t))
				n := checkpointNote(t, "example.com/a", "X1", "X2", "Y1")
				n.Text = []byte(checkpointText("example.com/b"))
				_, err := verify(v, n)
				testkit.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a log signature of another origin")
			},
		)

		t.Run("accepts the checkpoint of either of two logs of one origin", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Logs = append(p.Logs, checkpoint.Log{Key: rotatedSigner(t).Key()})
			v := mustVerifier(t, p)
			_, err := verify(v, checkpointNote(t, exampleLog, "X1", "X2", "Y1"))
			testkit.NoError(t, err, "Verify must accept the signature of the first log")
			n, err := note.Sign(t.Context(), []byte(checkpointText(exampleLog)), rotatedSigner(t),
				witness(t, xWitnesses["X1"]), witness(t, xWitnesses["X2"]), witness(t, yWitnesses["Y1"]))
			testkit.NoError(t, err, "note.Sign must sign the checkpoint")
			_, err = verify(v, &n)
			testkit.NoError(t, err, "Verify must accept the signature of the second log")
		})

		t.Run("accepts a checkpoint with the signature of the log alone under the quorum none", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Quorum = checkpoint.QuorumNone
			n, err := note.Sign(t.Context(), []byte(checkpointText(exampleLog)), logSigner(t, exampleLog))
			testkit.NoError(t, err, "note.Sign must sign the checkpoint")
			_, err = verify(mustVerifier(t, p), &n)
			testkit.NoError(t, err, "Verify must accept the log alone under the quorum none")
		})

		t.Run("accepts a checkpoint of a policy of one log without witnesses", func(t *testing.T) {
			t.Parallel()
			p := mustParsePolicy(t, []byte("log "+logKey(t, exampleLog).String()+"\nquorum none\n"))
			n, err := note.Sign(t.Context(), []byte(checkpointText(exampleLog)), logSigner(t, exampleLog))
			testkit.NoError(t, err, "note.Sign must sign the checkpoint")
			_, err = verify(mustVerifier(t, p), &n)
			testkit.NoError(t, err, "Verify must accept the log of a policy without witnesses")
		})

		t.Run("returns ErrThreshold without the signature of the log under the quorum none", func(t *testing.T) {
			t.Parallel()
			p := examplePolicy(t)
			p.Quorum = checkpoint.QuorumNone
			_, err := verify(mustVerifier(t, p), cosign(t, checkpointText(exampleLog), "X1", "X2", "Y1"))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Verify must refuse a checkpoint that the log did not sign")
		})

		t.Run("returns ErrBody for a note whose text is not a body", func(t *testing.T) {
			t.Parallel()
			n, err := note.Sign(t.Context(), []byte("not a checkpoint\n"), logSigner(t, exampleLog))
			testkit.NoError(t, err, "note.Sign must sign the text")
			_, err = verify(mustVerifier(t, examplePolicy(t)), &n)
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "Verify must refuse a text that is not a body")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		unchanged := []struct {
			name string
			give func(t *testing.T) *note.Note
		}{
			{
				name: "leaves b unchanged for a text that is not a body",
				give: func(t *testing.T) *note.Note {
					t.Helper()
					n, err := note.Sign(t.Context(), []byte("not a checkpoint\n"), logSigner(t, exampleLog))
					testkit.NoError(t, err, "note.Sign must sign the text")

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
					Origin: "before", Size: 1, Root: exampleDigest(t),
					Extensions: []checkpoint.Extension{"line"},
				}
				b := want
				err := mustVerifier(t, examplePolicy(t)).Verify(tt.give(t), &b)
				testkit.Error(t, err, "Verify must refuse the note")
				testkit.Equal(t, b, want, "Verify must leave b unchanged with an error")
			})
		}
	})
}

func BenchmarkVerifier(b *testing.B) {
	p := mustParsePolicy(b, []byte(oneGroupPolicyText(b)))
	r := resolver()
	v := mustVerifier(b, p)
	n := checkpointNote(b, exampleLog, "X1", "X2")

	b.Run("NewVerifier", func(b *testing.B) {
		benchAllocs(b, newVerifierAllocs, func() { sinkChecker, errSink = checkpoint.NewVerifier(p, r) })
	})

	b.Run("Reset", func(b *testing.B) {
		reused := mustVerifier(b, p)
		benchZeroAlloc(b, func() { errSink = reused.Reset(p, r) })
	})

	b.Run("Verify", func(b *testing.B) {
		var body checkpoint.Body
		testkit.NoError(b, v.Verify(n, &body), "Verify must accept the checkpoint")
		benchZeroAlloc(b, func() { errSink = v.Verify(n, &body) })
	})

	b.Run("Verify of ML-DSA-44 cosignatures", func(b *testing.B) {
		pq, cosigned := pqCheckpoint(b)
		var body checkpoint.Body
		testkit.NoError(b, pq.Verify(cosigned, &body), "Verify must accept the checkpoint")
		benchZeroAlloc(b, func() { errSink = pq.Verify(cosigned, &body) })
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

	v, err := checkpoint.NewVerifier(p, resolver())
	testkit.NoError(tb, err, "NewVerifier must accept the policy")

	return v
}

// countingResolver returns the resolver of resolver whose entries add each
// Verifier that they build to calls.
func countingResolver(calls *atomic.Int64) note.Resolver {
	r := resolver()
	for typ, entry := range r {
		r[typ] = func(k note.Key) (note.Verifier, error) {
			calls.Add(1)

			return entry(k)
		}
	}

	return r
}

// checkpointText returns the text of a checkpoint of origin, of the size
// and root of the example of tlog-checkpoint.
func checkpointText(origin note.Name) string {
	return string(origin) + "\n20852163\n" + exampleRoot + "\n"
}

// checkpointNote returns the checkpoint of origin, signed by the log of
// that name and cosigned by each witness of the example of tlog-policy
// that names lists.
func checkpointNote(tb testing.TB, origin note.Name, names ...checkpoint.PolicyName) *note.Note {
	tb.Helper()

	n := cosign(tb, checkpointText(origin), names...)

	signed, err := note.Sign(tb.Context(), n.Text, logSigner(tb, origin))
	testkit.NoError(tb, err, "note.Sign must sign the checkpoint")

	n.Signatures = append(signed.Signatures, n.Signatures...)

	return n
}

// twoLogs returns the example of tlog-policy with the logs example.com/a
// and example.com/b in place of its log.
func twoLogs(tb testing.TB) *checkpoint.Policy {
	tb.Helper()

	p := examplePolicy(tb)
	p.Logs = []checkpoint.Log{{Key: logKey(tb, "example.com/a")}, {Key: logKey(tb, "example.com/b")}}

	return p
}

// rotatedSigner returns the note Signer of type 0x01 of a second log named
// exampleLog, over another Ed25519 key, as a log has after a key rotation.
func rotatedSigner(tb testing.TB) note.Signer {
	tb.Helper()

	s, err := note.NewTextSigner(exampleLog, note.TypeEd25519, ed25519Signer(tb, exampleLog+" rotated"))
	testkit.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

	return s
}

// nestedPolicy returns a policy of the log of the example of tlog-policy
// whose quorum is a chain of depth groups: the first group lists the
// witness X1, and each later group lists the group before it.
func nestedPolicy(tb testing.TB, depth int) *checkpoint.Policy {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logKey(tb, exampleLog).String() + "\n")
	s.WriteString("witness g0 " + witnessKey(tb, xWitnesses["X1"]).String() + "\n")

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

	s.WriteString("log " + logKey(tb, exampleLog).String() + "\n")

	names := []checkpoint.PolicyName{"P1", "P2", "P3"}
	signers := make([]note.Signer, 0, 1+len(names))
	signers = append(signers, logSigner(tb, exampleLog))

	for _, name := range names {
		w, err := checkpoint.NewSubtreeV1Signer(note.Name("example.com/"+name), checkpoint.TypeMLDSA44Cosignature,
			mldsaSigner(tb, mldsa.MLDSA44, string(name), ""), fake.New(clockTime), time.Second)
		testkit.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")
		s.WriteString("witness " + string(name) + " " + w.Key().String() + "\n")
		signers = append(signers, w)
	}

	s.WriteString("group P 2 P1 P2 P3\nquorum P\n")

	n, err := note.Sign(tb.Context(), []byte(checkpointText(exampleLog)), signers[:3]...)
	testkit.NoError(tb, err, "note.Sign must sign the checkpoint")

	return mustVerifier(tb, mustParsePolicy(tb, []byte(s.String()))), &n
}

// oneGroupPolicyText returns a policy of the log of the example of
// tlog-policy and a quorum of 2 of the witnesses X1, X2 and X3.
func oneGroupPolicyText(tb testing.TB) string {
	tb.Helper()

	var s strings.Builder

	s.WriteString("log " + logKey(tb, exampleLog).String() + "\n")

	for _, n := range []checkpoint.PolicyName{"X1", "X2", "X3"} {
		s.WriteString("witness " + string(n) + " " + witnessKey(tb, xWitnesses[n]).String() + "\n")
	}

	s.WriteString("group X 2 X1 X2 X3\nquorum X\n")

	return s.String()
}
