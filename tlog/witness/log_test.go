// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/witness"
)

func TestLog(t *testing.T) {
	t.Parallel()

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)
		rotated := newTestLog(t, "example.com/rotated")

		tests := []struct {
			give witness.Log
			name string
		}{
			{name: "returns ErrConfig for a Log without a key set", give: witness.Log{Hasher: l.log.Hasher}},
			{
				name: "returns ErrConfig for a key set without a key",
				give: witness.Log{Hasher: l.log.Hasher, Keys: [][]note.Key{{}}},
			},
			{
				name: "returns ErrConfig for a key that is not Valid",
				give: witness.Log{Hasher: l.log.Hasher, Keys: [][]note.Key{{{Name: "a"}}}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				f.accepted[l.origin] = tt.give

				_, _, err := newServer(t, f.config()).Advance(t.Context(), l.notes[5],
					[]witness.Update{l.update(t, 0, 5)}, nil)
				testkit.ErrorIs(t, err, witness.ErrConfig, "Advance must refuse the Log")
			})
		}

		t.Run("commits a note of the keys of either key set", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{
				Hasher: l.log.Hasher,
				Keys:   [][]note.Key{{rotated.signer.Key()}, {l.signer.Key()}},
			}

			testkit.NotEqual(t, len(advance(t, newServer(t, f.config()), l, l.update(t, 0, 5))), 0,
				"Advance must accept the second key set")
		})
	})
}
