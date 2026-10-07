// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of the trees in memory.
const (
	// vectorBound is the largest tree size the recorded proofs cover.
	vectorBound = 64

	// measuredSize is the size of the tree of the allocation tests and the
	// benchmarks, and measuredIndex and measuredOld the leaf and the old
	// size of their proofs.
	measuredSize  = 1000
	measuredIndex = 517
	measuredOld   = 333
)

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

func TestTree(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("Root", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded root of every tree up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			for n := range uint64(vectorBound + 1) {
				expect.Equal(t, tlog.Root(h, all[:n]), v.roots[n], "the root of "+strconv.FormatUint(n, 10)+" leaves")
			}
		})

		t.Run("returns the hash of no input for no leaves", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tlog.Root(h, nil), h.Hash(nil), "the root of no leaves must be HASH()")
		})
	})

	t.Run("InclusionProof", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded proof of every leaf of every tree up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			var proofs [][]crypto.Digest
			for n := 1; n <= vectorBound; n++ {
				for i := range uint64(n) {
					p, err := tlog.InclusionProof(h, all[:n], i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					proofs = append(proofs, p)
				}
			}
			assert.Equal(t, digestOf(t, proofs), v.inclusion, "the proofs must match the recorded ones")
		})

		t.Run("appends the proof to dst", func(t *testing.T) {
			t.Parallel()
			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := tlog.InclusionProof(h, all[:5], 2, prefix)
			assert.NoError(t, err, "InclusionProof must succeed")
			assert.Length(t, p, 4, "the proof must follow the kept digest")
			assert.Equal(t, p[0], prefix[0], "dst must keep its contents")
		})

		t.Run("returns ErrRange with dst unchanged for an index at the size", func(t *testing.T) {
			t.Parallel()
			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := tlog.InclusionProof(h, all[:5], 5, prefix)
			expect.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
			expect.Equal(t, p, prefix, "dst must be returned unchanged")
		})
	})

	t.Run("ConsistencyProof", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded proof of every pair of trees up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			var proofs [][]crypto.Digest
			for n := 1; n <= vectorBound; n++ {
				for m := uint64(1); m <= uint64(n); m++ {
					p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
					assert.NoError(t, err, "ConsistencyProof must succeed")
					proofs = append(proofs, p)
				}
			}
			assert.Equal(t, digestOf(t, proofs), v.consistency, "the proofs must match the recorded ones")
		})

		t.Run("returns an empty proof for equal sizes", func(t *testing.T) {
			t.Parallel()
			p, err := tlog.ConsistencyProof(h, all[:9], 9, nil)
			assert.NoError(t, err, "ConsistencyProof must succeed")
			assert.Empty(t, p, "the proof between equal trees must be empty")
		})

		tests := []struct {
			name string
			old  uint64
		}{
			{name: "returns ErrRange with dst unchanged for an old size of zero", old: 0},
			{name: "returns ErrRange with dst unchanged for an old size past the tree", old: 10},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				prefix := []crypto.Digest{h.Hash([]byte("kept"))}
				p, err := tlog.ConsistencyProof(h, all[:9], tt.old, prefix)
				expect.ErrorIs(t, err, tlog.ErrRange, "the old size must be ErrRange")
				expect.Equal(t, p, prefix, "dst must be returned unchanged")
			})
		}
	})
}

// TestTreeAllocs checks the allocation contract of each function.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestTreeAllocs(t *testing.T) {
	h := coresha256.New()
	all := leaves(measuredSize)
	dst := make([]crypto.Digest, 0, vectorBound)

	t.Run("Root", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() { got = tlog.Root(h, all) }, 0, "Root must not allocate")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
	})

	t.Run("InclusionProof", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = tlog.InclusionProof(h, all, measuredIndex, dst[:0]) }, 0,
			"InclusionProof must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})

	t.Run("ConsistencyProof", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = tlog.ConsistencyProof(h, all, measuredOld, dst[:0]) }, 0,
			"ConsistencyProof must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})
}

// BenchmarkTree reports the cost of each function, and fails above the
// allocations that their contracts state.
func BenchmarkTree(b *testing.B) {
	h := coresha256.New()
	all := leaves(measuredSize)
	dst := make([]crypto.Digest, 0, vectorBound)

	b.Run("Root", func(b *testing.B) {
		var got crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tlog.Root(h, all)
		}

		assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a root")
	})

	b.Run("InclusionProof", func(b *testing.B) {
		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.InclusionProof(h, all, measuredIndex, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
	})

	b.Run("ConsistencyProof", func(b *testing.B) {
		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.ConsistencyProof(h, all, measuredOld, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
	})
}

// loadVectors reads testdata/vectors.txt.
func loadVectors(tb testing.TB) vectors {
	tb.Helper()

	f, err := os.Open("testdata/vectors.txt")
	assert.NoError(tb, err, "the vectors must open")
	defer f.Close()

	digest := func(s string) crypto.Digest {
		b, err := hex.DecodeString(s)
		assert.NoError(tb, err, "a vector must be hex")
		d, err := crypto.DigestFromBytes(b)
		assert.NoError(tb, err, "a vector must be a digest")

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
		assert.NoError(tb, err, "a vector size must be a number")

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
	assert.NoError(tb, lines.Err(), "the vectors must read")

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
	assert.NoError(tb, err, "a SHA-256 sum must be a digest")

	return d
}
