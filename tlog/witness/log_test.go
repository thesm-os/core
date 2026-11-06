// Copyright ThesmOS B.V. 2026
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
		key := l.signer.Key()

		// renamed is the public key of key under another key name.
		renamed := note.Key{Name: "example.com/other", Type: key.Type, PublicKey: key.PublicKey}

		tests := []struct {
			give witness.Log
			name string
		}{
			{name: "returns ErrConfig for a Log without a key set", give: witness.Log{Hasher: l.log.Hasher}},
			{
				name: "returns ErrConfig for a key set without a party",
				give: witness.Log{Hasher: l.log.Hasher, Keys: []witness.KeySet{{}}},
			},
			{
				name: "returns ErrConfig for a party without a key",
				give: witness.Log{Hasher: l.log.Hasher, Keys: []witness.KeySet{{Parties: [][]note.Key{{}}}}},
			},
			{
				name: "returns ErrConfig for a key that is not Valid",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{{Name: "a"}}}}},
				},
			},
			{
				name: "returns ErrConfig for a negative threshold",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{key}}, Threshold: -1}},
				},
			},
			{
				name: "returns ErrConfig for a threshold above the number of parties",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{key}}, Threshold: 2}},
				},
			},
			{
				name: "returns ErrConfig for a public key of two parties of a key set",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{key}, {key}}, Threshold: 1}},
				},
			},
			{
				name: "returns ErrConfig for a public key that a party lists twice",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{key, key}}}},
				},
			},
			{
				name: "returns ErrConfig for a public key under two key names in a key set",
				give: witness.Log{
					Hasher: l.log.Hasher,
					Keys:   []witness.KeySet{{Parties: [][]note.Key{{renamed}, {key}}, Threshold: 1}},
				},
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
				Keys: []witness.KeySet{
					{Parties: [][]note.Key{{rotated.signer.Key()}}},
					{Parties: [][]note.Key{{key}}},
				},
			}

			assert.NotEmpty(t, advance(t, newServer(t, f.config()), l, l.update(t, 0, 5)),
				"Advance must accept the second key set")
		})

		t.Run("commits a note of the keys of the first key set", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{
				Hasher: l.log.Hasher,
				Keys: []witness.KeySet{
					{Parties: [][]note.Key{{key}}},
					{Parties: [][]note.Key{{rotated.signer.Key()}}},
				},
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

		// custodian returns a signer of the origin of l over the Ed25519 key
		// of label.
		custodian := func(label string) *note.TextSigner {
			s, err := note.NewTextSigner(note.Name(l.origin), note.TypeEd25519, ed25519Signer(t, label))
			assert.NoError(t, err, "NewTextSigner must accept the key")

			return s
		}

		a, b, c := l.signer, custodian("custodian b"), custodian("custodian c")
		d, e := custodian("custodian d"), custodian("custodian e")

		// twoOfThree and threeOfThree are the key sets of a rotation from
		// the custodians a, b and c to the custodians a, d and e.
		twoOfThree := witness.KeySet{Parties: [][]note.Key{{a.Key()}, {b.Key()}, {c.Key()}}, Threshold: 2}
		threeOfThree := witness.KeySet{Parties: [][]note.Key{{a.Key()}, {d.Key()}, {e.Key()}}, Threshold: 3}

		// everyParty requires the custodians a, b and c, and hybrid requires
		// a party of the keys of a and b, or the party of c.
		everyParty := witness.KeySet{Parties: [][]note.Key{{a.Key()}, {b.Key()}, {c.Key()}}}
		hybrid := witness.KeySet{Parties: [][]note.Key{{a.Key(), b.Key()}, {c.Key()}}, Threshold: 1}

		lines := []struct {
			name        string
			giveKeys    []witness.KeySet
			giveSigners []note.Signer
			giveLine    []byte
			want        error
		}{
			{
				name:        "returns ErrSignature for a note without a line of every key of its party",
				giveKeys:    []witness.KeySet{{Parties: [][]note.Key{{rotated.signer.Key(), key}}}},
				giveSigners: []note.Signer{a},
				want:        witness.ErrSignature,
			},
			{
				name: "returns ErrSignature for an invalid line of a key beside the line of another key set",
				giveKeys: []witness.KeySet{
					{Parties: [][]note.Key{{rotated.signer.Key()}}},
					{Parties: [][]note.Key{{key}}},
				},
				giveSigners: []note.Signer{a},
				giveLine:    line(rotated.signer.Key().Name, rotated.signer.Key().ID()),
				want:        witness.ErrSignature,
			},
			{
				name:        "commits a note with a line of another name and the key ID of a key",
				giveKeys:    []witness.KeySet{{Parties: [][]note.Key{{key}}}},
				giveSigners: []note.Signer{a},
				giveLine:    line("example.com/other", key.ID()),
			},
			{
				name:        "commits a note with a line of the name of a key and another key ID",
				giveKeys:    []witness.KeySet{{Parties: [][]note.Key{{key}}}},
				giveSigners: []note.Signer{a},
				giveLine:    line(key.Name, key.ID()+1),
			},
			{
				name:        "commits a note of the threshold of parties of a key set",
				giveKeys:    []witness.KeySet{twoOfThree},
				giveSigners: []note.Signer{a, b},
			},
			{
				name:        "returns ErrSignature for a note of fewer parties than the threshold",
				giveKeys:    []witness.KeySet{twoOfThree},
				giveSigners: []note.Signer{a},
				want:        witness.ErrSignature,
			},
			{
				name:        "returns ErrSignature for a note without a line of every party of a zero threshold",
				giveKeys:    []witness.KeySet{everyParty},
				giveSigners: []note.Signer{a, b},
				want:        witness.ErrSignature,
			},
			{
				name:        "commits a note of every party of a zero threshold",
				giveKeys:    []witness.KeySet{everyParty},
				giveSigners: []note.Signer{a, b, c},
			},
			{
				name:        "returns ErrSignature for a party with the line of one of its keys",
				giveKeys:    []witness.KeySet{hybrid},
				giveSigners: []note.Signer{a},
				want:        witness.ErrSignature,
			},
			{
				name:        "commits a note of a party with the lines of all its keys",
				giveKeys:    []witness.KeySet{hybrid},
				giveSigners: []note.Signer{a, b},
			},
			{
				name:        "commits a note of the threshold of the second key set",
				giveKeys:    []witness.KeySet{twoOfThree, threeOfThree},
				giveSigners: []note.Signer{a, d, e},
			},
			{
				name:        "returns ErrSignature for a note below the threshold of each key set",
				giveKeys:    []witness.KeySet{twoOfThree, threeOfThree},
				giveSigners: []note.Signer{a, d},
				want:        witness.ErrSignature,
			},
			{
				name:        "returns ErrSignature for an invalid line of a party beyond the threshold",
				giveKeys:    []witness.KeySet{twoOfThree},
				giveSigners: []note.Signer{a, b},
				giveLine:    line(c.Key().Name, c.Key().ID()),
				want:        witness.ErrSignature,
			},
			{
				name: "commits a note of a key that two key sets list",
				giveKeys: []witness.KeySet{
					{Parties: [][]note.Key{{a.Key()}, {b.Key()}}, Threshold: 2},
					{Parties: [][]note.Key{{a.Key()}}},
				},
				giveSigners: []note.Signer{a},
			},
		}
		for _, tt := range lines {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t)
				f.accepted[l.origin] = witness.Log{Hasher: l.log.Hasher, Keys: tt.giveKeys}

				msg := append(signBody(t, l.body(5), tt.giveSigners...), tt.giveLine...)
				_, _, err := newServer(t, f.config()).Advance(bounded(t), msg, []witness.Update{l.update(t, 0, 5)}, nil)
				assert.ErrorIs(t, err, tt.want, "Advance must count the parties of the key sets of the Log")
			})
		}
	})
}
