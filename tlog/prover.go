// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"context"
	"fmt"
	"math/bits"
	"sync"
	"time"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// nodeTileHashes is the number of hashes of the node tile of a full tile:
// the 256 hashes of the tile, and the 254 roots of its groups of 2 to 128
// hashes. A proof reads the root of a full tile from the tile above it, so
// a node tile does not contain that root.
const nodeTileHashes = 510

// ProverConfig configures a [Prover]. Hasher, Tiles, Clock and Capacity are
// required, so the zero ProverConfig is invalid.
type ProverConfig struct {
	// Hasher is the hash function of the tree.
	Hasher crypto.Hasher

	// Tiles reads the tiles of the tree.
	Tiles TileReader

	// Clock is the clock that [cache.New] requires for the cache of node
	// tiles. The cache reads it once for each node tile that it adds,
	// because node tiles do not expire.
	Clock clock.Clock

	// Capacity bounds the sum of the bytes of the node tiles in the cache.
	// A node tile has 510 hashes: 16,320 bytes with SHA-256. Capacity is at
	// least one node tile of the digest size of Hasher.
	Capacity int64
}

// tilePos is the position of a tile in its tree: its level, and its index
// within the level. The cache of a [Prover] keys each node tile by the
// position of its tile.
type tilePos struct {
	index uint64
	level uint8
}

// nodeTile is a tile and the hash of every perfect subtree inside it below
// the root of the tile. For each fold level k from 0 to 7, it has the
// width>>k roots of the complete groups of 2^k hashes of the tile. Fold
// level k starts at hash 2*(tileWidth - tileWidth>>k) of data for every
// width, so a partial tile uses the layout of a full one, and data has
// room for nodeTileHashes hashes.
//
// A nodeTile is also the read of its tile, which concurrent proofs share
// through the map of reads of a [Prover]. The proof that starts the read is
// its leader. The mutex of the Prover guards done, err and abandoned. The
// leader sets data before it completes the read, and nothing changes data
// or width after that.
type nodeTile struct {
	// err is the error of the read.
	err error

	// done is nil until the first proof that joins the read creates it. The
	// leader closes it when it completes the read.
	done chan struct{}

	data []byte

	// width is the width of the tile.
	width uint16

	// abandoned reports that the context of the leader ended during the
	// read, so each proof that joined it reads the tile itself.
	abandoned bool
}

// Prover builds the proofs and roots of one tree stored as tiles, from a
// cache of node tiles. A node tile is a tile and the hash of every perfect
// subtree inside it below the root of the tile. When a proof finds its node
// tiles in the cache, it does not read, and it hashes only the nodes above
// the perfect subtrees of its spans. [Prover.InclusionProof],
// [Prover.ConsistencyProof] and [Prover.Root] return the proofs and roots of
// [ProveInclusion], [ProveConsistency] and [TreeRoot].
//
// The cache keeps the widest node tile that a proof read for each tile
// position. Such a node tile serves every narrower tile of its position,
// because the tree only appends. A proof reads the tiles that the cache does
// not serve in one [TileReader.ReadTiles] call, and the call reads at most
// two tiles per tile level. Concurrent proofs that miss one position share
// one read of its tile. The proof that starts the read leads it, and the
// other proofs wait for it. When the context of the leader ends during the
// read, each proof that waited reads the tile itself.
//
// A Prover does not verify the tiles that its reader returns, and
// ProveInclusion does not verify them either. The node tile of a wrong tile
// remains in the cache until the cache evicts it, and the proofs built from
// it do not verify. Every size that a caller passes is a size that the log
// published.
//
// # Concurrency
//
// Safe for concurrent use. A proof that finds its node tiles in the cache
// does not take a lock. Its lookups write to an entry of the cache only
// until the entry counts three hits. A proof that misses locks the mutex of
// the Prover to start or join the reads, and again to complete its own.
//
// # Allocation contract
//
// A proof that finds its node tiles in the cache allocates only to grow
// dst. For each tile that it reads, a proof allocates the node tile, its
// buffer and the entry of the cache. The first proof that waits for the read
// of another proof allocates the channel that it waits on. A read also
// allocates what ReadTiles allocates.
type Prover struct {
	h     crypto.Hasher
	r     TileReader
	tiles *cache.Cache[tilePos, *nodeTile]

	// flights maps the position of each running read to the node tile that
	// it reads.
	flights map[tilePos]*nodeTile

	// ds is the digest size of h.
	ds int

	// mu guards flights, and the fields of a node tile that its read
	// completes: done, err and abandoned.
	mu sync.Mutex
}

// NewProver returns a Prover with an empty cache.
//
// Error modes: a nil Hasher or Tiles, a Hasher whose digests have a size
// that [crypto.DigestFromBytes] refuses, a Capacity below one node tile of
// the digest size of the Hasher, and a nil Clock return an error that
// wraps [ErrConfig].
//
// # Allocation contract
//
// The Prover, its map of reads with room for the reads of one proof, and
// what cache.New allocates.
func NewProver(cfg ProverConfig) (*Prover, error) {
	if cfg.Hasher == nil || cfg.Tiles == nil {
		return nil, ErrConfig
	}

	// A proof converts the hashes of its node tiles to digests of this
	// size, so this check covers each of those conversions.
	ds := cfg.Hasher.Hash(nil).Size()

	var probe [crypto.MaxDigestSize]byte
	if _, err := crypto.DigestFromBytes(probe[:ds]); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	if cfg.Capacity < nodeTileHashes*int64(ds) {
		return nil, ErrConfig
	}

	// The Capacity is positive here, so cache.New refuses only a nil Clock.
	tiles, err := cache.New(cache.Config[tilePos, *nodeTile]{
		Clock:    cfg.Clock,
		Cost:     func(nt *nodeTile) int64 { return int64(len(nt.data)) },
		Capacity: cfg.Capacity,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return &Prover{
		h:       cfg.Hasher,
		r:       cfg.Tiles,
		tiles:   tiles,
		flights: make(map[tilePos]*nodeTile, 2*tileLevels),
		ds:      ds,
	}, nil
}

// InclusionProof appends to dst the audit path for the leaf at index in the
// tree of size leaves, and returns the extended slice. The proof is the
// proof that [ProveInclusion] returns.
//
// Error modes: returns dst unchanged and [ErrRange] when index is not below
// size, [ErrTileSize] when a tile read has the wrong length, and the error
// of ReadTiles when it fails. A proof that waits for the read of another
// proof returns the cause of ctx when ctx ends, and the error of that read
// when the read fails for a reason other than the end of the context of its
// leader.
//
// # Allocation contract
//
// As [Prover].
func (p *Prover) InclusionProof(
	ctx context.Context, size, index uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if index >= size {
		return dst, ErrRange
	}

	var spans [maxPath]span

	return p.prove(ctx, size, inclusionSpans(size, index, spans[:0]), dst)
}

// ConsistencyProof appends to dst the proof that the tree of oldSize leaves
// is a prefix of the tree of newSize leaves, and returns the extended
// slice. The proof is the proof that [ProveConsistency] returns. When the
// sizes are equal, the proof is empty and nothing is read.
//
// Error modes: returns dst unchanged and [ErrRange] unless
// 0 < oldSize <= newSize, and the other errors of [Prover.InclusionProof].
//
// # Allocation contract
//
// As [Prover].
func (p *Prover) ConsistencyProof(
	ctx context.Context, oldSize, newSize uint64, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	if oldSize == 0 || oldSize > newSize {
		return dst, ErrRange
	}

	var spans [maxPath]span

	return p.prove(ctx, newSize, consistencySpans(newSize, oldSize, spans[:0]), dst)
}

// Root returns the root of the tree of size leaves, as [TreeRoot] does. For
// a size of zero, it returns HASH() and does not read.
//
// Error modes: the errors of [Prover.InclusionProof] other than [ErrRange].
//
// # Allocation contract
//
// As [Prover].
func (p *Prover) Root(ctx context.Context, size uint64) (crypto.Digest, error) {
	if size == 0 {
		return p.h.Hash(nil), nil
	}

	var root [1]crypto.Digest
	if _, err := p.prove(ctx, size, []span{{0, size}}, root[:0]); err != nil {
		return crypto.Digest{}, err
	}

	return root[0], nil
}

// prove appends to dst the hash of every span in the tree of size leaves.
// It loads the node tiles of the spans that the cache does not serve, and
// combines the roots of the perfect subtrees of each span from the right.
func (p *Prover) prove(
	ctx context.Context, size uint64, spans []span, dst []crypto.Digest,
) ([]crypto.Digest, error) {
	s := proofStates.Get()
	defer proofStates.Put(s)

	s.split(size, spans)

	missed := false
	for _, t := range s.tiles {
		nt, ok := p.tiles.Get(tilePos{index: t.Index, level: t.Level})
		if !ok || nt.width < t.Width {
			nt, missed = nil, true
		}
		s.needs = append(s.needs, need{nt: nt})
	}

	if missed {
		if err := p.load(ctx, s); err != nil {
			return dst, err
		}
	}

	scratch := s.scratch[:]
	start := 0
	for _, end := range s.ends {
		// NewProver checked that crypto.DigestFromBytes takes a hash of the
		// digest size, so these conversions do not fail.
		root, _ := crypto.DigestFromBytes(s.hash(p.ds, end-1))
		for k := end - 2; k >= start; k-- {
			left, _ := crypto.DigestFromBytes(s.hash(p.ds, k))
			root = nodeHash(p.h, scratch, left, root)
		}
		dst = append(dst, root)
		start = end
	}

	return dst, nil
}

// load reads the node tiles of s that the cache did not serve. For each
// such tile, it joins a running read whose width is at least the width of
// the tile, or starts a read. It reads the tiles of its own reads in one
// ReadTiles call, and then waits for the reads that it joined. When the
// leader of a joined read returns the end of its own context, load reads
// that tile itself in one more ReadTiles call, without another join.
func (p *Prover) load(ctx context.Context, s *proofState) error {
	p.mu.Lock()

	for i, t := range s.tiles {
		n := &s.needs[i]
		if n.nt != nil {
			continue
		}

		pos := tilePos{index: t.Index, level: t.Level}
		if f := p.flights[pos]; f != nil && f.width >= t.Width {
			if f.done == nil {
				f.done = make(chan struct{})
			}
			n.nt, n.join = f, true

			continue
		}

		// A read of a wider tile replaces a narrower read in the map, and the
		// narrower read continues for the proofs that joined it.
		n.nt, n.lead = &nodeTile{width: t.Width}, true
		p.flights[pos] = n.nt
	}

	p.mu.Unlock()

	if err := p.read(ctx, s); err != nil {
		return err
	}

	again := false
	for i := range s.needs {
		n := &s.needs[i]
		if !n.join {
			continue
		}

		select {
		case <-n.nt.done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}

		if n.nt.abandoned {
			n.nt, n.lead, again = &nodeTile{width: s.tiles[i].Width}, true, true
		} else if n.nt.err != nil {
			return n.nt.err
		}
	}

	if again {
		return p.read(ctx, s)
	}

	return nil
}

// read reads the tiles that s leads in one ReadTiles call, each into the
// buffer of its node tile, and folds them. It then completes each read,
// also when the call fails. It records the error of each read and removes
// the read from the map of reads. The proofs that joined the read then
// return from their wait. The read of a leader whose context ended is
// abandoned, so each proof that joined it reads the tile itself. read does
// not call ReadTiles when s does not lead a read.
func (p *Prover) read(ctx context.Context, s *proofState) error {
	s.read, s.dst = s.read[:0], s.dst[:0]
	for i, t := range s.tiles {
		if n := &s.needs[i]; n.lead {
			n.nt.data = make([]byte, nodeTileHashes*p.ds)
			s.read = append(s.read, t)
			s.dst = append(s.dst, n.nt.data[:0])
		}
	}

	if len(s.read) == 0 {
		return nil
	}

	err := p.r.ReadTiles(ctx, s.read, s.dst)
	if err == nil {
		err = p.fold(s)
	}

	abandoned := err != nil && ctx.Err() != nil

	p.mu.Lock()

	for i, t := range s.tiles {
		n := &s.needs[i]
		if !n.lead {
			continue
		}

		n.lead = false
		n.nt.err, n.nt.abandoned = err, abandoned

		if pos := (tilePos{index: t.Index, level: t.Level}); p.flights[pos] == n.nt {
			delete(p.flights, pos)
		}

		if n.nt.done != nil {
			close(n.nt.done)
		}
	}

	p.mu.Unlock()

	return err
}

// fold checks the length of each tile that s read, and copies each tile to
// the start of the buffer of its node tile. The copy is a copy onto itself
// when the reader appended the tile to the buffer. fold then computes the
// node tile. For each fold level k from 1 to 7, it hashes each pair of
// hashes of level k − 1 with one call to the hasher's Hash. It adds each
// node tile to the cache, unless the cache has a node tile of its position
// that is at least as wide.
func (p *Prover) fold(s *proofState) error {
	for j, t := range s.read {
		if len(s.dst[j]) != int(t.Width)*p.ds {
			return ErrTileSize
		}
	}

	scratch := s.scratch[:]
	j := 0
	for i, t := range s.tiles {
		n := &s.needs[i]
		if !n.lead {
			continue
		}

		nt := n.nt
		copy(nt.data, s.dst[j])
		j++

		//dokimi:mutate-skip ror-boundary: a pass for fold level 8 starts at the end of the buffer and writes nothing
		for k := 1; k < tileHeight; k++ {
			prev, next := 2*(tileWidth-tileWidth>>(k-1))*p.ds, 2*(tileWidth-tileWidth>>k)*p.ds
			for pair := range int(nt.width) >> k {
				d := pairHash(p.h, scratch, nt.data[prev+2*pair*p.ds:prev+(2*pair+2)*p.ds])
				copy(nt.data[next+pair*p.ds:], d.Bytes())
			}
		}

		pos := tilePos{index: t.Index, level: t.Level}
		//dokimi:mutate-skip ror-boundary: a node tile of the same width has the hashes of the cached one
		if cached, ok := p.tiles.Get(pos); !ok || cached.width < nt.width {
			p.tiles.Set(pos, nt, time.Time{})
		}
	}

	return nil
}

// need is the node tile that serves one tile of a proof, and the part of
// the proof in the read of that tile.
type need struct {
	nt *nodeTile

	// lead reports that the proof reads the tile, and join that the proof
	// waits for the read of another proof.
	lead, join bool
}

// proofState is the state of one proof of a [Prover]. Proofs borrow it from
// proofStates.
type proofState struct {
	subtrees

	// needs[i] is the need of tiles[i].
	needs []need

	// read and dst are the tiles and the buffers of one ReadTiles call.
	read []Tile
	dst  [][]byte

	// scratch has room for the input of one node hash.
	scratch [nodeRoom]byte
}

// proofStates supplies the state of one proof.
var proofStates = pool.NewResetPool(func() *proofState { return new(proofState) })

// Reset empties s for the next proof, and drops the node tiles and the
// buffers of the last one. Each call of read truncates read and dst itself.
func (s *proofState) Reset() {
	s.reset()
	clear(s.needs)
	s.needs = s.needs[:0]
	clear(s.dst)
}

// hash returns the root of subtree i of s: ds bytes of the node tile of its
// tile. The root of 2^k hashes from position off of a tile is hash off>>k
// of fold level k.
func (s *proofState) hash(ds, i int) []byte {
	n := s.nodes[i]
	k := bits.TrailingZeros(uint(n.count))
	at := (2*(tileWidth-tileWidth>>k) + n.off>>k) * ds

	return s.needs[n.tile].nt.data[at : at+ds]
}
