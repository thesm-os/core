// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"encoding/binary"
	"hash/maphash"
	"math/bits"

	"go.thesmos.sh/core/id"
)

// The tag of a slot marks it empty or full. The tag of a full slot also
// contains seven bits of the hash of its key, so a lookup compares a key
// only in a slot whose tag matches.
const (
	// fullTag is the bit of the tag of a full slot. The tag of an empty slot
	// is 0.
	fullTag = 0x80

	// tagShift moves the top seven bits of a hash into the bits of a tag
	// below fullTag.
	tagShift = 57
)

// Key is the type set of the keys of a [Map] and of a [Concurrent]: the
// fixed-size identifiers [id.ID128], [id.ID160] and [id.ID256].
type Key interface {
	id.ID128 | id.ID160 | id.ID256
}

// words is a key as 64-bit words. w0 and w1 are the little-endian words of
// the first 16 bytes of the key. An ID160 has its last 4 bytes in w2, and an
// ID256 has its last 16 bytes in w2 and w3. A word that the key does not
// fill is zero.
//
// words is a struct and not an array. Go returns a struct of four words in
// registers, and it returns an array of more than one element on the stack.
type words struct {
	w0, w1, w2, w3 uint64
}

// seed is the pair of secret words of the hash of one map.
//
// A hash combines the first two words of a key with s0 and s1 by exclusive
// or, multiplies the two results into 128 bits, and combines the two halves
// by exclusive or. The third and fourth words of an ID160 or an ID256 enter
// a second multiplication of the same form, with the first hash in place
// of s0. Every factor contains a secret word, so a caller who does not know
// the seed cannot choose keys that share a hash.
type seed struct {
	s0, s1 uint64
}

// newHash returns the hash function of the keys of type K and a new seed.
// A type switch on a pointer to the function selects hash128, hash160 or
// hash256 once, so a hash does not branch on the size of the key. Each word
// of the seed is the hash of the empty string under a new random seed of
// hash/maphash.
//
// # Allocation contract
//
// Zero alloc.
func newHash[K Key]() (func(seed, K) (uint64, words), seed) {
	var hash func(seed, K) (uint64, words)

	switch f := any(&hash).(type) {
	case *func(seed, id.ID128) (uint64, words):
		*f = hash128
	case *func(seed, id.ID160) (uint64, words):
		*f = hash160
	case *func(seed, id.ID256) (uint64, words):
		*f = hash256
	}

	return hash, seed{maphash.String(maphash.MakeSeed(), ""), maphash.String(maphash.MakeSeed(), "")}
}

// hash128 returns the hash of the ID128 key k under the seed s, and the
// words of k.
func hash128(s seed, k id.ID128) (uint64, words) {
	w := words{w0: binary.LittleEndian.Uint64(k[0:8]), w1: binary.LittleEndian.Uint64(k[8:16])}

	return mix(w.w0^s.s0, w.w1^s.s1), w
}

// hash160 returns the hash of the ID160 key k under the seed s, and the
// words of k.
func hash160(s seed, k id.ID160) (uint64, words) {
	w := words{
		w0: binary.LittleEndian.Uint64(k[0:8]),
		w1: binary.LittleEndian.Uint64(k[8:16]),
		w2: uint64(binary.LittleEndian.Uint32(k[16:20])),
	}

	return mix(w.w2^mix(w.w0^s.s0, w.w1^s.s1), w.w3^s.s1), w
}

// hash256 returns the hash of the ID256 key k under the seed s, and the
// words of k.
func hash256(s seed, k id.ID256) (uint64, words) {
	w := words{
		w0: binary.LittleEndian.Uint64(k[0:8]),
		w1: binary.LittleEndian.Uint64(k[8:16]),
		w2: binary.LittleEndian.Uint64(k[16:24]),
		w3: binary.LittleEndian.Uint64(k[24:32]),
	}

	return mix(w.w2^mix(w.w0^s.s0, w.w1^s.s1), w.w3^s.s1), w
}

// mix multiplies a and b into 128 bits, and returns the exclusive or of the
// two halves of the product.
func mix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)

	return hi ^ lo
}
