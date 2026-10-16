// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
)

// iterEntries is the number of entries of the List of the benchmarks of a
// cursor.
const iterEntries = 1 << 16

func TestIter(t *testing.T) {
	t.Parallel()

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first entry", func(t *testing.T) {
			t.Parallel()
			l := listOf(t, entry{key: []byte("b"), seq: 1}, entry{key: []byte("a"), seq: 1})
			it := l.NewIter()
			assert.True(t, it.First(), "First finds an entry")
			assert.Equal(t, it.Key(), []byte("a"), "First moves to the smallest key")
		})

		t.Run("reports false for an empty List", func(t *testing.T) {
			t.Parallel()
			assert.False(t, newList(t).NewIter().First(), "First finds no entry")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		versions := []entry{
			{key: []byte("a"), seq: 1},
			{key: []byte("b"), seq: 5},
			{key: []byte("b"), seq: 2},
			{key: []byte("c"), seq: 1},
		}

		t.Run("moves to the first entry at or after the key and the sequence", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, versions...).NewIter()
			assert.True(t, it.SeekGE([]byte("b"), 3), "SeekGE finds an entry")
			expect.Equal(t, it.Key(), []byte("b"), "SeekGE moves to the key")
			expect.Equal(t, it.Seq(), uint64(2), "SeekGE moves to the newest version at or below the sequence")
		})

		t.Run("moves to the next key when every version of the key is above the sequence", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, versions...).NewIter()
			assert.True(t, it.SeekGE([]byte("b"), 1), "SeekGE finds an entry")
			assert.Equal(t, it.Key(), []byte("c"), "SeekGE moves to the next key")
		})

		t.Run("reports false past the last entry", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, versions...).NewIter()
			assert.False(t, it.SeekGE([]byte("d"), 0), "SeekGE finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the next entry in order", func(t *testing.T) {
			t.Parallel()
			l := listOf(t, entry{key: []byte("c"), seq: 1}, entry{key: []byte("a"), seq: 1},
				entry{key: []byte("b"), seq: 1})
			it := l.NewIter()
			assert.True(t, it.First(), "First finds an entry")
			assert.True(t, it.Next(), "Next finds the second entry")
			assert.Equal(t, it.Key(), []byte("b"), "Next moves to the next key")
		})

		t.Run("reports false after the last entry", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1}).NewIter()
			assert.True(t, it.First(), "First finds the entry")
			expect.False(t, it.Next(), "Next finds no entry after the last")
			expect.False(t, it.Next(), "Next of an exhausted cursor finds no entry")
		})

		t.Run("moves to an entry that Add added after the cursor moved", func(t *testing.T) {
			t.Parallel()
			l := listOf(t, entry{key: []byte("a"), seq: 1})
			it := l.NewIter()
			assert.True(t, it.First(), "First finds the entry")
			assert.NoError(t, l.Add([]byte("b"), 1, nil), "Add adds an entry after the cursor")
			assert.True(t, it.Next(), "Next finds the entry that Add added")
			assert.Equal(t, it.Key(), []byte("b"), "Next moves to the new entry")
		})
	})

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a key whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1, value: []byte("value")}).NewIter()
			assert.True(t, it.First(), "First finds the entry")
			assert.Equal(t, cap(it.Key()), len(it.Key()), "an append to the key does not write into the List")
		})

		t.Run("returns nil for an exhausted cursor", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1}).NewIter()
			assert.False(t, it.SeekGE([]byte("b"), 0), "SeekGE finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})
	})

	t.Run("Seq", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sequence of the current entry", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1<<40 + 7}).NewIter()
			assert.True(t, it.First(), "First finds the entry")
			assert.Equal(t, it.Seq(), uint64(1<<40+7), "Seq returns both halves of the sequence")
		})

		t.Run("returns 0 for an exhausted cursor", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 9}).NewIter()
			assert.False(t, it.SeekGE([]byte("b"), 0), "SeekGE finds no entry")
			assert.Equal(t, it.Seq(), uint64(0), "the cursor has no sequence")
		})
	})

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of the current entry", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1, value: []byte("value")}).NewIter()
			assert.True(t, it.First(), "First finds the entry")
			assert.Equal(t, it.Value(), []byte("value"), "Value returns the value of the entry")
		})

		t.Run("returns nil for an exhausted cursor", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1, value: []byte("value")}).NewIter()
			assert.False(t, it.SeekGE([]byte("b"), 0), "SeekGE finds no entry")
			assert.Nil(t, it.Value(), "the cursor has no value")
		})
	})

	t.Run("Err", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil after every move", func(t *testing.T) {
			t.Parallel()
			it := listOf(t, entry{key: []byte("a"), seq: 1}).NewIter()
			for _, move := range []func() bool{it.First, it.Next, it.Next} {
				move()
				expect.NoError(t, it.Err(), "a cursor over a List has no error")
			}
		})
	})
}

// TestIterAllocs checks the allocation contract of each method of a cursor.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestIterAllocs(t *testing.T) {
	it := filled(t, 100, benchKey).NewIter()
	var k [benchKeyLen]byte
	benchKey(&k, 50)

	var ok bool
	expect.MaxAllocs(t, func() { ok = it.First() }, 0, "First does not allocate")
	expect.True(t, ok, "the test measures a cursor on an entry")
	expect.MaxAllocs(t, func() { ok = it.SeekGE(k[:], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a SeekGE to an entry")
	expect.MaxAllocs(t, func() {
		if !it.Next() {
			it.First()
		}
	}, 0, "Next does not allocate")

	var key, value []byte
	var seq uint64
	var err error
	expect.MaxAllocs(t, func() { key = it.Key() }, 0, "Key does not allocate")
	expect.MaxAllocs(t, func() { seq = it.Seq() }, 0, "Seq does not allocate")
	expect.MaxAllocs(t, func() { value = it.Value() }, 0, "Value does not allocate")
	expect.MaxAllocs(t, func() { err = it.Err() }, 0, "Err does not allocate")
	expect.Length(t, key, benchKeyLen, "the test measures the key of an entry")
	expect.Equal(t, seq, uint64(1), "the test measures the sequence of an entry")
	expect.Length(t, value, benchValueLen, "the test measures the value of an entry")
	expect.NoError(t, err, "the test measures a cursor without an error")
}

// BenchmarkIter reports the cost of each move of a cursor, and fails when a
// move allocates.
func BenchmarkIter(b *testing.B) {
	l := filled(b, iterEntries, benchKey)

	b.Run("Next", func(b *testing.B) {
		it := l.NewIter()
		assert.True(b, it.First(), "First finds an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !it.Next() {
				it.First()
			}
			n += len(it.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})

	b.Run("SeekGE", func(b *testing.B) {
		it := l.NewIter()
		var k [benchKeyLen]byte
		var ok bool
		i := uint64(0)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			benchKey(&k, (i*keyGamma)%iterEntries)
			ok = it.SeekGE(k[:], 1)
			i++
		}

		assert.True(b, ok, "the benchmark measures seeks to entries")
	})
}

// listOf returns a List of the entries, which it adds in their order.
func listOf(tb testing.TB, entries ...entry) *List {
	tb.Helper()

	l := newList(tb)
	for _, e := range entries {
		assert.NoError(tb, l.Add(e.key, e.seq, e.value), "Add adds every entry")
	}

	return l
}
