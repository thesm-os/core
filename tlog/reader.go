// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"bytes"
	"context"
	"fmt"
	"math/bits"

	"go.thesmos.sh/core/arena"
	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/task"
)

// TileReader reads tile data from storage. It describes IO, and ctx
// governs cancellation and the deadline of the whole batch.
type TileReader interface {
	// ReadTiles appends the data of tiles[i] to dst[i] for every i:
	// tiles[i].Width hashes of the tree's digest size. dst has one
	// entry per tile. An implementation over remote storage reads the
	// tiles concurrently. A missing tile returns an error classified
	// errs.NotFound. The contents of dst are unspecified after an
	// error.
	ReadTiles(ctx context.Context, tiles []Tile, dst [][]byte) error
}

// BlobTiles returns a [TileReader] over the objects under prefix in s,
// at the paths [Tile.Path] gives. prefix is prepended as given, so a
// prefix that names a directory ends in a slash. The reader reads up to
// limit tiles concurrently through [task.Each], and returns
// [task.ErrLimit] when limit is below one.
//
// # Allocation contract
//
// One allocation for the reader. ReadTiles allocates one key per tile
// and what the store allocates. It reads each body with
// [bytes.Buffer.ReadFrom], so it allocates nothing to store the data
// when dst[i] has room for the tile and [bytes.MinRead] more bytes,
// which ReadFrom needs to see the end of the body.
func BlobTiles(s blob.Store, prefix string, limit int) TileReader {
	return &blobTiles{s: s, prefix: prefix, limit: limit}
}

// blobTiles is the [TileReader] of [BlobTiles].
type blobTiles struct {
	s      blob.Store
	prefix string
	limit  int
}

// ReadTiles reads every tile through Get and appends its body to its
// buffer. It returns [ErrRange] when dst has fewer entries than tiles.
func (r *blobTiles) ReadTiles(ctx context.Context, tiles []Tile, dst [][]byte) error {
	if len(dst) < len(tiles) {
		return ErrRange
	}

	// Each returns the first error of fn, which fn wraps with the key.
	//nolint:wrapcheck // see comment above
	return task.Each(ctx, r.limit, tiles, func(ctx context.Context, i int, t Tile) error {
		key := r.prefix + t.Path()

		rc, _, err := r.s.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("tlog: read %s: %w", key, err)
		}

		buf := bytes.NewBuffer(dst[i])
		_, err = buf.ReadFrom(rc)
		dst[i] = buf.Bytes()
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("tlog: read %s: %w", key, err)
		}

		return nil
	})
}

// TreeRoot returns the root of the tree of size leaves stored in r. It
// reads the tiles at the tree's right edge in one [TileReader.ReadTiles]
// call. A size of zero reads nothing and returns HASH().
//
// Returns [ErrTileSize] when a tile read has the wrong length, and the
// error of ReadTiles when it fails.
//
// # Allocation contract
//
// The buffers for the tiles come from a pool, so a call allocates
// nothing beyond what ReadTiles allocates.
func TreeRoot(ctx context.Context, h crypto.Hasher, r TileReader, size uint64) (crypto.Digest, error) {
	if size == 0 {
		return h.Hash(nil), nil
	}

	var root [1]crypto.Digest
	if _, err := prove(ctx, h, r, size, []span{{0, size}}, root[:0]); err != nil {
		return crypto.Digest{}, err
	}

	return root[0], nil
}

// ProveInclusion appends to dst the audit path for the leaf at index in
// the tree of size leaves stored in r, and returns the extended slice.
// It computes the tiles the path needs first and reads them in one
// [TileReader.ReadTiles] call, at most two tiles per tile level. size
// is a size the log published, so its partial tiles exist.
//
// Returns dst unchanged and [ErrRange] when index is not below size,
// [ErrTileSize] when a tile read has the wrong length, and the error of
// ReadTiles when it fails.
//
// # Allocation contract
//
// The buffers for the tiles come from a pool, so a call allocates only
// to grow dst and what ReadTiles allocates.
func ProveInclusion(
	ctx context.Context, h crypto.Hasher, r TileReader, size, index uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if index >= size {
		return dst, ErrRange
	}

	var spans [maxPath]span

	return prove(ctx, h, r, size, inclusionSpans(0, size, index, spans[:0]), dst)
}

// ProveConsistency appends to dst the proof that the tree of oldSize
// leaves stored in r is a prefix of the tree of newSize leaves, and
// returns the extended slice. It reads its tiles in one
// [TileReader.ReadTiles] call, at most two tiles per tile level.
//
// Returns dst unchanged and [ErrRange] unless 0 < oldSize <= newSize.
// When the sizes are equal, the proof is empty and nothing is read.
//
// # Allocation contract
//
// As [ProveInclusion].
func ProveConsistency(
	ctx context.Context, h crypto.Hasher, r TileReader, oldSize, newSize uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if oldSize == 0 || oldSize > newSize {
		return dst, ErrRange
	}

	var spans [maxPath]span

	return prove(ctx, h, r, newSize, consistencySpans(0, newSize, oldSize, spans[:0]), dst)
}

// prover is the state of one proof built from tiles. Proofs borrow it
// from provers.
type prover struct {
	tiles []Tile
	dst   [][]byte
	nodes []nodeRef

	// ends[i] is the end in nodes of the nodes of span i.
	ends []int

	// mem contains the data of every tile and one tile of scratch.
	mem arena.Arena
}

// nodeRef locates the hashes under one perfect subtree in a tile.
type nodeRef struct {
	tile  int
	off   int
	count int
}

// Reset empties p for the next proof.
func (p *prover) Reset() {
	p.tiles = p.tiles[:0]
	clear(p.dst)
	p.dst = p.dst[:0]
	p.nodes = p.nodes[:0]
	p.ends = p.ends[:0]
	p.mem.Reset()
}

// provers supplies the state of one proof.
var provers = pool.NewResetPool(func() *prover { return new(prover) })

// prove appends to dst the hash of every span in the tree of size
// leaves stored in r. It reads every tile the spans need in one
// ReadTiles call.
func prove(
	ctx context.Context, h crypto.Hasher, r TileReader, size uint64, spans []span, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if len(spans) == 0 {
		return dst, nil
	}

	p := provers.Get()
	defer provers.Put(p)

	// A span splits into at most 64 perfect subtrees, one per bit of its
	// length.
	for _, s := range spans {
		lo := s.lo
		for range maxPath {
			if lo >= s.hi {
				break
			}

			level := uint(bits.Len64(s.hi-lo) - 1)
			p.add(size, level, lo>>level)
			lo += 1 << level
		}
		p.ends = append(p.ends, len(p.nodes))
	}

	ds := h.Hash(nil).Size()
	total := tileWidth
	for _, t := range p.tiles {
		total += int(t.Width)
	}

	// Each tile buffer has bytes.MinRead bytes of room past the tile, so
	// a reader that reads with bytes.Buffer.ReadFrom, as BlobTiles does,
	// sees the end of the body without growing the buffer.
	mem := p.mem.Alloc(total*ds + len(p.tiles)*bytes.MinRead)
	off := 0
	for _, t := range p.tiles {
		end := off + int(t.Width)*ds + bytes.MinRead
		p.dst = append(p.dst, mem[off:off:end])
		off = end
	}
	scratch := mem[off:]

	if err := r.ReadTiles(ctx, p.tiles, p.dst); err != nil {
		return dst, err //nolint:wrapcheck // the reader's error passes through with its class
	}
	for i, t := range p.tiles {
		if len(p.dst[i]) != int(t.Width)*ds {
			return dst, ErrTileSize
		}
	}

	s := h.NewStream()
	defer s.Close()

	start := 0
	for _, end := range p.ends {
		var root crypto.Digest
		for k := end - 1; k >= start; k-- {
			n := p.nodes[k]
			d := subtreeRoot(s, ds, p.dst[n.tile][n.off*ds:(n.off+n.count)*ds], scratch)
			if k == end-1 {
				root = d
			} else {
				root = nodeHash(s, scratch, d, root)
			}
		}
		dst = append(dst, root)
		start = end
	}

	return dst, nil
}

// add records the perfect subtree at tree level and index in the tree
// of size leaves, and the tile that contains its hashes. A subtree of level
// 8L + k is the root of 2^k consecutive hashes of tile level L.
func (p *prover) add(size uint64, level uint, index uint64) {
	tileLevel, k := level/tileHeight, level%tileHeight
	row := index << k
	//nolint:gosec // G115: level is below 64, so tileLevel is below 8, and the width is at most 256
	t := Tile{
		Level: uint8(tileLevel),
		Index: row >> tileHeight,
		Width: uint16(min(tileWidth, (size>>(tileHeight*tileLevel))-(row>>tileHeight)<<tileHeight)),
	}

	i := 0
	for i < len(p.tiles) && p.tiles[i] != t {
		i++
	}
	if i == len(p.tiles) {
		p.tiles = append(p.tiles, t)
	}

	p.nodes = append(p.nodes, nodeRef{tile: i, off: int(row % tileWidth), count: 1 << k})
}
