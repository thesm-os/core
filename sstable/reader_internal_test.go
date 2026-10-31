// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"encoding/binary"
	"testing"

	"go.dokimi.dev/assert"
)

// TestChecker checks the layout checks of a Check on blocks that a case
// builds entry by entry, as the Writer builds them.
func TestChecker(t *testing.T) {
	t.Parallel()

	t.Run("checkData", func(t *testing.T) {
		t.Parallel()

		// The two versions of the key a. The second entry shares the key and
		// the first 7 bytes of its sequence with the first.
		be := binary.BigEndian
		first, second := be.AppendUint64([]byte("a"), 2), be.AppendUint64([]byte("a"), 1)
		entries := appendEntry(appendEntry(nil, first, []byte("x"), 0), second, []byte("y"), len(first)-1)

		t.Run("reports true for entries after a restart point at the offset 0", func(t *testing.T) {
			t.Parallel()
			var ck checker
			assert.True(t, ck.checkData(appendIndex(nil, entries, []uint32{0}), nil, second),
				"checkData accepts the block")
		})

		t.Run("reports false for a block without restart points", func(t *testing.T) {
			t.Parallel()
			var ck checker
			assert.False(t, ck.checkData(appendIndex(nil, entries, nil), nil, second),
				"checkData refuses entries without a restart point")
		})

		t.Run("reports false for two equal entries", func(t *testing.T) {
			t.Parallel()
			twice := appendEntry(appendEntry(nil, first, []byte("x"), 0), first, []byte("y"), len(first))
			var ck checker
			assert.False(t, ck.checkData(appendIndex(nil, twice, []uint32{0}), nil, first),
				"checkData refuses an entry that does not follow the entry before it")
		})
	})

	t.Run("checkIndex", func(t *testing.T) {
		t.Parallel()

		// Each entry is at a restart point with the place of a data block of 10
		// bytes at the offset 0, inside the data blocks of the Reader.
		be := binary.BigEndian
		place := be.AppendUint32(binary.AppendUvarint(binary.AppendUvarint(nil, 0), 10), 0)
		keys := [][]byte{
			be.AppendUint64([]byte("a"), 1), be.AppendUint64([]byte("b"), 1), be.AppendUint64([]byte("c"), 1),
		}
		var entries []byte
		offsets := make([]uint32, 0, len(keys))
		for _, k := range keys {
			offsets = append(offsets, uint32(len(entries)))
			entries = appendEntry(entries, k, place, 0)
		}
		r := &Reader{dataEnd: 1 << 10}

		t.Run("reports true for entries at consecutive restart points", func(t *testing.T) {
			t.Parallel()
			ck := checker{r: r}
			assert.True(t, ck.checkIndex(appendIndex(nil, entries, offsets), nil, keys[2], 3),
				"checkIndex accepts the partition")
		})

		t.Run("reports false for restart points that skip an entry", func(t *testing.T) {
			t.Parallel()
			ck := checker{r: r}
			assert.False(t, ck.checkIndex(appendIndex(nil, entries, []uint32{offsets[0], offsets[2]}), nil, keys[2], 2),
				"checkIndex refuses a restart point after an entry without one")
		})

		t.Run("reports false for an entry after the last restart point", func(t *testing.T) {
			t.Parallel()
			ck := checker{r: r}
			assert.False(t, ck.checkIndex(appendIndex(nil, entries, offsets[:2]), nil, keys[1], 2),
				"checkIndex refuses the bytes after its last entry")
		})
	})
}
