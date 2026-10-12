// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap_test

import (
	"iter"
	"maps"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/idmap"
)

// The names of the actions of the machines, and of the calls that the
// histories record.
const (
	opGet    = "Get"
	opSet    = "Set"
	opDelete = "Delete"
	opClear  = "Clear"
	opClone  = "Clone"
	opAll    = "All"
	opLoad   = "Load"
	opStore  = "Store"
)

// entry is the input of a step that stores a key: the key and its value.
type entry[K comparable] struct {
	key   K
	value int
}

// mapOf is the method set that a Map and a Typed share. M is the type of
// the map, which Clone returns.
type mapOf[K comparable, M any] interface {
	Get(key K) (int, bool)
	Set(key K, value int) (int, bool)
	Delete(key K) (int, bool)
	Len() int
	Clear()
	Clone() M
	All() iter.Seq2[K, int]
}

// keyOf returns the key number i of a test: i/4 in its first 8 bytes,
// little-endian, and i%4 in its last byte. Four consecutive keys differ in
// their last byte alone, which is past the first 16 bytes of an ID160 or an
// ID256 key.
func keyOf[K idmap.Key](i int) K {
	var k K
	for b := range 8 {
		k[b] = byte(i / 4 >> (8 * b))
	}

	k[len(k)-1] = byte(i % 4)

	return k
}

// checkMap runs random calls on a map that newMap returns, with the keys
// that key returns for a key space of three times the capacity, and checks
// each result against a Go map that receives the same calls. A Clone step
// continues the calls on the copy, and every later step checks that the map
// that Clone copied is unchanged.
func checkMap[K comparable, M mapOf[K, M]](t *testing.T, newMap func(capacity int) M, key func(i int) K) {
	t.Helper()

	prop.ForAll(t, "the map must return what a Go map returns for the same calls", func(pc *prop.Case) {
		capacity := pc.Draw(prop.Integer(1, 16), "capacity")
		m := newMap(capacity)
		ref := map[K]int{}
		var original M
		var originalRef map[K]int
		written := 0
		draw := func(pc *prop.Case, _ struct{}) any {
			return key(pc.Draw(prop.Integer(0, 3*capacity-1), "key"))
		}

		stateful.Steps(pc, stateful.Machine[struct{}]{
			Actions: []stateful.Action[struct{}]{
				{
					Name: opClear,
					Run: func(*prop.Case, int, any) {
						m.Clear()
						clear(ref)
					},
				},
				{
					Name: opClone,
					Run: func(*prop.Case, int, any) {
						original, originalRef = m, maps.Clone(ref)
						m = m.Clone()
					},
				},
				{
					Name:   opGet,
					Weight: 4,
					Input:  draw,
					Run: func(pc *prop.Case, _ int, in any) {
						got, ok := m.Get(in.(K))
						want, present := ref[in.(K)]
						assert.Equal(pc, ok, present, "Get must report whether the key is present")
						assert.Equal(pc, got, want, "Get must return the value of the key")
					},
				},
				{
					Name:   opSet,
					Weight: 4,
					Input: func(pc *prop.Case, state struct{}) any {
						written++

						return entry[K]{key: draw(pc, state).(K), value: written}
					},
					Run: func(pc *prop.Case, _ int, in any) {
						e := in.(entry[K])
						old, ok := m.Set(e.key, e.value)
						want, present := ref[e.key]
						ref[e.key] = e.value
						assert.Equal(pc, ok, present, "Set must report whether the key was present")
						assert.Equal(pc, old, want, "Set must return the previous value of the key")
					},
				},
				{
					Name:   opDelete,
					Weight: 4,
					Input:  draw,
					Run: func(pc *prop.Case, _ int, in any) {
						old, ok := m.Delete(in.(K))
						want, present := ref[in.(K)]
						delete(ref, in.(K))
						assert.Equal(pc, ok, present, "Delete must report whether the key was present")
						assert.Equal(pc, old, want, "Delete must return the value of the key")
					},
				},
				{
					Name: opAll,
					Run: func(pc *prop.Case, _ int, _ any) {
						yields := 0
						for range m.All() {
							yields++
						}
						assert.Equal(pc, maps.Collect(m.All()), ref, "All must yield every key with its value")
						assert.Equal(pc, yields, len(ref), "All must yield each key once")
					},
				},
			},
			Invariant: func(pc *prop.Case, _ struct{}) {
				assert.Equal(pc, m.Len(), len(ref), "Len must return the number of keys")
				if originalRef != nil {
					assert.Equal(pc, maps.Collect(original.All()), originalRef,
						"a write to the copy must not change the map that Clone copied")
				}
			},
		})
	})
}
