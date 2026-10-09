// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"fmt"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog"
)

// consistent returns nil when the checkpoint of size and root extends the
// committed checkpoint of oldSize and oldRoot under h, with proof, as
// check 7 of the protocol requires.
//
// The rules of tlog-witness apply in this order:
//   - A checkpoint of size 0 has the root of the empty tree, tlog.Root(h,
//     nil).
//   - An old size of 0 needs an empty proof, because every tree extends
//     the empty tree.
//   - Otherwise [tlog.VerifyConsistency] checks the proof, which requires
//     an empty proof and equal roots for equal sizes.
//
// Returns an error that wraps [ErrInconsistent] for a checkpoint that
// breaks a rule. The caller checks that oldSize is the committed size and
// at most size, and the sizes of the hashes, before it calls consistent.
//
// # Allocation contract
//
// Zero-alloc for a consistent checkpoint, apart from what the hasher
// allocates.
func consistent(h crypto.Hasher, oldSize uint64, oldRoot crypto.Digest, size uint64, root crypto.Digest,
	proof []crypto.Digest,
) error {
	if size == 0 && !root.Equal(tlog.Root(h, nil)) {
		return fmt.Errorf("%w: a tree of size 0 whose root is not the root of the empty tree", ErrInconsistent)
	}

	if oldSize == 0 {
		if len(proof) != 0 {
			return fmt.Errorf("%w: a proof from the old size 0", ErrInconsistent)
		}

		return nil
	}

	if err := tlog.VerifyConsistency(h, oldSize, size, oldRoot, root, proof); err != nil {
		return fmt.Errorf("%w: the proof from %d to %d: %w", ErrInconsistent, oldSize, size, err)
	}

	return nil
}
