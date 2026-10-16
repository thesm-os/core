// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import (
	"iter"
	"math/bits"
	"slices"
)

// maxRefSlab is the largest slab of a [RefSlabs] in bytes, so every offset
// of a [Ref] fits its 32 bits.
const maxRefSlab = 1 << 32

// Ref is the position of a region in a [RefSlabs]. The high 32 bits are the
// number of its slab plus one, and the low 32 bits are its offset in the
// slab. The zero Ref is no region.
type Ref uint64

// RefSlabs allocates regions of bytes from slabs and returns each region as
// a [Ref]. It takes single regions back for reuse in any order. A record
// that keeps a Ref contains no pointer, so the garbage collector marks a
// chunk of such records without scanning it.
//
// # Size classes, slabs and zeroing
//
// A RefSlabs has the size classes of [Slabs] and carves its slabs as Slabs
// does. [RefSlabs.Free] zeroes a region when it takes the region back. The
// region
// of n bytes takes the smallest size class that contains n. Alloc takes a
// region of its class from the free list of the class, or carves it from the
// current slab. When the current slab has no room for the class, Alloc puts
// the rest of the slab on the free lists of the largest classes that fit in
// it. It then allocates a new slab of slabSize bytes, or of the size of the
// class at offset 0 for a class larger than slabSize.
//
// A Ref does not record the length of its region. The caller passes the
// length to [RefSlabs.Bytes] and to Free. A Free with a length of another
// class than the Alloc of the region corrupts the free list of that class.
//
// The zero RefSlabs is empty and allocates from slabs of DefaultSlabSize
// bytes. A RefSlabs counts at most 2^32 - 1 slabs, which is at least 256 GiB.
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
// working set of the caller, an Alloc followed by a Free does not allocate.
// [RefSlabs.Bytes], [RefSlabs.InUse] and RefSlabs.All do not allocate.
type RefSlabs struct {
	// slabs are every slab in the order of their allocation. The slab of a
	// Ref with the number i plus one is slabs[i].
	slabs [][]byte

	// free[i] contains the regions of the size class with the index i that
	// Alloc may return. They are the regions that Free took back and the
	// rest of a slab that had no room for a class. Their bytes are zero.
	free [classes][]Ref

	// off is the offset in the last slab of the bytes that no region has
	// used.
	off int

	// size is the size of a slab, or 0 for DefaultSlabSize.
	size int

	// inUse is the sum of the classes that Alloc returned and Free has not
	// taken back.
	inUse int
}

// NewRefSlabs returns an empty [RefSlabs] whose slabs are slabSize bytes,
// MinClass bytes for a smaller slabSize, or 2^32 bytes for a larger one.
//
// # Allocation contract
//
// Allocates the RefSlabs. A RefSlabs takes 11,032 bytes on a 64-bit
// platform. The free lists of its 457 size classes take 10,968 of them. The
// first [RefSlabs.Alloc] allocates the first slab.
func NewRefSlabs(slabSize int) *RefSlabs {
	return &RefSlabs{size: int(min(uint64(max(slabSize, MinClass)), maxRefSlab))}
}

// Alloc reserves the size class of n zeroed bytes and returns its [Ref]. It
// returns the zero Ref for n of zero or less.
//
// The region is valid until the caller passes its Ref to [RefSlabs.Free].
// After that, a later Alloc may return the region to another caller.
//
// # Allocation contract
//
// Zero-alloc when the free list of the class of n contains a region, or the
// current slab has room for the class. Otherwise allocates a slab, as
// [RefSlabs] describes.
func (s *RefSlabs) Alloc(n int) Ref {
	if n < 1 {
		return 0
	}

	c, size := classOf(n)

	if free := s.free[c]; len(free) != 0 {
		r := free[len(free)-1]
		s.free[c] = free[:len(free)-1]
		s.inUse += size

		return r
	}

	if s.rest() < size {
		s.retire()
		s.slabs = append(s.slabs, make([]byte, max(s.slabSize(), size)))
		s.off = 0
	}

	//nolint:gosec // G115: a slab has at most 2^32 bytes, and s at most 2^32 - 1 slabs
	r := Ref(uint64(len(s.slabs))<<32 | uint64(s.off))
	s.off += size
	s.inUse += size

	return r
}

// Bytes returns the first n bytes of the region at r, with the capacity of
// the size class of n. It returns nil for the zero Ref, for n below 1, and
// for a Ref whose region of the class of n does not lie inside a slab of s.
// It does not panic, because a store calls it on every read.
//
// # Allocation contract
//
// Zero-alloc.
func (s *RefSlabs) Bytes(r Ref, n int) []byte {
	slab, off, size, ok := s.region(r, n)
	if !ok {
		return nil
	}

	return s.slabs[slab][off : off+n : off+size]
}

// Free zeroes the size class of n bytes at r and keeps the region for a
// later Alloc of the class. r is a Ref that [RefSlabs.Alloc] of s returned
// for n bytes, and the caller does not use the region after the call. Free
// does nothing for the zero Ref, which Alloc returns for no bytes.
//
// Free cannot detect a second Free of one region, or an n of another class
// than the Alloc of the region. After either, s returns the same memory from
// more than one Alloc.
//
// Error modes:
//
//   - A Ref whose region does not lie inside a slab of s returns [ErrRef],
//     and Free does nothing. An n below 1 has no region.
//
// # Allocation contract
//
// Zero-alloc when the free list of the class has room, as when its regions
// were allocated and freed before. Runs in time proportional to the size
// class because Free zeroes the class.
func (s *RefSlabs) Free(r Ref, n int) error {
	if r == 0 {
		return nil
	}

	slab, off, size, ok := s.region(r, n)
	if !ok {
		return ErrRef
	}

	clear(s.slabs[slab][off : off+size])
	c, _ := classOf(n)
	s.free[c] = append(s.free[c], r)
	s.inUse -= size

	return nil
}

// InUse returns the bytes of the size classes that [RefSlabs.Alloc]
// returned and [RefSlabs.Free] has not taken back. A store bounds its
// capacity by it.
//
// # Allocation contract
//
// Zero-alloc.
func (s *RefSlabs) InUse() int {
	return s.inUse
}

// All returns an iterator over every slab in the order of allocation. It
// yields each slab whole with its free space, and the capacity of a slab is
// its length. The slabs are for reading. A write to a slab changes a region
// or the zero bytes of the free space.
//
// # Allocation contract
//
// Zero-alloc.
func (s *RefSlabs) All() iter.Seq[[]byte] {
	return slices.Values(s.slabs)
}

// region returns the index of the slab of r, the offset of r in it and the
// size of the class of n bytes. It reports false for the zero Ref, for n
// below 1, and for a Ref whose class of n bytes does not lie inside a slab.
func (s *RefSlabs) region(r Ref, n int) (slab, off, size int, ok bool) {
	number := uint64(r >> 32)
	if n < 1 || number == 0 || number > uint64(len(s.slabs)) {
		return 0, 0, 0, false
	}

	//nolint:gosec // G115: the number of a slab is at most len(s.slabs)
	slab = int(number) - 1
	_, size = classOf(n)
	offset := uint64(r) & (1<<32 - 1)
	//nolint:gosec // G115: a size class is positive
	if offset+uint64(size) > uint64(len(s.slabs[slab])) {
		return 0, 0, 0, false
	}

	return slab, int(offset), size, true
}

// retire puts the rest of the current slab on the free lists so that a
// later Alloc of a class that fits can take it. It carves the rest into
// regions of the largest size classes that fit, as [Slabs] does, and leaves
// fewer than MinClass bytes of the slab unused.
func (s *RefSlabs) retire() {
	for rest := s.rest(); rest >= MinClass; rest = s.rest() {
		step := bits.Len(uint(rest)) - 1 - classBits
		size := rest >> step << step
		c, _ := classOf(size)
		//nolint:gosec // G115: a slab has at most 2^32 bytes, and s at most 2^32 - 1 slabs
		s.free[c] = append(s.free[c], Ref(uint64(len(s.slabs))<<32|uint64(s.off)))
		s.off += size
	}
}

// rest returns the bytes of the last slab that no region has used, or 0
// when s has no slab.
func (s *RefSlabs) rest() int {
	if len(s.slabs) == 0 {
		return 0
	}

	return len(s.slabs[len(s.slabs)-1]) - s.off
}

// slabSize returns the size of a new slab. It is the size of s, or
// DefaultSlabSize for the zero RefSlabs.
func (s *RefSlabs) slabSize() int {
	if s.size == 0 {
		return DefaultSlabSize
	}

	return s.size
}
