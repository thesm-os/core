// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"testing"

	"go.dokimi.dev/assert"

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

				_, _, err := newServer(t, f.config()).Advance(bounded(t), l.notes[5],
					[]witness.Update{l.update(t, 0, 5)}, nil)
				assert.ErrorIs(t, err, witness.ErrConfig, "Advance must refuse the Log")
			})
		}

		t.Run("commits a note of the keys of either key set", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{
				Hasher: l.log.Hasher,
				Keys:   [][]note.Key{{rotated.signer.Key()}, {l.signer.Key()}},
			}

			assert.NotEmpty(t, advance(t, newServer(t, f.config()), l, l.update(t, 0, 5)),
				"Advance must accept the second key set")
		})

		t.Run("commits a note of the keys of the first key set", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{
				Hasher: l.log.Hasher,
				Keys:   [][]note.Key{{l.signer.Key()}, {rotated.signer.Key()}},
			}

			assert.NotEmpty(t, advance(t, newServer(t, f.config()), l, l.update(t, 0, 5)),
				"Advance must accept the first key set")
		})

		// line returns the signature line of the name and the key ID of a
		// key, whose value is 64 zero bytes, a signature that no key made.
		line := func(name note.Name, id uint32) []byte {
			out, _ := note.Signature{Name: name, ID: id, Value: make([]byte, 64)}.AppendText(nil)

			return out
		}

		key := l.signer.Key()
		lines := []struct {
			name string
			keys [][]note.Key
			give []byte
			want error
		}{
			{
				name: "returns ErrSignature for a note without a line of every key of its key set",
				keys: [][]note.Key{{rotated.signer.Key(), key}},
				want: witness.ErrSignature,
			},
			{
				name: "returns ErrSignature for an invalid line of a key beside the line of another key set",
				keys: [][]note.Key{{rotated.signer.Key()}, {key}},
				give: line(rotated.signer.Key().Name, rotated.signer.Key().ID()),
				want: witness.ErrSignature,
			},
			{
				name: "commits a note with a line of another name and the key ID of a key",
				keys: [][]note.Key{{key}},
				give: line("example.com/other", key.ID()),
			},
			{
				name: "commits a note with a line of the name of a key and another key ID",
				keys: [][]note.Key{{key}},
				give: line(key.Name, key.ID()+1),
			},
		}
		for _, tt := range lines {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				f.accepted[l.origin] = witness.Log{Hasher: l.log.Hasher, Keys: tt.keys}

				msg := append(append([]byte(nil), l.notes[5]...), tt.give...)
				_, _, err := newServer(t, f.config()).Advance(bounded(t), msg, []witness.Update{l.update(t, 0, 5)}, nil)
				assert.ErrorIs(t, err, tt.want, "Advance must check the lines of the keys of the Log alone")
			})
		}
	})
}
