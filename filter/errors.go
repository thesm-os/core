// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors. [ErrBitsPerKey],
// [ErrTooLarge], [ErrFingerprintBits] and [ErrBuild] classify as
// [errs.Invalid]: the same arguments fail the same way on every call.
// [ErrCorrupt] classifies as [errs.Integrity]: an encoding failed its checks.
var (
	// ErrBitsPerKey reports a number of bits per key that [NewBloom]
	// refuses: NaN, at most 0, or more than 256.
	ErrBitsPerKey = errs.WithClass(errors.New("filter: bits per key out of range"), errs.Invalid)

	// ErrTooLarge is returned by [NewBloom] for a filter of more than 2^32
	// blocks. [FuseBuilder.Append] and [FuseSize] return it for a set of
	// hashes whose fuse filter has more than 2^32 fingerprints.
	ErrTooLarge = errs.WithClass(errors.New("filter: filter too large"), errs.Invalid)

	// ErrFingerprintBits is returned by [FuseBuilder.Append] and [FuseSize]
	// for a width of fingerprints other than 4, 8, 10, 12 or 16 bits.
	ErrFingerprintBits = errs.WithClass(errors.New("filter: unsupported fingerprint width"), errs.Invalid)

	// ErrBuild reports a set of hashes that none of the 1,024 seeds of
	// [FuseBuilder.Append] builds into a filter. The builder takes its seeds
	// from a fixed sequence, so the same set fails again.
	ErrBuild = errs.WithClass(errors.New("filter: no seed yields a filter"), errs.Invalid)

	// ErrCorrupt reports an encoding that [ParseFuse] refuses: an unknown
	// version, an unsupported width of fingerprints, a segment exponent
	// outside 2 to 18, a length that does not match the header, or a padding
	// byte that is not zero.
	ErrCorrupt = errs.WithClass(errors.New("filter: corrupt fuse encoding"), errs.Integrity)
)
