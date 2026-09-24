// Copyright Thesmos 2026
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
// One allocation for the string.
func (t Tile) Path() string {
	var buf [64]byte

	b := append(buf[:0], tilePrefix...)
	b = strconv.AppendUint(b, uint64(t.Level), 10)
	b = append(b, '/')
	b = appendIndex(b, t.Index, t.Width)

	return string(b)
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
// # Allocation contract
//
// One allocation to check the path against [Tile.Path].
func ParseTilePath(path string) (Tile, error) {
	rest, ok := strings.CutPrefix(path, tilePrefix)
	if !ok || len(rest) < 2 || rest[0] < '0' || rest[0] > '7' || rest[1] != '/' {
		return Tile{}, ErrTilePath
	}

	t := Tile{Level: rest[0] - '0', Width: tileWidth}
	rest = rest[2:]

	if i := strings.Index(rest, partialInfix); i >= 0 {
		w, err := strconv.ParseUint(rest[i+len(partialInfix):], 10, 16)
		if err != nil || w == 0 || w >= tileWidth {
			return Tile{}, ErrTilePath
		}
		t.Width = uint16(w)
		rest = rest[:i]
	}

	for group := range strings.SplitSeq(rest, "/") {
		g, err := strconv.ParseUint(strings.TrimPrefix(group, "x"), 10, 64)
		if err != nil || g >= pathBase || t.Index > (math.MaxUint64-g)/pathBase {
			return Tile{}, ErrTilePath
		}
		t.Index = t.Index*pathBase + g
	}

	// Leading zeros, a missing or misplaced x and a width with a sign
	// all parse, and none of them survive the round trip.
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
// One closure per call.
func Tiles(oldSize, newSize uint64) iter.Seq[Tile] {
	return func(yield func(Tile) bool) {
		if oldSize >= newSize {
			return
		}

		for level := range uint8(tileLevels) {
			shift := tileHeight * uint(level)
			oldN, newN := oldSize>>shift, newSize>>shift
			if oldN == newN {
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
