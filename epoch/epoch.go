// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

import (
	"cmp"
	"strconv"
)

// Epoch is a strictly-monotonic 64-bit counter. The zero value is
// the conventional "no epoch yet" sentinel; valid producer-issued
// epochs start at 1.
//
// # Comparability
//
// [Epoch] is a [uint64] alias and is therefore comparable with all
// the standard operators (`==`, `<`, `>`, `<=`, `>=`); the
// [Epoch.Compare] method is provided for call sites that prefer a
// three-way result.
//
// # Allocation contract
//
// Value type; pass by value. Construction, comparison, and
// [Successor] are zero-alloc. [String] allocates the result.
type Epoch uint64

// Zero is the reserved zero value. By convention, valid
// producer-issued epochs start at 1; [Zero] means "no epoch
// assigned yet."
const Zero Epoch = 0

// IsZero reports whether e is the reserved [Zero] sentinel.
func (e Epoch) IsZero() bool {
	return e == Zero
}

// Compare returns -1 if e is before other, +1 if after, 0 if
// equal. Equivalent to a three-way comparison on the underlying
// uint64 value.
func (e Epoch) Compare(other Epoch) int {
	return cmp.Compare(e, other)
}

// Successor returns the next [Epoch] in the strictly-monotonic
// sequence (e + 1).
//
// At [math.MaxUint64] the underlying uint64 wraps to zero, which
// breaks monotonicity. The wrap is unreachable in practice — a
// system advancing one epoch per nanosecond would take ~584 years
// to exhaust the range — and is therefore not guarded; consumers
// that genuinely need bounds-checked monotonicity must enforce it
// at a higher layer.
func (e Epoch) Successor() Epoch {
	return e + 1
}

// String returns e as a base-10 string. Diagnostic only;
// allocates the result string.
func (e Epoch) String() string {
	return strconv.FormatUint(uint64(e), 10)
}
