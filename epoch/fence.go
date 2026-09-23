// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

import "sync/atomic"

// Admissible reports whether a write bearing fence e may proceed
// against watermark w, per the admit-equal law: true iff e is [Zero]
// (the caller is not fencing this write) or e >= w.
//
// Admit-equal rather than strictly-greater, because one authority
// performs many writes per tenure — including multi-write operations
// under a single fence, such as an epoch rotation that commits two
// writes. Strictly-greater admission would break on the second.
//
// Admissible is pure. The atomicity obligation — validate and
// advance in the same step as the write — belongs to the caller; a
// check-then-write gap reintroduces the race fencing exists to
// close.
//
// # Precondition
//
// Admit-equal is sound only when the issuer grants each epoch to at
// most one holder. Two holders granted one epoch both pass, and
// nothing in an Epoch tells them apart. Take fence epochs from a
// source that is unique per grant, such as the revision a
// linearizable coordination service assigns to the grant: etcd's
// revision of the election key meets this. A term from an election
// that can grant one term twice does not.
//
// # Allocation contract
//
// Zero alloc.
func Admissible(w, e Epoch) bool {
	return e == Zero || e >= w
}

// Watermark tracks the highest fence epoch admitted, applying the
// three fence laws: admit-equal, advance-on-greater, zero-bypass.
// It has [Admissible]'s precondition: each epoch is granted to at
// most one holder.
//
// Watermark is the adapter kit, not the authority. An in-memory
// adapter may use it as its whole implementation; a durable adapter
// uses it as the in-process cache of a mark it persists atomically
// with each admitted write — validation against volatile state alone
// is out of contract, because authority must survive restart.
// [NewWatermark] takes the seed for exactly that reason: a
// zero-seeded Watermark after restart admits every zombie that ever
// held the scope. Load the seed from the same durable state the
// writes go to.
//
// # Concurrency
//
// Safe for concurrent use. [Watermark.Admit] is lock-free.
//
// # Allocation contract
//
// Pointer-allocated once at construction; [Watermark.Admit] and
// [Watermark.Current] are zero-alloc.
type Watermark struct {
	current atomic.Uint64
}

// NewWatermark returns a Watermark seeded at seed.
func NewWatermark(seed Epoch) *Watermark {
	w := &Watermark{}
	w.current.Store(uint64(seed))

	return w
}

// Admit applies the fence laws to e: nil and no advance when e is
// [Zero]; nil, advancing the watermark, when e is at or above it;
// [ErrFenced] when e is behind it.
//
// The advance is a CAS loop, so concurrent admits lose no update:
// racing epochs converge on the highest, and an admit that observes
// itself overtaken mid-loop reports [ErrFenced] — which is correct,
// because at the moment of decision its authority was already
// superseded.
func (w *Watermark) Admit(e Epoch) error {
	if e == Zero {
		return nil
	}

	for {
		cur := Epoch(w.current.Load())

		if e < cur {
			return ErrFenced
		}

		if e == cur {
			return nil
		}

		if w.current.CompareAndSwap(uint64(cur), uint64(e)) {
			return nil
		}
	}
}

// Current returns the highest epoch admitted so far, or the seed.
func (w *Watermark) Current() Epoch {
	return Epoch(w.current.Load())
}
