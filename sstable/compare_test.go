// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"cmp"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/skiplist"
	"go.thesmos.sh/core/sstable"
)

// A cursor over a List and a cursor over a table are both sources.
var (
	_ sstable.Source = (*skiplist.Iter)(nil)
	_ sstable.Source = (*sstable.Iter)(nil)
	_ sstable.Source = (*sstable.Merger)(nil)
)

func TestCompare(t *testing.T) {
	t.Parallel()

	t.Run("Compare", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			giveA   entry
			giveB   entry
			wantCmp int
		}{
			{
				name:  "returns -1 for a smaller key",
				giveA: entry{key: []byte("a"), seq: 1}, giveB: entry{key: []byte("b"), seq: 9}, wantCmp: -1,
			},
			{
				name:  "returns +1 for a larger key",
				giveA: entry{key: []byte("b"), seq: 9}, giveB: entry{key: []byte("a"), seq: 1}, wantCmp: 1,
			},
			{
				name:  "returns -1 for a key that is a prefix of the other",
				giveA: entry{key: []byte("a"), seq: 0}, giveB: entry{key: []byte("ab"), seq: math.MaxUint64},
				wantCmp: -1,
			},
			{
				name:  "returns -1 for one key with a higher sequence",
				giveA: entry{key: []byte("a"), seq: 5}, giveB: entry{key: []byte("a"), seq: 4}, wantCmp: -1,
			},
			{
				name:  "returns +1 for one key with a lower sequence",
				giveA: entry{key: []byte("a"), seq: 4}, giveB: entry{key: []byte("a"), seq: 5}, wantCmp: 1,
			},
			{
				name:  "returns 0 for equal entries",
				giveA: entry{key: []byte("a"), seq: 4}, giveB: entry{key: []byte("a"), seq: 4}, wantCmp: 0,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := sstable.Compare(tt.giveA.key, tt.giveA.seq, tt.giveB.key, tt.giveB.seq)
				assert.Equal(t, got, tt.wantCmp, "Compare orders by key, then by sequence from the highest")
			})
		}

		t.Run("orders by key as bytes.Compare orders keys, then by sequence from the highest", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Compare agrees with bytes.Compare and then with the reverse of the sequences",
				func(c *prop.Case) {
					a := entry{
						key: c.Draw(prop.Bytes(prop.MaxSize(4)), "a key"),
						seq: c.Draw(prop.Integer[uint64](0, 3), "a seq"),
					}
					b := entry{
						key: c.Draw(prop.Bytes(prop.MaxSize(4)), "b key"),
						seq: c.Draw(prop.Integer[uint64](0, 3), "b seq"),
					}
					want := bytes.Compare(a.key, b.key)
					if want == 0 {
						want = cmp.Compare(b.seq, a.seq)
					}
					assert.Equal(c, sstable.Compare(a.key, a.seq, b.key, b.seq), want,
						"Compare orders the two entries")
				})
		})
	})
}
