// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package fixed provides decimal arithmetic at a fixed scale of
// eight places, stored as one int64 and checked on every operation.
//
// [Fixed64] is what to reach for wherever float64 is the wrong type:
// money, rates, quantities, thresholds — anything that is compared,
// hashed, signed, or persisted and must be the same value on every
// machine that computes it.
//
// # Why not float64
//
// Three independent failures, each sufficient on its own:
//
//   - Representation. 0.1 + 0.2 is 0.30000000000000004. The value a
//     caller typed is not the value stored, and the gap compounds
//     across a ledger.
//   - Architecture. The Go specification permits an implementation to
//     fuse a multiply and an add into one operation with a single
//     rounding. ARM64 does; AMD64 does not. The same expression over
//     the same inputs differs by a unit in the last place, which is
//     enough to flip a comparison at a threshold and enough to make
//     two nodes disagree about a value both computed correctly.
//   - Ordering. Floating-point addition is not associative, so
//     summing a set in a different order gives a different total. Any
//     code that batches, parallelises, or reorders is exposed.
//
// None of these announces itself. They surface as a reconciliation
// off by a cent, a replica that disagrees, or a signature that will
// not verify against a recomputed value.
//
// # Exactness
//
// [Fixed64.Add] and [Fixed64.Sub] are exact: the result is the true
// sum or difference whenever it is in range.
//
// [Fixed64.Mul] and [Fixed64.Div] are not, and this package does not
// claim they are. Eight places is not closed under multiplication —
// [Smallest] times [Smallest] is 10⁻¹⁶, which rounds to [Zero]. What
// they guarantee is that no precision is lost BEFORE the rounding:
// both compute through a 128-bit intermediate, so the result is the
// true value rounded exactly once, in a direction the method name
// states.
//
// # Rounding is named, never configured
//
// Two directions, and the choice is visible in the code that made it
// rather than deferred to a runtime mode:
//
//   - Toward zero — [Fixed64.Mul], [Fixed64.Div], [Fixed64.Round].
//   - Away from zero — [Fixed64.MulAway], [Fixed64.DivAway],
//     [Fixed64.RoundAway].
//
// [Fixed64.Round] and [Fixed64.RoundAway] quantise to a chosen number
// of places, which is what a caller computing at eight places and
// emitting at two needs.
//
// Half-to-even is absent. It is a genuine rounding mode rather than
// an allocation policy, but its benefit — an unbiased mean across
// many roundings — is a reporting decision belonging to the type that
// knows it is reporting. Splitting a value across parts so they sum
// back to the original is a different problem again: it takes a ratio
// set, and how the remainder is handed out is the caller's policy.
//
// # Errors, not panics and not wrapping
//
// Every operation that can fail returns an error. Wrapping is how a
// billing bug becomes a credit; saturating produces a plausible
// number with no signal; panicking is wrong because an overflow is
// reachable from ordinary input rather than being a programmer error.
//
// Every sentinel in this package classifies as
// [go.thesmos.sh/core/errs.Invalid]: the same input will never
// succeed.
//
// # The domain excludes math.MinInt64
//
// [Min] is -[math.MaxInt64], so the domain is symmetric about zero
// and [Fixed64.Neg] and [Fixed64.Abs] cannot fail. One representable
// value is given up to delete an entire class of edge case — see
// [Fixed64.Neg] for the trade and its one sharp edge.
//
// # Encoding
//
// The binary form is 8 bytes, big-endian, two's complement, and is a
// stable wire contract (RFC-0014). The text form renders all eight
// places and round-trips exactly. Because [Fixed64] implements
// [encoding.TextMarshaler], [encoding/json] encodes it as a JSON
// string rather than a number, which keeps the value out of a float64
// on the way back in.
//
// # Allocation contract
//
// [Fixed64] is a value type ([int64]); pass by value, comparable,
// usable as a map key. Construction, inspection, comparison, all
// arithmetic and both rounding families are zero-allocation.
// [Fixed64.String], [Fixed64.MarshalText] and [Fixed64.MarshalBinary]
// each allocate their result; [Fixed64.AppendText] and
// [Fixed64.AppendBinary] allocate nothing when dst has capacity.
//
// # Concurrency
//
// [Fixed64] has no mutable state. Every operation is a pure function
// of its receiver and arguments and is safe for concurrent use.
package fixed
