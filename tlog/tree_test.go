// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/tlog"
)

// vectorBound is the largest tree size the recorded proofs cover.
const vectorBound = 64

// vectors are the values recorded from golang.org/x/mod/sumdb/tlog in
// testdata/vectors.txt.
type vectors struct {
	roots       map[uint64]crypto.Digest
	tiles       map[uint64]tileVector
	inclusion   crypto.Digest
	consistency crypto.Digest
}

// tileVector is the digest of every tile of one tree, and its root.
type tileVector struct {
	digest crypto.Digest
	root   crypto.Digest
}

// loadVectors reads testdata/vectors.txt.
func loadVectors(tb testing.TB) vectors {
	tb.Helper()

	f, err := os.Open("testdata/vectors.txt")
	testkit.NoError(tb, err, "the vectors must open")
	defer f.Close()

	digest := func(s string) crypto.Digest {
		b, err := hex.DecodeString(s)
		testkit.NoError(tb, err, "a vector must be hex")
		d, err := crypto.DigestFromBytes(b)
		testkit.NoError(tb, err, "a vector must be a digest")

		return d
	}

	v := vectors{roots: map[uint64]crypto.Digest{}, tiles: map[uint64]tileVector{}}
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) == 0 || fields[0] == "#" {
			continue
		}

		size, err := strconv.ParseUint(fields[1], 10, 64)
		testkit.NoError(tb, err, "a vector size must be a number")

		switch fields[0] {
		case "root":
			v.roots[size] = digest(fields[2])
		case "inclusion":
			v.inclusion = digest(fields[2])
		case "consistency":
			v.consistency = digest(fields[2])
		case "tiles":
			v.tiles[size] = tileVector{digest: digest(fields[2]), root: digest(fields[3])}
		}
	}
	testkit.NoError(tb, lines.Err(), "the vectors must read")

	return v
}

// leaves returns the leaf hashes of the entries "entry 0" to
// "entry n-1", as the vectors define them.
func leaves(n int) []crypto.Digest {
	h := coresha256.New()
	out := make([]crypto.Digest, n)
	for i := range out {
		out[i] = tlog.LeafHash(h, []byte("entry "+strconv.Itoa(i)))
	}

	return out
}

// skipUnderRace skips an allocation test in a binary built with the
// race detector. The detector makes sync.Pool drop values at random, so
// a pooled path allocates by chance.
func skipUnderRace(t *testing.T) {
	t.Helper()

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "-race" && s.Value == "true" {
			t.Skip("sync.Pool drops values at random under the race detector")
		}
	}
}

// digestOf returns the SHA-256 digest of every hash of proofs, in order.
func digestOf(tb testing.TB, proofs [][]crypto.Digest) crypto.Digest {
	tb.Helper()

	s := sha256.New()
	for _, p := range proofs {
		for _, d := range p {
			s.Write(d.Bytes())
		}
	}

	d, err := crypto.DigestFromBytes(s.Sum(nil))
	testkit.NoError(tb, err, "a SHA-256 sum must be a digest")

	return d
}

func TestRoot(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("matches the recorded root of every tree up to 64 leaves", func(t *testing.T) {
		t.Parallel()

		for n := range uint64(vectorBound + 1) {
			testkit.Equal(t, tlog.Root(h, all[:n]), v.roots[n], "the root of "+strconv.FormatUint(n, 10)+" leaves")
		}
	})

	t.Run("is the hash of no input for no leaves", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, tlog.Root(h, nil), h.Hash(nil), "the empty root must be HASH()")
	})
}

func TestInclusionProof(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("matches the recorded proof of every leaf of every tree up to 64 leaves", func(t *testing.T) {
		t.Parallel()

		var proofs [][]crypto.Digest
		for n := 1; n <= vectorBound; n++ {
			for i := range uint64(n) {
				p, err := tlog.InclusionProof(h, all[:n], i, nil)
				testkit.NoError(t, err, "InclusionProof must succeed")
				proofs = append(proofs, p)
			}
		}
		testkit.Equal(t, digestOf(t, proofs), v.inclusion, "the proofs must match the recorded ones")
	})

	t.Run("appends to dst", func(t *testing.T) {
		t.Parallel()

		prefix := []crypto.Digest{h.Hash([]byte("kept"))}
		p, err := tlog.InclusionProof(h, all[:5], 2, prefix)
		testkit.NoError(t, err, "InclusionProof must succeed")
		testkit.Len(t, p, 4, "the proof must follow the kept digest")
		testkit.Equal(t, p[0], prefix[0], "dst must keep its contents")
	})

	t.Run("refuses an index at or past the size", func(t *testing.T) {
		t.Parallel()

		p, err := tlog.InclusionProof(h, all[:5], 5, nil)
		testkit.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
		testkit.Len(t, p, 0, "dst must be returned unchanged")
	})
}

func TestConsistencyProof(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("matches the recorded proof of every pair of trees up to 64 leaves", func(t *testing.T) {
		t.Parallel()

		var proofs [][]crypto.Digest
		for n := 1; n <= vectorBound; n++ {
			for m := uint64(1); m <= uint64(n); m++ {
				p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
				testkit.NoError(t, err, "ConsistencyProof must succeed")
				proofs = append(proofs, p)
			}
		}
		testkit.Equal(t, digestOf(t, proofs), v.consistency, "the proofs must match the recorded ones")
	})

	t.Run("is empty for equal sizes", func(t *testing.T) {
		t.Parallel()

		p, err := tlog.ConsistencyProof(h, all[:9], 9, nil)
		testkit.NoError(t, err, "ConsistencyProof must succeed")
		testkit.Len(t, p, 0, "the proof between equal trees must be empty")
	})

	t.Run("refuses an old size of zero or past the tree", func(t *testing.T) {
		t.Parallel()

		for _, old := range []uint64{0, 10} {
			p, err := tlog.ConsistencyProof(h, all[:9], old, nil)
			testkit.ErrorIs(t, err, tlog.ErrRange, "old size "+strconv.FormatUint(old, 10)+" must be ErrRange")
			testkit.Len(t, p, 0, "dst must be returned unchanged")
		}
	})
}

// TestRootZeroAlloc enforces the allocation contract of Root.
// testing.AllocsPerRun reads a process-wide counter, so this test does
// not call t.Parallel, and it skips in a race build.
//
//nolint:paralleltest // see comment above
func TestRootZeroAlloc(t *testing.T) {
	skipUnderRace(t)

	h := coresha256.New()
	all := leaves(1000)

	t.Run("Root", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(20, func() { _ = tlog.Root(h, all) }), float64(0),
			"Root must not allocate")
	})
}

func BenchmarkRoot(b *testing.B) {
	h := coresha256.New()
	all := leaves(4096)
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.Root(h, all)
	}
}
