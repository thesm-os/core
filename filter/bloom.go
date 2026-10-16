// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"math"
	"sync/atomic"
)

// The layout of a Bloom.
const (
	// blockBytes is the size of a block in bytes: eight words of 32 bits.
	blockBytes = 32
	// maxBlocks is the largest number of blocks of a Bloom. The block of a
	// hash h in a filter of z blocks is ((h >> 32) * z) >> 32, which needs z
	// of at most 2^32.
	maxBlocks = 1 << 32
	// batchLen is the number of hashes whose blocks MayContainBatch loads
	// before it tests them.
	batchLen = 16
)

// salts are the eight odd constants of the split block Bloom filter of
// Parquet. The low 32 bits x of a hash set bit (x * salts[i]) >> 27 of word i
// of the block of the hash, in arithmetic modulo 2^32.
var salts = [8]uint32{
	0x47b6137b, 0x44974d91, 0x8824ad5b, 0xa2b7289d,
	0x705495c7, 0x2df1424b, 0x9efc4947, 0x5c6bfb31,
}

// block is the group of eight 32-bit words of a Bloom in which a hash sets
// one bit each.
type block [8]atomic.Uint32

// Bloom is a split block Bloom filter over 64-bit hashes, in the layout of
// Parquet. A hash selects one block of 256 bits and sets one bit in each of
// its eight words of 32 bits, so a query reads one block and has no branch on
// the data.
//
// A Bloom reports true for every hash that Add added. For a hash that Add did
// not add, it reports true at a rate that the bits per key set: about 1% at
// 10.5 bits per key, 0.1% at 16.9 and 0.01% at 26.4. A Bloom has no encoding,
// and a store that persists a filter persists a [Fuse].
//
// The zero Bloom has no blocks. Its MayContain reports true for every hash,
// and its Add does nothing, so it reports true for every hash that Add
// added.
//
// # Concurrency
//
// Add, MayContain, MayContainBatch and Size are safe for concurrent use. A
// MayContain that starts after an Add of the same hash returned reports
// true. Reset is not safe for concurrent use with any other method.
//
// # Allocation contract
//
// [NewBloom] allocates the filter and its blocks. The methods do not
// allocate.
type Bloom struct {
	blocks []block
}

// NewBloom returns an empty filter with bitsPerKey bits for each of keys
// keys, rounded up to whole blocks of 256 bits. A keys below 1 counts as 1.
//
// Error modes:
//
//   - A bitsPerKey that is NaN, at most 0 or more than 256 returns
//     [ErrBitsPerKey].
//   - A filter of more than 2^32 blocks returns [ErrTooLarge].
//
// # Allocation contract
//
// NewBloom allocates the filter and its blocks: two allocations.
func NewBloom(keys int, bitsPerKey float64) (*Bloom, error) {
	if math.IsNaN(bitsPerKey) || bitsPerKey <= 0 || bitsPerKey > 256 {
		return nil, ErrBitsPerKey
	}
	blocks := math.Ceil(float64(max(keys, 1)) * bitsPerKey / (8 * blockBytes))
	//dokimi:mutate-skip ror-boundary: a filter of 2^32 blocks takes 128 GiB, which no test allocates
	if blocks > maxBlocks {
		return nil, ErrTooLarge
	}

	return &Bloom{blocks: make([]block, int(blocks))}, nil
}

// Add adds the hash h. It loads each word of the block of h and sets the bit
// of h with an atomic OR only in a word that lacks the bit, so an Add of a
// hash that the filter contains does not write.
func (f *Bloom) Add(h uint64) {
	if len(f.blocks) == 0 {
		return
	}
	b := &f.blocks[((h>>32)*uint64(len(f.blocks)))>>32]
	x := uint32(h) //nolint:gosec // G115: the bits of a hash come from its low 32 bits
	for i, salt := range salts {
		m := uint32(1) << (x * salt >> 27)
		//dokimi:mutate-skip ror-true: an OR of a bit that the word has changes nothing, and the check saves the write
		if b[i].Load()&m == 0 {
			b[i].Or(m)
		}
	}
}

// MayContain reports whether the filter may contain h. It returns true for
// every h that Add added.
//
// It shifts each word of the block of h right by the bit of h in that word,
// and tests bit 0 of the AND of the eight results, without a branch.
func (f *Bloom) MayContain(h uint64) bool {
	if len(f.blocks) == 0 {
		return true
	}
	b := &f.blocks[((h>>32)*uint64(len(f.blocks)))>>32]
	x := uint32(h) //nolint:gosec // G115: the bits of a hash come from its low 32 bits
	r := b[0].Load() >> (x * salts[0] >> 27)
	r &= b[1].Load() >> (x * salts[1] >> 27)
	r &= b[2].Load() >> (x * salts[2] >> 27)
	r &= b[3].Load() >> (x * salts[3] >> 27)
	r &= b[4].Load() >> (x * salts[4] >> 27)
	r &= b[5].Load() >> (x * salts[5] >> 27)
	r &= b[6].Load() >> (x * salts[6] >> 27)
	r &= b[7].Load() >> (x * salts[7] >> 27)

	return r&1 != 0
}

// MayContainBatch sets out[i] to MayContain(hs[i]) for every i below the
// shorter length of hs and out, and returns that length. It loads the first
// word of the blocks of a group of 16 hashes before it tests any of them, so
// the cache misses of the group overlap. A remainder of fewer than 16 hashes
// takes single queries. It does not write an element of out past that
// length.
func (f *Bloom) MayContainBatch(hs []uint64, out []bool) int {
	n := min(len(hs), len(out))
	i := 0
	//dokimi:mutate-skip sbr-delete,ror-false: the single queries below return the same results
	if len(f.blocks) > 0 {
		z := uint64(len(f.blocks))
		//dokimi:mutate-skip sbr-delete,ror-boundary: the single queries below return the same results
		for ; i+batchLen <= n; i += batchLen {
			//dokimi:mutate-skip sbr-delete: a load of a block only moves it into the cache
			for _, h := range hs[i : i+batchLen] {
				//dokimi:mutate-skip sbr-delete,aor: a load of a block only moves it into the cache
				f.blocks[((h>>32)*z)>>32][0].Load()
			}
			for j, h := range hs[i : i+batchLen] {
				out[i+j] = f.MayContain(h)
			}
		}
	}
	for ; i < n; i++ {
		out[i] = f.MayContain(hs[i])
	}

	return n
}

// Reset removes every hash. It keeps the blocks for the next hashes.
func (f *Bloom) Reset() {
	clear(f.blocks)
}

// Size returns the size of the filter in bytes: 32 bytes for each block.
func (f *Bloom) Size() int {
	return len(f.blocks) * blockBytes
}
