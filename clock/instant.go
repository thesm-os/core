// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"cmp"
	"encoding/binary"
	"time"
)

// Instant is a Hybrid Logical Clock timestamp: wall-clock time fused
// with a Lamport-style logical counter and origin node identifier so
// events can be totally ordered across nodes even under clock skew.
//
// # When to use Instant
//
// Use Instant for timestamps that participate in a causally-ordered
// chain of events emitted by this deployment — kernel turns, ledger
// entries, lock acquisitions, anything where "did A happen before
// B?" is meaningful even when A and B were minted on different
// nodes.
//
// # When NOT to use Instant
//
// Instant is not "any timestamp we know about." External facts
// (when a third-party API published a model, when a TSA signed an
// RFC 3161 token, when S3 reports Last-Modified), calendar targets
// (wake at 5pm tomorrow, JWT expires at midnight), TTL or lifecycle
// bookkeeping (Session.IssuedAt, Session.ExpiresAt), and pure
// observation (request.Started, request.Ended) do not belong in our
// causal chain. Those fields use stdlib [time.Time].
//
// The practical test: if you would construct the timestamp with
// Logical=0 and Node=0, you do not want Instant — you want
// [time.Time].
//
// # Field semantics
//
//   - Wall: unix nanoseconds. The local node's reading of wall-clock
//     time at the moment the Instant was minted. Advances normally
//     as NTP corrects local time.
//   - Logical: Lamport-style counter that disambiguates events
//     within the same Wall tick. Production HLC implementations
//     reset Logical to 0 (or 1) each time Wall advances.
//   - Node: the minting node. Used as the final tiebreaker when
//     Wall and Logical are equal across two events on different
//     nodes.
//
// # Ordering
//
// Instants are totally ordered by (Wall, Logical, Node):
//
//   - If a.Wall != b.Wall, the higher Wall is later.
//   - If a.Wall == b.Wall and a.Logical != b.Logical, the higher
//     Logical is later.
//   - If a.Wall == b.Wall and a.Logical == b.Logical, Node breaks
//     the tie deterministically.
//
// # Allocation contract
//
// Instant is a value type; comparable; pass by value. Zero alloc.
type Instant struct {
	// Wall is unix nanoseconds (time since 1970-01-01T00:00:00Z UTC).
	Wall int64
	// Logical is the Lamport counter for the current Wall tick.
	// Resets when Wall advances under HLC implementations.
	Logical uint32
	// Node is the identifier of the node that minted this Instant.
	// Used as the final tiebreaker for total ordering.
	Node NodeID
}

// InstantSize is the byte length of the canonical [Instant] binary
// encoding: Wall (8) || Logical (4) || Node (4).
const InstantSize = 16

// AppendBinary appends the canonical 16-byte big-endian encoding of
// i to dst:
//
//	Wall     int64   8 bytes
//	Logical  uint32  4 bytes
//	Node     uint32  4 bytes
//
// The encoding is a stable wire contract. Instants are signed over
// and persisted in artefacts that must verify across builds and
// across years; the layout will not change within a major version.
//
// Field order follows comparison order, so encoded instants sort
// bytewise in the same order [Instant.Compare] ranks them — with one
// exception: Wall is two's-complement, so instants before the Unix
// epoch sort after later ones. Bytewise ordering holds only for
// post-epoch instants.
//
// The error is always nil; it exists to satisfy
// [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [InstantSize] more bytes.
func (i Instant) AppendBinary(dst []byte) ([]byte, error) {
	// G115 false positive: int64 -> uint64 is same-width and
	// bit-preserving, not truncating. UnmarshalBinary reverses it
	// exactly; TestInstantBinaryRoundTrip covers negative Wall.
	dst = binary.BigEndian.AppendUint64(dst, uint64(i.Wall)) //nolint:gosec
	dst = binary.BigEndian.AppendUint32(dst, i.Logical)
	dst = binary.BigEndian.AppendUint32(dst, uint32(i.Node))

	return dst, nil
}

// MarshalBinary returns the canonical [InstantSize]-byte encoding.
// Implements [encoding.BinaryMarshaler].
//
// The error is always nil.
//
// # Allocation contract
//
// One allocation for the returned slice.
//
// Calling this through the [encoding.BinaryMarshaler] interface
// costs a second allocation: Instant is a value type wider than a
// word, so converting it to an interface boxes it onto the heap.
// [encoding.BinaryUnmarshaler] is unaffected — its receiver is an
// *Instant, which is pointer-shaped. Hot paths use
// [Instant.AppendBinary] and avoid both.
func (i Instant) MarshalBinary() ([]byte, error) {
	return i.AppendBinary(make([]byte, 0, InstantSize))
}

// UnmarshalBinary decodes the canonical encoding produced by
// [Instant.AppendBinary]. Returns [ErrInstantSize] unless len(data)
// is exactly [InstantSize]. Implements [encoding.BinaryUnmarshaler].
//
// # Allocation contract
//
// Zero alloc — decodes into the receiver the caller already owns.
func (i *Instant) UnmarshalBinary(data []byte) error {
	if len(data) != InstantSize {
		return ErrInstantSize
	}

	// G115 false positive: reverses AppendBinary's same-width,
	// bit-preserving conversion. See TestInstantBinaryRoundTrip.
	i.Wall = int64(binary.BigEndian.Uint64(data[:8])) //nolint:gosec
	i.Logical = binary.BigEndian.Uint32(data[8:12])
	i.Node = NodeID(binary.BigEndian.Uint32(data[12:16]))

	return nil
}

// UnixMilli returns Wall truncated to milliseconds.
//
// Provided because open-coding Wall/1e6 is a recurring source of
// unit errors — the wrong divisor produces a plausible number rather
// than a failure, and when an instant is packed into a time-sortable
// identifier it silently collapses the representable window.
//
// Truncation is toward zero, so instants before the Unix epoch round
// up rather than down.
func (i Instant) UnixMilli() int64 {
	return i.Wall / 1e6
}

// UnixMicro returns Wall truncated to microseconds. Truncation is
// toward zero; see [Instant.UnixMilli].
func (i Instant) UnixMicro() int64 {
	return i.Wall / 1e3
}

// NodeID identifies a node in the cluster. Used as the final
// tiebreaker in [Instant] ordering.
//
// NodeID 0 is the conventional zero value used by tests and
// single-node deployments where cross-node ordering does not apply.
// Production multi-node deployments assign distinct NodeIDs at
// process start.
type NodeID uint32

// Time returns Wall as a stdlib [time.Time] for ergonomic interop.
// The returned time is in UTC.
func (i Instant) Time() time.Time {
	return time.Unix(0, i.Wall).UTC()
}

// Sub returns the wall-clock elapsed time between earlier and i.
// If earlier is after i, the result is negative.
//
// Sub uses only the Wall component — it measures elapsed real time,
// not causal distance. For causal ordering, use [Instant.HappensBefore].
func (i Instant) Sub(earlier Instant) time.Duration {
	return time.Duration(i.Wall - earlier.Wall)
}

// Add advances the instant's wall-clock component by d and returns
// the new Instant. Logical and Node are preserved.
func (i Instant) Add(d time.Duration) Instant {
	return Instant{
		Wall:    i.Wall + int64(d),
		Logical: i.Logical,
		Node:    i.Node,
	}
}

// Compare returns -1 if i is causally before other in the HLC
// total order, +1 if after, 0 if equal. The comparison is
// lexicographic over (Wall, Logical, Node) — see [Instant] for the
// rationale.
//
// Compare is the canonical ordering primitive; [Instant.HappensBefore]
// is a thin wrapper for callers that only need a boolean.
func (i Instant) Compare(other Instant) int {
	if c := cmp.Compare(i.Wall, other.Wall); c != 0 {
		return c
	}
	if c := cmp.Compare(i.Logical, other.Logical); c != 0 {
		return c
	}
	return cmp.Compare(i.Node, other.Node)
}

// HappensBefore reports whether i is causally before other.
// Equivalent to i.Compare(other) < 0; the boolean shape exists for
// readability at call sites that only ask the directional question.
func (i Instant) HappensBefore(other Instant) bool {
	return i.Compare(other) < 0
}

// IsZero reports whether i is the zero Instant — Wall, Logical, and
// Node all zero. The zero Instant means "no time stamped" and is
// distinguishable from a real instant whose Wall happens to be 0
// (1970-01-01) by checking the entire value.
func (i Instant) IsZero() bool {
	return i.Wall == 0 && i.Logical == 0 && i.Node == 0
}

// InstantRange is a half-open interval [Since, Until) over [Instant].
// Use it where a filter or query expresses "events between two
// causal points" rather than carrying two parallel Instant fields.
//
// # Open-ended endpoints
//
// A zero [Instant] in either endpoint means "no bound on that side":
//
//   - Since == zero: no lower bound (matches everything before Until).
//   - Until == zero: no upper bound (matches everything from Since on).
//
// The zero InstantRange (both fields zero) therefore matches every
// Instant — useful as the default for "all events".
//
// # Composability
//
// Half-open semantics let adjacent ranges compose without overlap or
// gap: [A, B) followed by [B, C) covers [A, C) exactly once.
//
// # Allocation contract
//
// Value type; pass by value. Zero alloc.
type InstantRange struct {
	// Since is the inclusive lower bound of the range. The zero
	// [Instant] disables the lower bound (matches everything
	// before [InstantRange.Until]).
	Since Instant
	// Until is the exclusive upper bound of the range. The zero
	// [Instant] disables the upper bound (matches everything from
	// [InstantRange.Since] onward).
	Until Instant
}

// Contains reports whether i falls within the range. The interval
// is half-open: Since is inclusive, Until is exclusive. A zero
// endpoint disables that bound (see [InstantRange]).
func (r InstantRange) Contains(i Instant) bool {
	if !r.Since.IsZero() && i.HappensBefore(r.Since) {
		return false
	}
	if !r.Until.IsZero() && !i.HappensBefore(r.Until) {
		return false
	}
	return true
}

// IsZero reports whether r is the zero InstantRange — both
// endpoints zero, matching every Instant.
func (r InstantRange) IsZero() bool {
	return r.Since.IsZero() && r.Until.IsZero()
}
