// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import "io"

// XOF is an extendable-output function: a hash whose output length is
// chosen by the caller rather than fixed by the algorithm.
//
// Unlike [Hasher], an XOF has no natural digest — the output is a
// stream, and reading more of it extends the same stream rather than
// starting a new one. Callers needing a fixed-size commitment read
// exactly that many bytes and build a [Digest] with [DigestFromBytes].
//
// Extendable output is what key derivation, post-quantum parameter
// expansion, and deterministic sampling are built on. In each case the
// caller decides how many bytes it needs, which is precisely what
// [Hasher] cannot express.
//
// # Concurrency
//
// An XOF is safe for concurrent use; the streams it returns are not.
// Each goroutine takes its own [XOF.NewXOFStream].
//
// # Allocation contract
//
// [XOF.ID] and [XOF.Algorithm] allocate nothing. NewXOFStream
// allocates the underlying sponge state once.
type XOF interface {
	// ID is the build-local implementation identifier.
	ID() ID

	// Algorithm is the long-term, cross-build name. Persist it
	// alongside anything squeezed from this function.
	Algorithm() Algorithm

	// NewXOFStream returns an absorbing/squeezing stream. Write
	// absorbs; Read squeezes. Reading continues one output stream;
	// writing after a read returns [ErrXOFSqueezing].
	NewXOFStream() XOFStream
}

// XOFStream absorbs input and squeezes output.
//
// The method set is the standard library's SHAKE type verbatim, so
// the shape is familiar and an implementation is a thin forwarder.
// A standard-library value nonetheless does NOT satisfy this
// contract: it panics on a write after a read, where this seam
// requires [ErrXOFSqueezing]. Implementations wrap rather than alias,
// converting that panic at the boundary.
//
// The name distinguishes it from [Stream], the fixed-output hashing
// stream, whose method set is incompatible — Sum and Close rather
// than Read.
//
// # Absorb, then squeeze
//
// The sponge has two phases and they do not interleave. Once Read has
// been called the state is being consumed, and resuming absorption
// would silently produce output unrelated to what a reader expects,
// so Write reports an error instead. [XOFStream.Reset] is the only
// transition back.
//
// # Concurrency
//
// Not safe for concurrent use. One stream per goroutine.
type XOFStream interface {
	// Write absorbs p. It returns [ErrXOFSqueezing] once Read has
	// been called, and absorbs nothing in that case.
	io.Writer

	// Read squeezes output. The stream never ends: Read fills its
	// buffer and never reports [io.EOF].
	io.Reader

	// Reset clears the sponge and returns the stream to absorbing,
	// discarding both absorbed input and squeeze position. After
	// Reset the stream is equivalent to a freshly returned one.
	Reset()
}
