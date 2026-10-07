// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"encoding/hex"
	"math/bits"
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

// The roles of the tests. nodeRole and otherRole are binary roles, and
// unaryRole is a role that CombineTagged refuses.
const (
	nodeRole  crypto.Role = 0x84
	otherRole crypto.Role = 0x85
	unaryRole crypto.Role = 0x01
)

// The pinned tree has three leaves, each the SHA-256 of pinnedLeafRole and
// one of pinnedPayloads, under node role nodeRole. pinnedPair pins
// CombineTagged(nodeRole, leaf0, leaf1), and pinnedRoot pins
// CombineTagged(nodeRole, pinnedPair, leaf2).
const (
	pinnedLeafRole crypto.Role = 0x01
	pinnedPair                 = "cf9b6328ccb2bd4156e7f22b4417d426fa7a0bce96e74690ecbb9ff95b723715"
	pinnedRoot                 = "fc2f792f6acc7aa9f5ebbc7de9ffa490ff08ef33cbffefb108da46b501d71fba"
)

// The fixture values of the cases of the tagged trees.
const (
	// differentialBound is the largest tree size the differential tests
	// cover. It is past the perfect tree of 128 leaves, so the tests
	// include trees whose right subtree has one or two leaves.
	differentialBound = 130

	// noLeaves is the panic of a tagged tree over no leaves.
	noLeaves = "tlog: a tagged tree over no leaves has no root"
)

// pinnedPayloads are the payloads of the pinned tree's leaves.
var pinnedPayloads = []string{`{"act":"infer","id":1}`, `{"act":"infer","id":2}`, `{"act":"score","id":3}`}

// rfcNodeHasher is SHA-256 whose CombineTagged returns the RFC 9162 node
// hash of its operands under any role. A tagged tree over it is the RFC
// 9162 tree that the recorded vectors describe.
type rfcNodeHasher struct {
	coresha256.Hasher
}

// CombineTagged returns HASH(0x01 || left || right), whatever the role.
func (rfcNodeHasher) CombineTagged(_ crypto.Role, left, right crypto.Digest) crypto.Digest {
	return tlog.NodeHash(coresha256.New(), left, right)
}

// countingHasher is SHA-256 that counts its CombineTagged calls.
type countingHasher struct {
	coresha256.Hasher

	combines int
}

// CombineTagged counts the call and returns SHA-256's CombineTagged.
func (c *countingHasher) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	c.combines++

	return c.Hasher.CombineTagged(r, left, right)
}

func TestTagged(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	h := coresha256.New()
	all := leaves(differentialBound)

	t.Run("TaggedTree", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("hashes each interior node once", func(t *testing.T) {
				t.Parallel()
				for _, n := range []int{1, 2, 3, 100, 129} {
					c := &countingHasher{}
					taggedTree(c, all[:n])
					expect.Equal(t, c.combines, n-1, "a tree of "+strconv.Itoa(n)+" leaves has n - 1 interior nodes")
				}
			})

			t.Run("replaces a larger tree with a smaller one", func(t *testing.T) {
				t.Parallel()
				tree := taggedTree(h, all[:9])
				tree.Reset(h, nodeRole, all[:5])
				expect.Equal(t, tree.Size(), uint64(5), "the tree must have the new leaves")
				expect.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:5]), "the root must be the new tree's")
			})

			t.Run("keeps a copy of the leaves", func(t *testing.T) {
				t.Parallel()
				own := slices.Clone(all[:6])
				tree := taggedTree(h, own)
				own[0] = all[7]
				assert.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:6]),
					"a change to the leaves of the caller must not change the tree")
			})

			t.Run("panics on a unary node role over one leaf", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				assert.Panics(t, func() { tree.Reset(h, unaryRole, all[:1]) }, "a unary role must panic")
			})

			t.Run("panics over no leaves", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				got := assert.Panics(t, func() { tree.Reset(h, nodeRole, nil) }, "no leaves must panic")
				assert.Equal(t, got, any(noLeaves), "Reset must panic with the text of a tree without leaves")
			})

			t.Run("empties the tree after a panic", func(t *testing.T) {
				t.Parallel()
				tree := taggedTree(h, all[:5])
				assert.Panics(t, func() { tree.Reset(h, unaryRole, all[:5]) }, "a unary role must panic")
				assert.Equal(t, tree.Size(), uint64(0), "the tree must be empty after a panic")
			})
		})

		t.Run("Size", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the number of leaves", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, taggedTree(h, all[:7]).Size(), uint64(7), "Size must count the leaves")
			})

			t.Run("returns zero for the zero TaggedTree", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				assert.Equal(t, tree.Size(), uint64(0), "the zero TaggedTree must have no leaves")
			})
		})

		t.Run("Root", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the root of TaggedRoot for every tree up to 130 leaves", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				for n := 1; n <= differentialBound; n++ {
					tree.Reset(h, nodeRole, all[:n])
					expect.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:n]),
						"the root of "+strconv.Itoa(n)+" leaves")
				}
			})

			t.Run("panics for the zero TaggedTree", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				got := assert.Panics(t, func() { _ = tree.Root() }, "the root of no leaves must panic")
				assert.Equal(t, got, any(noLeaves), "Root must panic with the text of a tree without leaves")
			})
		})

		t.Run("InclusionProof", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the path of TaggedInclusionProof for every leaf of every tree up to 130 leaves",
				func(t *testing.T) {
					t.Parallel()
					var tree tlog.TaggedTree
					for n := uint64(1); n <= differentialBound; n++ {
						tree.Reset(h, nodeRole, all[:n])
						for i := range n {
							got, err := tree.InclusionProof(i, nil)
							assert.NoError(t, err, "InclusionProof must succeed")
							want, err := tlog.TaggedInclusionProof(h, nodeRole, all[:n], i, nil)
							assert.NoError(t, err, "TaggedInclusionProof must succeed")
							expect.Equal(t, got, want,
								"the path of leaf "+strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(n, 10))
						}
					}
				})

			t.Run("returns the recorded proof of every leaf of every tree up to 64 leaves", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				var proofs [][]crypto.Digest
				for n := 1; n <= vectorBound; n++ {
					tree.Reset(rfcNodeHasher{}, nodeRole, all[:n])
					for i := range uint64(n) {
						p, err := tree.InclusionProof(i, nil)
						assert.NoError(t, err, "InclusionProof must succeed")
						proofs = append(proofs, p)
					}
				}
				assert.Equal(t, digestOf(t, proofs), v.inclusion, "the paths must be RFC 9162's")
			})

			t.Run("hashes nothing", func(t *testing.T) {
				t.Parallel()
				c := &countingHasher{}
				tree := taggedTree(c, all[:100])
				before := c.combines
				for i := range uint64(100) {
					_, err := tree.InclusionProof(i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
				}
				assert.Equal(t, c.combines, before, "InclusionProof must read the kept nodes")
			})

			t.Run("appends the path to dst", func(t *testing.T) {
				t.Parallel()
				prefix := []crypto.Digest{h.Hash([]byte("kept"))}
				p, err := taggedTree(h, all[:5]).InclusionProof(2, prefix)
				assert.NoError(t, err, "InclusionProof must succeed")
				assert.Length(t, p, 4, "the path must follow the kept digest")
				assert.Equal(t, p[0], prefix[0], "dst must keep its contents")
			})

			t.Run("returns ErrRange with dst unchanged for an index at the size", func(t *testing.T) {
				t.Parallel()
				prefix := []crypto.Digest{h.Hash([]byte("kept"))}
				p, err := taggedTree(h, all[:5]).InclusionProof(5, prefix)
				expect.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
				expect.Equal(t, p, prefix, "dst must be returned unchanged")
			})

			t.Run("returns ErrRange for every index of the zero TaggedTree", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				_, err := tree.InclusionProof(0, nil)
				assert.ErrorIs(t, err, tlog.ErrRange, "a tree with no leaves must have no paths")
			})
		})
	})

	t.Run("TaggedRoot", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded root of every tree up to 64 leaves under the node hash of RFC 9162",
			func(t *testing.T) {
				t.Parallel()
				for n := 1; n <= vectorBound; n++ {
					expect.Equal(t, tlog.TaggedRoot(rfcNodeHasher{}, nodeRole, all[:n]), v.roots[uint64(n)],
						"the root of "+strconv.Itoa(n)+" leaves")
				}
			})

		t.Run("returns the pinned root of three leaves", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tlog.TaggedRoot(h, nodeRole, pinnedLeaves()), pinned(t, pinnedRoot),
				"the root must hash its nodes with CombineTagged under the node role")
		})

		t.Run("returns the root of the recursive definition for every tree up to 130 leaves", func(t *testing.T) {
			t.Parallel()
			for n := 1; n <= differentialBound; n++ {
				expect.Equal(t, tlog.TaggedRoot(h, nodeRole, all[:n]), recursiveRoot(h, nodeRole, all[:n]),
					"the root of "+strconv.Itoa(n)+" leaves")
			}
		})

		t.Run("returns another root under another node role", func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, tlog.TaggedRoot(h, otherRole, all[:2]), tlog.TaggedRoot(h, nodeRole, all[:2]),
				"two roles must give two roots")
		})

		t.Run("hashes each interior node once", func(t *testing.T) {
			t.Parallel()
			c := &countingHasher{}
			_ = tlog.TaggedRoot(c, nodeRole, all[:100])
			assert.Equal(t, c.combines, 99, "a tree of 100 leaves has 99 interior nodes")
		})

		t.Run("panics on a unary node role over one leaf", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { _ = tlog.TaggedRoot(h, unaryRole, all[:1]) }, "a unary role must panic")
		})

		t.Run("panics over no leaves", func(t *testing.T) {
			t.Parallel()
			got := assert.Panics(t, func() { _ = tlog.TaggedRoot(h, nodeRole, nil) }, "no leaves must panic")
			assert.Equal(t, got, any(noLeaves), "TaggedRoot must panic with the text of a tree without leaves")
		})
	})

	t.Run("TaggedInclusionProof", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the recorded proof of every leaf of every tree up to 64 leaves under the node hash of RFC 9162",
			func(t *testing.T) {
				t.Parallel()
				var proofs [][]crypto.Digest
				for n := 1; n <= vectorBound; n++ {
					for i := range uint64(n) {
						p, err := tlog.TaggedInclusionProof(rfcNodeHasher{}, nodeRole, all[:n], i, nil)
						assert.NoError(t, err, "TaggedInclusionProof must succeed")
						proofs = append(proofs, p)
					}
				}
				assert.Equal(t, digestOf(t, proofs), v.inclusion, "the paths must be RFC 9162's")
			})

		t.Run("returns the pinned path of the last of three leaves", func(t *testing.T) {
			t.Parallel()
			p, err := tlog.TaggedInclusionProof(h, nodeRole, pinnedLeaves(), 2, nil)
			assert.NoError(t, err, "TaggedInclusionProof must succeed")
			assert.Equal(t, p, []crypto.Digest{pinned(t, pinnedPair)}, "the path must be the node over the first two")
		})

		t.Run("appends the path to dst", func(t *testing.T) {
			t.Parallel()
			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := tlog.TaggedInclusionProof(h, nodeRole, all[:5], 2, prefix)
			assert.NoError(t, err, "TaggedInclusionProof must succeed")
			assert.Length(t, p, 4, "the path must follow the kept digest")
			assert.Equal(t, p[0], prefix[0], "dst must keep its contents")
		})

		t.Run("returns ErrRange with dst unchanged for an index at the size", func(t *testing.T) {
			t.Parallel()
			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := tlog.TaggedInclusionProof(h, nodeRole, all[:5], 5, prefix)
			expect.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
			expect.Equal(t, p, prefix, "dst must be returned unchanged")
		})

		t.Run("panics on a unary node role", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { _, _ = tlog.TaggedInclusionProof(h, unaryRole, all[:1], 0, nil) },
				"a unary role must panic")
		})
	})

	t.Run("VerifyTaggedInclusion", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for every path of every leaf of every tree up to 64 leaves", func(t *testing.T) {
			t.Parallel()
			var tree tlog.TaggedTree
			for n := uint64(1); n <= vectorBound; n++ {
				tree.Reset(h, nodeRole, all[:n])
				for i := range n {
					p, err := tree.InclusionProof(i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					expect.NoError(t, tlog.VerifyTaggedInclusion(h, nodeRole, i, n, all[i], tree.Root(), p),
						"leaf "+strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(n, 10)+" must verify")
				}
			}
		})

		t.Run("returns ErrProof for every path one hash away from a path", func(t *testing.T) {
			t.Parallel()
			var tree tlog.TaggedTree
			for n := uint64(1); n <= vectorBound; n++ {
				tree.Reset(h, nodeRole, all[:n])
				for i := range n {
					p, err := tree.InclusionProof(i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					for _, m := range mutations(h, p) {
						expect.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, i, n, all[i], tree.Root(), m),
							tlog.ErrProof, "a mutation of the path of leaf "+strconv.FormatUint(i, 10)+" of "+
								strconv.FormatUint(n, 10)+" must fail")
					}
				}
			}
		})

		t.Run("returns ErrProof without hashing a hash past the path", func(t *testing.T) {
			t.Parallel()
			extra := h.Hash([]byte("not in the tree"))
			var tree tlog.TaggedTree
			for n := uint64(1); n <= countedBound; n++ {
				tree.Reset(h, nodeRole, all[:n])
				for i := range n {
					p, err := tree.InclusionProof(i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")

					path, longer := &countingHasher{}, &countingHasher{}
					assert.NoError(t, tlog.VerifyTaggedInclusion(path, nodeRole, i, n, all[i], tree.Root(), p),
						"the path must verify")
					assert.ErrorIs(t, tlog.VerifyTaggedInclusion(longer, nodeRole, i, n, all[i], tree.Root(),
						append(p, extra)), tlog.ErrProof, "a longer path must fail")
					expect.Equal(t, longer.combines, path.combines,
						"VerifyTaggedInclusion must not hash the hash past the path")
				}
			}
		})

		tree := taggedTree(h, all[:7])
		p, err := tree.InclusionProof(3, nil)
		assert.NoError(t, err, "InclusionProof must succeed")

		wider, zero := slices.Clone(p), slices.Clone(p)
		wider[1] = coresha512.New384().Hash([]byte("wider"))
		zero[1] = crypto.Digest{}

		tests := []struct {
			name  string
			leaf  crypto.Digest
			root  crypto.Digest
			proof []crypto.Digest
			index uint64
			role  crypto.Role
		}{
			{
				name:  "returns ErrProof for another leaf",
				index: 3, leaf: all[4], root: tree.Root(), proof: p, role: nodeRole,
			},
			{
				name:  "returns ErrProof for another index",
				index: 2, leaf: all[3], root: tree.Root(), proof: p, role: nodeRole,
			},
			{
				name:  "returns ErrProof for another root",
				index: 3, leaf: all[3], root: all[0], proof: p, role: nodeRole,
			},
			{
				name:  "returns ErrProof for another node role",
				index: 3, leaf: all[3], root: tree.Root(), proof: p, role: otherRole,
			},
			{
				name:  "returns ErrProof for a SHA-384 digest in the path",
				index: 3, leaf: all[3], root: tree.Root(), proof: wider, role: nodeRole,
			},
			{
				name:  "returns ErrProof for the zero Digest in the path",
				index: 3, leaf: all[3], root: tree.Root(), proof: zero, role: nodeRole,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, tlog.VerifyTaggedInclusion(h, tt.role, tt.index, 7, tt.leaf, tt.root, tt.proof),
					tlog.ErrProof, "VerifyTaggedInclusion must refuse the path")
			})
		}

		t.Run("returns ErrProof for an interior node in place of a leaf", func(t *testing.T) {
			t.Parallel()
			node := h.CombineTagged(nodeRole, all[0], all[1])
			sibling := h.CombineTagged(nodeRole, all[2], all[3])
			assert.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 0, 4, node, tlog.TaggedRoot(h, nodeRole, all[:4]),
				[]crypto.Digest{sibling}), tlog.ErrProof, "a node one level above the leaves must not verify as a leaf")
		})

		t.Run("returns ErrRange for an index at the size", func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 7, 7, all[0], all[0], nil), tlog.ErrRange,
				"an index past the tree must be ErrRange")
		})

		t.Run("panics on a unary node role for a path of no hashes", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { _ = tlog.VerifyTaggedInclusion(h, unaryRole, 0, 1, all[0], all[0], nil) },
				"a unary role must panic")
		})
	})
}

// TestTaggedAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestTaggedAllocs(t *testing.T) {
	h := coresha256.New()
	all := leaves(measuredSize)
	tree := taggedTree(h, all)
	dst := make([]crypto.Digest, 0, vectorBound)

	path, err := tree.InclusionProof(measuredIndex, nil)
	assert.NoError(t, err, "InclusionProof must succeed")

	t.Run("TaggedTree", func(t *testing.T) {
		t.Run("Reset", func(t *testing.T) {
			reused := taggedTree(h, all)
			expect.MaxAllocs(t, func() { reused.Reset(h, nodeRole, all) }, 0,
				"Reset must not allocate into a TaggedTree that contained as many leaves")
			assert.Equal(t, reused.Size(), uint64(measuredSize), "the test must measure a tree of the leaves")
		})

		t.Run("Size", func(t *testing.T) {
			var got uint64
			expect.MaxAllocs(t, func() { got = tree.Size() }, 0, "Size must not allocate")
			assert.Equal(t, got, uint64(measuredSize), "the test must measure the size of the tree")
		})

		t.Run("Root", func(t *testing.T) {
			var got crypto.Digest
			expect.MaxAllocs(t, func() { got = tree.Root() }, 0, "Root must not allocate")
			assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
		})

		t.Run("InclusionProof", func(t *testing.T) {
			var got []crypto.Digest
			expect.MaxAllocs(t, func() { got, _ = tree.InclusionProof(measuredIndex, dst[:0]) }, 0,
				"InclusionProof must not allocate into a dst with room")
			assert.NotEmpty(t, got, "the test must measure a path")
		})
	})

	t.Run("TaggedRoot", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() { got = tlog.TaggedRoot(h, nodeRole, all) }, 0, "TaggedRoot must not allocate")
		assert.NotEqual(t, got, crypto.Digest{}, "the test must measure a root")
	})

	t.Run("TaggedInclusionProof", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() { got, _ = tlog.TaggedInclusionProof(h, nodeRole, all, measuredIndex, dst[:0]) }, 0,
			"TaggedInclusionProof must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a path")
	})

	t.Run("VerifyTaggedInclusion", func(t *testing.T) {
		root := tree.Root()
		expect.MaxAllocs(t, func() {
			err = tlog.VerifyTaggedInclusion(h, nodeRole, measuredIndex, measuredSize, all[measuredIndex], root, path)
		}, 0, "VerifyTaggedInclusion must not allocate")
		assert.NoError(t, err, "the test must measure a path that verifies")
	})
}

// BenchmarkTagged reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkTagged(b *testing.B) {
	h := coresha256.New()
	all := leaves(measuredSize)
	tree := taggedTree(h, all)
	dst := make([]crypto.Digest, 0, vectorBound)

	path, err := tree.InclusionProof(measuredIndex, nil)
	assert.NoError(b, err, "InclusionProof must succeed")

	b.Run("TaggedTree", func(b *testing.B) {
		b.Run("Reset", func(b *testing.B) {
			reused := taggedTree(h, all)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				reused.Reset(h, nodeRole, all)
			}

			assert.Equal(b, reused.Size(), uint64(measuredSize), "the benchmark must measure a tree of the leaves")
		})

		b.Run("Size", func(b *testing.B) {
			var got uint64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = tree.Size()
			}

			assert.Equal(b, got, uint64(measuredSize), "the benchmark must measure the size of the tree")
		})

		b.Run("Root", func(b *testing.B) {
			var got crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = tree.Root()
			}

			assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a root")
		})

		b.Run("InclusionProof", func(b *testing.B) {
			var got []crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, _ = tree.InclusionProof(measuredIndex, dst[:0])
			}

			assert.NotEmpty(b, got, "the benchmark must measure a path")
		})
	})

	b.Run("TaggedRoot", func(b *testing.B) {
		var got crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tlog.TaggedRoot(h, nodeRole, all)
		}

		assert.NotEqual(b, got, crypto.Digest{}, "the benchmark must measure a root")
	})

	b.Run("TaggedInclusionProof", func(b *testing.B) {
		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.TaggedInclusionProof(h, nodeRole, all, measuredIndex, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a path")
	})

	b.Run("VerifyTaggedInclusion", func(b *testing.B) {
		root := tree.Root()

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = tlog.VerifyTaggedInclusion(h, nodeRole, measuredIndex, measuredSize, all[measuredIndex], root, path)
		}

		assert.NoError(b, err, "the benchmark must measure a path that verifies")
	})
}

// recursiveRoot returns the root of the tagged tree over leaves by the
// recursive definition of RFC 9162: a tree of n leaves joins the tree of
// its first k leaves, k the largest power of two below n, and the tree of
// the rest.
func recursiveRoot(h crypto.Hasher, role crypto.Role, leaves []crypto.Digest) crypto.Digest {
	if len(leaves) == 1 {
		return leaves[0]
	}

	k := 1 << (bits.Len(uint(len(leaves)-1)) - 1)

	return h.CombineTagged(role, recursiveRoot(h, role, leaves[:k]), recursiveRoot(h, role, leaves[k:]))
}

// pinnedLeaves returns the leaves of the pinned tree.
func pinnedLeaves() []crypto.Digest {
	h := coresha256.New()
	out := make([]crypto.Digest, len(pinnedPayloads))
	for i, p := range pinnedPayloads {
		out[i] = h.HashTagged(pinnedLeafRole, []byte(p))
	}

	return out
}

// pinned decodes a pinned hex digest.
func pinned(tb testing.TB, s string) crypto.Digest {
	tb.Helper()

	b, err := hex.DecodeString(s)
	assert.NoError(tb, err, "a pinned digest must be hex")
	d, err := crypto.DigestFromBytes(b)
	assert.NoError(tb, err, "a pinned digest must be a digest")

	return d
}

// taggedTree returns the TaggedTree over leaves under nodeRole.
func taggedTree(h crypto.Hasher, leaves []crypto.Digest) *tlog.TaggedTree {
	var t tlog.TaggedTree
	t.Reset(h, nodeRole, leaves)

	return &t
}
