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

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
	"go.thesmos.sh/core/tlog"
)

// errAbsent is the error memTiles returns for a tile it does not have.
var errAbsent = errs.WithClass(errors.New("tlog_test: no such tile"), errs.NotFound)

// memTiles is a TileReader over tiles in memory. It counts its calls
// and records the tiles of the last one.
type memTiles struct {
	data  map[tlog.Tile][]byte
	err   error
	last  []tlog.Tile
	calls int
	mu    sync.Mutex
}

func newMemTiles() *memTiles { return &memTiles{data: map[tlog.Tile][]byte{}} }

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

// grow integrates leaves into b in batches of the given sizes, commits
// each and stores its tiles in m.
func grow(tb testing.TB, b *tlog.Builder, m *memTiles, all []crypto.Digest, batches ...int) {
	tb.Helper()

	var u tlog.Update
	for _, n := range batches {
		start := b.Size()
		testkit.NoError(tb, b.Integrate(all[start:start+uint64(n)], &u), "Integrate must succeed")
		m.store(&u)
		testkit.NoError(tb, b.Commit(&u), "Commit must succeed")
	}
}

// ones returns n batches of one leaf.
func ones(n int) []int { return slices.Repeat([]int{1}, n) }

// storedTree returns the leaves of a tree of 600 leaves integrated one
// at a time, so the tiles of every size up to 600 are stored, and the
// leaves of a tree of 70,000 leaves integrated at once, with their
// tiles.
func storedTree(tb testing.TB) (
	small []crypto.Digest, smallTiles *memTiles, large []crypto.Digest, largeTiles *memTiles,
) {
	tb.Helper()

	h := coresha256.New()
	small, large = leaves(600), leaves(70000)
	smallTiles, largeTiles = newMemTiles(), newMemTiles()

	b, err := tlog.NewBuilder(tb.Context(), h, 0, smallTiles)
	testkit.NoError(tb, err, "NewBuilder must succeed")
	grow(tb, b, smallTiles, small, ones(600)...)

	b, err = tlog.NewBuilder(tb.Context(), h, 0, largeTiles)
	testkit.NoError(tb, err, "NewBuilder must succeed")
	grow(tb, b, largeTiles, large, 70000)

	return small, smallTiles, large, largeTiles
}

// sampleSizes are tree sizes around the tile boundaries.
var sampleSizes = []uint64{1, 2, 3, 7, 255, 256, 257, 511, 512, 513, 600}

// tilesPerLevel returns the number of tiles at each level of tiles.
func tilesPerLevel(tiles []tlog.Tile) map[uint8]int {
	per := map[uint8]int{}
	for _, t := range tiles {
		per[t.Level]++
	}

	return per
}

// brokenBodies is a blob store whose bodies fail on Read or on Close.
type brokenBodies struct {
	*memory.Store

	readErr, closeErr error
}

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

func (b brokenBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}

	return b.ReadCloser.Read(p) //nolint:wrapcheck // the test double passes the error through
}

func (b brokenBody) Close() error {
	_ = b.ReadCloser.Close()

	return b.closeErr
}

func TestBlobTiles(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	s := memory.New(fake.New(time.Unix(0, 0).UTC()))
	b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
	testkit.NoError(t, err, "NewBuilder must succeed")

	var u tlog.Update
	testkit.NoError(t, b.Integrate(leaves(600), &u), "Integrate must succeed")

	var (
		tiles []tlog.Tile
		want  [][]byte
	)
	for tile, data := range u.Tiles() {
		_, err := blob.PutBytes(t.Context(), s, "log/"+tile.Path(), data, blob.PutOptions{})
		testkit.NoError(t, err, "PutBytes must succeed")
		tiles = append(tiles, tile)
		want = append(want, bytes.Clone(data))
	}

	t.Run("ReadTiles/appends each tile read from its path under the prefix", func(t *testing.T) {
		t.Parallel()

		dst := make([][]byte, len(tiles))
		dst[0] = []byte("kept")
		testkit.NoError(t, tlog.BlobTiles(s, "log/", 2).ReadTiles(t.Context(), tiles, dst), "ReadTiles must succeed")

		testkit.Equal(t, dst[0], append([]byte("kept"), want[0]...), "the first tile must follow the kept bytes")
		testkit.Equal(t, dst[1:], want[1:], "every other tile must be read whole")
	})

	t.Run("ReadTiles/classifies a missing tile as NotFound", func(t *testing.T) {
		t.Parallel()

		err := tlog.BlobTiles(s, "other/", 2).ReadTiles(t.Context(), tiles[:1], make([][]byte, 1))
		testkit.Equal(t, errs.Classify(err), errs.NotFound, "a missing tile must classify as NotFound")
	})

	t.Run("ReadTiles/refuses fewer buffers than tiles", func(t *testing.T) {
		t.Parallel()

		err := tlog.BlobTiles(s, "log/", 2).ReadTiles(t.Context(), tiles, make([][]byte, 1))
		testkit.ErrorIs(t, err, tlog.ErrRange, "fewer buffers than tiles must be ErrRange")
	})

	t.Run("ReadTiles/refuses a limit below one", func(t *testing.T) {
		t.Parallel()

		err := tlog.BlobTiles(s, "log/", 0).ReadTiles(t.Context(), tiles, make([][]byte, len(tiles)))
		testkit.ErrorIs(t, err, task.ErrLimit, "a limit of zero must be task.ErrLimit")
	})

	t.Run("ReadTiles/returns the error of a body that fails to read or close", func(t *testing.T) {
		t.Parallel()

		readErr, closeErr := testkit.TestError("read failed"), testkit.TestError("close failed")
		for want, bodies := range map[error]brokenBodies{
			readErr:  {Store: s, readErr: readErr},
			closeErr: {Store: s, closeErr: closeErr},
		} {
			err := tlog.BlobTiles(bodies, "log/", 2).ReadTiles(t.Context(), tiles[:1], make([][]byte, 1))
			testkit.ErrorIs(t, err, want, "the body's error must be returned")
		}
	})
}

func TestTreeRoot(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)

	t.Run("equals the root in memory", func(t *testing.T) {
		t.Parallel()

		for _, size := range sampleSizes {
			root, err := tlog.TreeRoot(t.Context(), h, smallTiles, size)
			testkit.NoError(t, err, "TreeRoot must succeed")
			testkit.Equal(t, root, tlog.Root(h, small[:size]), "the root of "+strconv.FormatUint(size, 10)+" leaves")
		}

		root, err := tlog.TreeRoot(t.Context(), h, largeTiles, 70000)
		testkit.NoError(t, err, "TreeRoot must succeed")
		testkit.Equal(t, root, tlog.Root(h, large), "the root of 70,000 leaves")
	})

	t.Run("reads nothing for an empty tree", func(t *testing.T) {
		t.Parallel()

		root, err := tlog.TreeRoot(t.Context(), h, nil, 0)
		testkit.NoError(t, err, "TreeRoot must succeed")
		testkit.Equal(t, root, h.Hash(nil), "the empty root must be HASH()")
	})

	t.Run("refuses tile data of the wrong length", func(t *testing.T) {
		t.Parallel()

		short := newMemTiles()
		short.data[tlog.Tile{Width: 3}] = make([]byte, 2*h.Hash(nil).Size())
		_, err := tlog.TreeRoot(t.Context(), h, short, 3)
		testkit.ErrorIs(t, err, tlog.ErrTileSize, "a short tile must be ErrTileSize")
	})

	t.Run("returns the reader's error", func(t *testing.T) {
		t.Parallel()

		_, err := tlog.TreeRoot(t.Context(), h, newMemTiles(), 3)
		testkit.ErrorIs(t, err, errAbsent, "the reader's error must be returned")
	})
}

func TestProveInclusion(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)

	t.Run("equals the proof in memory for every leaf of the sampled trees", func(t *testing.T) {
		t.Parallel()

		for _, size := range sampleSizes {
			for i := range size {
				got, err := tlog.ProveInclusion(t.Context(), h, smallTiles, size, i, nil)
				testkit.NoError(t, err, "ProveInclusion must succeed")
				want, err := tlog.InclusionProof(h, small[:size], i, nil)
				testkit.NoError(t, err, "InclusionProof must succeed")
				testkit.True(t, slices.Equal(got, want),
					"the proof of leaf "+strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(size, 10)+
						" must equal the proof in memory")
			}
		}
	})

	t.Run("reads at most two tiles per level in one call", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		m.data = largeTiles.data
		for _, i := range []uint64{0, 1, 255, 256, 65535, 65536, 69999} {
			calls := m.calls
			got, err := tlog.ProveInclusion(t.Context(), h, m, 70000, i, nil)
			testkit.NoError(t, err, "ProveInclusion must succeed")
			want, err := tlog.InclusionProof(h, large, i, nil)
			testkit.NoError(t, err, "InclusionProof must succeed")

			testkit.Equal(t, got, want, "leaf "+strconv.FormatUint(i, 10)+" of 70,000")
			testkit.Equal(t, m.calls, calls+1, "the proof must read its tiles in one call")
			for level, n := range tilesPerLevel(m.last) {
				testkit.True(t, n <= 2, "the proof must read at most two tiles at level "+strconv.Itoa(int(level)))
			}
		}
	})

	t.Run("reads nothing for a tree of one leaf", func(t *testing.T) {
		t.Parallel()

		got, err := tlog.ProveInclusion(t.Context(), h, nil, 1, 0, nil)
		testkit.NoError(t, err, "ProveInclusion must succeed")
		testkit.Len(t, got, 0, "the proof in a tree of one leaf must be empty")
	})

	t.Run("refuses an index at or past the size", func(t *testing.T) {
		t.Parallel()

		dst := []crypto.Digest{h.Hash(nil)}
		got, err := tlog.ProveInclusion(t.Context(), h, smallTiles, 5, 5, dst)
		testkit.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
		testkit.Equal(t, got, dst, "dst must be returned unchanged")
	})
}

func TestProveConsistency(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	small, smallTiles, large, largeTiles := storedTree(t)

	t.Run("equals the proof in memory for the sampled pairs of trees", func(t *testing.T) {
		t.Parallel()

		for _, size := range sampleSizes {
			for _, old := range []uint64{1, 2, 3, size / 2, size - 1, size} {
				if old == 0 || old > size {
					continue
				}
				got, err := tlog.ProveConsistency(t.Context(), h, smallTiles, old, size, nil)
				testkit.NoError(t, err, "ProveConsistency must succeed")
				want, err := tlog.ConsistencyProof(h, small[:size], old, nil)
				testkit.NoError(t, err, "ConsistencyProof must succeed")
				testkit.True(t, slices.Equal(got, want),
					"the proof from "+strconv.FormatUint(old, 10)+" to "+strconv.FormatUint(size, 10)+
						" must equal the proof in memory")
			}
		}
	})

	t.Run("reads at most two tiles per level in one call", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		m.data = largeTiles.data
		for _, old := range []uint64{1, 255, 256, 65535, 65536, 69999} {
			calls := m.calls
			got, err := tlog.ProveConsistency(t.Context(), h, m, old, 70000, nil)
			testkit.NoError(t, err, "ProveConsistency must succeed")
			want, err := tlog.ConsistencyProof(h, large, old, nil)
			testkit.NoError(t, err, "ConsistencyProof must succeed")

			testkit.Equal(t, got, want, strconv.FormatUint(old, 10)+" to 70,000")
			testkit.Equal(t, m.calls, calls+1, "the proof must read its tiles in one call")
			for level, n := range tilesPerLevel(m.last) {
				testkit.True(t, n <= 2, "the proof must read at most two tiles at level "+strconv.Itoa(int(level)))
			}
		}
	})

	t.Run("reads nothing for equal sizes", func(t *testing.T) {
		t.Parallel()

		got, err := tlog.ProveConsistency(t.Context(), h, nil, 9, 9, nil)
		testkit.NoError(t, err, "ProveConsistency must succeed")
		testkit.Len(t, got, 0, "the proof between equal trees must be empty")
	})

	t.Run("refuses an old size of zero or past the new size", func(t *testing.T) {
		t.Parallel()

		for _, old := range []uint64{0, 10} {
			got, err := tlog.ProveConsistency(t.Context(), h, smallTiles, old, 9, nil)
			testkit.ErrorIs(t, err, tlog.ErrRange, "old size "+strconv.FormatUint(old, 10)+" must be ErrRange")
			testkit.Len(t, got, 0, "dst must be returned unchanged")
		}
	})
}

// TestProveZeroAlloc enforces the allocation contract of the prove
// functions: with a reader that allocates nothing and a dst with room,
// a proof allocates nothing. testing.AllocsPerRun reads a process-wide
// counter, so this test does not call t.Parallel, and it skips in a
// race build.
//
//nolint:paralleltest // see comment above
func TestProveZeroAlloc(t *testing.T) {
	skipUnderRace(t)

	h := coresha256.New()
	_, _, _, largeTiles := storedTree(t)
	ctx := t.Context()
	dst := make([]crypto.Digest, 0, 64)

	for name, fn := range map[string]func(){
		"TreeRoot":         func() { _, _ = tlog.TreeRoot(ctx, h, largeTiles, 70000) },
		"ProveInclusion":   func() { _, _ = tlog.ProveInclusion(ctx, h, largeTiles, 70000, 12345, dst[:0]) },
		"ProveConsistency": func() { _, _ = tlog.ProveConsistency(ctx, h, largeTiles, 12345, 70000, dst[:0]) },
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(20, fn), float64(0), name+" must not allocate")
		})
	}
}

func BenchmarkProveInclusion(b *testing.B) {
	h := coresha256.New()
	_, _, _, largeTiles := storedTree(b)
	dst := make([]crypto.Digest, 0, 64)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = tlog.ProveInclusion(b.Context(), h, largeTiles, 70000, 12345, dst[:0])
	}
}

func BenchmarkProveConsistency(b *testing.B) {
	h := coresha256.New()
	_, _, _, largeTiles := storedTree(b)
	dst := make([]crypto.Digest, 0, 64)
	b.ReportAllocs()

	for b.Loop() {
		_, _ = tlog.ProveConsistency(b.Context(), h, largeTiles, 12345, 70000, dst[:0])
	}
}
