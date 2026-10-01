// Copyright Thesmos 2026
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
	// MinClass is the smallest size class of [Slabs], in bytes.
	MinClass = 64

	// DefaultSlabSize is the size of the slabs of the zero [Slabs], in
	// bytes: 1 MiB.
	DefaultSlabSize = 1 << 20

	// minShift is log2 of MinClass.
	minShift = 6
)

// ErrSizeClass reports a slice that [Slabs.Free] cannot take back,
// because its capacity is not a size class. It classifies as
// [errs.Invalid].
var ErrSizeClass = errs.WithClass(errors.New("arena: the capacity of the slice is not a size class"), errs.Invalid)

// Slabs allocates byte slices from slabs, and takes single slices back
// for reuse in any order. A store that keeps its values in memory, and
// removes them one at a time, reuses the space of a removed value for a
// later value without an allocation and without moving the values that
// it keeps.
//
// # Size classes
//
// The capacity of a slice from [Slabs.Alloc] is its size class: the
// smallest power of two that is at least MinClass and contains the
// requested length. A slice of n bytes uses less than twice n bytes of a
// slab, and n rounded up to MinClass for n below MinClass. An append to
// the slice within its capacity writes only into its own class.
//
// # Slabs and free lists
//
// Alloc takes a slice of its class from the free list of the class when
// [Slabs.Free] has returned one, and otherwise carves it from the current
// slab. When the current slab has no room for the class, Alloc puts the
// rest of the slab on the free lists of the largest classes that fit it,
// and allocates a new slab: slabSize bytes, or the size of the class for
// a class larger than slabSize. Memory on the free list of one class
// serves that class only.
//
// # Zeroing
//
// Alloc returns zeroed bytes. Free zeroes the whole class of the slice
// that it takes back, so a slab contains the bytes of no value after its
// Free. [Slabs.All] yields every slab, free space included, so a test can
// search all the memory of the Slabs for a removed value.
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
// current slab has no room for the class: one slab, and the growth of the
// index of slabs. Free allocates only when the free list of the class
// grows past its capacity. Once the free lists have grown to the working
// set of the caller, an Alloc followed by a Free does not allocate.
// [Slabs.InUse] and Slabs.All do not allocate.
type Slabs struct {
	// slabs are every slab, in the order of their allocation.
	slabs [][]byte

	// free[c] contains the slices of class 1<<c that Alloc may return:
	// the slices that Free took back, and the rest of a slab that had no
	// room for a class. Each has length 0, the capacity of its class, and
	// zero bytes. A class is below 1<<bits.UintSize.
	free [bits.UintSize][][]byte

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
// Allocates the Slabs. The first slab is allocated by the first
// [Slabs.Alloc].
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

	c := classOf(n)
	size := 1 << c

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

// Free zeroes the whole size class of b, a slice that [Slabs.Alloc] of s
// returned, and keeps it for a later Alloc of the class. The caller uses
// b and every slice of its memory no more. Free does nothing for a slice
// of capacity 0, such as the nil that Alloc returns for no bytes.
//
// Free identifies the class by the capacity of b alone. A slice that s
// did not return, or a second Free of one slice, makes s return its
// memory from more than one Alloc. Free cannot detect either.
//
// Returns [ErrSizeClass], classified [errs.Invalid], and does nothing, for
// a slice whose capacity is not a size class, such as a slice that a
// caller resliced to a smaller capacity.
//
// # Allocation contract
//
// Zero-alloc when the free list of the class has room, as when its slices
// were allocated and freed before. Runs in time proportional to the size
// class, which it zeroes.
func (s *Slabs) Free(b []byte) error {
	size := cap(b)
	if size == 0 {
		return nil
	}

	c := bits.Len(uint(size)) - 1
	if size != 1<<c || size < MinClass {
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

// All returns an iterator over every slab, in the order of its allocation,
// free space included. Each slab is yielded whole, and its capacity is its
// length. The slabs are for reading. A write to a slab changes the slices
// that Alloc returned, or the zero bytes of its free space.
//
// # Allocation contract
//
// Zero-alloc.
func (s *Slabs) All() iter.Seq[[]byte] {
	return slices.Values(s.slabs)
}

// retire puts the rest of the current slab on the free lists, as slices of
// the largest size classes that fit in it, so that a later Alloc of a
// class that fits takes it. Less than MinClass bytes of the slab stay
// unused.
func (s *Slabs) retire() {
	for len(s.cur) >= MinClass {
		c := bits.Len(uint(len(s.cur))) - 1
		s.free[c] = append(s.free[c], s.cur[:0:1<<c])
		s.cur = s.cur[1<<c:]
	}
}

// slabSize returns the size of a new slab: the size of s, or
// DefaultSlabSize for the zero Slabs.
func (s *Slabs) slabSize() int {
	if s.size == 0 {
		return DefaultSlabSize
	}

	return s.size
}

// classOf returns log2 of the size class of n bytes, for n of at least 1:
// the smallest power of two that is at least MinClass and contains n.
func classOf(n int) int {
	return max(bits.Len(uint(n-1)), minShift)
}
