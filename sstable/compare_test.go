// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/skiplist"
	"go.thesmos.sh/core/sstable"
)

// versionBytes is the width of the versions of the keys of the cases with
// versions: a uint64 in big-endian order.
const versionBytes = 8

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

	t.Run("CompareWithSuffix", func(t *testing.T) {
		t.Parallel()

		be := binary.BigEndian
		tests := []struct {
			name    string
			giveA   entry
			giveB   entry
			wantCmp int
		}{
			{
				name:    "returns -1 for a key that is a prefix of the other at a lower version",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				giveB:   entry{key: be.AppendUint64([]byte("a\x00"), 9), seq: 1},
				wantCmp: -1,
			},
			{
				name:    "returns -1 for a key that is a prefix of the other at a higher version",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 9), seq: 1},
				giveB:   entry{key: be.AppendUint64([]byte("a\x00"), 5), seq: 2},
				wantCmp: -1,
			},
			{
				name:    "returns -1 for one key at a higher version",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 9), seq: 1},
				giveB:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				wantCmp: -1,
			},
			{
				name:    "returns +1 for one key at a lower version",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				giveB:   entry{key: be.AppendUint64([]byte("a"), 9), seq: 1},
				wantCmp: 1,
			},
			{
				name:    "returns -1 for one key and version with a higher sequence",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				giveB:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 1},
				wantCmp: -1,
			},
			{
				name:    "returns 0 for equal entries",
				giveA:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				giveB:   entry{key: be.AppendUint64([]byte("a"), 5), seq: 2},
				wantCmp: 0,
			},
			{
				name:    "returns -1 for a key shorter than the width before a key with bytes before its version",
				giveA:   entry{key: []byte("abc"), seq: 1},
				giveB:   entry{key: be.AppendUint64([]byte("\x00"), 0), seq: 1},
				wantCmp: -1,
			},
			{
				name:    "returns +1 for a key shorter than the width whose bytes start the version of the other",
				giveA:   entry{key: []byte("\x01"), seq: 1},
				giveB:   entry{key: be.AppendUint64(nil, 1<<56), seq: 1},
				wantCmp: 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := sstable.CompareWithSuffix(tt.giveA.key, tt.giveA.seq, tt.giveB.key, tt.giveB.seq, versionBytes)
				assert.Equal(t, got, tt.wantCmp, "CompareWithSuffix returns the order of the case")
			})
		}

		t.Run("returns what Compare returns for a width of 0 or below", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "CompareWithSuffix of a width of 0 or below agrees with Compare", func(c *prop.Case) {
				a := c.Draw(prop.Bytes(prop.MaxSize(4)), "a key")
				b := c.Draw(prop.Bytes(prop.MaxSize(4)), "b key")
				aSeq := c.Draw(prop.Integer[uint64](0, 3), "a seq")
				bSeq := c.Draw(prop.Integer[uint64](0, 3), "b seq")
				width := c.Draw(prop.Integer(-2, 0), "width")
				assert.Equal(c, sstable.CompareWithSuffix(a, aSeq, b, bSeq, width), sstable.Compare(a, aSeq, b, bSeq),
					"CompareWithSuffix orders the two entries as Compare does")
			})
		})

		t.Run("orders by the key before the version, then by the version in reverse, then by the sequence",
			func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "CompareWithSuffix agrees with a model of the three parts of an entry",
					func(c *prop.Case) {
						width := c.Draw(prop.Integer(1, 3), "width")
						a := c.Draw(prop.Bytes(prop.MaxSize(6)), "a key")
						b := c.Draw(prop.Bytes(prop.MaxSize(6)), "b key")
						aSeq := c.Draw(prop.Integer[uint64](0, 3), "a seq")
						bSeq := c.Draw(prop.Integer[uint64](0, 3), "b seq")
						an, bn := max(len(a)-width, 0), max(len(b)-width, 0)
						want := bytes.Compare(a[:an], b[:bn])
						if want == 0 {
							want = bytes.Compare(b[bn:], a[an:])
						}
						if want == 0 {
							want = cmp.Compare(bSeq, aSeq)
						}
						assert.Equal(c, sstable.CompareWithSuffix(a, aSeq, b, bSeq, width), want,
							"CompareWithSuffix orders the two entries")
					})
			})
	})
}
