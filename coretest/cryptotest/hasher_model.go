// Copyright ThesmOS B.V. 2026
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

// HasherHashTaggedAction returns a [model.Action] that draws a
// random unary [crypto.Role] and byte slice via rapid and asserts
// byte-exact equivalence between SUT.HashTagged and
// reference.HashTagged. The role is drawn from the unary half only —
// the binary half is a documented panic, and a model action that
// tripped it would be testing the guard rather than the layout,
// which [HasherContractAssertions] already does. On failure rapid
// shrinks to the minimal divergent input.
func HasherHashTaggedAction() model.Action[crypto.Hasher] {
	return action.Unknown[crypto.Hasher]("HashTagged", func(rt *model.T, sut, ref crypto.Hasher) model.ActionResult {
		role := crypto.Role(model.Byte().Draw(rt, "role") &^ 0x80)
		data := model.SliceOfN(model.Byte(), 0, 1024).Draw(rt, "data")
		sutD := sut.HashTagged(role, data)
		refD := ref.HashTagged(role, data)
		if !sutD.Equal(refD) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"tagged hash diverges on role %#02x over %d-byte input: sut=%x ref=%x",
					byte(role), len(data), sutD.Bytes(), refD.Bytes(),
				),
				Output: sutD,
			}
		}
		return model.ActionResult{Output: sutD}
	})
}

// HasherCombineTaggedAction returns a [model.Action] that draws a
// random binary [crypto.Role] and two random byte slices via rapid,
// hashes each slice through the SUT to produce two correctly-sized
// digests, then asserts byte-exact equivalence between
// SUT.CombineTagged and reference.CombineTagged over them. Hashing
// through the SUT side-steps the digest-width constraint
// [crypto.Hasher.CombineTagged] enforces by panic, and the role is
// drawn from the binary half for the same reason. On failure rapid
// shrinks to the minimal divergent input pair.
func HasherCombineTaggedAction() model.Action[crypto.Hasher] {
	return action.Unknown[crypto.Hasher]("CombineTagged", func(rt *model.T, sut, ref crypto.Hasher) model.ActionResult {
		role := crypto.Role(model.Byte().Draw(rt, "role") | 0x80)
		left := model.SliceOfN(model.Byte(), 0, 256).Draw(rt, "left")
		right := model.SliceOfN(model.Byte(), 0, 256).Draw(rt, "right")
		l := sut.Hash(left)
		r := sut.Hash(right)
		sutD := sut.CombineTagged(role, l, r)
		refD := ref.CombineTagged(role, l, r)
		if !sutD.Equal(refD) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"tagged combine diverges on role %#02x: sut=%x ref=%x",
					byte(role), sutD.Bytes(), refD.Bytes(),
				),
				Output: sutD,
			}
		}
		return model.ActionResult{Output: sutD}
	})
}
