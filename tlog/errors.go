// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
var (
	// ErrProof is returned by [VerifyInclusion] and [VerifyConsistency]
	// when a proof does not recompute the root, and by
	// [TileVerifier.Verify] when a tile or the leaf does not match the
	// root. It classifies as errs.Integrity.
	ErrProof = errs.WithClass(errors.New("tlog: proof does not verify"), errs.Integrity)

	// ErrRange is returned for an index or a size outside the tree, and
	// by a [TileReader] from [BlobTiles] given fewer buffers than tiles.
	// It classifies as errs.Invalid.
	ErrRange = errs.WithClass(errors.New("tlog: index or size out of range"), errs.Invalid)

	// ErrTilePath is returned by [ParseTilePath] for a path that names
	// no tile. It classifies as errs.Invalid.
	ErrTilePath = errs.WithClass(errors.New("tlog: malformed tile path"), errs.Invalid)

	// ErrTileSize is returned when tile data read from storage is not
	// the tile's width times the digest size. It classifies as
	// errs.Integrity.
	ErrTileSize = errs.WithClass(errors.New("tlog: tile data has the wrong length"), errs.Integrity)

	// ErrLeafSize is returned by [Builder.Integrate] for a leaf hash
	// whose size differs from the digest size of the Builder's hasher.
	// It classifies as errs.Invalid.
	ErrLeafSize = errs.WithClass(errors.New("tlog: leaf hash has the wrong size"), errs.Invalid)

	// ErrEntrySize is returned by [AppendBundleEntry] for an entry
	// longer than 65,535 bytes. It classifies as errs.Invalid.
	ErrEntrySize = errs.WithClass(errors.New("tlog: bundle entry longer than 65535 bytes"), errs.Invalid)

	// ErrBundle is yielded by [BundleEntries] for an entry bundle whose
	// lengths run past its data, and for one with more than
	// [MaxBundleEntries] entries. It classifies as errs.Integrity.
	ErrBundle = errs.WithClass(errors.New("tlog: malformed entry bundle"), errs.Integrity)

	// ErrStale is returned by [Builder.Commit] for an [Update] that was
	// not computed from the Builder's current tree. It classifies as
	// errs.Conflict.
	ErrStale = errs.WithClass(errors.New("tlog: update computed from another tree"), errs.Conflict)

	// ErrConfig is returned by [NewProver] for a [ProverConfig] without a
	// Hasher or Tiles, with a Hasher whose digests have none of the sizes of
	// a digest, or with a Capacity below one node tile. It classifies as
	// errs.Invalid.
	ErrConfig = errs.WithClass(errors.New("tlog: invalid prover configuration"), errs.Invalid)
)
