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
	// [filter.Fuse], and a Prefix without FilterBits or with an empty name.
	// [NewFragmenter] returns it for a nil [RangeDeleter]. It classifies as
	// [errs.Invalid].
	ErrOptions = errs.WithClass(errors.New("sstable: invalid writer options"), errs.Invalid)

	// ErrOrder is returned by [Writer.Add] for an entry at or before the
	// previous entry in the order of [Compare]. [Writer.AddRangeDelete]
	// returns it for a fragment that does not follow the fragment before it,
	// and [Fragmenter.Add] for a range tombstone that starts before the start
	// of the one before it. It classifies as errs.Invalid.
	ErrOrder = errs.WithClass(errors.New("sstable: the entry does not follow the previous entry"), errs.Invalid)

	// ErrClosed reports a call of Writer.Add, Writer.AddRangeDelete or
	// [Writer.Close] after Close wrote the table. It classifies as
	// errs.Invalid.
	ErrClosed = errs.WithClass(errors.New("sstable: the writer is closed"), errs.Invalid)

	// ErrTooLarge is returned by Writer.Add for a key that could bring the
	// index of the table past 2^32 - 1 bytes, and by Writer.AddRangeDelete
	// for a fragment that could bring the block of fragments past that size.
	// It classifies as errs.Invalid.
	ErrTooLarge = errs.WithClass(errors.New("sstable: the key does not fit the index of the table"), errs.Invalid)

	// ErrRange is returned by Writer.AddRangeDelete and Fragmenter.Add for a
	// range tombstone whose end is not after its start. It classifies as
	// errs.Invalid.
	ErrRange = errs.WithClass(errors.New("sstable: the end of the range is not after its start"), errs.Invalid)

	// ErrPrefix is returned by Writer.Add for a prefix of [PrefixExtractor]
	// that is not a byte prefix of its key, or that is before the prefix of
	// an earlier key in the order of bytes.Compare. It classifies as
	// errs.Invalid.
	ErrPrefix = errs.WithClass(errors.New("sstable: the prefix does not follow the prefix of its key"), errs.Invalid)

	// ErrCorrupt is returned by [Open], [Reader.Get], [Reader.Span],
	// [Reader.MayContainPrefix] and the moves of an [Iter] for a table that
	// fails a checksum, a decompression or its layout. It classifies as
	// [errs.Integrity].
	ErrCorrupt = errs.WithClass(errors.New("sstable: corrupt table"), errs.Integrity)
)
