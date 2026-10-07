// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of the readers.
const (
	// smallSize is the size of the tree that storedTree integrates one leaf
	// at a time, and largeSize the size of the tree that it integrates at
	// once.
	smallSize = 600
	largeSize = 70000

	// proofIndex and proofOld are the leaf and the old size of the proofs of
	// the allocation tests and the benchmarks.
	proofIndex = 12345
	proofOld   = 12345

	// readLimit is the number of tiles that the BlobTiles of the cases reads
	// at once.
	readLimit = 2
)

// errAbsent is the error memTiles returns for a tile it does not have.
var errAbsent = errs.WithClass(errors.New("tlog_test: no such tile"), errs.NotFound)

// sampleSizes are tree sizes around the tile boundaries.
var sampleSizes = []uint64{1, 2, 3, 7, 255, 256, 257, 511, 512, 513, smallSize}

// memTiles is a TileReader over tiles in memory. It counts its calls and
// records the tiles of the last one.
type memTiles struct {
	data  map[tlog.Tile][]byte
	err   error
	last  []tlog.Tile
	calls int
	mu    sync.Mutex
}

// ReadTiles appends the data of each tile to its buffer, and returns
// errAbsent for a tile that m does not have.
func (m *memTiles) ReadTiles(_ context.Context, tiles []tlog.Tile, dst [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++
	m.last = append(m.last[:0], tiles...)
	if m.err != nil {
		return m.err
	}

	for i, t := range tiles {
		d, ok := m.data[t]
		if !ok {
			return errAbsent
		}
		dst[i] = append(dst[i], d...)
	}

	return nil
}

// store keeps every tile of u.
func (m *memTiles) store(u *tlog.Update) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for t, d := range u.Tiles() {
		m.data[t] = bytes.Clone(d)
	}
}

// brokenBodies is a blob store whose bodies fail on Read or on Close.
type brokenBodies struct {
	*memory.Store

	readErr, closeErr error
}

// Get returns the body of key, which fails as s states.
func (s brokenBodies) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	rc, info, err := s.Store.Get(ctx, key)
	if err != nil {
		return nil, info, err //nolint:wrapcheck // the test double passes the error through
	}

	return brokenBody{rc, s.readErr, s.closeErr}, info, nil
}

// brokenBody fails its Read with readErr when set, and its Close with
// closeErr.
type brokenBody struct {
	io.ReadCloser

	readErr, closeErr error
}

// Read returns readErr when set, and the bytes of the body otherwise.
func (b brokenBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}

	return b.ReadCloser.Read(p) //nolint:wrapcheck // the test double passes the error through
}

// Close closes the body and returns closeErr.
func (b brokenBody) Close() error {
	_ = b.ReadCloser.Close()

	return b.closeErr
}

func TestReader(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)

	t.Run("BlobTiles", func(t *testing.T) {
		t.Parallel()

		s := memory.New(fake.New(time.Unix(0, 0).UTC()))
		b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		assert.NoError(t, err, "NewBuilder must succeed")

		var u tlog.Update
		assert.NoError(t, b.Integrate(small, &u), "Integrate must succeed")

		var (
			tiles []tlog.Tile
			want  [][]byte
		)
		for tile, data := range u.Tiles() {
			_, err := blob.PutBytes(t.Context(), s, "log/"+tile.Path(), data, blob.PutOptions{})
			assert.NoError(t, err, "PutBytes must succeed")
			tiles = append(tiles, tile)
			want = append(want, bytes.Clone(data))
		}

		t.Run("ReadTiles", func(t *testing.T) {
			t.Parallel()

			t.Run("appends each tile read from its path under the prefix", func(t *testing.T) {
				t.Parallel()
				dst := make([][]byte, len(tiles))
				dst[0] = []byte("kept")
				assert.NoError(t, tlog.BlobTiles(s, "log/", readLimit).ReadTiles(t.Context(), tiles, dst),
					"ReadTiles must succeed")
				expect.Equal(t, dst[0], append([]byte("kept"), want[0]...), "the first tile must follow the kept bytes")
				expect.Equal(t, dst[1:], want[1:], "every other tile must be read whole")
			})

			t.Run("returns an error classified NotFound for a missing tile", func(t *testing.T) {
				t.Parallel()
				err := tlog.BlobTiles(s, "other/", readLimit).ReadTiles(t.Context(), tiles[:1], make([][]byte, 1))
				assert.Equal(t, errs.Classify(err), errs.NotFound, "a missing tile must classify as NotFound")
			})

			t.Run("returns ErrRange for fewer buffers than tiles", func(t *testing.T) {
				t.Parallel()
				err := tlog.BlobTiles(s, "log/", readLimit).ReadTiles(t.Context(), tiles, make([][]byte, 1))
				assert.ErrorIs(t, err, tlog.ErrRange, "fewer buffers than tiles must be ErrRange")
			})

			t.Run("returns task.ErrLimit for a limit below one", func(t *testing.T) {
				t.Parallel()
				err := tlog.BlobTiles(s, "log/", 0).ReadTiles(t.Context(), tiles, make([][]byte, len(tiles)))
				assert.ErrorIs(t, err, task.ErrLimit, "a limit of zero must be task.ErrLimit")
			})

			t.Run("appends an object of width hashes of MaxDigestSize bytes", func(t *testing.T) {
				t.Parallel()
				wide := tlog.Tile{Width: 255}
				data := make([]byte, int(wide.Width)*crypto.MaxDigestSize)
				_, err := blob.PutBytes(t.Context(), s, "wide/"+wide.Path(), data, blob.PutOptions{})
				assert.NoError(t, err, "PutBytes must succeed")

				dst := make([][]byte, 1)
				assert.NoError(t, tlog.BlobTiles(s, "wide/", readLimit).ReadTiles(t.Context(), []tlog.Tile{wide}, dst),
					"ReadTiles must read a tile of the largest digest")
				assert.Length(t, dst[0], len(data), "the tile must be read whole")
			})

			t.Run("returns an error classified Invalid for an object too large for its tile", func(t *testing.T) {
				t.Parallel()
				wide := tlog.Tile{Width: 255}
				data := make([]byte, int(wide.Width)*crypto.MaxDigestSize+1)
				_, err := blob.PutBytes(t.Context(), s, "past/"+wide.Path(), data, blob.PutOptions{})
				assert.NoError(t, err, "PutBytes must succeed")

				dst := make([][]byte, 1)
				err = tlog.BlobTiles(s, "past/", readLimit).ReadTiles(t.Context(), []tlog.Tile{wide}, dst)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "an object too large for its tile must be Invalid")
			})

			readErr, closeErr := errors.New("read failed"), errors.New("close failed")
			tests := []struct {
				want   error
				name   string
				bodies brokenBodies
			}{
				{
					name:   "returns the error of a body that fails to read",
					bodies: brokenBodies{Store: s, readErr: readErr},
					want:   readErr,
				},
				{
					name:   "returns the error of a body that fails to close",
					bodies: brokenBodies{Store: s, closeErr: closeErr},
					want:   closeErr,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					err := tlog.BlobTiles(tt.bodies, "log/", readLimit).ReadTiles(t.Context(), tiles[:1],
						make([][]byte, 1))
					assert.ErrorIs(t, err, tt.want, "ReadTiles must return the error of the body")
				})
			}
		})
	})

	t.Run("TreeRoot", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the root in memory of each sampled size", func(t *testing.T) {
			t.Parallel()
			for _, size := range sampleSizes {
				root, err := tlog.TreeRoot(t.Context(), h, smallTiles, size)
				assert.NoError(t, err, "TreeRoot must succeed")
				expect.Equal(t, root, tlog.Root(h, small[:size]), "the root of "+strconv.FormatUint(size, 10)+" leaves")
			}
		})

		t.Run("returns the root in memory of a tree of 70000 leaves", func(t *testing.T) {
			t.Parallel()
			root, err := tlog.TreeRoot(t.Context(), h, largeTiles, largeSize)
			assert.NoError(t, err, "TreeRoot must succeed")
			assert.Equal(t, root, tlog.Root(h, large), "the root of 70000 leaves")
		})

		t.Run("returns HASH() for an empty tree without a read", func(t *testing.T) {
			t.Parallel()
			root, err := tlog.TreeRoot(t.Context(), h, nil, 0)
			assert.NoError(t, err, "TreeRoot must succeed")
			assert.Equal(t, root, h.Hash(nil), "the empty root must be HASH()")
		})

		t.Run("returns ErrTileSize for tile data of the wrong length", func(t *testing.T) {
			t.Parallel()
			short := &memTiles{data: map[tlog.Tile][]byte{{Width: 3}: make([]byte, 2*h.Hash(nil).Size())}}
			_, err := tlog.TreeRoot(t.Context(), h, short, 3)
			assert.ErrorIs(t, err, tlog.ErrTileSize, "a short tile must be ErrTileSize")
		})

		t.Run("returns the error of the reader", func(t *testing.T) {
			t.Parallel()
			_, err := tlog.TreeRoot(t.Context(), h, &memTiles{data: map[tlog.Tile][]byte{}}, 3)
			assert.ErrorIs(t, err, errAbsent, "the error of the reader must be returned")
		})
	})

	t.Run("ProveInclusion", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the proof in memory for every leaf of the sampled trees", func(t *testing.T) {
			t.Parallel()
			for _, size := range sampleSizes {
				for i := range size {
					got, err := tlog.ProveInclusion(t.Context(), h, smallTiles, size, i, nil)
					assert.NoError(t, err, "ProveInclusion must succeed")
					want, err := tlog.InclusionProof(h, small[:size], i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					expect.Equal(t, got, want, "the proof of leaf "+strconv.FormatUint(i, 10)+" of "+
						strconv.FormatUint(size, 10))
				}
			}
		})

		t.Run("returns the proof in memory for leaves of a tree of 70000", func(t *testing.T) {
			t.Parallel()
			for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, largeSize - 1} {
				got, err := tlog.ProveInclusion(t.Context(), h, largeTiles, largeSize, i, nil)
				assert.NoError(t, err, "ProveInclusion must succeed")
				want, err := tlog.InclusionProof(h, large, i, nil)
				assert.NoError(t, err, "InclusionProof must succeed")
				expect.Equal(t, got, want, "the proof of leaf "+strconv.FormatUint(i, 10)+" of 70000")
			}
		})

		t.Run("reads its tiles in one call", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, largeSize - 1} {
				calls := m.calls
				_, err := tlog.ProveInclusion(t.Context(), h, m, largeSize, i, nil)
				assert.NoError(t, err, "ProveInclusion must succeed")
				expect.Equal(t, m.calls, calls+1, "the proof of leaf "+strconv.FormatUint(i, 10)+" must read once")
			}
		})

		t.Run("reads at most two tiles per level", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, largeSize - 1} {
				_, err := tlog.ProveInclusion(t.Context(), h, m, largeSize, i, nil)
				assert.NoError(t, err, "ProveInclusion must succeed")
				for level, n := range tilesPerLevel(m.last) {
					expect.InRange(t, n, 1, 2, "the proof of leaf "+strconv.FormatUint(i, 10)+
						" must read at most two tiles at level "+strconv.Itoa(int(level)))
				}
			}
		})

		t.Run("returns dst unchanged for a tree of one leaf", func(t *testing.T) {
			t.Parallel()
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := tlog.ProveInclusion(t.Context(), h, nil, 1, 0, dst)
			assert.NoError(t, err, "ProveInclusion must succeed")
			assert.Equal(t, got, dst, "the proof in a tree of one leaf must be empty")
		})

		t.Run("returns ErrRange with dst unchanged for an index at the size", func(t *testing.T) {
			t.Parallel()
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := tlog.ProveInclusion(t.Context(), h, smallTiles, 5, 5, dst)
			expect.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
			expect.Equal(t, got, dst, "dst must be returned unchanged")
		})
	})

	t.Run("ProveConsistency", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the proof in memory for the sampled pairs of trees", func(t *testing.T) {
			t.Parallel()
			for _, size := range sampleSizes {
				for _, old := range []uint64{1, 2, 3, size / 2, size - 1, size} {
					if old == 0 || old > size {
						continue
					}
					got, err := tlog.ProveConsistency(t.Context(), h, smallTiles, old, size, nil)
					assert.NoError(t, err, "ProveConsistency must succeed")
					want, err := tlog.ConsistencyProof(h, small[:size], old, nil)
					assert.NoError(t, err, "ConsistencyProof must succeed")
					expect.Equal(t, got, want, "the proof from "+strconv.FormatUint(old, 10)+" to "+
						strconv.FormatUint(size, 10))
				}
			}
		})

		t.Run("returns the proof in memory for old sizes of a tree of 70000", func(t *testing.T) {
			t.Parallel()
			for _, old := range []uint64{1, 255, 256, 65535, 65536, largeSize - 1} {
				got, err := tlog.ProveConsistency(t.Context(), h, largeTiles, old, largeSize, nil)
				assert.NoError(t, err, "ProveConsistency must succeed")
				want, err := tlog.ConsistencyProof(h, large, old, nil)
				assert.NoError(t, err, "ConsistencyProof must succeed")
				expect.Equal(t, got, want, "the proof from "+strconv.FormatUint(old, 10)+" to 70000")
			}
		})

		t.Run("reads its tiles in one call", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			for _, old := range []uint64{1, 255, 256, 65535, 65536, largeSize - 1} {
				calls := m.calls
				_, err := tlog.ProveConsistency(t.Context(), h, m, old, largeSize, nil)
				assert.NoError(t, err, "ProveConsistency must succeed")
				expect.Equal(t, m.calls, calls+1, "the proof from "+strconv.FormatUint(old, 10)+" must read once")
			}
		})

		t.Run("reads at most two tiles per level", func(t *testing.T) {
			t.Parallel()
			m := &memTiles{data: largeTiles.data}
			for _, old := range []uint64{1, 255, 256, 65535, 65536, largeSize - 1} {
				_, err := tlog.ProveConsistency(t.Context(), h, m, old, largeSize, nil)
				assert.NoError(t, err, "ProveConsistency must succeed")
				for level, n := range tilesPerLevel(m.last) {
					expect.InRange(t, n, 1, 2, "the proof from "+strconv.FormatUint(old, 10)+
						" must read at most two tiles at level "+strconv.Itoa(int(level)))
				}
			}
		})

		t.Run("returns dst unchanged for equal sizes", func(t *testing.T) {
			t.Parallel()
			dst := []crypto.Digest{h.Hash(nil)}
			got, err := tlog.ProveConsistency(t.Context(), h, nil, 9, 9, dst)
			assert.NoError(t, err, "ProveConsistency must succeed")
			assert.Equal(t, got, dst, "the proof between equal trees must be empty")
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
				got, err := tlog.ProveConsistency(t.Context(), h, smallTiles, tt.old, 9, dst)
				expect.ErrorIs(t, err, tlog.ErrRange, "the old size must be ErrRange")
				expect.Equal(t, got, dst, "dst must be returned unchanged")
			})
		}
	})
}

// TestReaderAllocs checks the allocation contract of each prove function
// over a reader that allocates nothing. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestReaderAllocs(t *testing.T) {
	h := coresha256.New()
	_, _, _, largeTiles := storedTree(t)
	ctx := t.Context()
	dst := make([]crypto.Digest, 0, vectorBound)

	t.Run("TreeRoot", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = tlog.TreeRoot(ctx, h, largeTiles, largeSize) }, 0,
			"TreeRoot must not allocate")
		assert.NoError(t, err, "the test must measure a root that TreeRoot reads")
	})

	t.Run("ProveInclusion", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = tlog.ProveInclusion(ctx, h, largeTiles, largeSize, proofIndex, dst[:0]) },
			0, "ProveInclusion must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})

	t.Run("ProveConsistency", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = tlog.ProveConsistency(ctx, h, largeTiles, proofOld, largeSize, dst[:0]) },
			0, "ProveConsistency must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})
}

// BenchmarkReader reports the cost of each prove function over a reader
// that allocates nothing, and fails above the allocations that their
// contracts state.
func BenchmarkReader(b *testing.B) {
	h := coresha256.New()
	_, _, _, largeTiles := storedTree(b)
	dst := make([]crypto.Digest, 0, vectorBound)

	b.Run("TreeRoot", func(b *testing.B) {
		ctx := b.Context()

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = tlog.TreeRoot(ctx, h, largeTiles, largeSize)
		}

		assert.NoError(b, err, "the benchmark must measure a root that TreeRoot reads")
	})

	b.Run("ProveInclusion", func(b *testing.B) {
		ctx := b.Context()

		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.ProveInclusion(ctx, h, largeTiles, largeSize, proofIndex, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
	})

	b.Run("ProveConsistency", func(b *testing.B) {
		ctx := b.Context()

		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.ProveConsistency(ctx, h, largeTiles, proofOld, largeSize, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
	})
}

// grow integrates leaves into b in batches of the given sizes, commits
// each and stores its tiles in m.
func grow(tb assert.TB, b *tlog.Builder, m *memTiles, all []crypto.Digest, batches ...int) {
	tb.Helper()

	var u tlog.Update
	for _, n := range batches {
		start := b.Size()
		assert.NoError(tb, b.Integrate(all[start:start+uint64(n)], &u), "Integrate must succeed")
		m.store(&u)
		assert.NoError(tb, b.Commit(&u), "Commit must succeed")
	}
}

// storedTree returns the leaves of a tree of 600 leaves integrated one at
// a time, so the tiles of every size up to 600 are stored, and the leaves
// of a tree of 70000 leaves integrated at once, with their tiles.
func storedTree(tb testing.TB) (
	small []crypto.Digest, smallTiles *memTiles, large []crypto.Digest, largeTiles *memTiles,
) {
	tb.Helper()

	h := coresha256.New()
	small, large = leaves(smallSize), leaves(largeSize)
	smallTiles = &memTiles{data: map[tlog.Tile][]byte{}}
	largeTiles = &memTiles{data: map[tlog.Tile][]byte{}}

	b, err := tlog.NewBuilder(tb.Context(), h, 0, smallTiles)
	assert.NoError(tb, err, "NewBuilder must succeed")
	grow(tb, b, smallTiles, small, slices.Repeat([]int{1}, smallSize)...)

	b, err = tlog.NewBuilder(tb.Context(), h, 0, largeTiles)
	assert.NoError(tb, err, "NewBuilder must succeed")
	grow(tb, b, largeTiles, large, largeSize)

	return small, smallTiles, large, largeTiles
}

// tilesPerLevel returns the number of tiles at each level of tiles.
func tilesPerLevel(tiles []tlog.Tile) map[uint8]int {
	per := map[uint8]int{}
	for _, t := range tiles {
		per[t.Level]++
	}

	return per
}
