// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package skiplist

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/hash/xxh3"
)

// The names of the calls that the machines make and record.
const (
	opAdd    = "Add"
	opGet    = "Get"
	opScan   = "Scan"
	opSeek   = "SeekGE"
	opSeekLT = "SeekLT"
	opLast   = "Last"
	opWalk   = "Walk"
	opReset  = "Reset"
)

// The moves of a walk of the machine of a List.
const (
	// walkMoves is the largest number of moves of a walk.
	walkMoves = 6

	// listed is the number of entries that a seek of the machine reads.
	listed = 3
)

// The workload of the machine of concurrent calls: historyKeys keys with
// historySeqs sequences each.
const (
	historyKeys = 3
	historySeqs = 3

	// absent is the value that the machine records for an entry that the
	// List does not contain.
	absent = -1
)

// The workload of the writers that add the same entries at once.
const (
	// sharedEntries is the number of entries that every writer adds.
	sharedEntries = 2000

	// writers is the number of writers.
	writers = 4
)

// The workload of the check of the average size of a node.
const (
	// heightEntries is the number of entries whose nodes the check averages.
	heightEntries = 20000

	// heightKeyLen is the length of their keys.
	heightKeyLen = 8

	// nodeWords is the number of words of a node before its links.
	nodeWords = 7

	// heightTolerance is the bytes by which the average may differ from its
	// expectation. The standard deviation of the average of 20,000 nodes is
	// 0.027 bytes, so the tolerance is above 5 standard deviations.
	heightTolerance = 0.15
)

// listContract is the contract of listSteps, which TestList and FuzzList
// check.
const listContract = "every call must return what a sorted model of the entries returns"

// keys are the keys of the machines. "a" is a prefix of "ab", and the empty
// key comes first. "a" and "a\x00" have equal prefixes, and so do the three
// keys that start with "abcdefgh". The machines then compare whole keys too.
var keys = [][]byte{
	{},
	[]byte("a"), []byte("a\x00"), []byte("ab"), []byte("abcdefgh"), []byte("abcdefghx"), []byte("abcdefghy"),
	[]byte("b"), []byte("ba"), []byte("c"),
}

// benchKeySets are the keys of the benchmarks of Add and Get: random keys,
// and keys whose first 8 bytes are 0. A search compares the keys of the
// second set by their whole keys.
var benchKeySets = []struct {
	name string
	key  func(k *[benchKeyLen]byte, i uint64)
}{
	{name: "random keys", key: benchKey},
	{name: "keys that share their first 8 bytes", key: func(k *[benchKeyLen]byte, i uint64) {
		benchKey(k, i)
		clear(k[:8])
	}},
}

// errInvariant is the error that check returns for a broken invariant.
var errInvariant = errors.New("skiplist: invariant")

// versions is the state of the spec of the machine of concurrent calls: the
// value of each entry by its key and its sequence, and absent for an entry
// that the List does not contain.
type versions [historyKeys * historySeqs]int

// lookup is what a Get of the machine of concurrent calls returns, with the
// value as the number that its bytes encode.
type lookup struct {
	value int
	seq   uint64
	ok    bool
}

// walk is a SeekGE of a cursor to at, and the moves after it: true moves
// forward with Next, and false moves back with Prev.
type walk struct {
	at    entry
	moves []bool
}

func TestList(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty List", func(t *testing.T) {
			t.Parallel()
			l, err := New(dataBytes, nodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			_, _, ok := l.Get(nil, math.MaxUint64)
			expect.Equal(t, l.Len(), 0, "the List has no entries")
			expect.Equal(t, l.Size(), 0, "the List uses no bytes of its arenas")
			expect.False(t, ok, "Get finds no entry")
			expect.Empty(t, scan(l.NewIter()), "a cursor yields no entry")
		})

		t.Run("returns a List for arenas of one byte", func(t *testing.T) {
			t.Parallel()
			l, err := New(1, 1, 1)
			assert.NoError(t, err, "New accepts the smallest arenas")
			assert.ErrorIs(t, l.Add([]byte("a"), 1, nil), ErrFull, "a node does not fit one byte")
		})

		// The secrets are the first two outputs of java.util.SplittableRandom of
		// OpenJDK 27 for each seed. SplittableRandom adds the golden gamma to its
		// seed and mixes the sum with the function of splitmix64.
		secrets := []struct {
			name        string
			giveSeed    uint64
			wantSecret0 uint64
			wantSecret1 uint64
		}{
			{
				name:     "takes the secrets for the seed 0 from splitmix64",
				giveSeed: 0, wantSecret0: 0xe220a8397b1dcdaf, wantSecret1: 0x6e789e6aa1b965f4,
			},
			{
				name:     "takes the secrets for the seed 1 from splitmix64",
				giveSeed: 1, wantSecret0: 0x910a2dec89025cc1, wantSecret1: 0xbeeb8da1658eec67,
			},
			{
				name:     "takes the secrets for a seed of mixed bits from splitmix64",
				giveSeed: 0x123456789abcdef0, wantSecret0: 0x161922c645ce50e8, wantSecret1: 0xad760cafa1697b60,
			},
			{
				name:     "takes the secrets for the largest seed from splitmix64",
				giveSeed: math.MaxUint64, wantSecret0: 0xe4d971771b652c20, wantSecret1: 0xe99ff867dbf682c9,
			},
		}
		for _, tt := range secrets {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l, err := New(dataBytes, nodeBytes, tt.giveSeed)
				assert.NoError(t, err, "New returns a List")
				expect.Equal(t, l.secret0, tt.wantSecret0, "the first secret is the first output for the seed")
				expect.Equal(t, l.secret1, tt.wantSecret1, "the second secret is the second output for the seed")
			})
		}

		tests := []struct {
			name     string
			giveData int
			giveNode int
		}{
			{name: "returns ErrSize for a data arena of no bytes", giveData: 0, giveNode: nodeBytes},
			{name: "returns ErrSize for a node arena of no bytes", giveData: dataBytes, giveNode: 0},
			{name: "returns ErrSize for a negative capacity", giveData: -1, giveNode: nodeBytes},
			{name: "returns ErrSize for a data arena of 2^32 bytes", giveData: math.MaxUint32 + 1, giveNode: nodeBytes},
			{name: "returns ErrSize for a node arena of 2^32 bytes", giveData: dataBytes, giveNode: math.MaxUint32 + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l, err := New(tt.giveData, tt.giveNode, 1)
				assert.ErrorIs(t, err, ErrSize, "New refuses the capacity")
				assert.Nil(t, l, "New returns no List")
			})
		}
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds an entry that Get returns", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("key"), 5, []byte("value")), "Add adds the entry")
			value, seq, ok := l.Get([]byte("key"), 5)
			assert.True(t, ok, "Get finds the entry")
			expect.Equal(t, value, []byte("value"), "Get returns the value of the entry")
			expect.Equal(t, seq, uint64(5), "Get returns the sequence of the entry")
		})

		t.Run("returns ErrExists for an entry with the key and the sequence of an entry", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("key"), 5, []byte("first")), "Add adds the entry")
			var err error
			assert.Pure(t, l.Size, func() { err = l.Add([]byte("key"), 5, []byte("second")) },
				"Add of an entry of the List reserves nothing")
			assert.ErrorIs(t, err, ErrExists, "Add refuses the entry")
			value, _, _ := l.Get([]byte("key"), 5)
			expect.Equal(t, value, []byte("first"), "the List keeps the first value")
			expect.Equal(t, l.Len(), 1, "the List has one entry")
		})

		t.Run("adds an entry with the key of an entry and another sequence", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("key"), 5, []byte("first")), "Add adds the first version")
			assert.NoError(t, l.Add([]byte("key"), 6, []byte("second")), "Add adds the second version")
			assert.Equal(t, l.Len(), 2, "the List has both versions")
		})

		t.Run("returns ErrFull for an entry whose bytes do not fit the data arena", func(t *testing.T) {
			t.Parallel()
			l, err := New(8, nodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			err = l.Add([]byte("key"), 1, []byte("value6"))
			_, _, ok := l.Get([]byte("key"), 1)
			expect.ErrorIs(t, err, ErrFull, "Add refuses 9 bytes for 8 bytes of room")
			expect.False(t, ok, "the List does not contain the refused entry")
			expect.Equal(t, l.Len(), 0, "the List has no entries")
		})

		t.Run("returns ErrFull for an entry whose node does not fit the node arena", func(t *testing.T) {
			t.Parallel()
			l, err := New(dataBytes, 4, 1)
			assert.NoError(t, err, "New returns a List")
			assert.Pure(t, l.Size, func() { err = l.Add([]byte("key"), 1, nil) },
				"Add whose node does not fit reserves nothing")
			expect.ErrorIs(t, err, ErrFull, "Add refuses a node of 8 or more words for 1 word of room")
			expect.Equal(t, l.Len(), 0, "the List has no entries")
		})

		t.Run("adds an entry into the room that remains after ErrFull", func(t *testing.T) {
			t.Parallel()
			l, err := New(10, nodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			assert.NoError(t, l.Add([]byte("a"), 1, []byte("1234567")), "Add adds 8 bytes")
			assert.ErrorIs(t, l.Add([]byte("b"), 1, []byte("cdef")), ErrFull, "Add refuses 5 bytes")
			assert.NoError(t, l.Add([]byte("c"), 1, []byte("d")), "Add adds the 2 bytes that remain")
			assert.Equal(t, l.Len(), 2, "the List has the two entries that fit")
		})

		t.Run("adds an entry with an empty key and an empty value", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add(nil, 0, nil), "Add adds the empty entry")
			value, _, ok := l.Get(nil, 0)
			expect.True(t, ok, "Get finds the empty key")
			expect.Empty(t, value, "the value is empty")
		})

		t.Run("copies the key and the value", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			key, value := []byte("key"), []byte("value")
			assert.NoError(t, l.Add(key, 1, value), "Add adds the entry")
			copy(key, "xxx")
			copy(value, "xxxxx")
			got, _, ok := l.Get([]byte("key"), 1)
			assert.True(t, ok, "Get finds the entry under its original key")
			assert.Equal(t, got, []byte("value"), "the List keeps the bytes that Add received")
		})

		// A writer that loses the race for an entry has reserved its node and
		// its bytes, so the arenas have room for the entry of every writer.
		t.Run("adds each entry once while writers add the same entry at once", func(t *testing.T) {
			t.Parallel()
			l, err := New(writers*sharedEntries*heightKeyLen, writers*sharedEntries*benchNodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			added, disordered := addInRounds(t, l, func(round, _ int) []byte {
				return binary.BigEndian.AppendUint64(nil, uint64(round))
			})
			expect.Equal(t, added, sharedEntries, "exactly one Add of each entry succeeds")
			expect.Equal(t, l.Len(), sharedEntries, "the List has every entry once")
			expect.Length(t, scan(l.NewIter()), sharedEntries, "a cursor yields every entry once")
			expect.Equal(t, disordered, 0, "a cursor yields the entries in order while writers add")
		})

		t.Run("links every entry while writers add neighbouring entries at once", func(t *testing.T) {
			t.Parallel()
			l, err := New(writers*sharedEntries*heightKeyLen, writers*sharedEntries*benchNodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			added, disordered := addInRounds(t, l, func(round, writer int) []byte {
				return binary.BigEndian.AppendUint64(nil, uint64(round*writers+writer))
			})
			expect.Equal(t, added, writers*sharedEntries, "every Add of a new entry succeeds")
			expect.Length(t, scan(l.NewIter()), writers*sharedEntries, "a cursor yields every entry")
			expect.Equal(t, disordered, 0, "a cursor yields the entries in order while writers add")
		})

		t.Run("links an entry below a node of every level", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			// The second factor of the hash of the first entry is 0, so its hash
			// is 0 and its node has every level.
			l.secret1 = 1
			assert.NoError(t, l.Add([]byte("a"), 1, nil), "Add adds the entry of every level")
			assert.Equal(t, l.levels.Load(), uint32(maxHeight), "the List has every level")
			assert.NoError(t, l.Add([]byte("b"), 2, nil), "Add adds an entry after the node of every level")
			assert.NoError(t, check(l), "the List keeps the invariants of its levels")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the newest version at or below the sequence", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			for _, seq := range []uint64{1, 3, 5} {
				value := []byte(strconv.FormatUint(seq, 10))
				assert.NoError(t, l.Add([]byte("key"), seq, value), "Add adds the version")
			}
			value, seq, ok := l.Get([]byte("key"), 4)
			assert.True(t, ok, "Get finds a version at or below 4")
			expect.Equal(t, value, []byte("3"), "Get returns the value of version 3")
			expect.Equal(t, seq, uint64(3), "Get returns the sequence of version 3")
		})

		t.Run("returns the newest version for a sequence above every version", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			for _, seq := range []uint64{1, 3, 5} {
				value := []byte(strconv.FormatUint(seq, 10))
				assert.NoError(t, l.Add([]byte("key"), seq, value), "Add adds the version")
			}
			_, seq, ok := l.Get([]byte("key"), math.MaxUint64)
			assert.True(t, ok, "Get finds the newest version")
			assert.Equal(t, seq, uint64(5), "Get returns the sequence of version 5")
		})

		t.Run("reports false when every version of the key is above the sequence", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("key"), 5, nil), "Add adds the version")
			assert.NoError(t, l.Add([]byte("later"), 1, nil), "Add adds an entry of a later key")
			_, _, ok := l.Get([]byte("key"), 4)
			assert.False(t, ok, "Get finds no version at or below 4")
		})

		t.Run("reports false for a key that the List does not contain", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("a"), 1, nil), "Add adds a")
			assert.NoError(t, l.Add([]byte("c"), 1, nil), "Add adds c")
			_, _, ok := l.Get([]byte("b"), 1)
			assert.False(t, ok, "Get finds no entry of b")
		})

		t.Run("returns a value whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("a"), 1, []byte("value")), "Add adds a")
			assert.NoError(t, l.Add([]byte("b"), 1, []byte("next")), "Add adds b")
			value, _, _ := l.Get([]byte("a"), 1)
			assert.Equal(t, cap(value), len(value), "an append to the value does not write into the List")
		})

		t.Run("returns what one sequence of calls returns for concurrent calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "concurrent calls must linearize as the calls of one sequence", historySteps)
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of entries", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("a"), 1, nil), "Add adds a")
			assert.NoError(t, l.Add([]byte("b"), 1, nil), "Add adds b")
			assert.ErrorIs(t, l.Add([]byte("a"), 1, nil), ErrExists, "Add refuses a again")
			assert.Equal(t, l.Len(), 2, "Len counts each entry once")
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("spends the nodes of the heights of e^-k on average", func(t *testing.T) {
			t.Parallel()
			l, err := New(heightEntries*heightKeyLen, heightEntries*benchNodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			for i := range uint64(heightEntries) {
				assert.NoError(t, l.Add(binary.BigEndian.AppendUint64(nil, i), 0, nil), "Add adds the entry")
			}

			// A node has a height of 1, plus 1 for each level k from 1 to 19,
			// which it has with a probability of e^-k.
			height := 1.0
			for k := 1; k < 20; k++ {
				height += math.Exp(-float64(k))
			}
			perNode := float64(l.Size()-heightEntries*heightKeyLen) / heightEntries
			want := 4 * (nodeWords + height)
			assert.InRange(t, perNode, want-heightTolerance, want+heightTolerance,
				"a node takes 4 bytes for each of its words on average")
		})

		t.Run("derives the heights of the nodes from the seed", func(t *testing.T) {
			t.Parallel()
			sizes := make([]int, 0, 3)
			for _, seed := range []uint64{1, 1, 2} {
				l, err := New(dataBytes, nodeBytes, seed)
				assert.NoError(t, err, "New returns a List")
				for i := range uint64(1000) {
					assert.NoError(t, l.Add(binary.BigEndian.AppendUint64(nil, i), 0, nil), "Add adds the entry")
				}
				sizes = append(sizes, l.Size())
			}
			expect.Equal(t, sizes[1], sizes[0], "one seed gives the nodes of the same heights")
			expect.NotEqual(t, sizes[2], sizes[0], "another seed gives the nodes of other heights")
		})
	})

	t.Run("Cost", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the bytes that an Add of the entry reserves", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			nodeCost, dataCost := l.Cost([]byte("key"), 1, 5)
			assert.NoError(t, l.Add([]byte("key"), 1, []byte("value")), "Add adds the entry")
			expect.Equal(t, dataCost, 8, "the data of an entry takes the bytes of its key and its value")
			expect.Equal(t, l.Size(), nodeCost+dataCost, "Add takes the bytes that Cost returned")
		})

		tests := []struct {
			name         string
			giveValueLen int
			wantData     int
		}{
			{name: "counts a negative value length as 0", giveValueLen: -1, wantData: 3},
			{
				name:         "counts a value length above 2^32 - 1 as 2^32 - 1",
				giveValueLen: math.MaxUint32 + 1, wantData: 3 + math.MaxUint32,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, dataCost := newList(t).Cost([]byte("key"), 1, tt.giveValueLen)
				assert.Equal(t, dataCost, tt.wantData, "Cost bounds the length of the value")
			})
		}
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every entry", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			for _, k := range []string{"a", "b", "c"} {
				assert.NoError(t, l.Add([]byte(k), 1, []byte(k)), "Add adds the entry")
			}
			l.Reset()
			_, _, ok := l.Get([]byte("b"), 1)
			expect.Equal(t, l.Len(), 0, "the List has no entries")
			expect.Equal(t, l.Size(), 0, "the List uses no bytes of its arenas")
			expect.False(t, ok, "Get finds no entry")
			expect.Empty(t, scan(l.NewIter()), "a cursor yields no entry")
		})

		t.Run("lets a full List take entries again", func(t *testing.T) {
			t.Parallel()
			l, err := New(4, nodeBytes, 1)
			assert.NoError(t, err, "New returns a List")
			assert.NoError(t, l.Add([]byte("ab"), 1, []byte("cd")), "Add fills the data arena")
			assert.ErrorIs(t, l.Add([]byte("e"), 1, nil), ErrFull, "the data arena is full")
			l.Reset()
			assert.NoError(t, l.Add([]byte("ef"), 1, []byte("gh")), "Add adds an entry after Reset")
			assert.Equal(t, scan(l.NewIter()), []entry{{key: []byte("ef"), seq: 1, value: []byte("gh")}},
				"the List has the entry of the second fill alone")
		})
	})

	t.Run("NewIter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a cursor on no entry", func(t *testing.T) {
			t.Parallel()
			l := newList(t)
			assert.NoError(t, l.Add([]byte("a"), 1, nil), "Add adds the entry")
			it := l.NewIter()
			expect.Nil(t, it.Key(), "the new cursor has no key")
			expect.False(t, it.Next(), "the new cursor has no next entry")
		})
	})

	t.Run("height", func(t *testing.T) {
		t.Parallel()

		key, seq := []byte("key"), uint64(7)
		tests := []struct {
			name        string
			giveProduct uint64
			wantHeight  int
		}{
			{name: "returns maxHeight for a product of 0", giveProduct: 0, wantHeight: maxHeight},
			{name: "returns 1 for a product at the bound of level 1", giveProduct: levelBounds[0], wantHeight: 1},
			{
				name:        "returns 2 for a product below the bound of level 1",
				giveProduct: levelBounds[0] - 1,
				wantHeight:  2,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newList(t)
				// The first factor of the hash is 1 and the second factor is the
				// product, so the hash of the entry is the product.
				l.secret0, l.secret1 = xxh3.Hash(key)^1, seq^tt.giveProduct
				assert.Equal(t, l.height(key, seq), tt.wantHeight, "height counts the bounds above the hash")
			})
		}
	})

	t.Run("compare", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			giveKey  string
			giveSeq  uint64
			giveNode string
			want     int
		}{
			{
				name:    "returns -1 for a key whose prefix is below the prefix of the node",
				giveKey: "abcdefgh", giveSeq: 1, giveNode: "abcdefgi", want: -1,
			},
			{
				name:    "returns +1 for a key whose prefix is above the prefix of the node",
				giveKey: "b", giveSeq: 1, giveNode: "abcdefghx", want: 1,
			},
			{
				name:    "returns -1 for a key with the prefix of the node and a lower ninth byte",
				giveKey: "abcdefghx", giveSeq: 1, giveNode: "abcdefghy", want: -1,
			},
			{
				name:    "returns +1 for a key with the prefix of the node and a higher ninth byte",
				giveKey: "abcdefghy", giveSeq: 1, giveNode: "abcdefghx", want: 1,
			},
			{
				name:    "returns -1 for a key that the key of the node extends with a zero byte",
				giveKey: "a", giveSeq: 1, giveNode: "a\x00", want: -1,
			},
			{
				name:    "returns +1 for a key that extends the key of the node with a zero byte",
				giveKey: "a\x00", giveSeq: 1, giveNode: "a", want: 1,
			},
			{
				name:    "returns 0 for the key and the sequence of the node",
				giveKey: "abcdefghx", giveSeq: 1, giveNode: "abcdefghx", want: 0,
			},
			{
				name:    "returns -1 for the key of the node with a higher sequence",
				giveKey: "abcdefghx", giveSeq: 2, giveNode: "abcdefghx", want: -1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newList(t)
				assert.NoError(t, l.Add([]byte(tt.giveNode), 1, nil), "Add adds the entry of the node")
				nd := l.nodes[nodeHeader]
				key := []byte(tt.giveKey)
				assert.Equal(t, l.compare(key, keyPrefix(key), tt.giveSeq, nd), tt.want,
					"compare orders the entry against the entry of the node")
			})
		}
	})

	t.Run("returns what a sorted model returns under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, listContract, listSteps)
	})
}

// FuzzList checks the machine of listSteps on the call sequences that a
// fuzzer finds.
func FuzzList(f *testing.F) {
	prop.Fuzz(f, listContract, listSteps)
}

// TestListAllocs checks the allocation contract of each method of a List.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestListAllocs(t *testing.T) {
	value := make([]byte, benchValueLen)

	t.Run("Add", func(t *testing.T) {
		l, err := New(1<<20, 1<<20, 1)
		assert.NoError(t, err, "New returns a List")
		var k [benchKeyLen]byte
		i := uint64(0)
		expect.MaxAllocs(t, func() {
			benchKey(&k, i)
			err = l.Add(k[:], 1, value)
			i++
		}, 0, "Add does not allocate")
		assert.NoError(t, err, "the test measures Adds that succeed")
	})

	t.Run("Get", func(t *testing.T) {
		l := filled(t, 1000, benchKey)
		var k, missing [benchKeyLen]byte
		benchKey(&k, 500)
		benchKey(&missing, 1000)
		var hit, miss bool
		expect.MaxAllocs(t, func() { _, _, hit = l.Get(k[:], 1) }, 0, "Get of an entry does not allocate")
		expect.MaxAllocs(t, func() { _, _, miss = l.Get(missing[:], 1) }, 0, "Get of a missing key does not allocate")
		expect.True(t, hit, "the test measures a key of the List")
		expect.False(t, miss, "the test measures a missing key")
	})

	t.Run("Len", func(t *testing.T) {
		l := filled(t, 10, benchKey)
		var got int
		expect.MaxAllocs(t, func() { got = l.Len() }, 0, "Len does not allocate")
		assert.Equal(t, got, 10, "the test measures a List of 10 entries")
	})

	t.Run("Size", func(t *testing.T) {
		l := filled(t, 10, benchKey)
		var got int
		expect.MaxAllocs(t, func() { got = l.Size() }, 0, "Size does not allocate")
		assert.NotEqual(t, got, 0, "the test measures a List of 10 entries")
	})

	t.Run("Cost", func(t *testing.T) {
		l := filled(t, 10, benchKey)
		var k [benchKeyLen]byte
		benchKey(&k, 3)
		var dataCost int
		expect.MaxAllocs(t, func() { _, dataCost = l.Cost(k[:], 1, benchValueLen) }, 0, "Cost does not allocate")
		assert.Equal(t, dataCost, benchKeyLen+benchValueLen, "the test measures the cost of an entry")
	})

	t.Run("Reset", func(t *testing.T) {
		l := filled(t, 10, benchKey)
		var err error
		expect.MaxAllocs(t, func() {
			err = l.Add([]byte("key"), 1, value)
			l.Reset()
		}, 0, "Reset does not allocate")
		assert.NoError(t, err, "the test measures a Reset of an entry")
	})

	t.Run("NewIter", func(t *testing.T) {
		l := filled(t, 10, benchKey)
		var it *Iter
		expect.MaxAllocs(t, func() { it = l.NewIter() }, 1, "NewIter allocates the cursor")
		assert.True(t, it.First(), "the test measures a cursor of the List")
	})
}

// BenchmarkList reports the cost of each method of a List on a hot path,
// and fails when a method allocates.
func BenchmarkList(b *testing.B) {
	value := make([]byte, benchValueLen)

	b.Run("Add", func(b *testing.B) {
		b.Run("by one writer", func(b *testing.B) {
			for _, set := range benchKeySets {
				b.Run("of "+set.name, func(b *testing.B) {
					l, err := New(benchEntries*(benchKeyLen+benchValueLen), benchEntries*benchNodeBytes, 1)
					assert.NoError(b, err, "New returns a List")
					var k [benchKeyLen]byte
					i := uint64(0)

					c := bench.Start(b).MaxAllocs(0)
					defer c.End()

					for c.Loop() {
						if i == benchEntries {
							c.Excluding(l.Reset)
							i = 0
						}
						set.key(&k, i)
						err = l.Add(k[:], 1, value)
						i++
					}

					assert.NoError(b, err, "the benchmark measures Adds that succeed")
				})
			}
		})

		b.Run("by writers in parallel", func(b *testing.B) {
			l, err := New(b.N*(benchKeyLen+benchValueLen), b.N*benchNodeBytes, 1)
			assert.NoError(b, err, "New returns a List")
			var next atomic.Uint64
			var failed atomic.Int64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				var k [benchKeyLen]byte
				for pb.Next() {
					benchKey(&k, next.Add(1))
					if l.Add(k[:], 1, value) != nil {
						failed.Add(1)
					}
				}
			})

			assert.Equal(b, failed.Load(), int64(0), "the benchmark measures Adds that succeed")
		})
	})

	b.Run("Get", func(b *testing.B) {
		for _, set := range benchKeySets {
			b.Run("of "+set.name, func(b *testing.B) {
				l := filled(b, benchEntries, set.key)
				var k [benchKeyLen]byte
				var ok bool
				i := uint64(0)

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					set.key(&k, (i*keyGamma)%benchEntries)
					_, _, ok = l.Get(k[:], 1)
					i++
				}

				assert.True(b, ok, "the benchmark measures keys of the List")
			})
		}
	})

	b.Run("New", func(b *testing.B) {
		b.Run("with 1048576 entries live during runtime.GC", func(b *testing.B) {
			l := filled(b, benchEntries, benchKey)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				runtime.GC()
			}

			assert.Equal(b, l.Len(), benchEntries, "the benchmark keeps the List live")
		})
	})

	b.Run("Len", func(b *testing.B) {
		l := filled(b, 10, benchKey)
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = l.Len()
		}

		assert.Equal(b, got, 10, "the benchmark measures a List of 10 entries")
	})

	b.Run("Size", func(b *testing.B) {
		l := filled(b, 10, benchKey)
		var got int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = l.Size()
		}

		assert.NotEqual(b, got, 0, "the benchmark measures a List of 10 entries")
	})

	b.Run("Cost", func(b *testing.B) {
		l := filled(b, 10, benchKey)
		var k [benchKeyLen]byte
		var nodeCost int
		i := uint64(0)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			benchKey(&k, i)
			nodeCost, _ = l.Cost(k[:], 1, benchValueLen)
			i++
		}

		assert.NotEqual(b, nodeCost, 0, "the benchmark measures the cost of a node")
	})
}

// addInRounds runs writers goroutines that each add one entry with the key
// keyOf(round, writer) and the sequence 0 in each of sharedEntries rounds.
// The writers wait for each other before each round, so their Adds of one
// round run at once. Another goroutine scans the List forward and backward
// while they add. It returns the number of Adds that succeeded and the
// number of pairs of entries that a scan yielded out of order, and stops the
// test for an error other than ErrExists.
func addInRounds(tb testing.TB, l *List, keyOf func(round, writer int) []byte) (added, disordered int) {
	tb.Helper()

	var arrived, running atomic.Int64
	running.Store(writers)
	outcomes := history.Concurrently(writers+1, time.Minute, func(client int) (any, error) {
		if client == writers {
			// The scanner yields after each scan, so the writers progress on
			// one processor too.
			pairs := 0
			for running.Load() > 0 {
				got := scan(l.NewIter())
				for i := 1; i < len(got); i++ {
					if order(got[i-1], got[i]) >= 0 {
						pairs++
					}
				}
				back := l.NewIter()
				for ok, prev := back.Last(), []byte(nil); ok; ok = back.Prev() {
					if prev != nil && bytes.Compare(back.Key(), prev) >= 0 {
						pairs++
					}
					prev = back.Key()
				}
				runtime.Gosched()
			}

			return pairs, nil
		}

		defer running.Add(-1)
		n := 0
		for round := range sharedEntries {
			arrived.Add(1)
			for arrived.Load() < int64(writers*(round+1)) {
				runtime.Gosched()
			}
			err := l.Add(keyOf(round, client), 0, nil)
			if err == nil {
				n++
			} else if !errors.Is(err, ErrExists) {
				return n, err
			}
		}

		return n, nil
	})

	for _, o := range outcomes {
		assert.True(tb, o.Finished, "every goroutine finishes")
		assert.NoError(tb, o.Error, "Add fails only with ErrExists")
		if o.Client == writers {
			disordered = o.Output.(int)
		} else {
			added += o.Output.(int)
		}
	}

	return added, disordered
}

// listSteps runs the steps of a machine over the methods of a List on one
// goroutine, and checks every call against a sorted model of the entries.
func listSteps(c *prop.Case) {
	l, err := New(dataBytes, nodeBytes, c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed"))
	assert.NoError(c, err, "New returns a List")

	var m model
	written := 0
	key := prop.SampledFrom(keys...)
	seq := prop.Integer[uint64](0, 3)
	target := func(c *prop.Case, _ struct{}) any { return entry{key: c.Draw(key, "key"), seq: c.Draw(seq, "seq")} }

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:   opAdd,
				Weight: 4,
				Input: func(c *prop.Case, _ struct{}) any {
					written++

					return entry{key: c.Draw(key, "key"), seq: c.Draw(seq, "seq"), value: []byte(strconv.Itoa(written))}
				},
				Run: func(c *prop.Case, _ int, in any) {
					e := in.(entry)
					nodeCost, dataCost := l.Cost(e.key, e.seq, len(e.value))
					size := l.Size()
					err := l.Add(e.key, e.seq, e.value)
					if !m.add(e) {
						assert.ErrorIs(c, err, ErrExists, "Add returns ErrExists for an entry of the List")
						assert.Equal(c, l.Size(), size, "Add of an entry of the List reserves nothing")

						return
					}
					assert.NoError(c, err, "Add adds a new entry")
					assert.Equal(c, l.Size()-size, nodeCost+dataCost, "Add grows Size by the Cost of its entry")
				},
			},
			{
				Name:   opGet,
				Weight: 4,
				Input:  target,
				Run: func(c *prop.Case, _ int, in any) {
					e := in.(entry)
					value, at, ok := l.Get(e.key, e.seq)
					i, _ := slices.BinarySearchFunc(m, e, order)
					if i == len(m) || !bytes.Equal(m[i].key, e.key) {
						assert.False(c, ok, "Get finds no entry when the first entry at or after it has another key")

						return
					}
					assert.True(c, ok, "Get finds the first entry at or after its key and sequence")
					assert.Equal(c, value, m[i].value, "Get returns the value of the entry")
					assert.Equal(c, at, m[i].seq, "Get returns the sequence of the entry")
				},
			},
			{
				Name: opScan,
				Run: func(c *prop.Case, _ int, _ any) {
					assert.Equal(c, scan(l.NewIter()), []entry(m), "a cursor yields the entries in order",
						assert.EquateEmpty())
				},
			},
			{
				Name:  opSeek,
				Input: target,
				Run: func(c *prop.Case, _ int, in any) {
					e := in.(entry)
					it := l.NewIter()
					i, _ := slices.BinarySearchFunc(m, e, order)
					var got []entry
					for ok := it.SeekGE(e.key, e.seq); ok && len(got) < 3; ok = it.Next() {
						got = append(got, entry{key: it.Key(), seq: it.Seq(), value: it.Value()})
					}
					assert.Equal(c, got, []entry(m[i:min(i+3, len(m))]),
						"SeekGE moves to the first entry at or after its key and sequence", assert.EquateEmpty())
				},
			},
			{
				Name:  opSeekLT,
				Input: target,
				Run: func(c *prop.Case, _ int, in any) {
					e := in.(entry)
					it := l.NewIter()
					i, _ := slices.BinarySearchFunc(m, e, order)
					var got []entry
					for ok := it.SeekLT(e.key, e.seq); ok && len(got) < listed; ok = it.Prev() {
						got = append(got, entry{key: it.Key(), seq: it.Seq(), value: it.Value()})
					}
					want := slices.Clone(m[max(i-listed, 0):i])
					slices.Reverse(want)
					assert.Equal(c, got, want, "SeekLT and Prev move back from the last entry before the target",
						assert.EquateEmpty())
				},
			},
			{
				Name: opLast,
				Run: func(c *prop.Case, _ int, _ any) {
					it := l.NewIter()
					var got []entry
					for ok := it.Last(); ok && len(got) < listed; ok = it.Prev() {
						got = append(got, entry{key: it.Key(), seq: it.Seq(), value: it.Value()})
					}
					want := slices.Clone(m[max(len(m)-listed, 0):])
					slices.Reverse(want)
					assert.Equal(c, got, want, "Last and Prev move back from the last entry", assert.EquateEmpty())
				},
			},
			{
				Name: opWalk,
				Input: func(c *prop.Case, _ struct{}) any {
					return walk{
						at:    entry{key: c.Draw(key, "key"), seq: c.Draw(seq, "seq")},
						moves: c.Draw(prop.List(prop.Boolean(), prop.MaxSize(walkMoves)), "moves"),
					}
				},
				Run: func(c *prop.Case, _ int, in any) {
					w := in.(walk)
					it := l.NewIter()
					// The model of the cursor is a place in m. A move off either end
					// leaves the cursor on no entry, and every later move reports
					// false.
					at, _ := slices.BinarySearchFunc(m, w.at, order)
					on := at < len(m)
					ok := it.SeekGE(w.at.key, w.at.seq)
					for i := 0; ; i++ {
						assert.Equal(c, ok, on, "a move reports whether the model has an entry at its place")
						if on {
							assert.Equal(c, entry{key: it.Key(), seq: it.Seq(), value: it.Value()}, m[at],
								"a move puts the cursor on the entry of the model")
						}
						if i == len(w.moves) {
							return
						}
						if w.moves[i] {
							ok = it.Next()
							at++
							on = on && at < len(m)
						} else {
							ok = it.Prev()
							at--
							on = on && at >= 0
						}
					}
				},
			},
			{
				Name: opReset,
				Run: func(c *prop.Case, _ int, _ any) {
					// The bytes of the data of an entry follow from its lengths
					// alone, and the bytes of its node from the seed of the List.
					assert.Pure(c, func() int {
						nodeCost, _ := l.Cost(keys[1], 1, 1)

						return nodeCost
					}, func() {
						l.Reset()
						m = m[:0]
					}, "Reset keeps the bytes of the node of an entry")
					assert.Equal(c, l.Size(), 0, "Reset frees both arenas")
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, l.Len(), len(m), "Len counts the entries")
			assert.NoError(c, check(l), "the List keeps the invariants of its levels")
		},
	})
}

// check returns an error when l breaks an invariant of its nodes and its
// levels:
//
//   - The two prefix words of each node contain the first 8 bytes of its
//     key, padded with zero bytes.
//   - Level 0 links every entry in the order of the List.
//   - Each higher level links the nodes whose height exceeds the level, in
//     the order of level 0.
//   - The height of l is the height of its tallest node, or 1 when l is
//     empty.
//
// check takes the prefix of a key from the bytes of the key, so it detects
// prefix words that differ from those bytes. Each walk stops after one node
// more than the List has, so a link that loops ends the walk.
func check(l *List) error {
	var nodes []uint32
	tallest := 1
	for nd := l.nodes[nodeHeader]; nd != 0 && len(nodes) <= l.Len(); nd = l.nodes[nd+nodeHeader] {
		key := l.key(nd)
		seq := uint64(l.nodes[nd+wordSeqHigh])<<32 | uint64(l.nodes[nd+wordSeqLow])
		var want, stored [8]byte
		copy(want[:], key)
		binary.BigEndian.PutUint32(stored[:4], l.nodes[nd+wordPrefixHigh])
		binary.BigEndian.PutUint32(stored[4:], l.nodes[nd+wordPrefixLow])
		if stored != want {
			return fmt.Errorf("%w: the node of the key %q stores the prefix %q", errInvariant, key, stored[:])
		}
		prefix := binary.BigEndian.Uint64(want[:])
		if len(nodes) != 0 && l.compare(key, prefix, seq, nodes[len(nodes)-1]) <= 0 {
			return fmt.Errorf("%w: level 0 links a node out of order", errInvariant)
		}
		tallest = max(tallest, l.height(key, seq))
		nodes = append(nodes, nd)
	}
	if len(nodes) != l.Len() {
		return fmt.Errorf("%w: level 0 links %d nodes for %d entries", errInvariant, len(nodes), l.Len())
	}
	if levels := int(l.levels.Load()); levels != tallest {
		return fmt.Errorf("%w: a height of %d for a tallest node of %d levels", errInvariant, levels, tallest)
	}

	for level := 1; level < maxHeight; level++ {
		var want, got []uint32
		for _, nd := range nodes {
			seq := uint64(l.nodes[nd+wordSeqHigh])<<32 | uint64(l.nodes[nd+wordSeqLow])
			if l.height(l.key(nd), seq) > level {
				want = append(want, nd)
			}
		}
		at := uint32(level)
		for nd := l.nodes[nodeHeader+at]; nd != 0 && len(got) <= len(nodes); nd = l.nodes[nd+nodeHeader+at] {
			got = append(got, nd)
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("%w: level %d links %d nodes where %d nodes have the level", errInvariant, level,
				len(got), len(want))
		}
	}

	return nil
}

// historySteps runs Add and Get on a List from two concurrent clients, and
// checks that their history is linearizable against the spec of a List of
// historyKeys keys with historySeqs sequences each. The key of number k is
// the byte 'a' plus k, and a value encodes its number in 8 bytes.
func historySteps(c *prop.Case) {
	l, err := New(dataBytes, nodeBytes, c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed"))
	assert.NoError(c, err, "New returns a List")

	written := 0
	target := func(c *prop.Case, _ versions) any {
		return [2]int{c.Draw(prop.Integer(0, historyKeys-1), "key"), c.Draw(prop.Integer(0, historySeqs-1), "seq")}
	}

	stateful.Steps(c, stateful.Machine[versions]{
		Spec: history.Spec[versions]{
			Initial: func() versions {
				var state versions
				for i := range state {
					state[i] = absent
				}

				return state
			},
			Next: stepList,
		},
		Actions: []stateful.Action[versions]{
			{
				Name: opAdd,
				Input: func(c *prop.Case, state versions) any {
					written++
					at := target(c, state).([2]int)

					return [3]int{at[0], at[1], written}
				},
				Run: func(c *prop.Case, client int, in any) {
					a := in.([3]int)
					call := c.History().Invoke(client, opAdd, []any{a[0], a[1], a[2]})
					value := binary.BigEndian.AppendUint64(nil, uint64(a[2]))
					err := l.Add([]byte{byte('a' + a[0])}, uint64(a[1]), value)
					if err != nil {
						assert.ErrorIs(c, err, ErrExists, "Add fails only for an entry of the List")
					}
					call.OK(err == nil)
				},
			},
			{
				Name:  opGet,
				Input: target,
				Run: func(c *prop.Case, client int, in any) {
					g := in.([2]int)
					call := c.History().Invoke(client, opGet, []any{g[0], g[1]})
					value, seq, ok := l.Get([]byte{byte('a' + g[0])}, uint64(g[1]))
					got := lookup{value: absent, seq: seq, ok: ok}
					if ok {
						got.value = int(binary.BigEndian.Uint64(value))
					}
					call.OK(got)
				},
			},
		},
	}, stateful.Clients(2))
}

// stepList returns the state of a List after op, or none when the List
// cannot have returned the output of op in state. An Add reports whether it
// added its entry. A Get returns the newest version of its key at or below
// its sequence.
func stepList(state versions, op history.Operation) []versions {
	k, s := op.Args[0].(int), op.Args[1].(int)
	if op.Name == opAdd {
		i := k*historySeqs + s
		if state[i] != absent {
			if op.Returned(false) {
				return []versions{state}
			}

			return nil
		}
		if !op.Returned(true) {
			return nil
		}
		state[i] = op.Args[2].(int)

		return []versions{state}
	}

	want := lookup{value: absent}
	for v := s; v >= 0; v-- {
		if value := state[k*historySeqs+v]; value != absent {
			want = lookup{value: value, seq: uint64(v), ok: true}

			break
		}
	}
	if op.Returned(want) {
		return []versions{state}
	}

	return nil
}
