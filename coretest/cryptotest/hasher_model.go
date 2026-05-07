// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"fmt"
	"hash"

	"go.thesmos.sh/testkit/model"
	"go.thesmos.sh/testkit/model/action"

	"go.thesmos.sh/core/crypto"
)

// StdlibHasherSpec describes a Go stdlib hash function: its
// long-term Algorithm name, build-local ID, one-shot Sum, and
// streaming NewHash factory. Used to parameterise
// [NewStdlibHasherStub] so the model framework drives byte-exact
// cross-equivalence between the SUT and the stdlib reference
// across rapid-generated random inputs.
//
// Each [crypto.Hasher] implementation in core has a stdlib
// counterpart (`crypto/sha256`, `crypto/sha512`, `crypto/sha3`);
// the per-impl test file builds its Spec inline.
type StdlibHasherSpec struct {
	// Algorithm is the long-term cross-build algorithm name.
	Algorithm crypto.Algorithm

	// ID is the stable build-local identifier the reference
	// reports through [crypto.Hasher.ID].
	ID crypto.ID

	// Sum returns the algorithm's digest of data — the stdlib
	// one-shot helper. For `crypto/sha256`:
	//
	//	func(d []byte) []byte { h := sha256.Sum256(d); return h[:] }
	Sum func([]byte) []byte

	// NewHash returns a fresh stdlib [hash.Hash] for the
	// streaming reference path (e.g. `sha256.New`).
	NewHash func() hash.Hash
}

// HasherHashAction returns a [model.Action] that draws a random
// byte slice via rapid and asserts byte-exact equivalence
// between SUT.Hash(data) and reference.Hash(data). On failure
// rapid shrinks to the minimal divergent input.
func HasherHashAction() model.Action[crypto.Hasher] {
	return action.Unknown[crypto.Hasher]("Hash", func(rt *model.T, sut, ref crypto.Hasher) model.ActionResult {
		data := model.SliceOfN(model.Byte(), 0, 1024).Draw(rt, "data")
		sutD := sut.Hash(data)
		refD := ref.Hash(data)
		if !sutD.Equal(refD) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"hash diverges on %d-byte input: sut=%x ref=%x",
					len(data), sutD.Bytes(), refD.Bytes(),
				),
				Output: sutD,
			}
		}
		return model.ActionResult{Output: sutD}
	})
}

// HasherCombineAction returns a [model.Action] that draws two
// random byte slices via rapid, hashes each through the SUT to
// produce two correctly-sized digests, then asserts byte-exact
// equivalence between SUT.Combine and reference.Combine over
// those digests. Hashing through the SUT side-steps the digest-
// size matching constraint that [crypto.Hasher.Combine] enforces
// by panic. On failure rapid shrinks to the minimal divergent
// input pair.
func HasherCombineAction() model.Action[crypto.Hasher] {
	return action.Unknown[crypto.Hasher]("Combine", func(rt *model.T, sut, ref crypto.Hasher) model.ActionResult {
		left := model.SliceOfN(model.Byte(), 0, 256).Draw(rt, "left")
		right := model.SliceOfN(model.Byte(), 0, 256).Draw(rt, "right")
		l := sut.Hash(left)
		r := sut.Hash(right)
		sutD := sut.Combine(l, r)
		refD := ref.Combine(l, r)
		if !sutD.Equal(refD) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"combine diverges: sut=%x ref=%x",
					sutD.Bytes(), refD.Bytes(),
				),
				Output: sutD,
			}
		}
		return model.ActionResult{Output: sutD}
	})
}
