// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import "encoding/binary"

// Domain is the versioned domain-separation tag prefixing every framed
// byte sequence.
//
// Version makes layout evolution safe: a protocol that changes any
// field's width, order, or framing increments Version, and artefacts
// written under the old layout remain verifiable because the domain
// bytes differ. Without it, a field's width is frozen at the moment
// the first artefact is signed — widening it silently changes what
// old signatures cover, and nothing detects the mismatch.
//
// Name is the protocol's own vocabulary; this package ships no domain
// constants.
type Domain struct {
	// Name identifies the protocol and the role within it. Any
	// length: it is length-prefixed in the encoding, so no two
	// distinct names produce the same tag.
	Name string

	// Version is incremented whenever any field's width, order, or
	// framing changes.
	Version uint16
}

// Framer appends framed fields to a byte slice, producing the exact
// bytes a protocol hashes or signs.
//
// The contract is that no two distinct field sequences produce the
// same bytes. Variable-width fields are length-prefixed;
// fixed-width fields are not, because their width is a constant of
// the protocol — and when that width changes, [Domain.Version]
// changes with it.
//
// Framer has no decode half: a sequence is built, hashed, and
// discarded; verifiers rebuild it from the artefact's own fields.
//
// No method can fail, so none returns an error. The zero Framer is
// not usable — construct one with [NewFramer].
//
//	f := crypto.NewFramer(buf[:0], crypto.Domain{Name: "entry", Version: 1})
//	f.Fixed(prev.Bytes())
//	f.Uint64(seq)
//	f.Bytes(payload)
//	d := h.Hash(f.Frame())
//
// # Allocation contract
//
// Framer allocates nothing only when dst already has capacity for the
// finished sequence. That condition is the whole contract, and it is
// not the default: a Framer built on a nil buffer grows once per
// field, so a four-field sequence costs four allocations rather than
// one.
//
// The buffer must therefore be sized before [NewFramer] writes the
// domain tag — growing it afterwards cannot recover the growth the
// tag has already caused. Two call sites get it right:
//
//	f := crypto.NewFramer(buf[:0], d)                 // 0 allocations
//	f := crypto.NewFramer(make([]byte, 0, 128), d)    // 1 allocation
//
// Reuse a buffer across calls — pass buf[:0], or draw it from a
// [go.thesmos.sh/core/pool.Pool] — and the sequence costs nothing per
// call. Where reuse is impossible, sizing the buffer at construction
// collapses the growth to a single allocation.
//
// This is the same trade [strings.Builder] and [bytes.Buffer] make.
type Framer struct {
	dst []byte
}

// NewFramer begins a framed sequence in dst under d, writing the
// domain tag immediately.
//
// The tag encodes as uint64(len(d.Name)) || d.Name || uint16(Version),
// so the name needs no length limit and cannot be truncated into a
// collision with another name sharing its prefix.
//
// dst is appended to, not overwritten; pass buf[:0] to reuse a
// buffer's capacity.
func NewFramer(dst []byte, d Domain) Framer {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(d.Name)))
	dst = append(dst, d.Name...)
	dst = binary.BigEndian.AppendUint16(dst, d.Version)

	return Framer{dst: dst}
}

// Fixed appends p verbatim, with no length prefix.
//
// Use only for fields whose width is a constant of the protocol — a
// [Digest], an identifier, a fixed-size tag. Calling Fixed on a
// variable-width field reintroduces exactly the ambiguity this type
// removes, and the result is a silent collision rather than an error.
//
// When a fixed field's width changes, increment [Domain.Version].
func (f *Framer) Fixed(p []byte) {
	f.dst = append(f.dst, p...)
}

// Bytes appends p with a big-endian uint64 length prefix. Use for
// every field whose width is not a constant of the protocol.
func (f *Framer) Bytes(p []byte) {
	f.dst = binary.BigEndian.AppendUint64(f.dst, uint64(len(p)))
	f.dst = append(f.dst, p...)
}

// String appends s with a big-endian uint64 length prefix, as
// [Framer.Bytes] does for a byte slice.
func (f *Framer) String(s string) {
	f.dst = binary.BigEndian.AppendUint64(f.dst, uint64(len(s)))
	f.dst = append(f.dst, s...)
}

// Uint64 appends v as 8 big-endian bytes with no length prefix — its
// width is a constant.
func (f *Framer) Uint64(v uint64) {
	f.dst = binary.BigEndian.AppendUint64(f.dst, v)
}

// Uint32 appends v as 4 big-endian bytes with no length prefix — its
// width is a constant.
func (f *Framer) Uint32(v uint32) {
	f.dst = binary.BigEndian.AppendUint32(f.dst, v)
}

// Frame returns the accumulated bytes, ready to hash or sign.
//
// The returned slice aliases the Framer's buffer; it is valid until
// the next append to that buffer. Callers that retain it past the
// next use of the underlying array must copy.
func (f *Framer) Frame() []byte {
	return f.dst
}
