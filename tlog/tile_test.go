// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"iter"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of Tile.
const (
	// maxTiles is more tiles than any case of Tiles expects.
	maxTiles = 16

	// fullWidth is the width of a full tile.
	fullWidth = 256

	// measuredPath is the path of the allocation tests and the benchmarks.
	measuredPath = "tile/4/x001/x234/067.p/1"

	// measuredPrefix is the prefix of the concatenation of the allocation
	// tests and the benchmarks.
	measuredPrefix = "log/"
)

// paths are tiles and the paths of C2SP tlog-tiles that name them.
var paths = []struct {
	what string
	path string
	tile tlog.Tile
}{
	{what: "a full tile of level 0", path: "tile/0/000", tile: tlog.Tile{Width: fullWidth}},
	{what: "a partial tile of one hash", path: "tile/0/000.p/1", tile: tlog.Tile{Width: 1}},
	{
		what: "an index of two groups",
		path: "tile/1/x001/000.p/255",
		tile: tlog.Tile{Level: 1, Index: 1000, Width: 255},
	},
	{
		what: "an index of three groups",
		path: "tile/4/x001/x234/067.p/1",
		tile: tlog.Tile{Level: 4, Index: 1234067, Width: 1},
	},
	{
		what: "the last tile of level 0",
		path: "tile/0/x072/x057/x594/x037/x927/935",
		tile: tlog.Tile{Index: 1<<56 - 1, Width: fullWidth},
	},
	{what: "a partial tile of level 7", path: "tile/7/000.p/255", tile: tlog.Tile{Level: 7, Width: 255}},
}

// treeTiles generates the tiles of a tree of up to 2^64 leaves: a level of
// 0 to 7, an index that the level has, and a width of 1 to 256.
var treeTiles = prop.Composite(func(c *prop.Case) tlog.Tile {
	level := c.Draw(prop.Integer[uint8](0, 7), "level")

	return tlog.Tile{
		Level: level,
		Index: c.Draw(prop.Integer[uint64](0, math.MaxUint64>>(8*(uint(level)+1))), "index"),
		Width: c.Draw(prop.Integer[uint16](1, fullWidth), "width"),
	}
})

func TestTile(t *testing.T) {
	t.Parallel()

	t.Run("Path", func(t *testing.T) {
		t.Parallel()

		for _, tt := range paths {
			t.Run("returns the path of "+tt.what, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.tile.Path(), tt.path, "Path must write the C2SP path")
			})
		}
	})

	t.Run("ParseTilePath", func(t *testing.T) {
		t.Parallel()

		for _, tt := range paths {
			t.Run("returns the tile of the path of "+tt.what, func(t *testing.T) {
				t.Parallel()
				got, err := tlog.ParseTilePath(tt.path)
				assert.NoError(t, err, "ParseTilePath must accept the path")
				assert.Equal(t, got, tt.tile, "ParseTilePath must return the tile of the path")
			})
		}

		t.Run("returns the tile whose path Path writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(tile tlog.Tile) (string, error) { return tile.Path(), nil }, tlog.ParseTilePath,
				"ParseTilePath must return the tile whose path Path writes", prop.Using(treeTiles))
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrTilePath for the empty path", give: ""},
			{name: "returns ErrTilePath for the prefix alone", give: "tile/"},
			{name: "returns ErrTilePath for a path without an index", give: "tile/0"},
			{name: "returns ErrTilePath for an empty index", give: "tile/0/"},
			{name: "returns ErrTilePath for an empty level", give: "tile//000"},
			{name: "returns ErrTilePath for a level that is not a digit", give: "tile/./000"},
			{name: "returns ErrTilePath for level 8", give: "tile/8/000"},
			{name: "returns ErrTilePath for a level of two digits", give: "tile/00/000"},
			{name: "returns ErrTilePath for the path of an entry bundle", give: "tile/entries/000"},
			{name: "returns ErrTilePath for a group of one digit", give: "tile/0/0"},
			{name: "returns ErrTilePath for a group of four digits", give: "tile/0/0000"},
			{name: "returns ErrTilePath for an index of one group with an x", give: "tile/0/x000"},
			{name: "returns ErrTilePath for a group before the last without an x", give: "tile/0/000/001"},
			{name: "returns ErrTilePath for a last group with an x", give: "tile/0/x001/x234"},
			{name: "returns ErrTilePath for a group with a sign", give: "tile/0/+01"},
			{name: "returns ErrTilePath for a width of zero", give: "tile/0/000.p/0"},
			{name: "returns ErrTilePath for a width of 256", give: "tile/0/000.p/256"},
			{name: "returns ErrTilePath for a width with a leading zero", give: "tile/0/000.p/01"},
			{name: "returns ErrTilePath for a width with a sign", give: "tile/0/000.p/+1"},
			{name: "returns ErrTilePath for an empty width", give: "tile/0/000.p/"},
			{name: "returns ErrTilePath for an index past the last tile of level 7", give: "tile/7/001"},
			{
				name: "returns ErrTilePath for an index past the last tile of level 0",
				give: "tile/0/x072/x057/x594/x037/x927/936",
			},
			{name: "returns ErrTilePath for an index past 2^64", give: "tile/0/x999/x999/x999/x999/x999/x999/x999"},
			{name: "returns ErrTilePath for a path with a leading slash", give: "/tile/0/000"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tlog.ParseTilePath(tt.give)
				expect.ErrorIs(t, err, tlog.ErrTilePath, "ParseTilePath must refuse the path")
				expect.Equal(t, got, tlog.Tile{}, "ParseTilePath must return the zero Tile with an error")
			})
		}
	})

	t.Run("Tiles", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			want     []tlog.Tile
			old, new uint64
		}{
			{name: "yields nothing for equal sizes", old: 5, new: 5},
			{name: "yields nothing for a smaller new size", old: 6, new: 5},
			{name: "yields the partial tile of one leaf", old: 0, new: 1, want: []tlog.Tile{{Width: 1}}},
			{
				name: "yields a full tile before its parent", old: 0, new: 256,
				want: []tlog.Tile{{Width: fullWidth}, {Level: 1, Width: 1}},
			},
			{
				name: "completes the partial tile of the old tree", old: 255, new: 256,
				want: []tlog.Tile{{Width: fullWidth}, {Level: 1, Width: 1}},
			},
			{name: "starts a new partial tile", old: 256, new: 257, want: []tlog.Tile{{Index: 1, Width: 1}}},
			{
				name: "yields every tile that a tree of 513 leaves adds", old: 0, new: 513,
				want: []tlog.Tile{
					{Width: fullWidth}, {Index: 1, Width: fullWidth}, {Index: 2, Width: 1}, {Level: 1, Width: 2},
				},
			},
			{
				name: "yields only the levels that change", old: 70000, new: 70001,
				want: []tlog.Tile{{Index: 273, Width: 113}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, firstTiles(tlog.Tiles(tt.old, tt.new)), tt.want,
					"Tiles must yield the tiles from the old size to the new")
			})
		}

		t.Run("stops when the caller stops", func(t *testing.T) {
			t.Parallel()
			for _, stop := range []int{1, 2, 3} {
				n := 0
				for range tlog.Tiles(0, 513) {
					n++
					if n == stop {
						break
					}
				}
				expect.Equal(t, n, stop, "the iteration must end at the break of the caller")
			}
		})
	})
}

// TestTileAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestTileAllocs(t *testing.T) {
	tile := tlog.Tile{Level: 4, Index: 1234067, Width: 1}

	t.Run("Path", func(t *testing.T) {
		t.Run("the path", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = tile.Path() }, 1, "Path must allocate the path alone")
			assert.Equal(t, got, measuredPath, "the test must measure the path of the tile")
		})

		t.Run("a concatenation with the path", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = measuredPrefix + tile.Path() }, 1,
				"a concatenation with the path must allocate its result alone")
			assert.Equal(t, got, measuredPrefix+measuredPath, "the test must measure the concatenation")
		})

		t.Run("a comparison with the path", func(t *testing.T) {
			var same bool
			expect.MaxAllocs(t, func() { same = tile.Path() == measuredPath }, 0,
				"a comparison with the path must not allocate")
			assert.True(t, same, "the test must measure a comparison with the path of the tile")
		})
	})

	t.Run("ParseTilePath", func(t *testing.T) {
		var got tlog.Tile
		expect.MaxAllocs(t, func() { got, _ = tlog.ParseTilePath(measuredPath) }, 0,
			"ParseTilePath must not allocate")
		assert.Equal(t, got, tile, "the test must measure a path that ParseTilePath accepts")
	})

	t.Run("Tiles", func(t *testing.T) {
		n := 0
		expect.MaxAllocs(t, func() {
			for range tlog.Tiles(0, 513) {
				n++
			}
		}, 0, "a range over Tiles must not allocate")
		assert.NotEqual(t, n, 0, "the test must measure tiles")
	})
}

// BenchmarkTile reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkTile(b *testing.B) {
	tile := tlog.Tile{Level: 4, Index: 1234067, Width: 1}

	b.Run("Path", func(b *testing.B) {
		b.Run("the path", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = tile.Path()
			}

			assert.Equal(b, got, measuredPath, "the benchmark must measure the path of the tile")
		})

		b.Run("a concatenation with the path", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = measuredPrefix + tile.Path()
			}

			assert.Equal(b, got, measuredPrefix+measuredPath, "the benchmark must measure the concatenation")
		})

		b.Run("a comparison with the path", func(b *testing.B) {
			var same bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				same = tile.Path() == measuredPath
			}

			assert.True(b, same, "the benchmark must measure a comparison with the path of the tile")
		})
	})

	b.Run("ParseTilePath", func(b *testing.B) {
		var got tlog.Tile

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.ParseTilePath(measuredPath)
		}

		assert.Equal(b, got, tile, "the benchmark must measure a path that ParseTilePath accepts")
	})

	b.Run("Tiles", func(b *testing.B) {
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for range tlog.Tiles(0, 513) {
				n++
			}
		}

		assert.NotEqual(b, n, 0, "the benchmark must measure tiles")
	})
}

// firstTiles returns the tiles of seq, up to maxTiles. A Tiles that does
// not end then fails the test and cannot exhaust memory.
func firstTiles(seq iter.Seq[tlog.Tile]) []tlog.Tile {
	var out []tlog.Tile
	for t := range seq {
		out = append(out, t)
		if len(out) == maxTiles {
			break
		}
	}

	return out
}
