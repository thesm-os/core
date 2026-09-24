// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/tlog"
)

// tileDigest returns the SHA-256 digest of every tile of u, in the
// format of the tiles vectors.
func tileDigest(tb testing.TB, u *tlog.Update) crypto.Digest {
	tb.Helper()

	s := sha256.New()
	for t, data := range u.Tiles() {
		var hdr [11]byte
		hdr[0] = t.Level
		binary.BigEndian.PutUint64(hdr[1:], t.Index)
		binary.BigEndian.PutUint16(hdr[9:], t.Width)
		s.Write(hdr[:])
		s.Write(data)
	}

	d, err := crypto.DigestFromBytes(s.Sum(nil))
	testkit.NoError(tb, err, "a SHA-256 sum must be a digest")

	return d
}

// tilesOf returns the tiles of u and a copy of their data.
func tilesOf(u *tlog.Update) map[tlog.Tile][]byte {
	out := map[tlog.Tile][]byte{}
	for t, data := range u.Tiles() {
		out[t] = slices.Clone(data)
	}

	return out
}

// TestBuilder checks the tiles and roots a Builder produces against the
// vectors, against the tree in memory, and across a restart.
func TestBuilder(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(70000)

	t.Run("Integrate/writes the recorded tiles and root of every recorded tree", func(t *testing.T) {
		t.Parallel()

		for size, want := range v.tiles {
			b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
			testkit.NoError(t, err, "NewBuilder must succeed")

			var u tlog.Update
			testkit.NoError(t, b.Integrate(all[:size], &u), "Integrate must succeed")
			name := strconv.FormatUint(size, 10) + " leaves"
			testkit.Equal(t, tileDigest(t, &u), want.digest, "the tiles of "+name)
			testkit.Equal(t, u.Root(), want.root, "the root of "+name)
			testkit.Equal(t, u.Size(), size, "the size of "+name)

			testkit.NoError(t, b.Commit(&u), "Commit must succeed")
			testkit.Equal(t, b.Root(), want.root, "the committed root of "+name)
			testkit.Equal(t, b.Size(), size, "the committed size of "+name)
		}
	})

	t.Run("Integrate/gives the same tiles and roots in any batching", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		b, err := tlog.NewBuilder(t.Context(), h, 0, m)
		testkit.NoError(t, err, "NewBuilder must succeed")

		rnd := testkit.SeededRand(t)
		var u tlog.Update
		for b.Size() < uint64(len(all)) {
			n := min(uint64(1+rnd.IntN(4000)), uint64(len(all))-b.Size())
			testkit.NoError(t, b.Integrate(all[b.Size():b.Size()+n], &u), "Integrate must succeed")
			m.store(&u)
			testkit.NoError(t, b.Commit(&u), "Commit must succeed")
			testkit.Equal(t, b.Root(), tlog.Root(h, all[:b.Size()]), "the root at "+strconv.FormatUint(b.Size(), 10))
		}

		once, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		testkit.NoError(t, err, "NewBuilder must succeed")
		testkit.NoError(t, once.Integrate(all, &u), "Integrate must succeed")
		for tile, data := range u.Tiles() {
			testkit.Equal(t, m.data[tile], data, "tile "+tile.Path()+" must not depend on the batching")
		}
	})

	t.Run("NewBuilder/resumes from a published size", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		b, err := tlog.NewBuilder(t.Context(), h, 0, m)
		testkit.NoError(t, err, "NewBuilder must succeed")
		grow(t, b, m, all, 700, 300)

		resumed, err := tlog.NewBuilder(t.Context(), h, 1000, m)
		testkit.NoError(t, err, "NewBuilder must succeed")
		testkit.Equal(t, resumed.Root(), b.Root(), "the resumed root must be the published one")

		var u, ru tlog.Update
		testkit.NoError(t, b.Integrate(all[1000:1500], &u), "Integrate must succeed")
		testkit.NoError(t, resumed.Integrate(all[1000:1500], &ru), "Integrate must succeed")
		testkit.Equal(t, tilesOf(&ru), tilesOf(&u), "the resumed Builder must write the same tiles")
		testkit.Equal(t, ru.Root(), u.Root(), "the resumed Builder must reach the same root")
	})

	t.Run("NewBuilder/rewrites the same tiles after a crash before Commit", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		b, err := tlog.NewBuilder(t.Context(), h, 0, m)
		testkit.NoError(t, err, "NewBuilder must succeed")
		grow(t, b, m, all, 1000)

		var lost tlog.Update
		testkit.NoError(t, b.Integrate(all[1000:1300], &lost), "Integrate must succeed")
		m.store(&lost)

		resumed, err := tlog.NewBuilder(t.Context(), h, 1000, m)
		testkit.NoError(t, err, "NewBuilder must succeed")

		var again tlog.Update
		testkit.NoError(t, resumed.Integrate(all[1000:1300], &again), "Integrate must succeed")
		testkit.Equal(t, tilesOf(&again), tilesOf(&lost), "the retry must write the same bytes to the same paths")
	})

	t.Run("NewBuilder/refuses tile data of the wrong length", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		m.data[tlog.Tile{Width: 3}] = make([]byte, 2*h.Hash(nil).Size())
		_, err := tlog.NewBuilder(t.Context(), h, 3, m)
		testkit.ErrorIs(t, err, tlog.ErrTileSize, "a short tile must be ErrTileSize")
	})

	t.Run("NewBuilder/returns the reader's error", func(t *testing.T) {
		t.Parallel()

		_, err := tlog.NewBuilder(t.Context(), h, 3, newMemTiles())
		testkit.ErrorIs(t, err, errAbsent, "the reader's error must be returned")
	})

	t.Run("Integrate/refuses a leaf of the wrong size and leaves the update empty", func(t *testing.T) {
		t.Parallel()

		b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		testkit.NoError(t, err, "NewBuilder must succeed")

		var u tlog.Update
		wrong := coresha512.New384().Hash([]byte("a SHA-384 digest"))
		testkit.ErrorIs(t, b.Integrate([]crypto.Digest{all[0], wrong}, &u), tlog.ErrLeafSize,
			"a SHA-384 leaf in a SHA-256 tree must be ErrLeafSize")
		testkit.Len(t, tilesOf(&u), 0, "the update must have no tiles")
		testkit.Equal(t, u.Size(), uint64(0), "the update must describe the unchanged tree")
	})

	t.Run("Integrate/refuses a tree past 2^64 - 1 leaves", func(t *testing.T) {
		t.Parallel()

		// A tree of 2^64 - 1 leaves has a partial tile of 255 hashes at
		// every level. Their content does not matter here.
		m := newMemTiles()
		for level := range uint8(8) {
			tile := tlog.Tile{Level: level, Index: math.MaxUint64 >> (8 * (uint(level) + 1)), Width: 255}
			m.data[tile] = make([]byte, 255*h.Hash(nil).Size())
		}
		b, err := tlog.NewBuilder(t.Context(), h, math.MaxUint64, m)
		testkit.NoError(t, err, "NewBuilder must succeed")

		var u tlog.Update
		testkit.ErrorIs(t, b.Integrate(all[:1], &u), tlog.ErrRange, "the leaf past 2^64 - 1 must be ErrRange")
	})

	t.Run("Integrate/writes nothing for an empty batch", func(t *testing.T) {
		t.Parallel()

		m := newMemTiles()
		b, err := tlog.NewBuilder(t.Context(), h, 0, m)
		testkit.NoError(t, err, "NewBuilder must succeed")
		grow(t, b, m, all, 10)

		var u tlog.Update
		testkit.NoError(t, b.Integrate(nil, &u), "Integrate must succeed")
		testkit.Len(t, tilesOf(&u), 0, "an empty batch must write no tile")
		testkit.NoError(t, b.Commit(&u), "Commit must succeed")
		testkit.Equal(t, b.Size(), uint64(10), "an empty batch must not change the size")
	})

	t.Run("Commit/refuses an update from another tree", func(t *testing.T) {
		t.Parallel()

		b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		testkit.NoError(t, err, "NewBuilder must succeed")
		other, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		testkit.NoError(t, err, "NewBuilder must succeed")

		var first, second tlog.Update
		testkit.NoError(t, b.Integrate(all[:5], &first), "Integrate must succeed")
		testkit.NoError(t, b.Integrate(all[:7], &second), "Integrate must succeed")

		testkit.ErrorIs(t, other.Commit(&first), tlog.ErrStale, "an update of another Builder must be ErrStale")
		testkit.NoError(t, b.Commit(&first), "Commit must succeed")
		testkit.ErrorIs(t, b.Commit(&second), tlog.ErrStale, "an update of a superseded tree must be ErrStale")
	})

	t.Run("Update/yields no tiles as the zero value", func(t *testing.T) {
		t.Parallel()

		var u tlog.Update
		testkit.Len(t, tilesOf(&u), 0, "the zero Update must yield nothing")
	})

	t.Run("Update/stops yielding tiles when the caller stops", func(t *testing.T) {
		t.Parallel()

		b, err := tlog.NewBuilder(t.Context(), h, 0, nil)
		testkit.NoError(t, err, "NewBuilder must succeed")

		var u tlog.Update
		testkit.NoError(t, b.Integrate(all[:600], &u), "Integrate must succeed")

		n := 0
		for range u.Tiles() {
			n++
			break
		}
		testkit.Equal(t, n, 1, "the iteration must end at the caller's break")
	})
}

// TestIntegrateZeroAlloc enforces the allocation contract of Integrate
// into a reused Update. testing.AllocsPerRun reads a process-wide
// counter, so this test does not call t.Parallel, and it skips in a race
// build.
//
//nolint:paralleltest // see comment above
func TestIntegrateZeroAlloc(t *testing.T) {
	skipUnderRace(t)

	h := coresha256.New()
	all := leaves(1500)
	m := newMemTiles()

	b, err := tlog.NewBuilder(t.Context(), h, 0, m)
	testkit.NoError(t, err, "NewBuilder must succeed")
	grow(t, b, m, all, 700)

	var u tlog.Update

	t.Run("Integrate", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(20, func() { _ = b.Integrate(all[700:1500], &u) }), float64(0),
			"Integrate into a reused Update must not allocate")
	})
}

func BenchmarkIntegrate(b *testing.B) {
	h := coresha256.New()
	const batch = 4096
	all := leaves(batch)

	tb, err := tlog.NewBuilder(b.Context(), h, 0, nil)
	testkit.NoError(b, err, "NewBuilder must succeed")

	var u tlog.Update
	b.ReportAllocs()

	for b.Loop() {
		_ = tb.Integrate(all, &u)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/batch, "ns/leaf")
}
