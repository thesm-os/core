// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"iter"
	"math"
	"strconv"
	"strings"
)

// Tile geometry of C2SP tlog-tiles.
const (
	// tileHeight is the height of the subtree a tile stores.
	tileHeight = 8

	// tileWidth is the number of hashes in a full tile.
	tileWidth = 1 << tileHeight

	// maxPartialWidth is the number of hashes in the widest partial tile,
	// one below tileWidth.
	maxPartialWidth = 255

	// tileLevels is the number of tile levels a tree of up to 2^64
	// leaves has.
	tileLevels = 8

	// pathBase is the size of one group of an index in a tile path.
	pathBase = 1000
)

// Path prefixes of C2SP tlog-tiles.
const (
	tilePrefix    = "tile/"
	entriesPrefix = "tile/entries/"
	partialInfix  = ".p/"
)

// digits maps a decimal digit to its ASCII character.
const digits = "0123456789"

// Tile names one tile of a tree stored as C2SP tlog-tiles.
//
// # Allocation contract
//
// Value type; pass by value.
type Tile struct {
	// Index is the tile's position within its level.
	Index uint64

	// Width is the number of hashes, 1 to 256. A tile of width 256 is
	// full and immutable.
	Width uint16

	// Level is the tile level. Level L stores hashes of tree level 8L.
	// Levels 0 to 7 cover every tree of up to 2^64 leaves, and
	// [ParseTilePath] refuses a higher level.
	Level uint8
}

// Path returns the tile's path relative to the log's prefix, in the form
// tile/<L>/<N>[.p/<W>], where N is written in groups of three digits,
// each group but the last prefixed with x. A tile of width 256 has no
// .p/<W> suffix. Path assumes a Width of 1 to 256.
//
// # Allocation contract
//
// One allocation for the string. Path inlines, so a concatenation with
// the path, such as prefix + t.Path(), allocates only its result, and a
// comparison with the path allocates nothing.
func (t Tile) Path() string {
	var buf [64]byte

	return string(t.appendPath(buf[:0]))
}

// appendPath appends the path that [Tile.Path] returns to b, and returns
// the extended slice. Path calls it, so the body of Path fits the inlining
// budget of the compiler.
func (t Tile) appendPath(b []byte) []byte {
	b = append(b, tilePrefix...)
	b = strconv.AppendUint(b, uint64(t.Level), 10)
	b = append(b, '/')

	return appendIndex(b, t.Index, t.Width)
}

// appendIndex appends index in groups of three digits, and the partial
// suffix when width is below a full tile.
func appendIndex(b []byte, index uint64, width uint16) []byte {
	// A uint64 has at most 20 decimal digits, so 7 groups.
	var groups [7]uint64

	n := 0
	for {
		groups[n] = index % pathBase
		n++
		index /= pathBase
		if index == 0 {
			break
		}
	}

	for i := n - 1; i >= 0; i-- {
		if i > 0 {
			b = append(b, 'x')
		}
		g := groups[i]
		b = append(b, digits[g/100], digits[g/10%10], digits[g%10])
		if i > 0 {
			b = append(b, '/')
		}
	}

	if width < tileWidth {
		b = append(b, partialInfix...)
		b = strconv.AppendUint(b, uint64(width), 10)
	}

	return b
}

// ParseTilePath returns the Tile that path names.
//
// Returns [ErrTilePath] unless path is exactly what [Tile.Path] returns
// for a tile of level 0 to 7, width 1 to 256, and an index that a tree
// of up to 2^64 leaves has at that level.
//
// ParseTilePath reads a tile from path leniently, and then compares path
// with the path that Tile.Path writes for that tile. The comparison
// refuses every path that the reading lets through: one without the
// prefix or a slash, leading zeros, a missing or misplaced x, a group of
// more than three digits, and a width of 256 or more. A group or a width
// that is not a number reads as zero, and one past the range of its type
// as the largest value. No tile has a width of zero, and the comparison
// refuses the other values. An index past 2^64 wraps, and Path writes
// every uint64 in one form, so the wrapped index has another path.
//
// # Allocation contract
//
// Zero alloc.
func ParseTilePath(path string) (Tile, error) {
	level, rest, _ := strings.Cut(strings.TrimPrefix(path, tilePrefix), "/")

	// The subtraction wraps for a byte below '0', so the comparison refuses
	// every byte that is not the digit of a level.
	if len(level) != 1 || level[0]-'0' >= tileLevels {
		return Tile{}, ErrTilePath
	}

	t := Tile{Level: level[0] - '0', Width: tileWidth}

	if index, width, partial := strings.Cut(rest, partialInfix); partial {
		w, _ := strconv.ParseUint(width, 10, 16)
		if w == 0 {
			return Tile{}, ErrTilePath
		}

		t.Width = uint16(w)
		rest = index
	}

	for group := range strings.SplitSeq(rest, "/") {
		g, _ := strconv.ParseUint(strings.TrimPrefix(group, "x"), 10, 64)
		t.Index = t.Index*pathBase + g
	}

	if t.Index > maxTileIndex(t.Level) || t.Path() != path {
		return Tile{}, ErrTilePath
	}

	return t, nil
}

// maxTileIndex returns the highest tile index a tree of up to 2^64
// leaves has at level.
func maxTileIndex(level uint8) uint64 {
	return math.MaxUint64 >> (tileHeight * (uint(level) + 1))
}

// Tiles returns the tiles a tree of size newSize has that a tree of size
// oldSize does not have in the same form: the full tiles completed
// between the two sizes, and the partial tiles at the right edge of the
// tree of size newSize. It yields them level by level, and within a
// level by index. It yields nothing unless oldSize is below newSize.
//
// # Allocation contract
//
// A range over the call allocates nothing. An iterator that escapes, such
// as one that the caller passes to another function, allocates its
// closure.
func Tiles(oldSize, newSize uint64) iter.Seq[Tile] {
	return func(yield func(Tile) bool) {
		// The sizes agree from some level up, and above it no tile
		// changes. An old size at or past the new one agrees or passes it
		// at level 0.
		for level := range uint8(tileLevels) {
			shift := tileHeight * uint(level)
			oldN, newN := oldSize>>shift, newSize>>shift
			if oldN >= newN {
				return
			}

			for n := oldN >> tileHeight; n < newN>>tileHeight; n++ {
				if !yield(Tile{Level: level, Index: n, Width: tileWidth}) {
					return
				}
			}

			if w := newN % tileWidth; w > 0 {
				if !yield(Tile{Level: level, Index: newN >> tileHeight, Width: uint16(w)}) {
					return
				}
			}
		}
	}
}
