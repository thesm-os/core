// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"encoding/hex"
	"math/bits"
	"slices"
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

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

// pinnedPayloads are the payloads of the pinned tree's leaves.
var pinnedPayloads = []string{`{"act":"infer","id":1}`, `{"act":"infer","id":2}`, `{"act":"score","id":3}`}

// differentialBound is the largest tree size the differential tests
// cover. It is past the perfect tree of 128 leaves, so the tests include
// trees whose right subtree has one or two leaves.
const differentialBound = 130

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
	testkit.NoError(tb, err, "a pinned digest must be hex")
	d, err := crypto.DigestFromBytes(b)
	testkit.NoError(tb, err, "a pinned digest must be a digest")

	return d
}

// taggedTree returns the TaggedTree over leaves under nodeRole.
func taggedTree(h crypto.Hasher, leaves []crypto.Digest) *tlog.TaggedTree {
	var t tlog.TaggedTree
	t.Reset(h, nodeRole, leaves)

	return &t
}

func TestTaggedTree(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(differentialBound)

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("hashes each interior node once", func(t *testing.T) {
			t.Parallel()

			for _, n := range []int{1, 2, 3, 100, 129} {
				c := &countingHasher{}
				taggedTree(c, all[:n])
				testkit.Equal(t, c.combines, n-1, "a tree of "+strconv.Itoa(n)+" leaves has n - 1 interior nodes")
			}
		})

		t.Run("replaces a larger tree with a smaller one", func(t *testing.T) {
			t.Parallel()

			tree := taggedTree(h, all[:9])
			tree.Reset(h, nodeRole, all[:5])
			testkit.Equal(t, tree.Size(), uint64(5), "the tree must have the new leaves")
			testkit.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:5]), "the root must be the new tree's")
		})

		t.Run("keeps a copy of the leaves", func(t *testing.T) {
			t.Parallel()

			own := slices.Clone(all[:6])
			tree := taggedTree(h, own)
			own[0] = all[7]
			testkit.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:6]),
				"changing the caller's leaves must not change the tree")
		})

		t.Run("panics on a unary node role, even over one leaf", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			testkit.Panics(t, func() { tree.Reset(h, unaryRole, all[:1]) }, "a unary role must panic")
		})

		t.Run("panics over no leaves", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			testkit.Panics(t, func() { tree.Reset(h, nodeRole, nil) }, "no leaves must panic")
		})

		t.Run("is empty after a panic", func(t *testing.T) {
			t.Parallel()

			tree := taggedTree(h, all[:5])
			testkit.Panics(t, func() { tree.Reset(h, unaryRole, all[:5]) }, "a unary role must panic")
			testkit.Equal(t, tree.Size(), uint64(0), "the tree must be empty after a panic")
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of leaves", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, taggedTree(h, all[:7]).Size(), uint64(7), "Size must count the leaves")
		})

		t.Run("returns zero for the zero TaggedTree", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			testkit.Equal(t, tree.Size(), uint64(0), "the zero TaggedTree must have no leaves")
		})
	})

	t.Run("Root", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the root TaggedRoot returns for every tree up to 130 leaves", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			for n := 1; n <= differentialBound; n++ {
				tree.Reset(h, nodeRole, all[:n])
				testkit.Equal(t, tree.Root(), tlog.TaggedRoot(h, nodeRole, all[:n]),
					"the root of "+strconv.Itoa(n)+" leaves")
			}
		})

		t.Run("panics for a tree with no leaves", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			testkit.Panics(t, func() { _ = tree.Root() }, "the root of no leaves must panic")
		})
	})

	t.Run("InclusionProof", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the path TaggedInclusionProof returns for every leaf of every tree up to 130 leaves",
			func(t *testing.T) {
				t.Parallel()

				var tree tlog.TaggedTree
				for n := uint64(1); n <= differentialBound; n++ {
					tree.Reset(h, nodeRole, all[:n])
					for i := range n {
						got, err := tree.InclusionProof(i, nil)
						testkit.NoError(t, err, "InclusionProof must succeed")
						want, err := tlog.TaggedInclusionProof(h, nodeRole, all[:n], i, nil)
						testkit.NoError(t, err, "TaggedInclusionProof must succeed")
						testkit.Equal(t, got, want,
							"the path of leaf "+strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(n, 10))
					}
				}
			})

		t.Run("matches the recorded proof of every leaf of every tree up to 64 leaves", func(t *testing.T) {
			t.Parallel()

			v := loadVectors(t)
			var tree tlog.TaggedTree
			var proofs [][]crypto.Digest
			for n := 1; n <= vectorBound; n++ {
				tree.Reset(rfcNodeHasher{}, nodeRole, all[:n])
				for i := range uint64(n) {
					p, err := tree.InclusionProof(i, nil)
					testkit.NoError(t, err, "InclusionProof must succeed")
					proofs = append(proofs, p)
				}
			}
			testkit.Equal(t, digestOf(t, proofs), v.inclusion, "the paths must be RFC 9162's")
		})

		t.Run("hashes nothing", func(t *testing.T) {
			t.Parallel()

			c := &countingHasher{}
			tree := taggedTree(c, all[:100])
			before := c.combines
			for i := range uint64(100) {
				_, err := tree.InclusionProof(i, nil)
				testkit.NoError(t, err, "InclusionProof must succeed")
			}
			testkit.Equal(t, c.combines, before, "InclusionProof must read the kept nodes")
		})

		t.Run("appends to dst", func(t *testing.T) {
			t.Parallel()

			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := taggedTree(h, all[:5]).InclusionProof(2, prefix)
			testkit.NoError(t, err, "InclusionProof must succeed")
			testkit.Len(t, p, 4, "the path must follow the kept digest")
			testkit.Equal(t, p[0], prefix[0], "dst must keep its contents")
		})

		t.Run("refuses an index at or past the size", func(t *testing.T) {
			t.Parallel()

			p, err := taggedTree(h, all[:5]).InclusionProof(5, nil)
			testkit.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
			testkit.Len(t, p, 0, "dst must be returned unchanged")
		})

		t.Run("refuses every index of the zero TaggedTree", func(t *testing.T) {
			t.Parallel()

			var tree tlog.TaggedTree
			_, err := tree.InclusionProof(0, nil)
			testkit.ErrorIs(t, err, tlog.ErrRange, "a tree with no leaves must have no paths")
		})
	})
}

func TestTaggedRoot(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(differentialBound)

	t.Run("matches the recorded root of every tree up to 64 leaves under RFC 9162's node hash", func(t *testing.T) {
		t.Parallel()

		v := loadVectors(t)
		for n := 1; n <= vectorBound; n++ {
			testkit.Equal(t, tlog.TaggedRoot(rfcNodeHasher{}, nodeRole, all[:n]), v.roots[uint64(n)],
				"the root of "+strconv.Itoa(n)+" leaves")
		}
	})

	t.Run("matches the pinned root of three leaves", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, tlog.TaggedRoot(h, nodeRole, pinnedLeaves()), pinned(t, pinnedRoot),
			"the root must hash its nodes with CombineTagged under the node role")
	})

	t.Run("matches the recursive definition for every tree up to 130 leaves", func(t *testing.T) {
		t.Parallel()

		for n := 1; n <= differentialBound; n++ {
			testkit.Equal(t, tlog.TaggedRoot(h, nodeRole, all[:n]), recursiveRoot(h, nodeRole, all[:n]),
				"the root of "+strconv.Itoa(n)+" leaves")
		}
	})

	t.Run("depends on the node role", func(t *testing.T) {
		t.Parallel()
		testkit.NotEqual(t, tlog.TaggedRoot(h, otherRole, all[:2]), tlog.TaggedRoot(h, nodeRole, all[:2]),
			"two roles must give two roots")
	})

	t.Run("hashes each interior node once", func(t *testing.T) {
		t.Parallel()

		c := &countingHasher{}
		_ = tlog.TaggedRoot(c, nodeRole, all[:100])
		testkit.Equal(t, c.combines, 99, "a tree of 100 leaves has 99 interior nodes")
	})

	t.Run("panics on a unary node role, even over one leaf", func(t *testing.T) {
		t.Parallel()
		testkit.Panics(t, func() { _ = tlog.TaggedRoot(h, unaryRole, all[:1]) }, "a unary role must panic")
	})

	t.Run("panics over no leaves", func(t *testing.T) {
		t.Parallel()
		testkit.Panics(t, func() { _ = tlog.TaggedRoot(h, nodeRole, nil) }, "no leaves must panic")
	})
}

func TestTaggedInclusionProof(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("matches the recorded proof of every leaf of every tree up to 64 leaves under RFC 9162's node hash",
		func(t *testing.T) {
			t.Parallel()

			v := loadVectors(t)
			var proofs [][]crypto.Digest
			for n := 1; n <= vectorBound; n++ {
				for i := range uint64(n) {
					p, err := tlog.TaggedInclusionProof(rfcNodeHasher{}, nodeRole, all[:n], i, nil)
					testkit.NoError(t, err, "TaggedInclusionProof must succeed")
					proofs = append(proofs, p)
				}
			}
			testkit.Equal(t, digestOf(t, proofs), v.inclusion, "the paths must be RFC 9162's")
		})

	t.Run("matches the pinned path of the last of three leaves", func(t *testing.T) {
		t.Parallel()

		p, err := tlog.TaggedInclusionProof(h, nodeRole, pinnedLeaves(), 2, nil)
		testkit.NoError(t, err, "TaggedInclusionProof must succeed")
		testkit.Equal(t, p, []crypto.Digest{pinned(t, pinnedPair)}, "the path must be the node over the first two")
	})

	t.Run("appends to dst", func(t *testing.T) {
		t.Parallel()

		prefix := []crypto.Digest{h.Hash([]byte("kept"))}
		p, err := tlog.TaggedInclusionProof(h, nodeRole, all[:5], 2, prefix)
		testkit.NoError(t, err, "TaggedInclusionProof must succeed")
		testkit.Len(t, p, 4, "the path must follow the kept digest")
		testkit.Equal(t, p[0], prefix[0], "dst must keep its contents")
	})

	t.Run("refuses an index at or past the size", func(t *testing.T) {
		t.Parallel()

		p, err := tlog.TaggedInclusionProof(h, nodeRole, all[:5], 5, nil)
		testkit.ErrorIs(t, err, tlog.ErrRange, "an index past the tree must be ErrRange")
		testkit.Len(t, p, 0, "dst must be returned unchanged")
	})

	t.Run("panics on a unary node role", func(t *testing.T) {
		t.Parallel()
		testkit.Panics(t, func() { _, _ = tlog.TaggedInclusionProof(h, unaryRole, all[:1], 0, nil) },
			"a unary role must panic")
	})
}

// TestVerifyTaggedInclusion checks every path of every leaf of every tree
// up to 64 leaves, and every path one hash away from it.
func TestVerifyTaggedInclusion(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	all := leaves(vectorBound)

	t.Run("accepts every path and rejects every mutation of it", func(t *testing.T) {
		t.Parallel()

		var tree tlog.TaggedTree
		for n := uint64(1); n <= vectorBound; n++ {
			tree.Reset(h, nodeRole, all[:n])
			root := tree.Root()
			for i := range n {
				p, err := tree.InclusionProof(i, nil)
				testkit.NoError(t, err, "InclusionProof must succeed")

				name := strconv.FormatUint(i, 10) + " of " + strconv.FormatUint(n, 10)
				testkit.NoError(t, tlog.VerifyTaggedInclusion(h, nodeRole, i, n, all[i], root, p),
					"leaf "+name+" must verify")
				for _, m := range mutations(h, p) {
					testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, i, n, all[i], root, m), tlog.ErrProof,
						"a mutation of the path of leaf "+name+" must fail")
				}
			}
		}
	})

	t.Run("rejects another leaf, index, root or node role", func(t *testing.T) {
		t.Parallel()

		tree := taggedTree(h, all[:7])
		p, err := tree.InclusionProof(3, nil)
		testkit.NoError(t, err, "InclusionProof must succeed")

		testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 3, 7, all[4], tree.Root(), p), tlog.ErrProof,
			"another leaf must fail")
		testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 2, 7, all[3], tree.Root(), p), tlog.ErrProof,
			"another index must fail")
		testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 3, 7, all[3], all[0], p), tlog.ErrProof,
			"another root must fail")
		testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, otherRole, 3, 7, all[3], tree.Root(), p), tlog.ErrProof,
			"another node role must fail")
	})

	t.Run("rejects a path hash of another size without panicking", func(t *testing.T) {
		t.Parallel()

		tree := taggedTree(h, all[:7])
		p, err := tree.InclusionProof(3, nil)
		testkit.NoError(t, err, "InclusionProof must succeed")

		for name, bad := range map[string]crypto.Digest{
			"a SHA-384 digest": coresha512.New384().Hash([]byte("wider")),
			"the zero Digest":  {},
		} {
			m := slices.Clone(p)
			m[1] = bad
			testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 3, 7, all[3], tree.Root(), m), tlog.ErrProof,
				name+" in the path must fail")
		}
	})

	t.Run("refuses an index at or past the size", func(t *testing.T) {
		t.Parallel()
		testkit.ErrorIs(t, tlog.VerifyTaggedInclusion(h, nodeRole, 7, 7, all[0], all[0], nil), tlog.ErrRange,
			"an index past the tree must be ErrRange")
	})

	t.Run("panics on a unary node role, even for a path of no hashes", func(t *testing.T) {
		t.Parallel()
		testkit.Panics(t, func() { _ = tlog.VerifyTaggedInclusion(h, unaryRole, 0, 1, all[0], all[0], nil) },
			"a unary role must panic")
	})
}

func BenchmarkTaggedRoot(b *testing.B) {
	h := coresha256.New()
	all := leaves(4096)
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.TaggedRoot(h, nodeRole, all)
	}
}

// BenchmarkTaggedTree measures one batch of 1,000 leaves: the tree and the
// path of every leaf, into a TaggedTree and a path reused across batches.
// One batch before the loop grows both, so the loop measures a reused
// tree.
func BenchmarkTaggedTree(b *testing.B) {
	h := coresha256.New()
	all := leaves(1000)
	var tree tlog.TaggedTree
	var path []crypto.Digest
	batch := func() {
		tree.Reset(h, nodeRole, all)
		for i := range uint64(len(all)) {
			path, _ = tree.InclusionProof(i, path[:0])
		}
	}
	batch()
	b.ReportAllocs()

	for b.Loop() {
		batch()
	}
}

// BenchmarkTaggedInclusionProof measures the path of one leaf of 1,000.
// One path before the loop grows the reused path.
func BenchmarkTaggedInclusionProof(b *testing.B) {
	h := coresha256.New()
	all := leaves(1000)
	path, err := tlog.TaggedInclusionProof(h, nodeRole, all, 517, nil)
	testkit.NoError(b, err, "TaggedInclusionProof must succeed")
	b.ReportAllocs()

	for b.Loop() {
		path, _ = tlog.TaggedInclusionProof(h, nodeRole, all, 517, path[:0])
	}
}

func BenchmarkVerifyTaggedInclusion(b *testing.B) {
	h := coresha256.New()
	all := leaves(1 << 16)
	tree := taggedTree(h, all)
	p, err := tree.InclusionProof(40000, nil)
	testkit.NoError(b, err, "InclusionProof must succeed")
	root := tree.Root()
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.VerifyTaggedInclusion(h, nodeRole, 40000, 1<<16, all[40000], root, p)
	}
}
