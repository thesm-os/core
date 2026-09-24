// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"encoding/binary"
	"iter"
	"math"
)

// AppendBundleEntry appends entry to a C2SP entry bundle, a big-endian
// uint16 length followed by the entry, and returns the extended slice.
//
// Returns dst unchanged and [ErrEntrySize] for an entry longer than
// 65,535 bytes.
//
// # Allocation contract
//
// Allocates only to grow dst.
func AppendBundleEntry(dst, entry []byte) ([]byte, error) {
	if len(entry) > math.MaxUint16 {
		return dst, ErrEntrySize
	}

	dst = binary.BigEndian.AppendUint16(dst, uint16(len(entry))) //nolint:gosec // G115: checked against MaxUint16 above

	return append(dst, entry...), nil
}

// BundleEntries iterates over the entries of a bundle. Each entry
// aliases data and has no spare capacity. It yields [ErrBundle] and
// stops at a length that runs past the end of data.
//
// # Allocation contract
//
// One closure per call. Iterating allocates nothing.
func BundleEntries(data []byte) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		rest := data
		for len(rest) > 0 {
			if len(rest) < 2 {
				yield(nil, ErrBundle)

				return
			}

			n := 2 + int(binary.BigEndian.Uint16(rest))
			if len(rest) < n {
				yield(nil, ErrBundle)

				return
			}

			if !yield(rest[2:n:n], nil) {
				return
			}
			rest = rest[n:]
		}
	}
}

// BundlePath returns the path of the entry bundle at index with the
// given width, relative to the log's prefix: tile/entries/<N>[.p/<W>],
// where N is written as in [Tile.Path]. A bundle of width 256 has no
// .p/<W> suffix. BundlePath assumes a width of 1 to 256.
//
// # Allocation contract
//
// One allocation for the string.
func BundlePath(index uint64, width uint16) string {
	var buf [64]byte

	return string(appendIndex(append(buf[:0], entriesPrefix...), index, width))
}
