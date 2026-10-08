// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"encoding/hex"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

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

	// rangeBound is the largest tree of the generated ranges. It is past
	// the perfect tree of 1,024 leaves, so the generated trees have up to
	// eleven levels.
	rangeBound = 1100

	// exhaustiveBound is the largest tree whose every range a test proves.
	// It is past the perfect tree of 32 leaves.
	exhaustiveBound = 33

	// measuredRange is the number of leaves of the range of the allocation
	// tests and the benchmarks, which starts at measuredIndex.
	measuredRange = 16

	// noLeaves is the panic of a tagged tree over no leaves.
	noLeaves = "tlog: a tagged tree over no leaves has no root"

	// noFold is the panic of Add on a TaggedFold that Reset did not start.
	noFold = "tlog: Add on a TaggedFold that Reset did not start"

	// concurrentRoot labels a case of the machine of TaggedFold that ran Root
	// over leaves on a client of a concurrent section, and concurrentShare
	// is the share of the cases that the machine requires to carry it. 48
	// of 100 cases carried it in a run of 100 cases.
	concurrentRoot  = "a concurrent Root of a fold of leaves"
	concurrentShare = 0.3

	// addWeight is how much more often a step of the machine of TaggedFold
	// adds a leaf than it takes another action, so that most folds have
	// leaves when the concurrent section reads them.
	addWeight = 4
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
	many := leaves(rangeBound)

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

		t.Run("RangeProof", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the proof of TaggedRangeProof for generated ranges", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "RangeProof must return the proof of TaggedRangeProof", func(c *prop.Case) {
					n, begin, end := drawRange(c)
					got, err := taggedTree(h, many[:n]).RangeProof(begin, end, nil)
					assert.NoError(c, err, "RangeProof must succeed")
					want, err := tlog.TaggedRangeProof(h, nodeRole, many[:n], begin, end, nil)
					assert.NoError(c, err, "TaggedRangeProof must succeed")
					assert.Equal(c, got, want, "the proof of the kept nodes must be the proof of the leaves")
				})
			})

			t.Run("hashes nothing", func(t *testing.T) {
				t.Parallel()
				c := &countingHasher{}
				tree := taggedTree(c, all[:100])
				before := c.combines
				for begin := range uint64(100) {
					_, err := tree.RangeProof(begin, 100, nil)
					assert.NoError(t, err, "RangeProof must succeed")
				}
				assert.Equal(t, c.combines, before, "RangeProof must read the kept nodes")
			})

			t.Run("appends the proof to dst", func(t *testing.T) {
				t.Parallel()
				prefix := []crypto.Digest{h.Hash([]byte("kept"))}
				p, err := taggedTree(h, all[:5]).RangeProof(1, 3, prefix)
				assert.NoError(t, err, "RangeProof must succeed")
				want, err := tlog.TaggedRangeProof(h, nodeRole, all[:5], 1, 3, nil)
				assert.NoError(t, err, "TaggedRangeProof must succeed")
				assert.Equal(t, p, append(slices.Clone(prefix), want...), "the proof must follow the kept digest")
			})

			tests := []struct {
				name       string
				begin, end uint64
			}{
				{name: "returns ErrRange with dst unchanged for an empty range", begin: 2, end: 2},
				{name: "returns ErrRange with dst unchanged for a range that ends before it begins", begin: 3, end: 2},
				{name: "returns ErrRange with dst unchanged for a range past the size", begin: 2, end: 6},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					prefix := []crypto.Digest{h.Hash([]byte("kept"))}
					p, err := taggedTree(h, all[:5]).RangeProof(tt.begin, tt.end, prefix)
					expect.ErrorIs(t, err, tlog.ErrRange, "a range outside the tree must be ErrRange")
					expect.Equal(t, p, prefix, "dst must be returned unchanged")
				})
			}

			t.Run("returns ErrRange for every range of the zero TaggedTree", func(t *testing.T) {
				t.Parallel()
				var tree tlog.TaggedTree
				_, err := tree.RangeProof(0, 1, nil)
				assert.ErrorIs(t, err, tlog.ErrRange, "a tree with no leaves must have no ranges")
			})
		})
	})

	t.Run("TaggedFold", func(t *testing.T) {
		t.Parallel()

		// The state of the spec is the leaves since the last Reset, one byte
		// per leaf: its index in all. Size returns their number, and Root
		// their root by the recursive definition, or the panic of a tree
		// without leaves.
		spec := history.Spec[string]{
			Initial: func() string { return "" },
			Next: func(state string, op history.Operation) []string {
				if op.Name == "reset" {
					return []string{""}
				}

				if op.Name == "add" {
					index, _ := op.Args[0].(int)

					return []string{state + string([]byte{byte(index)})}
				}

				var want any = uint64(len(state))
				if op.Name == "root" {
					want = noLeaves
					if state != "" {
						folded := make([]crypto.Digest, len(state))
						for i := range len(state) {
							folded[i] = all[state[i]]
						}
						want = recursiveRoot(h, nodeRole, folded)
					}
				}

				if op.Returned(want) {
					return []string{state}
				}

				return nil
			},
		}

		t.Run("folds the leaves since the last Reset for any sequence of calls", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Size and Root must follow the leaves since the last Reset", func(c *prop.Case) {
				var f tlog.TaggedFold
				f.Reset(h, nodeRole)

				// concurrent records a read of a fold of leaves on a client of a
				// concurrent section, which runs on a goroutine of its own.
				var concurrent atomic.Bool

				// Reset and Add state an Enabled, so the concurrent sections of
				// the clients run Size and Root alone, the calls that may run at
				// once. Swarm(false) keeps the four actions in a case, so that
				// most concurrent sections read a fold of leaves.
				stateful.Steps(c, stateful.Machine[string]{
					Spec: spec,
					Actions: []stateful.Action[string]{{
						Name:    "reset",
						Enabled: func(string) bool { return true },
						Run: func(c *prop.Case, client int, _ any) {
							call := c.History().Invoke(client, "reset", nil)
							f.Reset(h, nodeRole)
							call.OK(nil)
						},
					}, {
						Name:    "add",
						Weight:  addWeight,
						Enabled: func(string) bool { return true },
						Input: func(c *prop.Case, _ string) any {
							return c.Draw(prop.Integer(0, len(all)-1), "leaf")
						},
						Run: func(c *prop.Case, client int, input any) {
							call := c.History().Invoke(client, "add", []any{input})
							index, _ := input.(int)
							f.Add(all[index])
							call.OK(nil)
						},
					}, {
						Name: "size",
						Run: func(c *prop.Case, client int, _ any) {
							call := c.History().Invoke(client, "size", nil)
							call.OK(f.Size())
						},
					}, {
						Name: "root",
						Run: func(c *prop.Case, client int, _ any) {
							call := c.History().Invoke(client, "root", nil)
							if f.Size() == 0 {
								call.OK(assert.Panics(c, func() { _ = f.Root() }, "the root of no leaves must panic"))

								return
							}
							call.OK(f.Root())
							if client > 0 {
								concurrent.Store(true)
							}
						},
					}},
				}, stateful.Clients(2), stateful.Swarm(false))

				if concurrent.Load() {
					c.Classify(concurrentRoot)
				}
			}, prop.Require(concurrentRoot, concurrentShare))
		})

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("panics on a unary node role", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				assert.Panics(t, func() { f.Reset(h, unaryRole) }, "a unary role must panic")
			})

			t.Run("leaves the fold unchanged after a panic", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				f.Reset(h, nodeRole)
				for _, leaf := range all[:3] {
					f.Add(leaf)
				}
				assert.Panics(t, func() { f.Reset(h, unaryRole) }, "a unary role must panic")
				assert.Equal(t, f.Root(), tlog.TaggedRoot(h, nodeRole, all[:3]),
					"a Reset that panics must keep the leaves")
			})
		})

		t.Run("Add", func(t *testing.T) {
			t.Parallel()

			t.Run("hashes n - popcount(n) nodes over n leaves", func(t *testing.T) {
				t.Parallel()
				c := &countingHasher{}
				var f tlog.TaggedFold
				f.Reset(c, nodeRole)
				for i, leaf := range all {
					f.Add(leaf)
					n := uint64(i + 1)
					expect.Equal(t, uint64(c.combines), n-uint64(bits.OnesCount64(n)),
						"the leaves of a fold of "+strconv.FormatUint(n, 10)+" leaves")
				}
			})

			t.Run("panics for the zero TaggedFold", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				got := assert.Panics(t, func() { f.Add(all[0]) }, "a fold that Reset did not start must panic")
				assert.Equal(t, got, any(noFold), "Add must panic with the text of a fold without a hasher")
			})

			t.Run("panics for a leaf that is not a digest of the hasher", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				f.Reset(h, nodeRole)
				f.Add(all[0])
				wider := coresha512.New384().Hash([]byte("wider"))
				assert.Panics(t, func() { f.Add(wider) }, "a SHA-384 leaf must panic in a fold of SHA-256 leaves")
			})
		})

		t.Run("Size", func(t *testing.T) {
			t.Parallel()

			t.Run("returns zero for the zero TaggedFold", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				assert.Equal(t, f.Size(), uint64(0), "the zero TaggedFold must have no leaves")
			})
		})

		t.Run("Root", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the root of the recursive definition after each leaf of a tree of 130", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				f.Reset(h, nodeRole)
				for i, leaf := range all {
					f.Add(leaf)
					expect.Equal(t, f.Root(), recursiveRoot(h, nodeRole, all[:i+1]),
						"the root of "+strconv.Itoa(i+1)+" leaves")
				}
			})

			t.Run("returns the root of TaggedRoot for generated leaves", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "the fold must return the same root as TaggedRoot", func(c *prop.Case) {
					n := c.Draw(prop.Integer(1, rangeBound), "size")
					var f tlog.TaggedFold
					f.Reset(h, nodeRole)
					for _, leaf := range many[:n] {
						f.Add(leaf)
					}
					assert.Equal(c, f.Root(), tlog.TaggedRoot(h, nodeRole, many[:n]),
						"the fold of the leaves must return the same root as TaggedRoot")
				})
			})

			t.Run("hashes popcount(n) - 1 nodes", func(t *testing.T) {
				t.Parallel()
				c := &countingHasher{}
				var f tlog.TaggedFold
				f.Reset(c, nodeRole)
				for i, leaf := range all {
					f.Add(leaf)
					before := c.combines
					_ = f.Root()
					n := uint64(i + 1)
					expect.Equal(t, c.combines-before, bits.OnesCount64(n)-1,
						"the root of a fold of "+strconv.FormatUint(n, 10)+" leaves")
				}
			})

			t.Run("panics for a fold of no leaves", func(t *testing.T) {
				t.Parallel()
				var f tlog.TaggedFold
				got := assert.Panics(t, func() { _ = f.Root() }, "the root of no leaves must panic")
				assert.Equal(t, got, any(noLeaves), "Root must panic with the text of a tree without leaves")
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

	t.Run("TaggedRangeProof", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the path of TaggedInclusionProof for a range of one leaf", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the range proof of one leaf must be its path", func(c *prop.Case) {
				n := c.Draw(prop.Integer[uint64](1, rangeBound), "size")
				i := c.Draw(prop.Integer[uint64](0, n-1), "index")
				got, err := tlog.TaggedRangeProof(h, nodeRole, many[:n], i, i+1, nil)
				assert.NoError(c, err, "TaggedRangeProof must succeed")
				want, err := tlog.TaggedInclusionProof(h, nodeRole, many[:n], i, nil)
				assert.NoError(c, err, "TaggedInclusionProof must succeed")
				assert.Equal(c, got, want, "the proof of one leaf must be its path")
			})
		})

		t.Run("returns at most 2⌈log2 n⌉ hashes for a tree of n leaves", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a range proof must have at most two hashes per level", func(c *prop.Case) {
				n, begin, end := drawRange(c)
				got, err := tlog.TaggedRangeProof(h, nodeRole, many[:n], begin, end, nil)
				assert.NoError(c, err, "TaggedRangeProof must succeed")
				bound := float64(2 * bits.Len64(n-1))
				assert.InRange(c, len(got), 0, bound, "the proof must have at most two hashes per level")
			})
		})

		t.Run("returns the subtrees beside the range in the order of the proof", func(t *testing.T) {
			t.Parallel()
			// The leaves [3, 5) of a tree of 16 straddle the split of [0, 8).
			// The left edge leaves [2, 3) and [0, 2), the right edge [5, 6)
			// and [6, 8), and the path leaves [8, 16).
			got, err := tlog.TaggedRangeProof(h, nodeRole, all[:16], 3, 5, nil)
			assert.NoError(t, err, "TaggedRangeProof must succeed")
			assert.Equal(t, got, []crypto.Digest{
				all[2],
				tlog.TaggedRoot(h, nodeRole, all[0:2]),
				all[5],
				tlog.TaggedRoot(h, nodeRole, all[6:8]),
				tlog.TaggedRoot(h, nodeRole, all[8:16]),
			}, "the proof must list the subtrees beside the range in the order of the proof")
		})

		t.Run("appends the proof to dst", func(t *testing.T) {
			t.Parallel()
			prefix := []crypto.Digest{h.Hash([]byte("kept"))}
			p, err := tlog.TaggedRangeProof(h, nodeRole, all[:16], 3, 5, prefix)
			assert.NoError(t, err, "TaggedRangeProof must succeed")
			assert.Length(t, p, 6, "the proof must follow the kept digest")
			assert.Equal(t, p[0], prefix[0], "dst must keep its contents")
		})

		tests := []struct {
			name       string
			begin, end uint64
		}{
			{name: "returns ErrRange with dst unchanged for an empty range", begin: 2, end: 2},
			{name: "returns ErrRange with dst unchanged for a range that ends before it begins", begin: 3, end: 2},
			{name: "returns ErrRange with dst unchanged for a range past the size", begin: 2, end: 6},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				prefix := []crypto.Digest{h.Hash([]byte("kept"))}
				p, err := tlog.TaggedRangeProof(h, nodeRole, all[:5], tt.begin, tt.end, prefix)
				expect.ErrorIs(t, err, tlog.ErrRange, "a range outside the tree must be ErrRange")
				expect.Equal(t, p, prefix, "dst must be returned unchanged")
			})
		}

		t.Run("panics on a unary node role", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { _, _ = tlog.TaggedRangeProof(h, unaryRole, all[:1], 0, 1, nil) },
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

	t.Run("TaggedRangeRoot", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the root of TaggedRoot for every range of every tree up to 33 leaves", func(t *testing.T) {
			t.Parallel()
			for n := uint64(1); n <= exhaustiveBound; n++ {
				root := tlog.TaggedRoot(h, nodeRole, all[:n])
				for begin := range n {
					for end := begin + 1; end <= n; end++ {
						proof, err := tlog.TaggedRangeProof(h, nodeRole, all[:n], begin, end, nil)
						assert.NoError(t, err, "TaggedRangeProof must succeed")
						got, err := tlog.TaggedRangeRoot(h, nodeRole, begin, n, all[begin:end], proof)
						assert.NoError(t, err, "TaggedRangeRoot must succeed")
						expect.Equal(t, got, root, "the range ["+strconv.FormatUint(begin, 10)+", "+
							strconv.FormatUint(end, 10)+") of "+strconv.FormatUint(n, 10)+" leaves")
					}
				}
			}
		})

		t.Run("returns the root of TaggedRoot for generated ranges", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a range and its proof must recompute the root", func(c *prop.Case) {
				n, begin, end := drawRange(c)
				tree := taggedTree(h, many[:n])
				proof, err := tree.RangeProof(begin, end, nil)
				assert.NoError(c, err, "RangeProof must succeed")
				got, err := tlog.TaggedRangeRoot(h, nodeRole, begin, n, many[begin:end], proof)
				assert.NoError(c, err, "TaggedRangeRoot must succeed")
				assert.Equal(c, got, tree.Root(), "the range and its proof must recompute the root")
			})
		})

		t.Run("returns the root for one leaf exactly when VerifyTaggedInclusion accepts its path", func(t *testing.T) {
			t.Parallel()
			var tree tlog.TaggedTree
			for n := uint64(1); n <= vectorBound; n++ {
				tree.Reset(h, nodeRole, all[:n])
				for i := range n {
					p, err := tree.InclusionProof(i, nil)
					assert.NoError(t, err, "InclusionProof must succeed")
					for _, m := range append(mutations(h, p), p) {
						got, err := tlog.TaggedRangeRoot(h, nodeRole, i, n, all[i:i+1], m)
						verified := tlog.VerifyTaggedInclusion(h, nodeRole, i, n, all[i], tree.Root(), m) == nil
						expect.Equal(t, err == nil && got.Equal(tree.Root()), verified, "a path of leaf "+
							strconv.FormatUint(i, 10)+" of "+strconv.FormatUint(n, 10)+" must recompute the root "+
							"exactly when VerifyTaggedInclusion accepts it")
					}
				}
			}
		})

		t.Run("returns another root for a range with another leaf", func(t *testing.T) {
			t.Parallel()
			other := h.Hash([]byte("not in the tree"))
			prop.ForAll(t, "a changed leaf of the range must change the root", func(c *prop.Case) {
				n, begin, end := drawRange(c)
				tree := taggedTree(h, many[:n])
				proof, err := tree.RangeProof(begin, end, nil)
				assert.NoError(c, err, "RangeProof must succeed")
				rng := slices.Clone(many[begin:end])
				rng[c.Draw(prop.Integer(0, len(rng)-1), "changed leaf")] = other
				got, err := tlog.TaggedRangeRoot(h, nodeRole, begin, n, rng, proof)
				assert.NoError(c, err, "TaggedRangeRoot must succeed")
				assert.NotEqual(c, got, tree.Root(), "a changed leaf must change the root")
			})
		})

		t.Run("returns another root for a proof with another hash", func(t *testing.T) {
			t.Parallel()
			other := h.Hash([]byte("not in the tree"))
			prop.ForAll(t, "a changed hash of the proof must change the root", func(c *prop.Case) {
				n, begin, end := drawRange(c)
				tree := taggedTree(h, many[:n])
				proof, err := tree.RangeProof(begin, end, nil)
				assert.NoError(c, err, "RangeProof must succeed")
				c.Assume(len(proof) > 0)
				proof[c.Draw(prop.Integer(0, len(proof)-1), "changed hash")] = other
				got, err := tlog.TaggedRangeRoot(h, nodeRole, begin, n, many[begin:end], proof)
				assert.NoError(c, err, "TaggedRangeRoot must succeed")
				assert.NotEqual(c, got, tree.Root(), "a changed hash must change the root")
			})
		})

		t.Run("returns ErrProof without hashing for a proof one hash longer", func(t *testing.T) {
			t.Parallel()
			extra := h.Hash([]byte("not in the tree"))
			prop.ForAll(t, "a longer proof must cost no hash", func(c *prop.Case) {
				n, begin, end := drawRange(c)
				proof, err := taggedTree(h, many[:n]).RangeProof(begin, end, nil)
				assert.NoError(c, err, "RangeProof must succeed")
				counting := &countingHasher{}
				_, err = tlog.TaggedRangeRoot(counting, nodeRole, begin, n, many[begin:end], append(proof, extra))
				assert.ErrorIs(c, err, tlog.ErrProof, "a longer proof must be ErrProof")
				assert.Equal(c, counting.combines, 0, "a longer proof must cost no hash")
			})
		})

		t.Run("returns ErrProof without hashing for a proof one hash shorter", func(t *testing.T) {
			t.Parallel()
			proof, err := tlog.TaggedRangeProof(h, nodeRole, all[:16], 3, 5, nil)
			assert.NoError(t, err, "TaggedRangeProof must succeed")
			counting := &countingHasher{}
			_, err = tlog.TaggedRangeRoot(counting, nodeRole, 3, 16, all[3:5], proof[:len(proof)-1])
			assert.ErrorIs(t, err, tlog.ErrProof, "a shorter proof must be ErrProof")
			assert.Equal(t, counting.combines, 0, "a shorter proof must cost no hash")
		})

		proof, err := tlog.TaggedRangeProof(h, nodeRole, all[:16], 3, 5, nil)
		assert.NoError(t, err, "TaggedRangeProof must succeed")

		wider, zero := slices.Clone(proof), slices.Clone(proof)
		wider[1] = coresha512.New384().Hash([]byte("wider"))
		zero[1] = crypto.Digest{}

		tests := []struct {
			want        error
			name        string
			rng         []crypto.Digest
			proof       []crypto.Digest
			begin, size uint64
		}{
			{
				name: "returns ErrProof for a SHA-384 digest in the proof",
				rng:  all[3:5], proof: wider, begin: 3, size: 16, want: tlog.ErrProof,
			},
			{
				name: "returns ErrProof for the zero Digest in the proof",
				rng:  all[3:5], proof: zero, begin: 3, size: 16, want: tlog.ErrProof,
			},
			{
				name: "returns ErrRange for an empty range",
				rng:  nil, proof: proof, begin: 3, size: 16, want: tlog.ErrRange,
			},
			{
				name: "returns ErrRange for a range past the size",
				rng:  all[15:17], proof: proof, begin: 15, size: 16, want: tlog.ErrRange,
			},
			{
				name: "returns ErrRange for a begin at the size",
				rng:  all[16:17], proof: proof, begin: 16, size: 16, want: tlog.ErrRange,
			},
			{
				name: "returns ErrRange for a range whose end overflows",
				rng:  all[:1], proof: proof, begin: math.MaxUint64, size: 16, want: tlog.ErrRange,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tlog.TaggedRangeRoot(h, nodeRole, tt.begin, tt.size, tt.rng, tt.proof)
				assert.ErrorIs(t, err, tt.want, "TaggedRangeRoot must refuse the range or its proof")
			})
		}

		t.Run("panics on a unary node role", func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { _, _ = tlog.TaggedRangeRoot(h, unaryRole, 0, 1, all[:1], nil) },
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

	rng := all[measuredIndex : measuredIndex+measuredRange]
	rangeProof, err := tree.RangeProof(measuredIndex, measuredIndex+measuredRange, nil)
	assert.NoError(t, err, "RangeProof must succeed")

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

		t.Run("RangeProof", func(t *testing.T) {
			var got []crypto.Digest
			expect.MaxAllocs(t, func() {
				got, _ = tree.RangeProof(measuredIndex, measuredIndex+measuredRange, dst[:0])
			}, 0, "RangeProof must not allocate into a dst with room")
			assert.NotEmpty(t, got, "the test must measure a proof")
		})
	})

	t.Run("TaggedFold", func(t *testing.T) {
		var f tlog.TaggedFold

		t.Run("Reset", func(t *testing.T) {
			expect.MaxAllocs(t, func() { f.Reset(h, nodeRole) }, 0, "Reset must not allocate")
			assert.Equal(t, f.Size(), uint64(0), "the test must measure a fold that Reset starts")
		})

		t.Run("Add", func(t *testing.T) {
			f.Reset(h, nodeRole)
			i := 0
			expect.MaxAllocs(t, func() {
				f.Add(all[i%measuredSize])
				i++
			}, 0, "Add must not allocate")
			assert.NotEqual(t, f.Size(), uint64(0), "the test must measure a fold of leaves")
		})

		t.Run("Size", func(t *testing.T) {
			var got uint64
			expect.MaxAllocs(t, func() { got = f.Size() }, 0, "Size must not allocate")
			assert.NotEqual(t, got, uint64(0), "the test must measure the size of a fold of leaves")
		})

		t.Run("Root", func(t *testing.T) {
			f.Reset(h, nodeRole)
			for _, leaf := range all {
				f.Add(leaf)
			}

			var got crypto.Digest
			expect.MaxAllocs(t, func() { got = f.Root() }, 0, "Root must not allocate")
			assert.Equal(t, got, tree.Root(), "the test must measure the root of the leaves")
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

	t.Run("TaggedRangeProof", func(t *testing.T) {
		var got []crypto.Digest
		expect.MaxAllocs(t, func() {
			got, _ = tlog.TaggedRangeProof(h, nodeRole, all, measuredIndex, measuredIndex+measuredRange, dst[:0])
		}, 0, "TaggedRangeProof must not allocate into a dst with room")
		assert.NotEmpty(t, got, "the test must measure a proof")
	})

	t.Run("VerifyTaggedInclusion", func(t *testing.T) {
		root := tree.Root()
		expect.MaxAllocs(t, func() {
			err = tlog.VerifyTaggedInclusion(h, nodeRole, measuredIndex, measuredSize, all[measuredIndex], root, path)
		}, 0, "VerifyTaggedInclusion must not allocate")
		assert.NoError(t, err, "the test must measure a path that verifies")
	})

	t.Run("TaggedRangeRoot", func(t *testing.T) {
		var got crypto.Digest
		expect.MaxAllocs(t, func() {
			got, err = tlog.TaggedRangeRoot(h, nodeRole, measuredIndex, measuredSize, rng, rangeProof)
		}, 0, "TaggedRangeRoot must not allocate")
		assert.NoError(t, err, "the test must measure a proof that TaggedRangeRoot accepts")
		assert.Equal(t, got, tree.Root(), "the test must measure a proof of the root")
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

	rng := all[measuredIndex : measuredIndex+measuredRange]
	rangeProof, err := tree.RangeProof(measuredIndex, measuredIndex+measuredRange, nil)
	assert.NoError(b, err, "RangeProof must succeed")

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

		b.Run("RangeProof", func(b *testing.B) {
			var got []crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, _ = tree.RangeProof(measuredIndex, measuredIndex+measuredRange, dst[:0])
			}

			assert.NotEmpty(b, got, "the benchmark must measure a proof")
		})
	})

	b.Run("TaggedFold", func(b *testing.B) {
		b.Run("Reset", func(b *testing.B) {
			var f tlog.TaggedFold

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				f.Reset(h, nodeRole)
			}

			assert.Equal(b, f.Size(), uint64(0), "the benchmark must measure a fold that Reset starts")
		})

		b.Run("Add", func(b *testing.B) {
			var f tlog.TaggedFold
			f.Reset(h, nodeRole)
			i := 0

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				f.Add(all[i%measuredSize])
				i++
			}

			assert.NotEqual(b, f.Size(), uint64(0), "the benchmark must measure a fold of leaves")
		})

		b.Run("Size", func(b *testing.B) {
			var f tlog.TaggedFold
			f.Reset(h, nodeRole)
			f.Add(all[0])

			var got uint64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = f.Size()
			}

			assert.Equal(b, got, uint64(1), "the benchmark must measure the size of a fold of one leaf")
		})

		b.Run("Root", func(b *testing.B) {
			var f tlog.TaggedFold
			f.Reset(h, nodeRole)
			for _, leaf := range all {
				f.Add(leaf)
			}

			var got crypto.Digest

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = f.Root()
			}

			assert.Equal(b, got, tree.Root(), "the benchmark must measure the root of the leaves")
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

	b.Run("TaggedRangeProof", func(b *testing.B) {
		var got []crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.TaggedRangeProof(h, nodeRole, all, measuredIndex, measuredIndex+measuredRange, dst[:0])
		}

		assert.NotEmpty(b, got, "the benchmark must measure a proof")
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

	b.Run("TaggedRangeRoot", func(b *testing.B) {
		var (
			got crypto.Digest
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = tlog.TaggedRangeRoot(h, nodeRole, measuredIndex, measuredSize, rng, rangeProof)
		}

		assert.NoError(b, err, "the benchmark must measure a proof that TaggedRangeRoot accepts")
		assert.Equal(b, got, tree.Root(), "the benchmark must measure a proof of the root")
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

// drawRange draws the size n of a tree of up to rangeBound leaves, and a
// range [begin, end) of its leaves.
func drawRange(c *prop.Case) (n, begin, end uint64) {
	n = c.Draw(prop.Integer[uint64](1, rangeBound), "size")
	begin = c.Draw(prop.Integer[uint64](0, n-1), "begin")
	end = c.Draw(prop.Integer[uint64](begin+1, n), "end")

	return n, begin, end
}
