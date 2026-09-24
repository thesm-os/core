// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/tlog"
)

func TestTile(t *testing.T) {
	t.Parallel()

	paths := map[string]tlog.Tile{
		"tile/0/000":                          {Level: 0, Index: 0, Width: 256},
		"tile/0/000.p/1":                      {Level: 0, Index: 0, Width: 1},
		"tile/1/x001/000.p/255":               {Level: 1, Index: 1000, Width: 255},
		"tile/4/x001/x234/067.p/1":            {Level: 4, Index: 1234067, Width: 1},
		"tile/0/x072/x057/x594/x037/x927/935": {Level: 0, Index: 1<<56 - 1, Width: 256},
		"tile/7/000.p/255":                    {Level: 7, Index: 0, Width: 255},
	}

	t.Run("Path/writes the C2SP path", func(t *testing.T) {
		t.Parallel()

		for path, tile := range paths {
			testkit.Equal(t, tile.Path(), path, "the path of a tile")
		}
	})

	t.Run("ParseTilePath/reads every path Path writes", func(t *testing.T) {
		t.Parallel()

		for path, tile := range paths {
			got, err := tlog.ParseTilePath(path)
			testkit.NoError(t, err, "ParseTilePath must accept "+path)
			testkit.Equal(t, got, tile, "ParseTilePath must return the tile of "+path)
		}
	})

	t.Run("ParseTilePath/refuses a path that names no tile", func(t *testing.T) {
		t.Parallel()

		for _, path := range []string{
			"",
			"tile/",
			"tile/0",
			"tile/0/",
			"tile/8/000",
			"tile/00/000",
			"tile/entries/000",
			"tile/0/0",
			"tile/0/0000",
			"tile/0/x000",
			"tile/0/000/001",
			"tile/0/x001/x234",
			"tile/0/+01",
			"tile/0/000.p/0",
			"tile/0/000.p/256",
			"tile/0/000.p/01",
			"tile/0/000.p/+1",
			"tile/0/000.p/",
			"tile/7/001",
			"tile/0/x072/x057/x594/x037/x927/936",
			"tile/0/x999/x999/x999/x999/x999/x999/x999",
			"/tile/0/000",
		} {
			_, err := tlog.ParseTilePath(path)
			testkit.ErrorIs(t, err, tlog.ErrTilePath, "ParseTilePath must refuse "+path)
		}
	})
}

func TestTiles(t *testing.T) {
	t.Parallel()

	full := func(level uint8, index uint64) tlog.Tile { return tlog.Tile{Level: level, Index: index, Width: 256} }

	cases := []struct {
		name     string
		old, new uint64
		want     []tlog.Tile
	}{
		{"yields nothing for equal sizes", 5, 5, nil},
		{"yields nothing for a smaller new size", 6, 5, nil},
		{"yields the partial tile of one leaf", 0, 1, []tlog.Tile{{Width: 1}}},
		{"yields a full tile and its parent", 0, 256, []tlog.Tile{full(0, 0), {Level: 1, Width: 1}}},
		{"completes the partial tile of the old tree", 255, 256, []tlog.Tile{full(0, 0), {Level: 1, Width: 1}}},
		{"starts a new partial tile", 256, 257, []tlog.Tile{{Index: 1, Width: 1}}},
		{
			"yields every full tile completed and every new partial tile", 0, 513,
			[]tlog.Tile{full(0, 0), full(0, 1), {Index: 2, Width: 1}, {Level: 1, Width: 2}},
		},
		{
			"yields only the levels that change", 70000, 70001,
			[]tlog.Tile{{Index: 273, Width: 113}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, slices.Collect(tlog.Tiles(tc.old, tc.new)), tc.want,
				"the tiles from the old size to the new")
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
			testkit.Equal(t, n, stop, "the iteration must end at the caller's break")
		}
	})
}
