// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package radix_test

import (
	"flag"
	"fmt"
	"iter"
	"maps"
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/radix"
)

// The workloads of the benchmarks and of the allocation test.
const (
	// benchKeys is the number of keys of the maps of most benchmarks.
	benchKeys = 1 << 16

	// gcKeys is the number of keys of the map that the benchmark of a
	// collection keeps live.
	gcKeys = 1 << 20

	// widest is the most keys that a fill of the machine sets, more than the
	// 48 children of a full node48.
	widest = 80
)

// modelContract is the contract of modelSteps, which TestMap and FuzzMap
// check.
const modelContract = "a map returns what a Go map returns under any sequence of calls"

// childEnv is set in the environment of the child process in which a case
// runs alone.
const childEnv = "CORE_TEST_CHILD"

// inlineLens are the inline key lengths of the three widths of a leaf.
var inlineLens = []int{12, 28, 60}

// stems are the keys that the steps of the machine extend. Their lengths
// are on both sides of every inline length. Many share a long run of one
// byte, so a node skips more than 8 bytes, and a key splits a skip past its
// first 8 bytes.
var stems = func() []string {
	ss := []string{""}
	for _, n := range []int{1, 2, 8, 9, 11, 12, 13, 27, 28, 29, 59, 60, 61} {
		ss = append(ss, strings.Repeat("k", n))
	}

	return append(ss, strings.Repeat("k", 20)+"x", strings.Repeat("k", 9)+"y"+strings.Repeat("k", 10))
}()

// tails are the endings that a step appends to a stem.
var tails = []string{"", "a", "b", "c", "ab", "ba", "abc"}

// pair is a key and its value, as a scan yields them.
type pair struct {
	key   string
	value int
}

// stored is the length of a stored prefix of a key and its value, as
// Prefixes yields them.
type stored struct {
	n, value int
}

// fill is the input of the steps that set and delete the keys of a stem
// followed by each of n bytes from lo on.
type fill struct {
	stem  string
	lo, n int
}

func TestMap(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty map", func(t *testing.T) {
			t.Parallel()
			m := radix.New[string, int](radix.Config{Limit: 3, InlineKeyLen: 28})
			expect.Equal(t, m.Len(), 0, "the new map has no key")
			_, ok := m.Get("")
			expect.False(t, ok, "the new map does not contain the empty key")
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for the zero map", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			_, ok := m.Get("a")
			assert.False(t, ok, "the zero map contains no key")
		})

		t.Run("returns the value of the empty key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for i, k := range []string{"", "a", "ab"} {
				_, _, err := m.Set(k, i+1)
				assert.NoError(t, err, "Set stores the key")
			}
			v, ok := m.Get("")
			assert.True(t, ok, "the map contains the empty key")
			assert.Equal(t, v, 1, "Get returns the value of the empty key")
		})

		t.Run("reports false for a key that differs past the first 8 bytes of a skip", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			long := strings.Repeat("k", 30)
			for _, k := range []string{long + "a", long + "b"} {
				_, _, err := m.Set(k, 1)
				assert.NoError(t, err, "Set stores the key")
			}
			_, ok := m.Get(strings.Repeat("k", 20) + "x" + strings.Repeat("k", 9) + "a")
			assert.False(t, ok, "the map does not contain a key that differs inside the skip")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		for _, inline := range inlineLens {
			t.Run("returns what a Go map returns with "+strconv.Itoa(inline)+" bytes in place", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, modelContract, func(c *prop.Case) { modelSteps(c, inline) })
			})
		}

		t.Run("returns the previous value of a key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			_, ok, err := m.Set("key", 1)
			assert.NoError(t, err, "Set stores the new key")
			assert.False(t, ok, "the map did not contain the key")
			old, ok, err := m.Set("key", 2)
			assert.NoError(t, err, "Set replaces the value")
			assert.True(t, ok, "the map contained the key")
			assert.Equal(t, old, 1, "Set returns the previous value")
		})

		tests := []struct {
			name     string
			giveKeys []string
			giveNew  string
		}{
			{
				name:     "returns ErrFull for a new key that extends the key of a leaf",
				giveKeys: []string{"a", "b"},
				giveNew:  "ab",
			},
			{name: "returns ErrFull for a new key that ends at a node", giveKeys: []string{"ab", "ac"}, giveNew: "a"},
			{
				name:     "returns ErrFull for a new key at a free slot of a node",
				giveKeys: []string{"ab", "ac"},
				giveNew:  "ad",
			},
			{
				name:     "returns ErrFull for a new key that differs inside the skip of a node",
				giveKeys: []string{"abcx", "abcy"},
				giveNew:  "abz",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := radix.New[string, int](radix.Config{Limit: len(tt.giveKeys)})
				want := make([]pair, 0, len(tt.giveKeys))
				for _, k := range tt.giveKeys {
					_, _, err := m.Set(k, 1)
					assert.NoError(t, err, "Set stores a key below the limit")
					want = append(want, pair{key: k, value: 1})
				}
				_, _, err := m.Set(tt.giveNew, 1)
				assert.ErrorIs(t, err, radix.ErrFull, "Set refuses the new key")
				expect.Equal(t, m.Len(), len(tt.giveKeys), "the full map keeps its keys")
				expect.Equal(t, collect(m.All()), want, "the full map is unchanged")
			})
		}

		t.Run("replaces the value of a key in a map at its limit", func(t *testing.T) {
			t.Parallel()
			m := radix.New[string, int](radix.Config{Limit: 1})
			_, _, err := m.Set("a", 1)
			assert.NoError(t, err, "Set stores the first key")
			old, ok, err := m.Set("a", 2)
			assert.NoError(t, err, "Set replaces the value in the full map")
			expect.True(t, ok, "the map contained the key")
			expect.Equal(t, old, 1, "Set returns the previous value")
		})

		t.Run("returns ErrTooLong for a key of 2^32 bytes", func(t *testing.T) {
			t.Parallel()
			// The key takes 4 GiB of address space. The runtime zeroes a large
			// allocation that reuses pages that a collection freed, and so
			// touches every page. The case therefore runs alone in a child
			// process of the test binary, whose heap has freed no pages yet.
			if os.Getenv(childEnv) == "" {
				exe, err := os.Executable()
				assert.NoError(t, err, "the test binary has a path")
				levels := strings.Split(t.Name(), "/")
				for i, level := range levels {
					levels[i] = "^" + regexp.QuoteMeta(level) + "$"
				}
				args := []string{"-test.run=" + strings.Join(levels, "/"), "-test.count=1"}
				if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
					args = append(args, "-test.gocoverdir="+dir.Value.String())
				}
				if deadline, ok := t.Deadline(); ok {
					args = append(args, "-test.timeout="+time.Until(deadline).String())
				}
				cmd := exec.CommandContext(t.Context(), exe, args...)
				cmd.Env = append(os.Environ(), childEnv+"=1")
				out, err := cmd.CombinedOutput()
				assert.NoError(t, err, "the case passes in a child process, whose output is:\n"+string(out))

				return
			}
			key := make([]byte, math.MaxUint32+1)
			var m radix.Map[[]byte, int]
			_, _, err := m.Set(key, 1)
			assert.ErrorIs(t, err, radix.ErrTooLong, "Set refuses the key")
			assert.Equal(t, m.Len(), 0, "the map stores no key")
		})

		t.Run("copies the bytes of a key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[[]byte, int]
			lengths := []int{5, 40}
			for i, n := range lengths {
				key := []byte(strings.Repeat("k", n))
				_, _, err := m.Set(key, i)
				assert.NoError(t, err, "Set stores the key")
				key[0] = 'x'
			}
			for i, n := range lengths {
				v, ok := m.Get([]byte(strings.Repeat("k", n)))
				expect.True(t, ok, "the map contains the key as Set received it")
				expect.Equal(t, v, i, "Get returns the value of the key")
			}
		})
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for a key that the map does not contain", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			_, ok := m.Delete("a")
			expect.False(t, ok, "the zero map deletes no key")
			_, _, err := m.Set("ab", 1)
			assert.NoError(t, err, "Set stores the key")
			for _, k := range []string{"", "a", "abc", "b"} {
				_, ok := m.Delete(k)
				expect.False(t, ok, "Delete finds no key "+strconv.Quote(k))
			}
			expect.Equal(t, m.Len(), 1, "the map keeps its key")
		})

		t.Run("returns the value of the key that it removes", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			_, _, err := m.Set("ab", 7)
			assert.NoError(t, err, "Set stores the key")
			v, ok := m.Delete("ab")
			assert.True(t, ok, "Delete finds the key")
			expect.Equal(t, v, 7, "Delete returns the value")
			expect.Equal(t, m.Len(), 0, "the map is empty")
		})

		t.Run("keeps the key that ends at a node of one child", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for i, k := range []string{"a", "ab", "ac"} {
				_, _, err := m.Set(k, i)
				assert.NoError(t, err, "Set stores the key")
			}
			_, ok := m.Delete("ac")
			assert.True(t, ok, "Delete removes the key")
			assert.Equal(t, collect(m.All()), []pair{{key: "a", value: 0}, {key: "ab", value: 1}},
				"the map keeps the key of the node and its child")
		})

		// Delete reads the key of a leaf before it removes the leaf. The freed
		// record of the first key contains the empty key. The map does not
		// store the empty key.
		t.Run("reports false for the empty key after the first key is deleted", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for i, k := range []string{"a", "b"} {
				_, _, err := m.Set(k, i)
				assert.NoError(t, err, "Set stores the key")
			}
			_, ok := m.Delete("a")
			assert.True(t, ok, "Delete removes the first key")
			_, ok = m.Delete("")
			assert.False(t, ok, "Delete finds no empty key")
			assert.Equal(t, m.Len(), 1, "the map keeps its other key")
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the keys that Set added", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for i, k := range []string{"a", "b", "a", "ab"} {
				_, _, err := m.Set(k, i)
				assert.NoError(t, err, "Set stores the key")
			}
			assert.Equal(t, m.Len(), 3, "Len counts each key once")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for i := range 100 {
				_, _, err := m.Set(strings.Repeat("k", i), i)
				assert.NoError(t, err, "Set stores the key")
			}
			m.Reset()
			expect.Equal(t, m.Len(), 0, "the reset map has no key")
			expect.Empty(t, collect(m.All()), "the reset map yields no key")
			_, _, err := m.Set("a", 1)
			assert.NoError(t, err, "Set stores a key after Reset")
			expect.Equal(t, collect(m.All()), []pair{{key: "a", value: 1}}, "the reset map contains the new key")
		})
	})

	t.Run("LongestPrefix", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveStore []string
			giveKey   string
			wantN     int
			wantOK    bool
		}{
			{
				name:      "returns the longest of several stored prefixes",
				giveStore: []string{"a", "ab", "abcd"},
				giveKey:   "abc",
				wantN:     2,
				wantOK:    true,
			},
			{name: "returns the key itself", giveStore: []string{"a", "ab"}, giveKey: "ab", wantN: 2, wantOK: true},
			{name: "returns the empty key", giveStore: []string{"", "b"}, giveKey: "abc", wantN: 0, wantOK: true},
			{name: "reports false without a stored prefix", giveStore: []string{"ab", "b"}, giveKey: "a"},
			{
				name:      "reports false for a stored key that differs inside a skip",
				giveStore: []string{strings.Repeat("k", 20), strings.Repeat("k", 20) + "a"},
				giveKey:   strings.Repeat("k", 10) + "x" + strings.Repeat("k", 9) + "a",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var m radix.Map[string, int]
				for _, k := range tt.giveStore {
					_, _, err := m.Set(k, len(k))
					assert.NoError(t, err, "Set stores the key")
				}
				n, v, ok := m.LongestPrefix(tt.giveKey)
				assert.Equal(t, ok, tt.wantOK, "LongestPrefix reports whether a stored key is a prefix")
				expect.Equal(t, n, tt.wantN, "LongestPrefix returns the length of the stored prefix")
				expect.Equal(t, v, tt.wantN, "LongestPrefix returns the value of the stored prefix")
			})
		}
	})

	t.Run("Prefixes", func(t *testing.T) {
		t.Parallel()

		t.Run("stops when the yield returns false", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for _, k := range []string{"a", "ab", "abc"} {
				_, _, err := m.Set(k, len(k))
				assert.NoError(t, err, "Set stores the key")
			}
			var got []int
			for n := range m.Prefixes("abcd") {
				got = append(got, n)
				if n == 2 {
					break
				}
			}
			assert.Equal(t, got, []int{1, 2}, "Prefixes yields up to the break")
		})

		t.Run("ends after the loop body adds a key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for _, k := range []string{"a", "ab", "abc"} {
				_, _, err := m.Set(k, len(k))
				assert.NoError(t, err, "Set stores the key")
			}
			var got []int
			for n := range m.Prefixes("abcd") {
				got = append(got, n)
				_, _, err := m.Set("abcd", 4)
				assert.NoError(t, err, "the loop body adds a key")
			}
			assert.Equal(t, got, []int{1}, "Prefixes ends after the body adds a key")
		})
	})

	t.Run("Scan", func(t *testing.T) {
		t.Parallel()

		t.Run("yields no key of the zero map", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			assert.Empty(t, collect(m.Scan("a")), "Scan yields no key")
		})

		t.Run("stops when the yield returns false", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for _, k := range []string{"a", "ab", "b"} {
				_, _, err := m.Set(k, len(k))
				assert.NoError(t, err, "Set stores the key")
			}
			var got []string
			for k := range m.Scan("") {
				got = append(got, string(k))
				break
			}
			assert.Equal(t, got, []string{"a"}, "Scan yields up to the break")
		})

		t.Run("ends after the loop body deletes a key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for _, k := range []string{"a", "ab", "b"} {
				_, _, err := m.Set(k, len(k))
				assert.NoError(t, err, "Set stores the key")
			}
			var got []string
			for k := range m.Scan("") {
				got = append(got, string(k))
				m.Delete("b")
			}
			assert.Equal(t, got, []string{"a"}, "Scan ends after the body deletes a key")
		})

		writes := []struct {
			name      string
			giveKeys  []string
			giveWrite func(m *radix.Map[string, int]) error
		}{
			{
				name:     "ends after the loop body adds the key of a node",
				giveKeys: []string{"ab", "ac"},
				giveWrite: func(m *radix.Map[string, int]) error {
					_, _, err := m.Set("a", 0)

					return err
				},
			},
			{
				name:     "ends after the loop body adds a key that splits a skip",
				giveKeys: []string{"abcx", "abcy"},
				giveWrite: func(m *radix.Map[string, int]) error {
					_, _, err := m.Set("abz", 0)

					return err
				},
			},
			{
				name:     "ends after the loop body resets the map",
				giveKeys: []string{"ab", "ac"},
				giveWrite: func(m *radix.Map[string, int]) error {
					m.Reset()

					return nil
				},
			},
		}
		for _, tt := range writes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var m radix.Map[string, int]
				for i, k := range tt.giveKeys {
					_, _, err := m.Set(k, i)
					assert.NoError(t, err, "Set stores the key")
				}
				var got []string
				for k := range m.Scan("") {
					got = append(got, string(k))
					assert.NoError(t, tt.giveWrite(&m), "the loop body writes to the map")
				}
				assert.Equal(t, got, tt.giveKeys[:1], "Scan ends after the body writes to the map")
			})
		}

		t.Run("continues after the loop body replaces a value", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			for _, k := range []string{"a", "ab", "b"} {
				_, _, err := m.Set(k, len(k))
				assert.NoError(t, err, "Set stores the key")
			}
			for k := range m.Scan("") {
				_, _, err := m.Set(string(k), -1)
				assert.NoError(t, err, "the loop body replaces a value")
			}
			want := []pair{{key: "a", value: -1}, {key: "ab", value: -1}, {key: "b", value: -1}}
			assert.Equal(t, collect(m.All()), want, "Scan yields every key to a body that replaces values")
		})

		t.Run("yields no key after a Delete of the last key", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			_, _, err := m.Set("a", 1)
			assert.NoError(t, err, "Set stores the key")
			_, ok := m.Delete("a")
			assert.True(t, ok, "Delete removes the key")
			assert.Empty(t, collect(m.Scan("")), "Scan yields no key")
		})

		t.Run("yields no key for a prefix that differs inside a skip", func(t *testing.T) {
			t.Parallel()
			var m radix.Map[string, int]
			long := strings.Repeat("k", 30)
			for _, k := range []string{long + "a", long + "b"} {
				_, _, err := m.Set(k, 1)
				assert.NoError(t, err, "Set stores the key")
			}
			assert.Empty(t, collect(m.Scan(strings.Repeat("k", 20)+"x")), "Scan yields no key")
		})
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every key in the order of their bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "All yields the keys of the map in the order of bytes.Compare", func(c *prop.Case) {
				var m radix.Map[[]byte, int]
				for i, k := range c.Draw(prop.List(prop.Bytes(prop.MaxSize(70)), prop.MaxSize(200)), "keys") {
					_, _, err := m.Set(k, i)
					assert.NoError(c, err, "Set stores the key")
				}
				var keys []string
				for k := range m.All() {
					keys = append(keys, string(k))
				}
				assert.Length(c, keys, m.Len(), "All yields every key")
				assert.Pairwise(c, keys, func(a, b string) bool { return a < b }, "All yields the keys in order")
			})
		})
	})
}

// FuzzMap checks the machine of modelSteps with leaves of 16 bytes on the
// sequences of calls that a fuzzer finds.
func FuzzMap(f *testing.F) {
	prop.Fuzz(f, modelContract, func(c *prop.Case) { modelSteps(c, inlineLens[0]) })
}

// TestMapAllocs checks the allocation contract of the methods of a map.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestMapAllocs(t *testing.T) {
	keys := urls(4096)
	m := radix.New[string, int](radix.Config{InlineKeyLen: 28})
	for i, k := range keys {
		_, _, err := m.Set(k, i)
		assert.NoError(t, err, "Set stores the key")
	}
	key := keys[100]

	t.Run("Get", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = m.Get(key) }, 0, "Get does not allocate")
		expect.True(t, ok, "the test measures a Get of a key of the map")
	})

	t.Run("LongestPrefix", func(t *testing.T) {
		probe := key + "/more"
		var ok bool
		expect.MaxAllocs(t, func() { _, _, ok = m.LongestPrefix(probe) }, 0, "LongestPrefix does not allocate")
		expect.True(t, ok, "the test measures a key with a stored prefix")
	})

	t.Run("Prefixes", func(t *testing.T) {
		n := 0
		expect.MaxAllocs(t, func() {
			for range m.Prefixes(key) {
				n++
			}
		}, 0, "a range loop over Prefixes does not allocate")
		expect.NotEqual(t, n, 0, "the test measures stored prefixes")
	})

	t.Run("Scan", func(t *testing.T) {
		n := 0
		expect.MaxAllocs(t, func() {
			for range m.Scan(key[:30]) {
				n++
			}
		}, 0, "a range loop over Scan does not allocate")
		expect.NotEqual(t, n, 0, "the test measures keys with the prefix")
	})

	t.Run("Set", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, _, err = m.Set(key, 1) }, 0, "a Set that replaces a value does not allocate")
		expect.NoError(t, err, "the test measures a Set that succeeds")
	})

	t.Run("Delete", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() {
			m.Delete(key)
			_, _, err = m.Set(key, 1)
		}, 0, "a Delete and a Set of the same key reuse the records and the region")
		expect.NoError(t, err, "the test measures a Set that succeeds")
	})

	t.Run("Reset", func(t *testing.T) {
		short := radix.New[string, int](radix.Config{})
		expect.MaxAllocsWithSetup(t, func() *radix.Map[string, int] {
			for i, k := range keys[:64] {
				_, _, err := short.Set(k[len(k)-8:], i)
				assert.NoError(t, err, "Set stores the key")
			}

			return short
		}, (*radix.Map[string, int]).Reset, 0, "Reset does not allocate")
		expect.Equal(t, short.Len(), 0, "the test measures a Reset that removes the keys")

		// The 64 keys of a fill take 64 KiB of regions, so a Reset that keeps
		// the regions out of the slabs makes the fills allocate a slab of 1 MiB
		// every 16 calls.
		long := radix.New[string, int](radix.Config{})
		longKeys := make([]string, 0, 64)
		for i := range 64 {
			longKeys = append(longKeys, strings.Repeat("k", 1000)+strconv.Itoa(1000+i))
		}
		var err error
		expect.MaxAllocs(t, func() {
			long.Reset()
			for i, k := range longKeys {
				_, _, err = long.Set(k, i)
			}
		}, 0, "a Reset and the Sets of the same long keys reuse the regions")
		expect.NoError(t, err, "the test measures Sets that succeed")
		expect.Equal(t, long.Len(), len(longKeys), "the test measures fills of every long key")
	})
}

// BenchmarkMap reports the cost of the methods of a map, of a longest-prefix
// match over a btree.Map with one lookup per length, and of a collection
// with a large map live. It fails when a measured call allocates.
func BenchmarkMap(b *testing.B) {
	keys := urls(benchKeys)
	m := radix.New[string, int](radix.Config{InlineKeyLen: 60})
	var bt btree.Map[string, int]
	for i, k := range keys {
		_, _, err := m.Set(k, i)
		assert.NoError(b, err, "Set stores the key")
		bt.Set(k, i)
	}

	b.Run("Get", func(b *testing.B) {
		var ok bool
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = m.Get(keys[i%len(keys)])
			i++
		}

		assert.True(b, ok, "the benchmark measures Gets of keys of the map")
	})

	b.Run("LongestPrefix", func(b *testing.B) {
		probes := make([]string, len(keys))
		for i, k := range keys {
			probes[i] = k + "/index.html"
		}

		b.Run("radix", func(b *testing.B) {
			var ok bool
			i := 0

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, _, ok = m.LongestPrefix(probes[i%len(probes)])
				i++
			}

			assert.True(b, ok, "the benchmark measures keys with a stored prefix")
		})

		b.Run("btree", func(b *testing.B) {
			var ok bool
			i := 0

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				p := probes[i%len(probes)]
				ok = false
				for n := len(p); n >= 0 && !ok; n-- {
					_, ok = bt.Get(p[:n])
				}
				i++
			}

			assert.True(b, ok, "the benchmark measures keys with a stored prefix")
		})
	})

	b.Run("Set", func(b *testing.B) {
		var err error
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, _, err = m.Set(keys[i%len(keys)], i)
			i++
		}

		assert.NoError(b, err, "the benchmark measures Sets that succeed")
	})

	b.Run("All", func(b *testing.B) {
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for range m.All() {
				n++
			}
		}

		assert.NotEqual(b, n, 0, "the benchmark measures scans of keys")
	})

	b.Run("GC", func(b *testing.B) {
		big := radix.New[string, int](radix.Config{InlineKeyLen: 60})
		for i, k := range urls(gcKeys) {
			_, _, err := big.Set(k, i)
			assert.NoError(b, err, "Set stores the key")
		}

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			runtime.GC()
		}

		assert.Equal(b, big.Len(), gcKeys, "the benchmark keeps the map live")
	})
}

// modelSteps runs the steps of a machine of calls over a map whose leaves
// store keys of up to inline bytes in place, with a Go map as its model.
// After each step, the map must have the length of the model and yield its
// keys and values in order.
func modelSteps(c *prop.Case, inline int) {
	m := radix.New[string, int](radix.Config{InlineKeyLen: inline})
	model := map[string]int{}
	value := 0

	key := func(c *prop.Case, _ struct{}) any {
		return c.Draw(prop.SampledFrom(stems...), "stem") + c.Draw(prop.SampledFrom(tails...), "tail")
	}
	span := func(c *prop.Case, _ struct{}) any {
		return fill{
			stem: c.Draw(prop.SampledFrom(stems...), "stem"),
			lo:   c.Draw(prop.Integer(0, 255), "lo"),
			n:    c.Draw(prop.Integer(1, widest), "n"),
		}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:   "set",
				Weight: 3,
				Input:  key,
				Run: func(c *prop.Case, _ int, in any) {
					k := in.(string)
					value++
					old, ok, err := m.Set(k, value)
					assert.NoError(c, err, "Set stores the key")
					want, present := model[k]
					assert.Equal(c, ok, present, "Set reports whether the model contains the key")
					assert.Equal(c, old, want, "Set returns the value of the model")
					model[k] = value
				},
			},
			{
				Name:  "get",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					k := in.(string)
					got, ok := m.Get(k)
					want, present := model[k]
					assert.Equal(c, ok, present, "Get reports whether the model contains the key")
					assert.Equal(c, got, want, "Get returns the value of the model")
				},
			},
			{
				Name:   "delete",
				Weight: 2,
				Input:  key,
				Run: func(c *prop.Case, _ int, in any) {
					k := in.(string)
					got, ok := m.Delete(k)
					want, present := model[k]
					assert.Equal(c, ok, present, "Delete reports whether the model contains the key")
					assert.Equal(c, got, want, "Delete returns the value of the model")
					delete(model, k)
				},
			},
			{
				Name:  "longestPrefix",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					k := in.(string)
					n, v, ok := m.LongestPrefix(k)
					want := modelPrefixes(model, k)
					assert.Equal(c, ok, len(want) != 0, "LongestPrefix reports whether the model has a prefix")
					if ok {
						assert.Equal(c, stored{n: n, value: v}, want[len(want)-1],
							"LongestPrefix returns the longest prefix of the model")
					}
				},
			},
			{
				Name:  "prefixes",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					k := in.(string)
					var got []stored
					for n, v := range m.Prefixes(k) {
						got = append(got, stored{n: n, value: v})
					}
					assert.Equal(c, got, modelPrefixes(model, k), "Prefixes yields the prefixes of the model in order")
				},
			},
			{
				Name:  "scan",
				Input: key,
				Run: func(c *prop.Case, _ int, in any) {
					p := in.(string)
					var want []pair
					for _, k := range slices.Sorted(maps.Keys(model)) {
						if strings.HasPrefix(k, p) {
							want = append(want, pair{key: k, value: model[k]})
						}
					}
					assert.Equal(c, collect(m.Scan(p)), want, "Scan yields the keys of the model with the prefix")
				},
			},
			{
				Name:  "fill",
				Input: span,
				Run: func(c *prop.Case, _ int, in any) {
					f := in.(fill)
					value++
					for j := range f.n {
						k := f.stem + string([]byte{byte(f.lo + j)})
						_, _, err := m.Set(k, value)
						assert.NoError(c, err, "Set stores the key of the fill")
						model[k] = value
					}
				},
			},
			{
				Name:  "erase",
				Input: span,
				Run: func(c *prop.Case, _ int, in any) {
					f := in.(fill)
					for j := range f.n {
						k := f.stem + string([]byte{byte(f.lo + j)})
						_, ok := m.Delete(k)
						_, present := model[k]
						assert.Equal(c, ok, present, "Delete reports whether the model contains the key of the erase")
						delete(model, k)
					}
				},
			},
			{
				Name: "reset",
				Run: func(*prop.Case, int, any) {
					m.Reset()
					clear(model)
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, m.Len(), len(model), "the map has the length of the model")
			want := make([]pair, 0, len(model))
			for _, k := range slices.Sorted(maps.Keys(model)) {
				want = append(want, pair{key: k, value: model[k]})
			}
			assert.Equal(c, collect(m.All()), want, "All yields the keys and the values of the model in order",
				assert.EquateEmpty())
		},
	})
}

// modelPrefixes returns the keys of model that are prefixes of key, from the
// shortest to the longest, as Prefixes yields them.
func modelPrefixes(model map[string]int, key string) []stored {
	var ps []stored
	for n := range len(key) + 1 {
		if v, ok := model[key[:n]]; ok {
			ps = append(ps, stored{n: n, value: v})
		}
	}

	return ps
}

// collect returns a copy of every key and value that seq yields.
func collect(seq iter.Seq2[[]byte, int]) []pair {
	var ps []pair
	for k, v := range seq {
		ps = append(ps, pair{key: string(k), value: v})
	}

	return ps
}

// urls returns n distinct keys like URLs, 56 bytes long on average, which
// share their scheme, their hosts and their first path segments.
func urls(n int) []string {
	keys := make([]string, 0, n)
	for i := range n {
		keys = append(keys, fmt.Sprintf("https://host-%02d.example.com/api/v%d/items/%07d", i%31, i%3, i))
	}

	return keys
}
