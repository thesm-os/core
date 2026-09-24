// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"slices"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/tlog"
)

// mutations returns every proof that differs from proof by one changed
// hash, one removed hash or one added hash.
func mutations(h crypto.Hasher, proof []crypto.Digest) [][]crypto.Digest {
	out := make([][]crypto.Digest, 0, 3*len(proof)+1)
	extra := h.Hash([]byte("not in the tree"))

	for i := range proof {
		changed := slices.Clone(proof)
		changed[i] = extra
		out = append(out, changed, slices.Delete(slices.Clone(proof), i, i+1))
	}
	for i := range len(proof) + 1 {
		out = append(out, slices.Insert(slices.Clone(proof), i, extra))
	}

	return out
}

// TestVerifyInclusion checks every proof of every leaf of every tree up
// to 64 leaves, and every proof one hash away from it.
func TestVerifyInclusion(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("accepts every proof and rejects every mutation of it", func(t *testing.T) {
		t.Parallel()

		for n := uint64(1); n <= vectorBound; n++ {
			root := tlog.Root(h, all[:n])
			for i := range n {
				p, err := tlog.InclusionProof(h, all[:n], i, nil)
				testkit.NoError(t, err, "InclusionProof must succeed")

				name := strconv.FormatUint(i, 10) + " of " + strconv.FormatUint(n, 10)
				testkit.NoError(t, tlog.VerifyInclusion(h, i, n, all[i], root, p), "leaf "+name+" must verify")
				for _, m := range mutations(h, p) {
					testkit.ErrorIs(t, tlog.VerifyInclusion(h, i, n, all[i], root, m), tlog.ErrProof,
						"a mutation of the proof of leaf "+name+" must fail")
				}
			}
		}
	})

	t.Run("rejects another leaf, index or root", func(t *testing.T) {
		t.Parallel()

		root := tlog.Root(h, all[:7])
		p, err := tlog.InclusionProof(h, all[:7], 3, nil)
		testkit.NoError(t, err, "InclusionProof must succeed")

		testkit.ErrorIs(t, tlog.VerifyInclusion(h, 3, 7, all[4], root, p), tlog.ErrProof, "another leaf must fail")
		testkit.ErrorIs(t, tlog.VerifyInclusion(h, 2, 7, all[3], root, p), tlog.ErrProof, "another index must fail")
		testkit.ErrorIs(t, tlog.VerifyInclusion(h, 3, 7, all[3], all[0], p), tlog.ErrProof, "another root must fail")
	})

	t.Run("refuses an index at or past the size", func(t *testing.T) {
		t.Parallel()
		testkit.ErrorIs(t, tlog.VerifyInclusion(h, 7, 7, all[0], all[0], nil), tlog.ErrRange,
			"an index past the tree must be ErrRange")
	})
}

// TestVerifyConsistency checks every proof between every pair of trees
// up to 64 leaves, and every proof one hash away from it.
func TestVerifyConsistency(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("accepts every proof and rejects every mutation of it", func(t *testing.T) {
		t.Parallel()

		for n := uint64(1); n <= vectorBound; n++ {
			root := tlog.Root(h, all[:n])
			for m := uint64(1); m < n; m++ {
				old := tlog.Root(h, all[:m])
				p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
				testkit.NoError(t, err, "ConsistencyProof must succeed")

				name := strconv.FormatUint(m, 10) + " to " + strconv.FormatUint(n, 10)
				testkit.NoError(t, tlog.VerifyConsistency(h, m, n, old, root, p), "the proof from "+name+" must verify")
				for _, bad := range mutations(h, p) {
					testkit.ErrorIs(t, tlog.VerifyConsistency(h, m, n, old, root, bad), tlog.ErrProof,
						"a mutation of the proof from "+name+" must fail")
				}
			}
		}
	})

	t.Run("rejects another old or new root", func(t *testing.T) {
		t.Parallel()

		old, root := tlog.Root(h, all[:3]), tlog.Root(h, all[:7])
		p, err := tlog.ConsistencyProof(h, all[:7], 3, nil)
		testkit.NoError(t, err, "ConsistencyProof must succeed")

		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 3, 7, all[0], root, p), tlog.ErrProof,
			"another old root must fail")
		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 3, 7, old, all[0], p), tlog.ErrProof,
			"another new root must fail")
		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 3, 7, old, root, nil), tlog.ErrProof,
			"an empty proof must fail")
	})

	t.Run("accepts equal sizes only with an empty proof and equal roots", func(t *testing.T) {
		t.Parallel()

		root := tlog.Root(h, all[:5])
		testkit.NoError(t, tlog.VerifyConsistency(h, 5, 5, root, root, nil), "equal trees must verify")
		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 5, 5, root, all[0], nil), tlog.ErrProof,
			"equal sizes with different roots must fail")
		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 5, 5, root, root, []crypto.Digest{root}), tlog.ErrProof,
			"equal sizes with a non-empty proof must fail")
	})

	t.Run("refuses an old size of zero or past the new size", func(t *testing.T) {
		t.Parallel()

		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 0, 5, all[0], all[0], nil), tlog.ErrRange,
			"an old size of zero must be ErrRange")
		testkit.ErrorIs(t, tlog.VerifyConsistency(h, 6, 5, all[0], all[0], nil), tlog.ErrRange,
			"an old size past the new size must be ErrRange")
	})
}

// TestVerifyZeroAlloc enforces the allocation contracts of the
// verifiers. testing.AllocsPerRun reads a process-wide counter, so this
// test does not call t.Parallel, and it skips in a race build.
//
//nolint:paralleltest // see comment above
func TestVerifyZeroAlloc(t *testing.T) {
	skipUnderRace(t)

	h := coresha256.New()
	all := leaves(1000)
	root, old := tlog.Root(h, all), tlog.Root(h, all[:333])

	inclusion, err := tlog.InclusionProof(h, all, 517, nil)
	testkit.NoError(t, err, "InclusionProof must succeed")
	consistency, err := tlog.ConsistencyProof(h, all, 333, nil)
	testkit.NoError(t, err, "ConsistencyProof must succeed")

	for name, fn := range map[string]func(){
		"VerifyInclusion":   func() { _ = tlog.VerifyInclusion(h, 517, 1000, all[517], root, inclusion) },
		"VerifyConsistency": func() { _ = tlog.VerifyConsistency(h, 333, 1000, old, root, consistency) },
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, fn), float64(0), name+" must not allocate")
		})
	}
}

func BenchmarkVerifyInclusion(b *testing.B) {
	h := coresha256.New()
	all := leaves(1 << 16)
	root := tlog.Root(h, all)
	p, err := tlog.InclusionProof(h, all, 40000, nil)
	testkit.NoError(b, err, "InclusionProof must succeed")
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.VerifyInclusion(h, 40000, 1<<16, all[40000], root, p)
	}
}

func BenchmarkVerifyConsistency(b *testing.B) {
	h := coresha256.New()
	all := leaves(1 << 16)
	root, old := tlog.Root(h, all), tlog.Root(h, all[:40000])
	p, err := tlog.ConsistencyProof(h, all, 40000, nil)
	testkit.NoError(b, err, "ConsistencyProof must succeed")
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.VerifyConsistency(h, 40000, 1<<16, old, root, p)
	}
}
