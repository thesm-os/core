// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"bytes"
	"context"

	"go.thesmos.sh/core/crypto"
)

// TileVerifier checks that leaf hashes are in the tree of a size and a root,
// against the tiles that it reads through a [TileReader]. It verifies each
// tile that it reads before it compares a leaf with the tile: the partial
// tiles of the right edge against the root, and a full tile against the hash
// of its root in the verified tile of the level above.
//
// A TileVerifier keeps the verified right edge, and the full tile at each
// level of the path of the last leaf that it checked. Verify, called for the
// leaves in index order, reads each tile of the tree once and hashes each
// interior node once, size - 1 node hashes in all. A check of a leaf whose
// path does not contain the kept tile of a level reads the full tiles of its
// path at that level and below again, and hashes 255 nodes for each.
//
// The zero TileVerifier checks a tree of no leaves, so [TileVerifier.Verify]
// returns ErrRange for every index. [TileVerifier.Reset] starts the checks of
// a tree. A copy of a TileVerifier shares the buffers of its kept tiles, so a
// caller does not copy a TileVerifier after its first Verify.
//
// # Concurrency
//
// Not safe for concurrent use. Verify changes the tiles that the TileVerifier
// keeps.
//
// # Allocation contract
//
// Reset allocates nothing. Verify allocates each buffer of a kept tile once,
// the first time that it reads into it, with room for a full tile and
// [bytes.MinRead] more bytes, so [BlobTiles] reads into it without growing it.
// After that, Verify allocates nothing beyond what ReadTiles allocates, until
// a Reset to a hasher of a larger digest.
type TileVerifier struct {
	h crypto.Hasher
	r TileReader

	// slot[i] is the field of edgeData or pathData that keeps the data of
	// read[i].
	slot [2 * tileLevels]*[]byte

	// edgeData is the data of the tiles of edge, and pathData the data of the
	// tiles of path.
	edgeData [tileLevels][]byte
	pathData [tileLevels][]byte

	// dst is the list of buffers of one ReadTiles call, one for each tile of
	// read.
	dst [2 * tileLevels][]byte

	root crypto.Digest

	// edgeOK reports whether edgeData recomputes root.
	edgeOK bool

	size uint64

	// ds is the digest size of h.
	ds int

	// edge is the partial tile at each level of the right edge of the tree. A
	// tile of width zero marks a level without a partial tile.
	edge [tileLevels]Tile

	// path is the verified full tile at each level of the path of the last
	// leaf that Verify checked. A tile of width zero marks a level without a
	// verified tile.
	path [tileLevels]Tile

	// read is the list of tiles of one ReadTiles call. A call reads at most
	// the eight partial tiles of the edge and the seven full tiles of a path.
	read [2 * tileLevels]Tile

	// scratch has room to fold a full tile of the largest digest into its
	// root.
	scratch [tileWidth * crypto.MaxDigestSize]byte
}

// Reset starts the checks of the tree of size leaves with root under h, whose
// tiles r reads. size is a size that the log published, so the partial tiles
// of its right edge exist. Reset drops the tiles that v keeps and keeps their
// buffers. Reset does not read, and the first Verify after it reads and
// verifies the right edge.
func (v *TileVerifier) Reset(h crypto.Hasher, r TileReader, size uint64, root crypto.Digest) {
	v.h, v.r, v.root, v.size = h, r, root, size
	v.ds = h.Hash(nil).Size()
	v.edgeOK = false

	for level := range uint8(tileLevels) {
		row := size >> (tileHeight * uint(level))
		v.edge[level] = Tile{Level: level, Index: row >> tileHeight, Width: uint16(row % tileWidth)}
		v.edgeData[level] = v.edgeData[level][:0]
		v.path[level] = Tile{}
	}
}

// Verify returns nil when leaf is the leaf hash at index in the tree. In one
// [TileReader.ReadTiles] call, it reads the full tiles of the path of index
// that v does not keep, and the right edge on the first call after Reset. It
// verifies these tiles from the top down before it compares leaf with the hash
// at index. A call for a leaf of the kept tiles does not read or hash.
//
// Returns [ErrRange] when index is not below the size of the tree,
// [ErrTileSize] when a tile read has the wrong length, and the error of
// ReadTiles when it fails. Returns [ErrProof] when the right edge does not
// recompute the root, when the root of a full tile differs from its hash in
// the tile above it, or when leaf differs from the hash at index. A leaf of
// another digest size differs from every hash. v keeps only the tiles that it
// verified, so a call after an error reads the unverified tiles again.
func (v *TileVerifier) Verify(ctx context.Context, index uint64, leaf crypto.Digest) error {
	if index >= v.size {
		return ErrRange
	}

	// full is the full tile at each level of the path below top, the lowest
	// level whose tile on the path is the partial tile of the right edge. At
	// level 7 of a tree below 2^64 leaves, every tile is partial.
	var (
		full [tileLevels]Tile
		top  uint8
	)
	for level := range uint8(tileLevels) {
		row := index >> (tileHeight * uint(level))
		if row>>tileHeight == v.size>>(tileHeight*uint(level+1)) {
			top = level

			break
		}
		full[level] = Tile{Level: level, Index: row >> tileHeight, Width: tileWidth}
	}

	data, err := v.load(ctx, index, &full, top)
	if err != nil {
		return err
	}

	at := int(index%tileWidth) * v.ds
	if !bytes.Equal(leaf.Bytes(), data[at:at+v.ds]) {
		return ErrProof
	}

	return nil
}

// load returns the data of the tile of level 0 of the path of index, whose
// tile at each level below top is full[level]. It reads the tiles of the path
// that v does not keep, and the right edge when v has not verified it, in one
// ReadTiles call. It then verifies them from the top down: the edge against
// the root, and each full tile against its hash in the tile above it. It drops
// the tile of a level before it reads the level again, so v keeps only the
// tiles that it verified.
func (v *TileVerifier) load(ctx context.Context, index uint64, full *[tileLevels]Tile, top uint8) ([]byte, error) {
	n := 0
	if !v.edgeOK {
		for level, t := range v.edge {
			if t.Width > 0 {
				n = v.plan(n, t, &v.edgeData[level])
			}
		}
	}
	for level := range top {
		if full[level] != v.path[level] {
			v.path[level] = Tile{}
			n = v.plan(n, full[level], &v.pathData[level])
		}
	}

	var s crypto.Stream
	if n > 0 {
		if err := v.r.ReadTiles(ctx, v.read[:n], v.dst[:n]); err != nil {
			return nil, err //nolint:wrapcheck // the reader's error passes through with its class
		}

		for i, t := range v.read[:n] {
			if len(v.dst[i]) != int(t.Width)*v.ds {
				return nil, ErrTileSize
			}
			*v.slot[i] = v.dst[i]
		}

		s = v.h.NewStream()
		defer s.Close()
	}

	if !v.edgeOK {
		if !rootOf(s, v.ds, &v.edgeData, v.scratch[:]).Equal(v.root) {
			return nil, ErrProof
		}
		v.edgeOK = true
	}

	above := v.edgeData[top]
	for level := int(top) - 1; level >= 0; level-- {
		data := v.pathData[level]
		if full[level] != v.path[level] {
			at := int(index>>(tileHeight*uint(level+1))%tileWidth) * v.ds
			if !bytes.Equal(subtreeRoot(s, v.ds, data, v.scratch[:]).Bytes(), above[at:at+v.ds]) {
				return nil, ErrProof
			}
			v.path[level] = full[level]
		}
		above = data
	}

	return above, nil
}

// plan adds t to the next ReadTiles call at position n, with the buffer at buf
// as its destination. It returns n + 1. It allocates the buffer once, with
// room for a full tile of the hasher of v and [bytes.MinRead] more bytes.
func (v *TileVerifier) plan(n int, t Tile, buf *[]byte) int {
	if room := tileWidth*v.ds + bytes.MinRead; cap(*buf) < room {
		*buf = make([]byte, 0, room)
	}

	v.read[n], v.dst[n], v.slot[n] = t, (*buf)[:0], buf

	return n + 1
}
