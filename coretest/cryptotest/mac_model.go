// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"fmt"
	"hash"

	"go.thesmos.sh/testkit/model"
	"go.thesmos.sh/testkit/model/action"

	"go.thesmos.sh/core/crypto"
)

// StdlibMACSpec describes a Go stdlib-backed HMAC reference for
// model-driven cross-stdlib byte-equivalence testing. Used to
// parameterise [NewStdlibMACStub]: the per-impl test supplies
// the algorithm-of-record values plus the same key the SUT was
// constructed with, so the model framework drives byte-exact
// SUT-vs-stdlib equivalence across rapid-generated random
// inputs.
type StdlibMACSpec struct {
	// Algorithm is the long-term cross-build algorithm name.
	Algorithm crypto.Algorithm

	// ID is the stable build-local identifier the reference
	// reports through [crypto.MAC.ID].
	ID crypto.ID

	// Size is the MAC output size in bytes (one of
	// [crypto.DigestSize256], [crypto.DigestSize384],
	// [crypto.DigestSize512]).
	Size int

	// Key is the HMAC key — must match the key the SUT was
	// constructed with for byte-exact equivalence.
	Key []byte

	// NewHash returns a fresh stdlib [hash.Hash] for the
	// underlying digest function (e.g. [crypto/sha256.New]).
	// Wrapped in [crypto/hmac.New] internally.
	NewHash func() hash.Hash
}

// MACSignAction returns a [model.Action] that draws a random
// byte slice via rapid and asserts byte-exact equivalence
// between SUT.Sign(data) and reference.Sign(data). On failure
// rapid shrinks to the minimal divergent input.
func MACSignAction() model.Action[crypto.MAC] {
	return action.Unknown[crypto.MAC]("Sign", func(rt *model.T, sut, ref crypto.MAC) model.ActionResult {
		data := model.SliceOfN(model.Byte(), 0, 1024).Draw(rt, "data")
		sutD := sut.Sign(data)
		refD := ref.Sign(data)
		if !sutD.Equal(refD) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"sign diverges on %d-byte input: sut=%x ref=%x",
					len(data), sutD.Bytes(), refD.Bytes(),
				),
				Output: sutD,
			}
		}
		return model.ActionResult{Output: sutD}
	})
}

// MACVerifyAction returns a [model.Action] that draws a random
// byte slice and exercises Verify in two cases: with the
// canonical MAC (must accept on both SUT and ref) and with a
// bit-flipped MAC (must reject on both SUT and ref). Locks the
// "Verify accepts iff bytes match Sign output" property
// across rapid-generated inputs.
func MACVerifyAction() model.Action[crypto.MAC] {
	return action.Unknown[crypto.MAC]("Verify", func(rt *model.T, sut, ref crypto.MAC) model.ActionResult {
		data := model.SliceOfN(model.Byte(), 0, 1024).Draw(rt, "data")
		canonical := sut.Sign(data).Bytes()

		if !sut.Verify(data, canonical) || !ref.Verify(data, canonical) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"verify rejected canonical MAC for %d-byte input",
					len(data),
				),
			}
		}

		tampered := bytes.Clone(canonical)
		tampered[0] ^= 0x01
		if sut.Verify(data, tampered) || ref.Verify(data, tampered) {
			return model.ActionResult{
				Err: fmt.Errorf(
					"verify accepted a tampered MAC for %d-byte input",
					len(data),
				),
			}
		}
		return model.ActionResult{}
	})
}
