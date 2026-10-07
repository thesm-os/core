// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog/checkpoint"
)

func TestStateInternal(t *testing.T) {
	t.Parallel()

	rec := string(appendName(nil, 1, []byte("a record")))
	grp := string(appendName(nil, 1, []byte("a group")))
	root := crypto.NewDigest256([32]byte{1})

	t.Run("take", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the positions of a snapshot", func(t *testing.T) {
			t.Parallel()
			st := newState()
			snap := &snapshot{
				Record:  rec,
				Objects: []object{{Key: recordPrefix + rec}, {Key: groupPrefix + grp, Updates: 1}},
				Origins: sortedOrigins(
					snapOrigin{Origin: "example.com/a", Size: 5, Time: 1, Object: 0, Root: root},
					snapOrigin{Origin: "example.com/b", Size: 6, Time: 2, Object: 1, Root: root},
				),
				End: 10,
			}

			assert.NoError(t, st.take(snap, "a snapshot", 100, nil), "take must take the snapshot")
			assert.Equal(t, st.seq, uint64(1), "the state must move to the record of the snapshot")
			assert.Equal(t, st.time, uint64(2), "the state must take the latest time of the origins")
			assert.Equal(t, st.end, uint64(10), "the state must take the end of the snapshot")

			b, _ := st.origins.Get(hashOrigin("example.com/b"))
			assert.True(t, b.served.group, "an origin of a group must have a served position")
			assert.True(t, b.snapshot, "an origin of a snapshot must have the snapshot flag")

			a, _ := st.origins.Get(hashOrigin("example.com/a"))
			assert.Equal(t, a.served, position{}, "an origin of a record must have no served position")
		})

		h := hashOrigin("example.com/a")
		inGroup := &snapshot{
			Record:  rec,
			Objects: []object{{Key: groupPrefix + grp, Updates: 1}},
			Origins: []snapOrigin{{Origin: "example.com/a", Size: 5, Time: 1, Root: root}},
		}

		t.Run("takes the position of the snapshot for an origin whose latest update is at its record",
			func(t *testing.T) {
				t.Parallel()
				at := position{key: recordPrefix + rec, seq: 1, call: 1}
				st := newState()
				st.origins.Set(h, originState{origin: "example.com/a", latest: at})
				snap := &snapshot{
					Record:  rec,
					Objects: []object{{Key: recordPrefix + rec}},
					Origins: []snapOrigin{{Origin: "example.com/a", Size: 5, Time: 1, Root: root}},
				}

				assert.NoError(t, st.take(snap, "a snapshot", 100, nil), "take must take the snapshot")
				o, _ := st.origins.Get(h)
				assert.Equal(t, o.latest.call, 0, "the origin must take the position of the snapshot")
			})

		t.Run("keeps a served position after the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			later := position{key: recordPrefix + string(appendName(nil, 3, []byte("a later record"))), seq: 3}
			st := newState()
			st.origins.Set(h, originState{origin: "example.com/a", latest: later, served: later})

			assert.NoError(t, st.take(inGroup, "a snapshot", 100, nil), "take must take the snapshot")
			o, _ := st.origins.Get(h)
			assert.Equal(t, o.served, later, "the origin must keep its later served position")
		})

		t.Run("serves the group of the snapshot for an origin served at the record of the snapshot",
			func(t *testing.T) {
				t.Parallel()
				at := position{key: recordPrefix + rec, seq: 1}
				st := newState()
				st.origins.Set(h, originState{origin: "example.com/a", latest: at, served: at})

				assert.NoError(t, st.take(inGroup, "a snapshot", 100, nil), "take must take the snapshot")
				o, _ := st.origins.Get(h)
				assert.True(t, o.served.group, "the origin must serve the group")
			})

		t.Run("keeps the head of a state at the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			st := newState()
			st.seq, st.time, st.end = 1, 100, 50

			assert.NoError(t, st.take(inGroup, "a snapshot", 100, nil), "take must take the snapshot")
			assert.Equal(t, st.time, uint64(100), "the state must keep the time of its head")
			assert.Equal(t, st.end, uint64(50), "the state must keep the end of its head")
		})

		t.Run("keeps a latest position after the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			later := position{key: recordPrefix + string(appendName(nil, 3, []byte("a later record"))), seq: 3}
			st := newState()
			st.origins.Set(h, originState{origin: "example.com/a", latest: later, size: 9})

			assert.NoError(t, st.take(inGroup, "a snapshot", 100, nil), "take must take the snapshot")
			o, _ := st.origins.Get(h)
			expect.Equal(t, o.latest, later, "the origin must keep its later latest position")
			expect.Equal(t, o.size, uint64(9), "the origin must keep the size of its later update")
		})

		t.Run("adds the record of a snapshot ahead of the state to its chain", func(t *testing.T) {
			t.Parallel()
			st := newState()

			assert.NoError(t, st.take(inGroup, "a snapshot", 100, nil), "take must take the snapshot")
			got, _ := st.chain.Get(1)
			assert.Equal(t, got, rec, "the chain must name the record of the snapshot at its Seq")
		})

		t.Run("drops the names of the chain before the record of the base", func(t *testing.T) {
			t.Parallel()
			st := newState()
			for seq := uint64(1); seq <= 3; seq++ {
				st.chain.Set(seq, string(appendName(nil, seq, []byte("a record"))))
			}

			snap := *inGroup
			snap.Record = string(appendName(nil, 3, []byte("a record")))
			snap.Base = string(appendName(nil, 2, []byte("a base")))

			assert.NoError(t, st.take(&snap, "a snapshot", 100, nil), "take must take the snapshot")
			expect.False(t, st.chain.Has(1), "the chain must drop the name before the base")
			expect.True(t, st.chain.Has(2), "the chain must keep the name of the base")
		})

		asc := sortedOrigins(
			snapOrigin{Origin: "example.com/a", Root: root},
			snapOrigin{Origin: "example.com/b", Root: root},
		)
		tests := []struct {
			give *snapshot
			name string
		}{
			{name: "returns ErrJournal for a record that is not a name", give: &snapshot{Record: "a record"}},
			{
				name: "returns ErrJournal for an object that is neither a record nor a group",
				give: &snapshot{Record: rec, Objects: []object{{Key: "other/" + rec}}},
			},
			{
				name: "returns ErrJournal for an object under another prefix of the length of the record prefix",
				give: &snapshot{Record: rec, Objects: []object{{Key: "unknown/" + rec}}},
			},
			{
				name: "returns ErrJournal for a record with a count of updates",
				give: &snapshot{Record: rec, Objects: []object{{Key: recordPrefix + rec, Updates: 1}}},
			},
			{
				name: "returns ErrJournal for a group without a count of updates",
				give: &snapshot{Record: rec, Objects: []object{{Key: groupPrefix + grp}}},
			},
			{
				name: "returns ErrJournal for an object whose name is not a name",
				give: &snapshot{Record: rec, Objects: []object{{Key: recordPrefix + "a record"}}},
			},
			{
				name: "returns ErrJournal for a position of an object that the snapshot does not list",
				give: &snapshot{
					Record: rec, Origins: []snapOrigin{{Origin: "example.com/a", Object: 1, Root: root}},
					Objects: []object{{Key: recordPrefix + rec}},
				},
			},
			{
				name: "returns ErrJournal for origins out of the order of their hashes",
				give: &snapshot{
					Record: rec, Objects: []object{{Key: recordPrefix + rec}},
					Origins: []snapOrigin{asc[1], asc[0]},
				},
			},
			{
				name: "returns ErrJournal for an origin listed twice",
				give: &snapshot{
					Record: rec, Objects: []object{{Key: recordPrefix + rec}},
					Origins: []snapOrigin{asc[0], asc[0]},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				st := newState()
				err := st.take(tt.give, "a snapshot", 100, nil)
				assert.ErrorIs(t, err, ErrJournal, "take must refuse the snapshot")
				assert.Equal(t, st.snap.name, "", "take must leave the state unchanged")
				assert.Equal(t, st.seq, uint64(0), "take must leave the state unchanged")
			})
		}
	})

	t.Run("removeRetired", func(t *testing.T) {
		t.Parallel()

		a, b := checkpoint.Origin("example.com/a"), checkpoint.Origin("example.com/b")
		c := checkpoint.Origin("example.com/c")

		// stateOf returns a state of origins, each with its latest update at
		// the record of Seq 5.
		stateOf := func(origins ...checkpoint.Origin) *state {
			st := newState()
			for _, o := range origins {
				st.origins.Set(hashOrigin(o), originState{origin: o, latest: position{key: recordPrefix + rec, seq: 5}})
			}

			return st
		}

		// hashesOf returns the hashes of origins in ascending order.
		hashesOf := func(origins ...checkpoint.Origin) []originHash {
			hashes := make([]originHash, 0, len(origins))
			for _, o := range origins {
				hashes = append(hashes, hashOrigin(o))
			}

			slices.SortFunc(hashes, func(x, y originHash) int { return bytes.Compare(x[:], y[:]) })

			return hashes
		}

		t.Run("keeps every origin that hashes lists", func(t *testing.T) {
			t.Parallel()
			st := stateOf(a, b, c)
			st.removeRetired(hashesOf(a, b, c), 5)
			assert.Equal(t, st.origins.Len(), 3, "removeRetired must keep the listed origins")
		})

		t.Run("removes an origin after every listed hash", func(t *testing.T) {
			t.Parallel()
			hashes := hashesOf(a, b)
			st := stateOf(a, b)
			st.removeRetired(hashes[:1], 5)
			assert.Equal(t, st.origins.Len(), 1, "removeRetired must remove the origin after the listed hash")
			assert.True(t, st.origins.Has(hashes[0]), "removeRetired must keep the listed origin")
		})

		t.Run("removes an unlisted origin whose latest update is at the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			st := stateOf(a)
			st.removeRetired(nil, 5)
			assert.Equal(t, st.origins.Len(), 0, "removeRetired must remove the origin")
		})

		t.Run("keeps an unlisted origin whose latest update is after the record of the snapshot", func(t *testing.T) {
			t.Parallel()
			st := stateOf(a)
			st.removeRetired(nil, 4)
			assert.Equal(t, st.origins.Len(), 1, "removeRetired must keep the origin")
		})
	})
}

// sortedOrigins returns origins in the order of the hashes of their
// origins.
func sortedOrigins(origins ...snapOrigin) []snapOrigin {
	if len(origins) == 2 {
		a, b := hashOrigin(origins[0].Origin), hashOrigin(origins[1].Origin)
		if string(a[:]) > string(b[:]) {
			origins[0], origins[1] = origins[1], origins[0]
		}
	}

	return origins
}
