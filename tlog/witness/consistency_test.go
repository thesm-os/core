// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"crypto/sha256"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

func TestConsistency(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)
		other := crypto.NewDigest256(sha256.Sum256([]byte("another root")))

		t.Run("commits a tree of size 0 with the root of the empty tree", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			testkit.NotEqual(t, len(advance(t, s, l, l.update(t, 0, 0))), 0, "Advance must cosign the empty tree")
		})

		tests := []struct {
			give func(tb testing.TB) witness.Update
			name string
			base uint64
		}{
			{
				name: "returns ErrInconsistent for a tree of size 0 with another root",
				give: func(testing.TB) witness.Update {
					return witness.Update{Body: checkpoint.Body{Origin: l.origin, Size: 0, Root: other}}
				},
			},
			{
				name: "returns ErrInconsistent for a proof from the old size 0",
				give: func(tb testing.TB) witness.Update {
					tb.Helper()

					u := l.update(tb, 0, 5)
					u.Proof = l.update(tb, 3, 5).Proof

					return u
				},
			},
			{
				name: "returns ErrInconsistent for a proof that does not verify", base: 3,
				give: func(tb testing.TB) witness.Update {
					tb.Helper()

					u := l.update(tb, 3, 5)
					u.Proof = append([]crypto.Digest(nil), u.Proof...)
					u.Proof[0] = other

					return u
				},
			},
			{
				name: "returns ErrInconsistent for an equal size with another root", base: 5,
				give: func(testing.TB) witness.Update {
					return witness.Update{Body: checkpoint.Body{Origin: l.origin, Size: 5, Root: other}, OldSize: 5}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := newServer(t, newFixture(t, l).config())
				if tt.base > 0 {
					advance(t, s, l, l.update(t, 0, tt.base))
				}

				u := tt.give(t)
				_, failures, err := s.Advance(bounded(t), signBody(t, u.Body, l.signer), []witness.Update{u}, nil)
				testkit.NoError(t, err, "Advance must check the update")
				testkit.Len(t, failures, 1, "Advance must return the failure")
				testkit.ErrorIs(t, failures[0].Err, witness.ErrInconsistent, "the failure must be ErrInconsistent")
			})
		}
	})
}
