// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of Prover.
const (
	// nodeTileBytes is the size of a node tile of SHA-256: 510 hashes of 32
	// bytes.
	nodeTileBytes = 16320

	// everyTile is more node tiles than the tree of 70000 leaves has: 274
	// at level 0, 2 at level 1 and 1 at level 2.
	everyTile = 300

	// joiners is the number of proofs of the cases that join the read of
	// another proof.
	joiners = 3

	// publishedStep is the number of leaves between the published sizes of
	// the tree of the machines of the Prover above smallSize.
	publishedStep = 997

	// coveredShare is the least share of the cases of the sequential machine
	// of the Prover that each kind of cache counts.
	coveredShare = 0.25

	// neverEvicts and evicts label the cases of the sequential machine of
	// the Prover by their cache: one with room for every node tile, which
	// checks the tiles that each call reads, and one that evicts.
	neverEvicts = "a cache that never evicts"
	evicts      = "a cache that evicts"
)

// gatedTiles is a TileReader over the tiles of m whose call number i waits
// until gates[i] is closed or its context ends. A call past the gates does
// not wait. gatedTiles counts its calls. When err is set, it returns err in
// place of the tiles, and it calls after once the tiles arrive.
type gatedTiles struct {
	m     *memTiles
	err   error
	after func()
	gates []chan struct{}
	calls atomic.Int64
}

// ReadTiles waits for the gate of the call, and then returns err or reads
// tiles from m.
func (g *gatedTiles) ReadTiles(ctx context.Context, tiles []tlog.Tile, dst [][]byte) error {
	if i := int(g.calls.Add(1)) - 1; i < len(g.gates) {
		select {
		case <-g.gates[i]:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if g.err != nil {
		return g.err
	}

	if err := g.m.ReadTiles(ctx, tiles, dst); err != nil {
		return err
	}

	if g.after != nil {
		g.after()
	}

	return nil
}

// freshTiles is a TileReader over the tiles of m that returns each tile in
// a buffer of its own, in place of the buffer that the call passes.
type freshTiles struct {
	m *memTiles
}

// ReadTiles drops each buffer of dst, so m appends each tile to a new one.
func (f freshTiles) ReadTiles(ctx context.Context, tiles []tlog.Tile, dst [][]byte) error {
	clear(dst)

	return f.m.ReadTiles(ctx, tiles, dst)
}

// zeroHasher is SHA-256 whose Hash returns the zero digest, which has no
// size.
type zeroHasher struct {
	coresha256.Hasher
}

// Hash returns the zero digest.
func (zeroHasher) Hash([]byte) crypto.Digest {
	return crypto.Digest{}
}

// position is the position of a tile, which the machine of the Prover
// keys the widths of the tiles that it read by.
type position struct {
	index uint64
	level uint8
}

// outcome is the result of one call of a Prover or of a prove function.
type outcome struct {
	err   error
	proof []crypto.Digest
	root  crypto.Digest
}

// proverCall is the input of one step of the concurrent machine of the
// Prover, and the result of the prove function for it.
type proverCall struct {
	want             outcome
	size, index, old uint64
}

func TestProver(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)

	// caches are the capacities of the cases that compare the Prover with
	// the prove functions.
	caches := []struct {
		name  string
		tiles int64
	}{
		{name: "a cache of one node tile", tiles: 1},
		{name: "a cache of 64 node tiles", tiles: 64},
		{name: "a cache of every node tile", tiles: everyTile},
	}

	t.Run("Prover", func(t *testing.T) {
		t.Parallel()

		sizes, published := publishedTree(t)

		t.Run("returns the results of the prove functions under any sequence of calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the Prover must return the results of the prove functions", func(c *prop.Case) {
				// A cache of a few node tiles evicts on most proofs. A cache of
				// every node tile never evicts, so the case also checks the tiles
				// that each call reads.
				capacity := c.Draw(prop.OneOf(prop.Integer[int64](1, 8), prop.Just[int64](everyTile)), "node tiles")
				if capacity == everyTile {
					c.Classify(neverEvicts)
				} else {
					c.Classify(evicts)
				}
				r := &memTiles{data: published.data}
				p := newProver(c, r, capacity)
				widest := map[position]uint16{}

				// reads checks that the last call of p read the tiles of need that
				// the cache did not serve, when the cache never evicts. It then
				// records the widths of need as the widths in the cache.
				reads := func(c *prop.Case, need []tlog.Tile, calls int) {
					var want []tlog.Tile
					for _, tile := range need {
						pos := position{index: tile.Index, level: tile.Level}
						if widest[pos] < tile.Width {
							want = append(want, tile)
						}
						widest[pos] = max(widest[pos], tile.Width)
					}

					if capacity < everyTile {
						return
					}

					assert.InRange(c, r.calls-calls, 0, 1, "the Prover must read its tiles in at most one call")
					var got []tlog.Tile
					if r.calls > calls {
						got = slices.Clone(r.last)
					}
					assert.Equal(c, got, want, "the Prover must read the tiles whose node tiles the cache lacks")
				}

				stateful.Steps(c, stateful.Machine[struct{}]{
					Actions: []stateful.Action[struct{}]{
						{
							Name: "root",
							Input: func(c *prop.Case, _ struct{}) any {
								return sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]
							},
							Run: func(c *prop.Case, _ int, in any) {
								size, _ := in.(uint64)
								ref := &memTiles{data: published.data}
								want, err := tlog.TreeRoot(c.Context(), h, ref, size)
								assert.NoError(c, err, "TreeRoot must compute the root")
								calls := r.calls
								got, err := p.Root(c.Context(), size)
								assert.NoError(c, err, "Root must compute the root")
								assert.Equal(c, got, want, "Root must return the root of TreeRoot")
								reads(c, ref.last, calls)
							},
						},
						{
							Name: "inclusion proof",
							Input: func(c *prop.Case, _ struct{}) any {
								size := sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]

								return [2]uint64{size, c.Draw(prop.Integer[uint64](0, size-1), "index")}
							},
							Run: func(c *prop.Case, _ int, in any) {
								pair, _ := in.([2]uint64)
								ref := &memTiles{data: published.data}
								want, err := tlog.ProveInclusion(c.Context(), h, ref, pair[0], pair[1], nil)
								assert.NoError(c, err, "ProveInclusion must prove the leaf")
								calls := r.calls
								got, err := p.InclusionProof(c.Context(), pair[0], pair[1], nil)
								assert.NoError(c, err, "InclusionProof must prove the leaf")
								assert.Equal(c, got, want, "InclusionProof must return the proof of ProveInclusion")
								reads(c, ref.last, calls)
							},
						},
						{
							Name: "consistency proof",
							Input: func(c *prop.Case, _ struct{}) any {
								size := sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]

								return [2]uint64{c.Draw(prop.Integer[uint64](1, size), "old size"), size}
							},
							Run: func(c *prop.Case, _ int, in any) {
								pair, _ := in.([2]uint64)
								ref := &memTiles{data: published.data}
								want, err := tlog.ProveConsistency(c.Context(), h, ref, pair[0], pair[1], nil)
								assert.NoError(c, err, "ProveConsistency must prove consistency")
								calls := r.calls
								got, err := p.ConsistencyProof(c.Context(), pair[0], pair[1], nil)
								assert.NoError(c, err, "ConsistencyProof must prove consistency")
								assert.Equal(c, got, want, "ConsistencyProof must return the proof of ProveConsistency")
								reads(c, ref.last, calls)
							},
						},
					},
				})
			}, prop.Require(neverEvicts, coveredShare), prop.Require(evicts, coveredShare))
		})

		t.Run("returns the results of the prove functions to concurrent calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the Prover must return the results of the prove functions to concurrent calls",
				func(c *prop.Case) {
					p := newProver(c, &memTiles{data: published.data},
						c.Draw(prop.Integer[int64](1, 8), "node tiles"))

					// The input of each step contains the result of its prove
					// function, so the spec accepts each call that returns that
					// result.
					stateful.Steps(c, stateful.Machine[struct{}]{
						Spec: history.Spec[struct{}]{
							Initial: func() struct{} { return struct{}{} },
							Next: func(s struct{}, op history.Operation) []struct{} {
								in, _ := op.Args[0].(proverCall)
								if op.Returned(in.want) {
									return []struct{}{s}
								}

								return nil
							},
						},
						Actions: []stateful.Action[struct{}]{
							{
								Name: "root",
								Input: func(c *prop.Case, _ struct{}) any {
									size := sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]
									root, err := tlog.TreeRoot(c.Context(), h, published, size)

									return proverCall{size: size, want: outcome{root: root, err: err}}
								},
								Run: func(c *prop.Case, client int, in any) {
									pc, _ := in.(proverCall)
									call := c.History().Invoke(client, "root", []any{pc})
									root, err := p.Root(c.Context(), pc.size)
									call.OK(outcome{root: root, err: err})
								},
							},
							{
								Name: "inclusion proof",
								Input: func(c *prop.Case, _ struct{}) any {
									size := sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]
									index := c.Draw(prop.Integer[uint64](0, size-1), "index")
									proof, err := tlog.ProveInclusion(c.Context(), h, published, size, index, nil)

									return proverCall{size: size, index: index, want: outcome{proof: proof, err: err}}
								},
								Run: func(c *prop.Case, client int, in any) {
									pc, _ := in.(proverCall)
									call := c.History().Invoke(client, "inclusion proof", []any{pc})
									proof, err := p.InclusionProof(c.Context(), pc.size, pc.index, nil)
									call.OK(outcome{proof: proof, err: err})
								},
							},
							{
								Name: "consistency proof",
								Input: func(c *prop.Case, _ struct{}) any {
									size := sizes[c.Draw(prop.Integer(0, len(sizes)-1), "size")]
									old := c.Draw(prop.Integer[uint64](1, size), "old size")
									proof, err := tlog.ProveConsistency(c.Context(), h, published, old, size, nil)

									return proverCall{size: size, old: old, want: outcome{proof: proof, err: err}}
								},
								Run: func(c *prop.Case, client int, in any) {
									pc, _ := in.(proverCall)
									call := c.History().Invoke(client, "consistency proof", []any{pc})
									proof, err := p.ConsistencyProof(c.Context(), pc.old, pc.size, nil)
									call.OK(outcome{proof: proof, err: err})
								},
							},
						},
					}, stateful.Clients(2))
				})
		})
	})

	t.Run("NewProver", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			cfg  tlog.ProverConfig
		}{
			{
				name: "returns ErrConfig for a nil Hasher",
				cfg:  tlog.ProverConfig{Tiles: smallTiles, Capacity: nodeTileBytes},
			},
			{
				name: "returns ErrConfig for nil Tiles",
				cfg:  tlog.ProverConfig{Hasher: h, Capacity: nodeTileBytes},
			},
			{
				name: "returns ErrConfig for a Capacity of zero",
				cfg:  tlog.ProverConfig{Hasher: h, Tiles: smallTiles},
			},
			{
				name: "returns ErrConfig for a Capacity below one node tile",
				cfg:  tlog.ProverConfig{Hasher: h, Tiles: smallTiles, Capacity: nodeTileBytes - 1},
			},
			{
				name: "returns ErrConfig for a Hasher whose digests have no size",
				cfg:  tlog.ProverConfig{Hasher: zeroHasher{h}, Tiles: smallTiles, Capacity: nodeTileBytes},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p, err := tlog.NewProver(tt.cfg)
				assert.ErrorIs(t, err, tlog.ErrConfig, "the configuration must be refused")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, p, "NewProver must not return a Prover")
			})
		}

		t.Run("returns the error of cache.New for a Capacity of zero", func(t *testing.T) {
			t.Parallel()
			_, err := tlog.NewProver(tlog.ProverConfig{Hasher: h, Tiles: smallTiles})
			assert.ErrorIs(t, err, cache.ErrConfig, "the error must wrap the refusal of the cache")
		})

		t.Run("returns a Prover for a Capacity of one node tile", func(t *testing.T) {
			t.Parallel()
			p, err := tlog.NewProver(tlog.ProverConfig{Hasher: h, Tiles: smallTiles, Capacity: nodeTileBytes})
			assert.NoError(t, err, "a Capacity of one node tile must be accepted")
			assert.NotNil(t, p, "NewProver must return a Prover")
		})
	})

	t.Run("InclusionProof", func(t *testing.T) {
		t.Parallel()

		for _, c := range caches {
			t.Run("returns the proof of ProveInclusion for every leaf of the sampled trees from "+c.name,
				func(t *testing.T) {
					t.Parallel()
					p := newProver(t, smallTiles, c.tiles)
					for _, size := range sampleSizes {
						for i := range size {
							got, err := p.InclusionProof(t.Context(), size, i, nil)
							assert.NoError(t, err, "InclusionProof must succeed")
							want, err := tlog.ProveInclusion(t.Context(), h, smallTiles, size, i, nil)
							assert.NoError(t, err, "ProveInclusion must succeed")
							expect.Equal(t, got, want, "the proof of leaf "+strconv.FormatUint(i, 10)+" of "+
								strconv.FormatUint(size, 10))
						}
					}
				})

			t.Run("returns the proof of ProveInclusion for leaves of every tree up to 600 from "+c.name,
				func(t *testing.T) {
					t.Parallel()
					p := newProver(t, smallTiles, c.tiles)
					for size := uint64(1); size <= smallSize; size++ {
						for _, i := range []uint64{0, size / 2, size - 1} {
							got, err := p.InclusionProof(t.Context(), size, i, nil)
							assert.NoError(t, err, "InclusionProof must succeed")
							want, err := tlog.InclusionProof(h, small[:size], i, nil)
							assert.NoError(t, err, "InclusionProof in memory must succeed")
							expect.Equal(t, got, want, "the proof of leaf "+strconv.FormatUint(i, 10)+" of "+
								strconv.FormatUint(size, 10))
						}
					}
				})

			t.Run("returns the proof of ProveInclusion for leaves of a tree of 70000 from "+c.name,
				func(t *testing.T) {
					t.Parallel()
					p := newProver(t, largeTiles, c.tiles)
					for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, largeSize - 1, 0} {
						got, err := p.InclusionProof(t.Context(), largeSize, i, nil)
						assert.NoError(t, err, "InclusionProof must succeed")
						want, err := tlog.InclusionProof(h, large, i, nil)
						assert.NoError(t, err, "InclusionProof in memory must succeed")
						expect.Equal(t, got, want, "the proof of leaf "+strconv.FormatUint(i, 10)+" of 70000")
					}
				})
		}

		t.Run("appends the proof to dst", func(t *testing.T) {
			t.Parallel()
			p := newProver(t, largeTiles, everyTile)
			dst := []crypto.Digest{h.Hash([]byte("kept"))}
			got, err := p.InclusionProof(t.Context(), largeSize, proofIndex, dst)
			assert.NoError(t, err, "InclusionProof must succeed")
			want, err := tlog.InclusionProof(h, large, proofIndex, dst[:1:1])
			assert.NoError(t, err, "InclusionProof in memory must succeed")
			assert.Equal(t, got, want, "the proof must follow the kept digest")
		})

		t.Run("does not read for a proof whose node tiles the cache contains", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			p := newProver(t, m, everyTile)
			_, err := p.InclusionProof(t.Context(), largeSize, proofIndex, nil)
			assert.NoError(t, err, "the first proof must succeed")
			calls := m.calls
			for _, i := range []uint64{proofIndex, proofIndex + 1, proofIndex - 1} {
				_, err := p.InclusionProof(t.Context(), largeSize, i, nil)
				assert.NoError(t, err, "InclusionProof must succeed")
			}
			assert.Equal(t, m.calls, calls, "the proofs of the leaves of one tile must not read again")
		})

		t.Run("reads its tiles in one call with at most two tiles per level", func(t *testing.T) {
			t.Parallel()
			for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, largeSize - 1} {
				m := &memTiles{data: largeTiles.data}
				_, err := newProver(t, m, everyTile).InclusionProof(t.Context(), largeSize, i, nil)
				assert.NoError(t, err, "InclusionProof must succeed")
				expect.Equal(t, m.calls, 1, "the proof of leaf "+strconv.FormatUint(i, 10)+" must read once")
				for level, n := range tilesPerLevel(m.last) {
					expect.InRange(t, n, 1, 2, "the proof of leaf "+strconv.FormatUint(i, 10)+
						" must read at most two tiles at level "+strconv.Itoa(int(level)))
				}
			}
		})

		t.Run("reads the tiles of a proof again after the cache evicts their node tiles", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			p := newProver(t, m, 1)
			for range 2 {
				_, err := p.InclusionProof(t.Context(), largeSize, proofIndex, nil)
				assert.NoError(t, err, "InclusionProof must succeed")
			}
			assert.Equal(t, m.calls, 2, "a cache of one node tile must not keep the node tiles of a proof")
		})

		t.Run("serves a smaller size from the node tile of a larger size without a read", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: smallTiles.data}
			p := newProver(t, m, everyTile)
			_, err := p.InclusionProof(t.Context(), 400, 399, nil)
			assert.NoError(t, err, "the proof at 400 must succeed")
			calls := m.calls
			got, err := p.InclusionProof(t.Context(), 300, 299, nil)
			assert.NoError(t, err, "the proof at 300 must succeed")
			want, err := tlog.InclusionProof(h, small[:300], 299, nil)
			assert.NoError(t, err, "InclusionProof in memory must succeed")
			expect.Equal(t, got, want, "the proof at 300 must be the proof in memory")
			expect.Equal(t, m.calls, calls, "the proof at 300 must not read")
		})

		t.Run("replaces a node tile with the wider node tile of a larger size", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: smallTiles.data}
			p := newProver(t, m, everyTile)
			for _, size := range []uint64{300, 400} {
				calls := m.calls
				_, err := p.InclusionProof(t.Context(), size, size-1, nil)
				assert.NoError(t, err, "InclusionProof must succeed")
				expect.Equal(t, m.calls, calls+1, "the proof at "+strconv.FormatUint(size, 10)+" must read once")
			}
			calls := m.calls
			_, err := p.InclusionProof(t.Context(), 350, 349, nil)
			assert.NoError(t, err, "the proof at 350 must succeed")
			assert.Equal(t, m.calls, calls, "the proof at 350 must use the node tile of 400")
		})

		t.Run("folds a tile that the reader returns in a new buffer", func(t *testing.T) {
			t.Parallel()
			p := newProver(t, freshTiles{m: &memTiles{data: largeTiles.data}}, everyTile)
			got, err := p.InclusionProof(t.Context(), largeSize, proofIndex, nil)
			assert.NoError(t, err, "InclusionProof must succeed")
			want, err := tlog.InclusionProof(h, large, proofIndex, nil)
			assert.NoError(t, err, "InclusionProof in memory must succeed")
			assert.Equal(t, got, want, "the proof must be the proof in memory")
		})

		t.Run("returns dst unchanged for a tree of one leaf", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: smallTiles.data}
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := newProver(t, m, everyTile).InclusionProof(t.Context(), 1, 0, dst)
			assert.NoError(t, err, "InclusionProof must succeed")
			expect.Equal(t, got, dst, "the proof in a tree of one leaf must be empty")
			expect.Equal(t, m.calls, 0, "the proof in a tree of one leaf must not read")
		})

		t.Run("returns ErrRange with dst unchanged for an index at the size", func(t *testing.T) {
			t.Parallel()
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := newProver(t, smallTiles, everyTile).InclusionProof(t.Context(), 5, 5, dst)
			expect.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
			expect.Equal(t, got, dst, "dst must be returned unchanged")
		})

		t.Run("returns ErrTileSize with dst unchanged for tile data of the wrong length", func(t *testing.T) {
			t.Parallel()
			short := &memTiles{data: map[tlog.Tile][]byte{{Width: 3}: make([]byte, 2*h.Hash(nil).Size())}}
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := newProver(t, short, everyTile).InclusionProof(t.Context(), 3, 0, dst)
			expect.ErrorIs(t, err, tlog.ErrTileSize, "a short tile must be ErrTileSize")
			expect.Equal(t, got, dst, "dst must be returned unchanged")
		})

		t.Run("returns the error of the reader", func(t *testing.T) {
			t.Parallel()
			empty := &memTiles{data: map[tlog.Tile][]byte{}}
			_, err := newProver(t, empty, everyTile).InclusionProof(t.Context(), 3, 0, nil)
			assert.ErrorIs(t, err, errAbsent, "the error of the reader must be returned")
		})

		t.Run("reads the tiles of a failed read again", func(t *testing.T) {
			t.Parallel()
			failed := errors.New("read failed")
			g := &gatedTiles{m: &memTiles{data: largeTiles.data}, err: failed}
			p := newProver(t, g, everyTile)
			_, err := p.InclusionProof(t.Context(), largeSize, proofIndex, nil)
			assert.ErrorIs(t, err, failed, "the failed read must return its error")
			g.err = nil
			_, err = p.InclusionProof(t.Context(), largeSize, proofIndex, nil)
			assert.NoError(t, err, "the second proof must succeed")
			assert.Equal(t, g.calls.Load(), int64(2), "the second proof must read the tiles again")
		})

		t.Run("reads a tile once for concurrent proofs that miss it", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := &gatedTiles{m: &memTiles{data: largeTiles.data}, gates: []chan struct{}{make(chan struct{})}}
				p := newProver(t, g, everyTile)
				results := proveAll(t, p, append([]uint64{proofIndex}, siblings(joiners)...))
				synctest.Wait()
				expect.Equal(t, g.calls.Load(), int64(1), "the proofs must share one read while it waits")
				close(g.gates[0])
				checkAll(t, h, large, results)
				expect.Equal(t, g.calls.Load(), int64(1), "the proofs must share one read")
			})
		})

		t.Run("reads the tile in each proof that joined the read of a leader whose context ended", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := &gatedTiles{m: &memTiles{data: largeTiles.data}, gates: []chan struct{}{make(chan struct{})}}
				p := newProver(t, g, everyTile)
				ctx, cancel := context.WithCancel(t.Context())
				leader := make(chan error, 1)
				go func() {
					_, err := p.InclusionProof(ctx, largeSize, proofIndex, nil)
					leader <- err
				}()
				synctest.Wait()
				results := proveAll(t, p, siblings(joiners))
				synctest.Wait()
				cancel()
				expect.ErrorIs(t, <-leader, context.Canceled, "the leader must return the end of its context")
				checkAll(t, h, large, results)
				expect.Equal(t, g.calls.Load(), int64(1+joiners), "each proof that joined must read the tile itself")
			})
		})

		t.Run("reads again only the tiles of the joined reads whose leader's context ended", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				m := &memTiles{data: smallTiles.data}
				g := &gatedTiles{m: m, gates: []chan struct{}{make(chan struct{}), make(chan struct{})}}
				p := newProver(t, g, everyTile)

				// The proof at 300 leads the reads of both its tiles. The proof
				// at 400 leads the read of the wider tile of level 0, and joins
				// the read of the tile of level 1.
				ctx, cancel := context.WithCancel(t.Context())
				leader := make(chan error, 1)
				go func() {
					_, err := p.InclusionProof(ctx, 300, 299, nil)
					leader <- err
				}()
				synctest.Wait()
				wide := proveAt(t, p, 400, 399)
				synctest.Wait()
				cancel()
				expect.ErrorIs(t, <-leader, context.Canceled, "the leader must return the end of its context")
				close(g.gates[1])

				r := <-wide
				assert.NoError(t, r.err, "the proof at 400 must succeed")
				want, err := tlog.InclusionProof(h, small[:400], 399, nil)
				assert.NoError(t, err, "InclusionProof in memory must succeed")
				expect.Equal(t, r.proof, want, "the proof at 400 must be the proof in memory")
				expect.Equal(t, g.calls.Load(), int64(3), "the proof at 400 must read the abandoned tile once more")
				expect.Equal(t, m.last, []tlog.Tile{{Level: 1, Width: 1}},
					"the last read must contain the abandoned tile alone")
			})
		})

		t.Run("keeps the read of a leader whose context ends after the tiles arrive", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				g := &gatedTiles{
					m:     &memTiles{data: largeTiles.data},
					gates: []chan struct{}{make(chan struct{})},
					after: cancel,
				}
				p := newProver(t, g, everyTile)
				leader := make(chan error, 1)
				go func() {
					_, err := p.InclusionProof(ctx, largeSize, proofIndex, nil)
					leader <- err
				}()
				synctest.Wait()
				results := proveAll(t, p, siblings(joiners))
				synctest.Wait()
				close(g.gates[0])
				expect.NoError(t, <-leader, "the leader must return its proof")
				checkAll(t, h, large, results)
				expect.Equal(t, g.calls.Load(), int64(1), "the proofs that joined must use the read of the leader")
			})
		})

		failed := errors.New("read failed")
		short := &memTiles{data: map[tlog.Tile][]byte{}}
		for tile, data := range largeTiles.data {
			short.data[tile] = data[:len(data)-1]
		}
		failures := []struct {
			want error
			name string
			g    *gatedTiles
		}{
			{
				name: "returns the error of the read of the leader to each proof that joined it",
				g:    &gatedTiles{m: &memTiles{data: largeTiles.data}, err: failed},
				want: failed,
			},
			{
				name: "returns ErrTileSize of the read of the leader to each proof that joined it",
				g:    &gatedTiles{m: short},
				want: tlog.ErrTileSize,
			},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					tt.g.gates = []chan struct{}{make(chan struct{})}
					p := newProver(t, tt.g, everyTile)
					results := proveAll(t, p, append([]uint64{proofIndex}, siblings(joiners)...))
					synctest.Wait()
					close(tt.g.gates[0])
					for range cap(results) {
						r := <-results
						expect.ErrorIs(t, r.err, tt.want, "the proof of leaf "+strconv.FormatUint(r.index, 10)+
							" must return the error of the read")
					}
					expect.Equal(t, tt.g.calls.Load(), int64(1), "the proofs must share the failed read")
				})
			})
		}

		t.Run("returns the cause of its context for a proof whose context ends while it waits", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := &gatedTiles{m: &memTiles{data: largeTiles.data}, gates: []chan struct{}{make(chan struct{})}}
				p := newProver(t, g, everyTile)
				results := proveAll(t, p, []uint64{proofIndex})
				synctest.Wait()
				cause := errors.New("caller gone")
				ctx, cancel := context.WithCancelCause(t.Context())
				waiter := make(chan error, 1)
				go func() {
					_, err := p.InclusionProof(ctx, largeSize, siblings(1)[0], nil)
					waiter <- err
				}()
				synctest.Wait()
				cancel(cause)
				expect.ErrorIs(t, <-waiter, cause, "the waiting proof must return the cause of its context")
				close(g.gates[0])
				checkAll(t, h, large, results)
			})
		})

		t.Run("reads a wider tile beside the running read of a narrower one", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := &gatedTiles{
					m:     &memTiles{data: smallTiles.data},
					gates: []chan struct{}{make(chan struct{}), make(chan struct{})},
				}
				p := newProver(t, g, everyTile)
				narrow := proveAt(t, p, 300, 299)
				synctest.Wait()
				wide := proveAt(t, p, 400, 399)
				synctest.Wait()
				expect.Equal(t, g.calls.Load(), int64(2), "the wider tile must have a read of its own")
				joined := proveAt(t, p, 300, 290)
				synctest.Wait()
				expect.Equal(t, g.calls.Load(), int64(2), "a narrower tile must join the wider read")

				// The wider read completes first. The proof at 300 that joined it
				// waits for the tile of level 1 that the narrower read reads.
				close(g.gates[1])
				synctest.Wait()
				close(g.gates[0])
				for _, r := range []result{<-narrow, <-wide, <-joined} {
					assert.NoError(t, r.err, "the proof of leaf "+strconv.FormatUint(r.index, 10)+" must succeed")
					want, err := tlog.InclusionProof(h, small[:r.size], r.index, nil)
					assert.NoError(t, err, "InclusionProof in memory must succeed")
					expect.Equal(t, r.proof, want, "the proof of leaf "+strconv.FormatUint(r.index, 10))
				}
			})
		})

		t.Run("joins the running read of a wider tile after the narrower read of its position completes",
			func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					g := &gatedTiles{
						m:     &memTiles{data: smallTiles.data},
						gates: []chan struct{}{make(chan struct{}), make(chan struct{})},
					}
					p := newProver(t, g, everyTile)
					narrow := proveAt(t, p, 300, 299)
					synctest.Wait()
					wide := proveAt(t, p, 400, 399)
					synctest.Wait()
					close(g.gates[0])
					expect.NoError(t, (<-narrow).err, "the proof at 300 must succeed")
					late := proveAt(t, p, 400, 398)
					synctest.Wait()
					expect.Equal(t, g.calls.Load(), int64(2), "the later proof at 400 must join the wider read")
					close(g.gates[1])
					expect.NoError(t, (<-wide).err, "the proof at 400 must succeed")
					r := <-late
					assert.NoError(t, r.err, "the later proof at 400 must succeed")
					want, err := tlog.InclusionProof(h, small[:400], 398, nil)
					assert.NoError(t, err, "InclusionProof in memory must succeed")
					expect.Equal(t, r.proof, want, "the later proof at 400 must be the proof in memory")
				})
			})

		t.Run("keeps the wider node tile when a narrower read completes after it", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := &gatedTiles{
					m:     &memTiles{data: smallTiles.data},
					gates: []chan struct{}{make(chan struct{}), make(chan struct{})},
				}
				p := newProver(t, g, everyTile)
				narrow := proveAt(t, p, 300, 299)
				synctest.Wait()
				wide := proveAt(t, p, 400, 399)
				synctest.Wait()

				// The proof at 400 adds the wider node tile, and then waits for
				// the tile of level 1 that the proof at 300 reads.
				close(g.gates[1])
				synctest.Wait()
				close(g.gates[0])
				expect.NoError(t, (<-wide).err, "the proof at 400 must succeed")
				expect.NoError(t, (<-narrow).err, "the proof at 300 must succeed")
				_, err := p.InclusionProof(t.Context(), 400, 398, nil)
				assert.NoError(t, err, "the proof at 400 must succeed again")
				assert.Equal(t, g.calls.Load(), int64(2), "the node tile of 400 must remain in the cache")
			})
		})
	})

	t.Run("ConsistencyProof", func(t *testing.T) {
		t.Parallel()

		for _, c := range caches {
			t.Run("returns the proof of ProveConsistency for the sampled pairs of trees from "+c.name,
				func(t *testing.T) {
					t.Parallel()
					p := newProver(t, smallTiles, c.tiles)
					for size := uint64(1); size <= smallSize; size++ {
						for _, old := range []uint64{1, size / 2, size - 1, size} {
							if old == 0 {
								continue
							}
							got, err := p.ConsistencyProof(t.Context(), old, size, nil)
							assert.NoError(t, err, "ConsistencyProof must succeed")
							want, err := tlog.ConsistencyProof(h, small[:size], old, nil)
							assert.NoError(t, err, "ConsistencyProof in memory must succeed")
							expect.Equal(t, got, want, "the proof from "+strconv.FormatUint(old, 10)+" to "+
								strconv.FormatUint(size, 10))
						}
					}
				})
		}

		t.Run("returns the proof of ProveConsistency for old sizes of a tree of 70000", func(t *testing.T) {
			t.Parallel()
			p := newProver(t, largeTiles, everyTile)
			for _, old := range []uint64{1, 255, 256, 65535, 65536, largeSize - 1} {
				got, err := p.ConsistencyProof(t.Context(), old, largeSize, nil)
				assert.NoError(t, err, "ConsistencyProof must succeed")
				want, err := tlog.ConsistencyProof(h, large, old, nil)
				assert.NoError(t, err, "ConsistencyProof in memory must succeed")
				expect.Equal(t, got, want, "the proof from "+strconv.FormatUint(old, 10)+" to 70000")
			}
		})

		t.Run("returns dst unchanged for equal sizes without a read", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: smallTiles.data}
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := newProver(t, m, everyTile).ConsistencyProof(t.Context(), 9, 9, dst)
			assert.NoError(t, err, "ConsistencyProof must succeed")
			expect.Equal(t, got, dst, "the proof between equal trees must be empty")
			expect.Equal(t, m.calls, 0, "the proof between equal trees must not read")
		})

		tests := []struct {
			name string
			old  uint64
		}{
			{name: "returns ErrRange with dst unchanged for an old size of zero", old: 0},
			{name: "returns ErrRange with dst unchanged for an old size past the new size", old: 10},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				dst := []crypto.Digest{h.Hash(nil)}
				got, err := newProver(t, smallTiles, everyTile).ConsistencyProof(t.Context(), tt.old, 9, dst)
				expect.ErrorIs(t, err, tlog.ErrRange, "the old size must be ErrRange")
				expect.Equal(t, got, dst, "dst must be returned unchanged")
			})
		}

		t.Run("returns the error of the reader", func(t *testing.T) {
			t.Parallel()
			_, err := newProver(t, &memTiles{data: map[tlog.Tile][]byte{}}, everyTile).ConsistencyProof(
				t.Context(), 2, 3, nil)
			assert.ErrorIs(t, err, errAbsent, "the error of the reader must be returned")
		})
	})

	t.Run("Root", func(t *testing.T) {
		t.Parallel()

		for _, c := range caches {
			t.Run("returns the root of TreeRoot for every tree up to 600 from "+c.name, func(t *testing.T) {
				t.Parallel()
				p := newProver(t, smallTiles, c.tiles)
				for size := uint64(1); size <= smallSize; size++ {
					got, err := p.Root(t.Context(), size)
					assert.NoError(t, err, "Root must succeed")
					expect.Equal(t, got, tlog.Root(h, small[:size]), "the root of "+strconv.FormatUint(size, 10))
				}
			})
		}

		t.Run("returns the root of TreeRoot for a tree of 70000", func(t *testing.T) {
			t.Parallel()
			got, err := newProver(t, largeTiles, everyTile).Root(t.Context(), largeSize)
			assert.NoError(t, err, "Root must succeed")
			assert.Equal(t, got, tlog.Root(h, large), "the root of 70000 leaves")
		})

		t.Run("returns HASH() for an empty tree without a read", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: smallTiles.data}
			got, err := newProver(t, m, everyTile).Root(t.Context(), 0)
			assert.NoError(t, err, "Root must succeed")
			expect.Equal(t, got, h.Hash(nil), "the empty root must be HASH()")
			expect.Equal(t, m.calls, 0, "the empty root must not read")
		})

		t.Run("returns the zero digest and the error of the reader", func(t *testing.T) {
			t.Parallel()
			got, err := newProver(t, &memTiles{data: map[tlog.Tile][]byte{}}, everyTile).Root(t.Context(), 3)
			expect.ErrorIs(t, err, errAbsent, "the error of the reader must be returned")
			expect.Equal(t, got, crypto.Digest{}, "the root of a failed read must be the zero digest")
		})
	})
}

// TestProverAllocs checks the allocation contract of each method over a
// reader that does not allocate. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestProverAllocs(t *testing.T) {
	_, _, _, largeTiles := storedTree(t)
	ctx := t.Context()
	dst := make([]crypto.Digest, 0, vectorBound)

	p := newProver(t, largeTiles, everyTile)
	for i := range uint64(largeSize/fullWidth + 1) {
		_, err := p.InclusionProof(ctx, largeSize, i*fullWidth, nil)
		assert.NoError(t, err, "the proofs that fill the cache must succeed")
	}

	t.Run("InclusionProof", func(t *testing.T) {
		t.Run("does not allocate for a proof whose node tiles the cache contains", func(t *testing.T) {
			var got []crypto.Digest
			expect.MaxAllocs(t, func() { got, _ = p.InclusionProof(ctx, largeSize, proofIndex, dst[:0]) }, 0,
				"InclusionProof must not allocate into a dst with room")
			assert.NotEmpty(t, got, "the test must measure a proof")
		})

		t.Run("allocates three objects for each tile that it reads", func(t *testing.T) {
			m := &memTiles{data: largeTiles.data}
			_, err := tlog.ProveInclusion(ctx, coresha256.New(), m, largeSize, proofIndex, nil)
			assert.NoError(t, err, "the proof that counts the tiles must succeed")
			ceiling := uint64(3 * len(m.last))

			var got []crypto.Digest
			expect.MaxAllocsWithSetup(t, func() *tlog.Prover { return newProver(t, largeTiles, everyTile) },
				func(fresh *tlog.Prover) { got, _ = fresh.InclusionProof(ctx, largeSize, proofIndex, dst[:0]) },
				ceiling, "InclusionProof must allocate a node tile, its buffer and a cache entry for each tile")
			assert.NotEmpty(t, got, "the test must measure a proof")
		})
	})

	t.Run("ConsistencyProof", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = p.ConsistencyProof(ctx, proofOld, largeSize, dst[:0]) }, 0,
			"ConsistencyProof must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})

	t.Run("Root", func(t *testing.T) {
		var (
			got crypto.Digest
			err error
		)
		expect.MaxAllocs(t, func() { got, err = p.Root(ctx, largeSize) }, 0, "Root must not allocate")
		assert.NoError(t, err, "the test must measure a root")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
	})
}

// BenchmarkProver reports the cost of each method over a reader that does
// not allocate, and fails above the allocations that their contracts state.
func BenchmarkProver(b *testing.B) {
	_, _, _, largeTiles := storedTree(b)
	dst := make([]crypto.Digest, 0, vectorBound)

	p := newProver(b, largeTiles, everyTile)
	for i := range uint64(largeSize/fullWidth + 1) {
		_, err := p.InclusionProof(b.Context(), largeSize, i*fullWidth, nil)
		assert.NoError(b, err, "the proofs that fill the cache must succeed")
	}

	b.Run("InclusionProof", func(b *testing.B) {
		b.Run("cached", func(b *testing.B) {
			ctx := b.Context()

			var got []crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, _ = p.InclusionProof(ctx, largeSize, proofIndex, dst[:0])
			}

			assert.NotEmpty(b, got, "the benchmark must measure a proof")
		})

		b.Run("read", func(b *testing.B) {
			ctx := b.Context()
			m := &memTiles{data: largeTiles.data}
			_, err := tlog.ProveInclusion(ctx, coresha256.New(), m, largeSize, proofIndex, nil)
			assert.NoError(b, err, "the proof that counts the tiles must succeed")

			var got []crypto.Digest

			// Each iteration reads into node tiles of 16 KiB, which start
			// collections, and a collection empties the pool of the states of
			// a proof, so the ceiling has one object of headroom.
			c := bench.Start(b).MaxAllocs(uint64(3*len(m.last) + 1))
			defer c.End()

			for c.Loop() {
				var fresh *tlog.Prover
				c.Excluding(func() { fresh = newProver(b, largeTiles, everyTile) })
				got, _ = fresh.InclusionProof(ctx, largeSize, proofIndex, dst[:0])
			}

			assert.NotEmpty(b, got, "the benchmark must measure a proof")
		})

		b.Run("parallel", func(b *testing.B) {
			ctx := b.Context()

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				out := make([]crypto.Digest, 0, vectorBound)
				for pb.Next() {
					out, _ = p.InclusionProof(ctx, largeSize, proofIndex, out[:0])
				}
			})
		})
	})

	b.Run("ConsistencyProof", func(b *testing.B) {
		ctx := b.Context()

		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = p.ConsistencyProof(ctx, proofOld, largeSize, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
	})

	b.Run("Root", func(b *testing.B) {
		ctx := b.Context()

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = p.Root(ctx, largeSize)
		}

		assert.NoError(b, err, "the benchmark must measure a root")
	})
}

// result is the outcome of one proof that a case runs on a goroutine of its
// own.
type result struct {
	err         error
	proof       []crypto.Digest
	size, index uint64
}

// publishedTree returns the sizes at which a tree of 70000 leaves was
// published, and the tiles of every published size: every size up to 600,
// and then a size every 997 leaves, and 70000.
func publishedTree(tb testing.TB) (sizes []uint64, tiles *memTiles) {
	tb.Helper()

	all := leaves(largeSize)
	tiles = &memTiles{data: map[tlog.Tile][]byte{}}

	batches := slices.Repeat([]int{1}, smallSize)
	for size := smallSize; size < largeSize; size += publishedStep {
		batches = append(batches, min(publishedStep, largeSize-size))
	}

	b, err := tlog.NewBuilder(tb.Context(), coresha256.New(), 0, tiles)
	assert.NoError(tb, err, "NewBuilder must succeed")
	grow(tb, b, tiles, all, batches...)

	size := uint64(0)
	for _, n := range batches {
		size += uint64(n)
		sizes = append(sizes, size)
	}

	return sizes, tiles
}

// newProver returns a Prover over r whose cache has room for tiles node
// tiles of SHA-256.
func newProver(tb assert.TB, r tlog.TileReader, tiles int64) *tlog.Prover {
	tb.Helper()

	p, err := tlog.NewProver(tlog.ProverConfig{
		Hasher:   coresha256.New(),
		Tiles:    r,
		Capacity: tiles * nodeTileBytes,
	})
	assert.NoError(tb, err, "NewProver must accept the configuration")

	return p
}

// proveAt starts the inclusion proof of index in the tree of size leaves on
// a goroutine of its own, and returns the channel of its result.
func proveAt(tb testing.TB, p *tlog.Prover, size, index uint64) <-chan result {
	tb.Helper()

	out := make(chan result, 1)
	go func() {
		proof, err := p.InclusionProof(tb.Context(), size, index, nil)
		out <- result{err: err, proof: proof, size: size, index: index}
	}()

	return out
}

// proveAll starts the inclusion proof of each of indexes in the tree of
// 70000 leaves, each on a goroutine of its own, and returns the channel of
// their results.
func proveAll(tb testing.TB, p *tlog.Prover, indexes []uint64) <-chan result {
	tb.Helper()

	out := make(chan result, len(indexes))
	for _, i := range indexes {
		go func() {
			proof, err := p.InclusionProof(tb.Context(), largeSize, i, nil)
			out <- result{err: err, proof: proof, size: largeSize, index: i}
		}()
	}

	return out
}

// checkAll receives every result of results, and requires each to be the
// proof in memory of its leaf of the tree of 70000 leaves.
func checkAll(tb testing.TB, h crypto.Hasher, large []crypto.Digest, results <-chan result) {
	tb.Helper()

	for range cap(results) {
		r := <-results
		assert.NoError(tb, r.err, "the proof of leaf "+strconv.FormatUint(r.index, 10)+" must succeed")
		want, err := tlog.InclusionProof(h, large, r.index, nil)
		assert.NoError(tb, err, "InclusionProof in memory must succeed")
		expect.Equal(tb, r.proof, want, "the proof of leaf "+strconv.FormatUint(r.index, 10))
	}
}

// siblings returns n leaves of the tile of level 0 of proofIndex other than
// proofIndex, so their proofs need the tiles of its proof.
func siblings(n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = proofIndex + 1 + uint64(i)
	}

	return out
}
