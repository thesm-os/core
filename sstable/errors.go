// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors.
var (
	// ErrOptions reports writer options that [NewWriter] refuses. NewWriter
	// refuses a BlockSize below 0 or above 2^32 - 1 and a negative
	// RestartInterval. It also refuses an unknown Compression and a
	// FilterBits that is neither 0 nor a width of the fingerprints of a
	// [filter.Fuse]. It classifies as [errs.Invalid].
	ErrOptions = errs.WithClass(errors.New("sstable: invalid writer options"), errs.Invalid)

	// ErrOrder is returned by [Writer.Add] for an entry at or before the
	// previous entry in the order of [Compare]. It classifies as errs.Invalid.
	ErrOrder = errs.WithClass(errors.New("sstable: the entry does not follow the previous entry"), errs.Invalid)

	// ErrClosed reports a call of Writer.Add or [Writer.Close] after Close
	// wrote the table. It classifies as errs.Invalid.
	ErrClosed = errs.WithClass(errors.New("sstable: the writer is closed"), errs.Invalid)

	// ErrTooLarge is returned by Writer.Add for a key that could bring the
	// index of the table past 2^32 - 1 bytes. It classifies as errs.Invalid.
	ErrTooLarge = errs.WithClass(errors.New("sstable: the key does not fit the index of the table"), errs.Invalid)

	// ErrCorrupt is returned by [Open], [Reader.Get], [Reader.Span] and the
	// moves of an [Iter] for a table that fails a checksum, a decompression
	// or its layout. It classifies as [errs.Integrity].
	ErrCorrupt = errs.WithClass(errors.New("sstable: corrupt table"), errs.Integrity)
)
