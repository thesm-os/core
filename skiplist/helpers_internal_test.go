// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
)

// The sizes of the arenas of most cases: room for every entry that a case
// adds.
const (
	dataBytes = 1 << 16
	nodeBytes = 1 << 16
)

// The benchmarks add keys of 16 bytes with values of 100 bytes, and look up
// keys in a List of benchEntries entries.
const (
	benchKeyLen   = 16
	benchValueLen = 100
	benchEntries  = 1 << 20

	// benchNodeBytes is the room of a node of the benchmarks: the 27 words
	// of the tallest node.
	benchNodeBytes = 108
)

// keyMix and keyGamma are the multiplier and the increment of the mix that
// turns the number of a key of the benchmarks into its bytes: the
// constants of splitmix64.
const (
	keyMix   = 0xbf58476d1ce4e5b9
	keyGamma = 0x9e3779b97f4a7c15
)

// entry is an entry of a List: a key, a sequence and a value.
type entry struct {
	key   []byte
	seq   uint64
	value []byte
}

// model is the entries of a List in the order of a List.
type model []entry

// add inserts e in the order of m, and reports false when m contains an
// entry with the key and the sequence of e.
func (m *model) add(e entry) bool {
	i, found := slices.BinarySearchFunc(*m, e, order)
	if found {
		return false
	}
	*m = slices.Insert(*m, i, e)

	return true
}

// order returns -1, 0 or +1 for the order of the entries a and b in a List:
// by key, then by sequence from the highest to the lowest.
func order(a, b entry) int {
	if c := bytes.Compare(a.key, b.key); c != 0 {
		return c
	}

	return cmp.Compare(b.seq, a.seq)
}

// scan returns every entry that a cursor yields from its first entry.
func scan(it *Iter) []entry {
	var got []entry
	for ok := it.First(); ok; ok = it.Next() {
		got = append(got, entry{key: it.Key(), seq: it.Seq(), value: it.Value()})
	}

	return got
}

// newList returns an empty List with arenas of dataBytes and nodeBytes.
func newList(tb testing.TB) *List {
	tb.Helper()

	l, err := New(dataBytes, nodeBytes, 1)
	assert.NoError(tb, err, "New returns a List")

	return l
}

// benchKey writes the key of number i of the benchmarks into k: 16 bytes
// that look random, so a List of the keys is a List of random keys.
func benchKey(k *[benchKeyLen]byte, i uint64) {
	x := (i + keyGamma) * keyMix
	binary.BigEndian.PutUint64(k[:8], x^x>>31)
	binary.BigEndian.PutUint64(k[8:], (x+keyGamma)*keyMix)
}

// filled returns a List of the n keys that key writes for the numbers from
// 0 up, each with the sequence 1 and the value of benchValueLen bytes.
func filled(tb testing.TB, n int, key func(k *[benchKeyLen]byte, i uint64)) *List {
	tb.Helper()

	l, err := New(n*(benchKeyLen+benchValueLen), n*benchNodeBytes, 1)
	assert.NoError(tb, err, "New returns a List with room for the entries")
	value := make([]byte, benchValueLen)
	var k [benchKeyLen]byte
	for i := range uint64(n) {
		key(&k, i)
		assert.NoError(tb, l.Add(k[:], 1, value), "Add adds every entry")
	}

	return l
}
