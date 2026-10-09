// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/tlog"
)

// The fixture values of the cases of the verifiers.
const (
	// countedBound is the largest tree size of the cases that count the
	// hashes of a verification.
	countedBound = 16

	// benchSize is the size of the tree of the benchmarks of the verifiers,
	// and benchIndex the leaf of their proofs.
	benchSize  = 1 << 16
	benchIndex = 40000
)

// sumCounter is SHA-256 whose streams count the digests that they return.
type sumCounter struct {
	coresha256.Hasher

	sums *int
}

// NewStream returns a SHA-256 stream that counts its sums in c.
func (c sumCounter) NewStream() crypto.Stream {
	return countedStream{Stream: c.Hasher.NewStream(), sums: c.sums}
}

// countedStream is a crypto.Stream that counts its calls to Sum.
type countedStream struct {
	crypto.Stream

	sums *int
}

// Sum counts the call and returns the sum of the embedded Stream.
func (s countedStream) Sum() crypto.Digest {
	*s.sums++

	return s.Stream.Sum()
}

func TestVerify(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(vectorBound)
	extra := h.Hash([]byte("not in the tree"))

	t.Run("VerifyInclusion", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for every proof of every leaf of every tree up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= vectorBound; n++ {
				root := tlog.Root(h, all[:n])
				for i := range n {
					p, err := tlog.InclusionProof(h, all[:n], i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					expect.NoError(t, tlog.VerifyInclusion(h, i, n, all[i], root, p),
						"leaf "+strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(n, 10)+" must verify")
				}
			}
		})

		t.Run("returns ErrProof for every proof one hash away from a proof", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= vectorBound; n++ {
				root := tlog.Root(h, all[:n])
				for i := range n {
					p, err := tlog.InclusionProof(h, all[:n], i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					for _, m := range mutations(h, p) {
						expect.ErrorIs(t, tlog.VerifyInclusion(h, i, n, all[i], root, m), tlog.ErrProof,
							"a mutation of the proof of leaf "+strconv.FormatUint(i, 10)+" of "+
								strconv.FormatUint(n, 10)+" must fail")
					}
				}
			}
		})

		t.Run("returns ErrProof without hashing a hash past the path", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= countedBound; n++ {
				root := tlog.Root(h, all[:n])
				for i := range n {
					p, err := tlog.InclusionProof(h, all[:n], i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")

					var path, longer int
					assert.NoError(t, tlog.VerifyInclusion(sumCounter{sums: &path}, i, n, all[i], root, p),
						"the proof must verify")
					assert.ErrorIs(t, tlog.VerifyInclusion(sumCounter{sums: &longer}, i, n, all[i], root,
						append(p, extra)), tlog.ErrProof, "a longer proof must fail")
					expect.Equal(t, longer, path, "VerifyInclusion must not hash the hash past the path")
				}
			}
		})

		root := tlog.Root(h, all[:7])
		p, err := tlog.InclusionProof(h, all[:7], 3, nil)
		assert.NoError(t, err, "InclusionProof must succeed")

		wider := slices.Clone(p)
		wider[1] = coresha512.New384().Hash([]byte("wider"))

		tests := []struct {
			name  string
			leaf  crypto.Digest
			root  crypto.Digest
			proof []crypto.Digest
			index uint64
		}{
			{
				name:  "returns ErrProof for another leaf",
				index: 3, leaf: all[4], root: root, proof: p,
			},
			{
				name:  "returns ErrProof for another index",
				index: 2, leaf: all[3], root: root, proof: p,
			},
			{
				name:  "returns ErrProof for another root",
				index: 3, leaf: all[3], root: all[0], proof: p,
			},
			{
				name:  "returns ErrProof for a proof hash of another size",
				index: 3, leaf: all[3], root: root, proof: wider,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, tlog.VerifyInclusion(h, tt.index, 7, tt.leaf, tt.root, tt.proof), tlog.ErrProof,
					"VerifyInclusion must refuse the proof")
			})
		}

		t.Run("returns ErrProof for an interior node in place of a leaf", func(t *testing.T) {
			t.Parallel()
			node := tlog.NodeHash(h, all[0], all[1])
			sibling := tlog.NodeHash(h, all[2], all[3])
			assert.ErrorIs(t, tlog.VerifyInclusion(h, 0, 4, node, tlog.Root(h, all[:4]), []crypto.Digest{sibling}),
				tlog.ErrProof, "a node one level above the leaves must not verify as a leaf")
		})

		t.Run("returns ErrRange for an index at the size", func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, tlog.VerifyInclusion(h, 7, 7, all[0], all[0], nil), tlog.ErrRange,
				"an index past the tree must be ErrRange")
		})
	})

	t.Run("VerifyConsistency", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for every proof between every pair of trees up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= vectorBound; n++ {
				root := tlog.Root(h, all[:n])
				for m := uint64(1); m < n; m++ {
					p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
					assert.NoError(t, err, "ConsistencyProof must succeed")
					expect.NoError(t, tlog.VerifyConsistency(h, m, n, tlog.Root(h, all[:m]), root, p),
						"the proof from "+strconv.FormatUint(m, 10)+" to "+strconv.FormatUint(n, 10)+" must verify")
				}
			}
		})

		t.Run("returns ErrProof for every proof one hash away from a proof", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= vectorBound; n++ {
				root := tlog.Root(h, all[:n])
				for m := uint64(1); m < n; m++ {
					old := tlog.Root(h, all[:m])
					p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
					assert.NoError(t, err, "ConsistencyProof must succeed")
					for _, bad := range mutations(h, p) {
						expect.ErrorIs(t, tlog.VerifyConsistency(h, m, n, old, root, bad), tlog.ErrProof,
							"a mutation of the proof from "+strconv.FormatUint(m, 10)+" to "+
								strconv.FormatUint(n, 10)+" must fail")
					}
				}
			}
		})

		t.Run("returns ErrProof without hashing a hash past the proof", func(t *testing.T) {
			t.Parallel()
			for n := uint64(2); n <= countedBound; n++ {
				root := tlog.Root(h, all[:n])
				for m := uint64(1); m < n; m++ {
					old := tlog.Root(h, all[:m])
					p, err := tlog.ConsistencyProof(h, all[:n], m, nil)
					assert.NoError(t, err, "ConsistencyProof must succeed")

					var proof, longer int
					assert.NoError(t, tlog.VerifyConsistency(sumCounter{sums: &proof}, m, n, old, root, p),
						"the proof must verify")
					assert.ErrorIs(t, tlog.VerifyConsistency(sumCounter{sums: &longer}, m, n, old, root,
						append(p, extra)), tlog.ErrProof, "a longer proof must fail")
					expect.Equal(t, longer, proof, "VerifyConsistency must not hash the hash past the proof")
				}
			}
		})

		old, root := tlog.Root(h, all[:3]), tlog.Root(h, all[:7])
		p, err := tlog.ConsistencyProof(h, all[:7], 3, nil)
		assert.NoError(t, err, "ConsistencyProof must succeed")
		five := tlog.Root(h, all[:5])

		tests := []struct {
			name             string
			oldRoot, newRoot crypto.Digest
			proof            []crypto.Digest
			oldSize, newSize uint64
		}{
			{
				name:    "returns ErrProof for another old root",
				oldSize: 3, newSize: 7, oldRoot: all[0], newRoot: root, proof: p,
			},
			{
				name:    "returns ErrProof for another new root",
				oldSize: 3, newSize: 7, oldRoot: old, newRoot: all[0], proof: p,
			},
			{
				name:    "returns ErrProof for an empty proof",
				oldSize: 3, newSize: 7, oldRoot: old, newRoot: root,
			},
			{
				name:    "returns ErrProof for equal sizes with different roots",
				oldSize: 5, newSize: 5, oldRoot: five, newRoot: all[0],
			},
			{
				name:    "returns ErrProof for equal sizes with a proof",
				oldSize: 5, newSize: 5, oldRoot: five, newRoot: five, proof: []crypto.Digest{five},
			},
			{
				name:    "returns ErrProof for a proof that ends below the root of the new tree",
				oldSize: 3, newSize: 4, oldRoot: extra, newRoot: extra, proof: []crypto.Digest{extra},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, tlog.VerifyConsistency(h, tt.oldSize, tt.newSize, tt.oldRoot, tt.newRoot, tt.proof),
					tlog.ErrProof, "VerifyConsistency must refuse the proof")
			})
		}

		t.Run("returns nil for equal trees without a proof", func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, tlog.VerifyConsistency(h, 5, 5, five, five, nil), "equal trees must verify")
		})

		ranges := []struct {
			name             string
			oldSize, newSize uint64
		}{
			{name: "returns ErrRange for an old size of zero", oldSize: 0, newSize: 5},
			{name: "returns ErrRange for an old size past the new size", oldSize: 6, newSize: 5},
		}
		for _, tt := range ranges {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, tlog.VerifyConsistency(h, tt.oldSize, tt.newSize, all[0], all[0], nil), tlog.ErrRange,
					"the old size must be ErrRange")
			})
		}
	})
}

// TestVerifyAllocs checks the allocation contract of each function.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestVerifyAllocs(t *testing.T) {
	h := coresha256.New()
	all := leaves(measuredSize)
	root, old := tlog.Root(h, all), tlog.Root(h, all[:measuredOld])

	inclusion, err := tlog.InclusionProof(h, all, measuredIndex, nil)
	assert.NoError(t, err, "InclusionProof must succeed")
	consistency, err := tlog.ConsistencyProof(h, all, measuredOld, nil)
	assert.NoError(t, err, "ConsistencyProof must succeed")

	t.Run("VerifyInclusion", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			err = tlog.VerifyInclusion(h, measuredIndex, measuredSize, all[measuredIndex], root, inclusion)
		}, 0, "VerifyInclusion must not allocate")
		assert.NoError(t, err, "the test must measure a proof that verifies")
	})

	t.Run("VerifyConsistency", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			err = tlog.VerifyConsistency(h, measuredOld, measuredSize, old, root, consistency)
		}, 0, "VerifyConsistency must not allocate")
		assert.NoError(t, err, "the test must measure a proof that verifies")
	})
}

// BenchmarkVerify reports the cost of each function, and fails above the
// allocations that their contracts state.
func BenchmarkVerify(b *testing.B) {
	h := coresha256.New()
	all := leaves(benchSize)
	root, old := tlog.Root(h, all), tlog.Root(h, all[:benchIndex])

	inclusion, err := tlog.InclusionProof(h, all, benchIndex, nil)
	assert.NoError(b, err, "InclusionProof must succeed")
	consistency, err := tlog.ConsistencyProof(h, all, benchIndex, nil)
	assert.NoError(b, err, "ConsistencyProof must succeed")

	b.Run("VerifyInclusion", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = tlog.VerifyInclusion(h, benchIndex, benchSize, all[benchIndex], root, inclusion)
		}

		assert.NoError(b, err, "the benchmark must measure a proof that verifies")
	})

	b.Run("VerifyConsistency", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = tlog.VerifyConsistency(h, benchIndex, benchSize, old, root, consistency)
		}

		assert.NoError(b, err, "the benchmark must measure a proof that verifies")
	})
}

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
