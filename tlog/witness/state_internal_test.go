// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
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

			testkit.NoError(t, st.take(snap, "a snapshot", 100, nil), "take must take the snapshot")
			testkit.Equal(t, st.seq, uint64(1), "the state must move to the record of the snapshot")
			testkit.Equal(t, st.time, uint64(2), "the state must take the latest time of the origins")
			testkit.Equal(t, st.end, uint64(10), "the state must take the end of the snapshot")

			b, _ := st.origins.Get(hashOrigin("example.com/b"))
			testkit.True(t, b.served.group, "an origin of a group must have a served position")
			testkit.True(t, b.snapshot, "an origin of a snapshot must have the snapshot flag")

			a, _ := st.origins.Get(hashOrigin("example.com/a"))
			testkit.True(t, a.served == position{}, "an origin of a record must have no served position")
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
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				st := newState()
				err := st.take(tt.give, "a snapshot", 100, nil)
				testkit.ErrorIs(t, err, ErrJournal, "take must refuse the snapshot")
				testkit.Equal(t, st.snap.name, "", "take must leave the state unchanged")
				testkit.Equal(t, st.seq, uint64(0), "take must leave the state unchanged")
			})
		}
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
