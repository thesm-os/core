// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"cmp"
	"encoding/binary"
	"time"
)

// Instant is a Hybrid Logical Clock timestamp: a wall-clock reading
// with a Lamport-style logical counter and the identifier of the node
// that issued it, so the events of different nodes have a total order
// even under clock skew.
//
// # When to use Instant
//
// An Instant stamps an event of a causally ordered chain of events of
// one deployment, such as a log entry, a lock acquisition or a state
// transition: any event for which "did A happen before B?" matters
// when different nodes issued A and B.
//
// # When not to use Instant
//
// Timestamps outside the causal chain are [time.Time] values:
//
//   - External facts, such as the time a third party published a
//     document, the time of an RFC 3161 timestamp token, or an S3
//     Last-Modified time.
//   - Calendar targets, such as a wake-up time or the expiry of a JWT.
//   - Lifetimes, such as the issue and expiry times of a session.
//   - Observations, such as the start and the end of a request.
//
// A timestamp whose Logical and Node would always be 0 is a
// [time.Time], not an Instant.
//
// # Field semantics
//
//   - Wall: Unix nanoseconds, the node's reading of wall-clock time
//     when it issued the Instant. NTP corrections move it.
//   - Logical: a Lamport-style counter that orders the events of one
//     Wall tick. HLC implementations reset it to 0 or 1 when Wall
//     advances.
//   - Node: the node that issued the Instant. It breaks the tie
//     between events of different nodes with equal Wall and Logical.
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
// Instant is a comparable value type. Zero alloc.
type Instant struct {
	// Wall is Unix nanoseconds, the time since 1970-01-01T00:00:00Z.
	Wall int64
	// Logical is the Lamport counter of the current Wall tick. HLC
	// implementations reset it when Wall advances.
	Logical uint32
	// Node identifies the node that issued the Instant, and breaks the
	// last tie of the total order.
	Node NodeID
}

// InstantSize is the length in bytes of the binary form of an
// [Instant]: Wall (8), Logical (4) and Node (4).
const InstantSize = 16

// AppendBinary appends the binary form of i to dst, 16 bytes
// big-endian:
//
//	Wall     int64   8 bytes
//	Logical  uint32  4 bytes
//	Node     uint32  4 bytes
//
// The binary form never changes, so an Instant that a consumer signs
// or persists verifies and reads back in every build. A kanon record
// stores the same 16 bytes.
//
// The fields follow the order of comparison, so the binary forms of
// two instants at or after the Unix epoch sort bytewise as
// [Instant.Compare] orders the instants. Wall is two's complement, so
// an instant before the epoch sorts after every later one.
//
// AppendBinary appends what [Instant.AppendKanon] appends, and the error
// is always nil. It satisfies [encoding.BinaryAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [InstantSize] more bytes.
func (i Instant) AppendBinary(dst []byte) ([]byte, error) {
	return i.AppendKanon(dst), nil
}

// MarshalBinary returns the binary form of i, which
// [Instant.AppendBinary] describes. The error is always nil.
// Implements [encoding.BinaryMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice.
//
// A call through the [encoding.BinaryMarshaler] interface costs a
// second allocation, because an Instant is wider than a word and
// converting it to an interface copies it to the heap.
// [encoding.BinaryUnmarshaler] does not add an allocation, because its
// receiver is an *Instant, which is pointer-shaped. Hot paths call
// [Instant.AppendBinary] and avoid both.
func (i Instant) MarshalBinary() ([]byte, error) {
	return i.AppendBinary(make([]byte, 0, InstantSize))
}

// UnmarshalBinary sets i to the instant whose binary form is data.
// Returns [ErrInstantSize] unless len(data) is [InstantSize].
// Implements [encoding.BinaryUnmarshaler].
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (i *Instant) UnmarshalBinary(data []byte) error {
	if len(data) != InstantSize {
		return ErrInstantSize
	}

	// The conversion reverses the one in AppendBinary and keeps the
	// bit pattern, so gosec's G115 does not apply.
	i.Wall = int64(binary.BigEndian.Uint64(data[:8])) //nolint:gosec // the bit pattern of AppendBinary
	i.Logical = binary.BigEndian.Uint32(data[8:12])
	i.Node = NodeID(binary.BigEndian.Uint32(data[12:16]))

	return nil
}

// SizeKanon returns [InstantSize], the length of the binary form of
// every Instant. kanon's generated code sizes an instant with it and
// writes the binary form once, in place. Implements
// [go.thesmos.sh/kanon.Sizer].
//
// # Allocation contract
//
// Zero alloc.
func (Instant) SizeKanon() int {
	return InstantSize
}

// ExactKanon declares Instant a [go.thesmos.sh/kanon.Exact] type. kanon's
// generated code then writes an Instant field without an error path, and a
// canonical decode does not encode the decoded Instant a second time. No
// code calls the method.
//
// An Instant keeps the two guarantees of the declaration:
//
//   - [Instant.AppendBinary] returns no error and appends
//     [Instant.SizeKanon] bytes, [InstantSize], for every Instant.
//   - [Instant.UnmarshalBinary] accepts only the [InstantSize] bytes that
//     AppendBinary writes for the Instant that it decodes.
func (Instant) ExactKanon() {}

// AppendKanon appends the binary form of i that [Instant.AppendBinary]
// describes to dst, and returns the extended slice. It has no error
// result, because every Instant has a binary form, so kanon's generated
// code writes an Instant through it without an error path in every
// position, the elements of a slice included. Implements
// [go.thesmos.sh/kanon.Appender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for [InstantSize] more bytes.
func (i Instant) AppendKanon(dst []byte) []byte {
	// The conversion keeps the bit pattern of the int64, so gosec's
	// G115 does not apply. UnmarshalBinary reverses it, and
	// TestInstantBinaryRoundTrip covers a negative Wall.
	dst = binary.BigEndian.AppendUint64(dst, uint64(i.Wall)) //nolint:gosec // the bit pattern of the int64
	dst = binary.BigEndian.AppendUint32(dst, i.Logical)
	dst = binary.BigEndian.AppendUint32(dst, uint32(i.Node))

	return dst
}

// UnixMilli returns Wall truncated toward zero to milliseconds, so an
// instant before the Unix epoch rounds up.
func (i Instant) UnixMilli() int64 {
	return i.Wall / 1e6
}

// UnixMicro returns Wall truncated toward zero to microseconds, as
// [Instant.UnixMilli] truncates to milliseconds.
func (i Instant) UnixMicro() int64 {
	return i.Wall / 1e3
}

// NodeID identifies a node of a cluster, and breaks the last tie of
// the order of [Instant].
//
// NodeID 0 is the zero value, which tests and single-node deployments
// use, because they order no events across nodes. A multi-node
// deployment assigns each process a distinct NodeID when it starts.
type NodeID uint32

// Time returns Wall as a [time.Time] in UTC.
func (i Instant) Time() time.Time {
	return time.Unix(0, i.Wall).UTC()
}

// Sub returns the wall-clock time from earlier to i, which is negative
// when earlier is after i.
//
// Sub reads only Wall, so it measures elapsed real time and not causal
// distance. [Instant.HappensBefore] orders instants causally.
func (i Instant) Sub(earlier Instant) time.Duration {
	return time.Duration(i.Wall - earlier.Wall)
}

// Add returns i with its Wall advanced by d, and its Logical and Node
// unchanged.
func (i Instant) Add(d time.Duration) Instant {
	return Instant{
		Wall:    i.Wall + int64(d),
		Logical: i.Logical,
		Node:    i.Node,
	}
}

// Compare returns -1 if i is before other in the total order of HLC
// instants, +1 if after, 0 if equal. It compares (Wall, Logical, Node)
// lexicographically, as [Instant] describes.
//
// Compare is the ordering primitive, and [Instant.HappensBefore]
// reports its result as a boolean.
func (i Instant) Compare(other Instant) int {
	if c := cmp.Compare(i.Wall, other.Wall); c != 0 {
		return c
	}
	if c := cmp.Compare(i.Logical, other.Logical); c != 0 {
		return c
	}
	return cmp.Compare(i.Node, other.Node)
}

// HappensBefore reports whether i is causally before other, which is
// i.Compare(other) < 0.
func (i Instant) HappensBefore(other Instant) bool {
	return i.Compare(other) < 0
}

// IsZero reports whether i is the zero Instant, whose Wall, Logical
// and Node are all zero, and which means "no time stamped". IsZero
// checks all three fields, so an instant at Wall 0, 1970-01-01, whose
// Logical or Node is not zero is not the zero Instant.
func (i Instant) IsZero() bool {
	return i.Wall == 0 && i.Logical == 0 && i.Node == 0
}

// InstantRange is a half-open interval [Since, Until) over [Instant],
// for a filter or a query of the events between two causal points.
//
// # Open-ended endpoints
//
// A zero [Instant] at either endpoint removes the bound on that side:
//
//   - Since is zero: no lower bound, so the range contains every
//     instant before Until.
//   - Until is zero: no upper bound, so the range contains every
//     instant from Since on.
//
// The zero InstantRange, with both fields zero, contains every
// Instant. It is the default range for all events.
//
// # Composability
//
// Adjacent half-open ranges neither overlap nor leave a gap: [A, B)
// and [B, C) cover [A, C) exactly once.
//
// # Allocation contract
//
// InstantRange is a value type. Zero alloc.
type InstantRange struct {
	// Since is the inclusive lower bound of the range. A zero
	// [Instant] removes the lower bound, so the range contains every
	// instant before [InstantRange.Until].
	Since Instant
	// Until is the exclusive upper bound of the range. A zero
	// [Instant] removes the upper bound, so the range contains every
	// instant from [InstantRange.Since] on.
	Until Instant
}

// Contains reports whether i is within the range: at or after Since,
// and before Until. A zero endpoint removes its bound, as
// [InstantRange] describes.
func (r InstantRange) Contains(i Instant) bool {
	if !r.Since.IsZero() && i.HappensBefore(r.Since) {
		return false
	}
	if !r.Until.IsZero() && !i.HappensBefore(r.Until) {
		return false
	}
	return true
}

// IsZero reports whether r is the zero InstantRange, with both
// endpoints zero, which contains every Instant.
func (r InstantRange) IsZero() bool {
	return r.Since.IsZero() && r.Until.IsZero()
}
