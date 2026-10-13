// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import (
	"errors"
	"iter"
	"math/bits"
	"slices"

	"go.thesmos.sh/core/errs"
)

const (
	// MinClass is the smallest size class of [Slabs] in bytes.
	MinClass = 64

	// DefaultSlabSize is the size in bytes of a slab of the zero [Slabs].
	// It is 1 MiB.
	DefaultSlabSize = 1 << 20

	// minShift is log2 of MinClass.
	minShift = 6

	// classBits is log2 of the number of size classes between two powers
	// of two. The 8 classes above 2^e are 2^(e-3) apart.
	classBits = 3

	// classes is the number of size classes. The first class is MinClass,
	// and 8 classes follow each power of two from MinClass up to
	// 2^(bits.UintSize-2).
	classes = (bits.UintSize-1-minShift)<<classBits + 1

	// minIndex is one less than e<<classBits + q of classOf for the
	// smallest class above MinClass. classOf subtracts it, so that this
	// class has the index 1 and MinClass has the index 0.
	minIndex = (minShift+1)<<classBits - 1
)

// ErrSizeClass reports a slice that [Slabs.Free] cannot take back,
// because its capacity is not a size class. It classifies as
// [errs.Invalid].
var ErrSizeClass = errs.WithClass(errors.New("arena: the capacity of the slice is not a size class"), errs.Invalid)

// Slabs allocates byte slices from slabs and takes single slices back for
// reuse in any order. A store that keeps its values in memory and removes
// them one at a time reuses the space of a removed value for a later value
// without an allocation. The values that the store keeps remain at their
// addresses.
//
// # Size classes
//
// The capacity of a slice from [Slabs.Alloc] is the smallest size class
// that contains the requested length. MinClass is the class of every
// length up to MinClass. Between two powers of two from MinClass up, eight
// classes divide the interval in equal steps. The classes above 2^e are
// 2^e plus one to eight times 2^(e-3). A slice of n bytes above MinClass
// uses less than 1.125 times n bytes of a slab. Every class is a multiple
// of 8 bytes. An append to the slice within its capacity writes only into
// its own class.
//
// # Slabs and free lists
//
// Alloc takes a slice of its class from the free list of the class when
// [Slabs.Free] has returned one. Otherwise it carves the slice from the
// current slab. When the current slab has no room for the class, Alloc
// puts the rest of the slab on the free lists of the largest classes that
// fit in it. It then allocates a new slab of slabSize bytes, or of the
// size of the class for a class larger than slabSize. Memory on the free
// list of one class serves that class only.
//
// # Zeroing
//
// Alloc returns zeroed bytes. Free zeroes the whole class of the slice
// that it takes back, so the slabs do not contain the bytes of a value
// after its Free. [Slabs.All] yields every slab with its free space, so a
// test can search all the memory of the Slabs for a removed value.
//
// The zero Slabs is empty and allocates from slabs of DefaultSlabSize
// bytes.
//
// # Concurrency
//
// Not safe for concurrent use. A caller that allocates from more than one
// goroutine serialises the calls.
//
// # Allocation contract
//
// Alloc allocates only when the free list of its class is empty and the
// current slab has no room for the class. It then allocates one slab and
// grows the index of slabs. Free allocates only when the free list of the
// class grows past its capacity. Once the free lists have grown to the
// working set of the caller, an Alloc followed by a Free does not
// allocate. [Slabs.InUse] and Slabs.All do not allocate.
type Slabs struct {
	// slabs are every slab in the order of their allocation.
	slabs [][]byte

	// free[i] contains the slices of the size class with the index i that
	// Alloc may return. They are the slices that Free took back and the
	// rest of a slab that had no room for a class. Each has length 0, the
	// capacity of its class and zero bytes.
	free [classes][][]byte

	// cur is the part of the current slab that no slice has used.
	cur []byte

	// size is the size of a slab, or 0 for DefaultSlabSize.
	size int

	// inUse is the sum of the classes that Alloc returned and Free has
	// not taken back.
	inUse int
}

// NewSlabs returns an empty [Slabs] whose slabs are slabSize bytes, or
// MinClass bytes for a smaller slabSize.
//
// # Allocation contract
//
// Allocates the Slabs. A Slabs takes 11,032 bytes on a 64-bit platform.
// The free lists of its 457 size classes take 10,968 of them. The first
// [Slabs.Alloc] allocates the first slab.
func NewSlabs(slabSize int) *Slabs {
	return &Slabs{size: max(slabSize, MinClass)}
}

// Alloc returns n zeroed bytes whose capacity is the size class of n. It
// returns nil for n of zero or less.
//
// The slice is valid until the caller passes it to [Slabs.Free]. After
// that, a later Alloc may return its bytes to another caller.
//
// # Allocation contract
//
// Zero-alloc when the free list of the class of n contains a slice, or
// the current slab has room for the class. Otherwise allocates a slab, as
// [Slabs] describes.
func (s *Slabs) Alloc(n int) []byte {
	if n < 1 {
		return nil
	}

	c, size := classOf(n)

	if free := s.free[c]; len(free) != 0 {
		b := free[len(free)-1]
		s.free[c] = free[:len(free)-1]
		s.inUse += size

		return b[:n]
	}

	if len(s.cur) < size {
		s.retire()
		s.cur = make([]byte, max(s.slabSize(), size))
		s.slabs = append(s.slabs, s.cur)
	}

	b := s.cur[:n:size]
	s.cur = s.cur[size:]
	s.inUse += size

	return b
}

// Free zeroes the whole size class of b and keeps it for a later Alloc of
// the class. b is a slice that [Slabs.Alloc] of s returned, and the caller
// does not use b or any slice of its memory after the call. Free does
// nothing for a slice of capacity 0, such as the nil that Alloc returns
// for no bytes.
//
// Free identifies the class by the capacity of b alone. It cannot detect
// a slice that s did not return or a second Free of one slice. After
// either, s returns the same memory from more than one Alloc.
//
// Error modes:
//
//   - A capacity of b that is not a size class returns [ErrSizeClass], and
//     Free does nothing. A slice that a caller resliced to a smaller
//     capacity has such a capacity.
//
// # Allocation contract
//
// Zero-alloc when the free list of the class has room, as when its slices
// were allocated and freed before. Runs in time proportional to the size
// class because Free zeroes the class.
func (s *Slabs) Free(b []byte) error {
	size := cap(b)
	if size == 0 {
		return nil
	}

	c, class := classOf(size)
	if class != size {
		return ErrSizeClass
	}

	b = b[:size]
	clear(b)
	s.free[c] = append(s.free[c], b[:0])
	s.inUse -= size

	return nil
}

// InUse returns the bytes of the size classes that [Slabs.Alloc] returned
// and [Slabs.Free] has not taken back. A store bounds its capacity by it.
//
// # Allocation contract
//
// Zero-alloc.
func (s *Slabs) InUse() int {
	return s.inUse
}

// All returns an iterator over every slab in the order of allocation. It
// yields each slab whole with its free space, and the capacity of a slab
// is its length. The slabs are for reading. A write to a slab changes the
// slices that Alloc returned or the zero bytes of its free space.
//
// # Allocation contract
//
// Zero-alloc.
func (s *Slabs) All() iter.Seq[[]byte] {
	return slices.Values(s.slabs)
}

// retire puts the rest of the current slab on the free lists so that a
// later Alloc of a class that fits can take it. It carves the rest into
// slices of the largest size classes that fit. For n bytes from 2^e up to
// 2^(e+1), the largest class of at most n bytes is n rounded down to a
// multiple of 2^(e-3). retire leaves fewer than MinClass bytes of the slab
// unused.
func (s *Slabs) retire() {
	for len(s.cur) >= MinClass {
		step := bits.Len(uint(len(s.cur))) - 1 - classBits
		size := len(s.cur) >> step << step
		c, _ := classOf(size)
		s.free[c] = append(s.free[c], s.cur[:0:size])
		s.cur = s.cur[size:]
	}
}

// slabSize returns the size of a new slab. It is the size of s, or
// DefaultSlabSize for the zero Slabs.
func (s *Slabs) slabSize() int {
	if s.size == 0 {
		return DefaultSlabSize
	}

	return s.size
}

// classOf returns the index and the size of the size class of n bytes. n
// is at least 1. The class of every n up to MinClass is MinClass at the
// index 0. For n above MinClass and 2^e < n <= 2^(e+1), the class is
// the smallest multiple of 2^(e-3) that is at least n. The index of a
// class is the number of smaller classes.
func classOf(n int) (int, int) {
	//dokimi:mutate-skip ror-boundary: the arithmetic below returns the index 0 and the size MinClass for n of MinClass too
	if n <= MinClass {
		return 0, MinClass
	}

	// m is n-1, and e is the exponent of the largest power of two up to m.
	// q is from 8 to 15, and the class is q+1 steps of 2^(e-3).
	m := n - 1
	e := bits.Len(uint(m)) - 1
	step := e - classBits
	q := m >> step
	size := (q + 1) << step
	i := e<<classBits + q

	//dokimi:mutate-skip aor: i+minIndex gives every class up to 2^49 bytes a free list of its own too
	return i - minIndex, size
}
