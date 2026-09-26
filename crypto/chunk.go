// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"encoding/binary"
	"math"

	"go.thesmos.sh/core/rand"
)

// Chunk constants. A verifier in another language rebuilds the chunk
// header and a chunk's associated data from these.
const (
	// ChunkDomainName separates the associated data of a chunk from every
	// other framed sequence in a deployment.
	ChunkDomainName = "thesmos.crypto.chunk"

	// ChunkDomainVersion is the layout of the chunk header and of a
	// chunk's associated data that this build writes and opens. It is the
	// first byte of an encoded [ChunkHeader] and the [Domain.Version] of
	// the chunk frame.
	ChunkDomainVersion = 1

	// ChunkHeaderSize is the length of an encoded [ChunkHeader]: the
	// version byte, the chunk size as 4 big-endian bytes and the 16-byte
	// message ID.
	ChunkHeaderSize = 21

	// chunkSizeOffset is the offset of the chunk size in an encoded
	// header.
	chunkSizeOffset = 1

	// chunkIDOffset is the offset of the message ID in an encoded header.
	chunkIDOffset = 5

	// chunkLast and chunkNotLast are the values of the last-chunk flag in
	// the chunk frame.
	chunkLast    = 0x01
	chunkNotLast = 0x00
)

// ChunkHeader identifies one chunked message and fixes its chunk size.
// The caller stores the encoded header once per message, and passes the
// header to every seal and open of the message's chunks.
//
// A header belongs to one message. The chunks of a second message sealed
// under the same header, key and aad open as chunks of the first.
//
// The zero ChunkHeader has a chunk size of 0, and [AppendSealChunk]
// refuses it.
type ChunkHeader struct {
	// MessageID is the random identifier that [NewChunkHeader] reads from
	// its source.
	MessageID [16]byte

	// ChunkSize is the length of every chunk except the final one. The
	// final chunk is 1 to ChunkSize bytes long. Only an empty message has
	// an empty final chunk.
	ChunkSize uint32
}

// NewChunkHeader returns the header of a new message, with a message ID
// read from r and the given chunk size.
//
// Outside tests r must be a cryptographic source. Two messages whose
// headers share an ID can exchange chunks when they also share a key and
// aad.
//
// Returns [ErrChunkSize] for a chunkSize below 1 or above
// [math.MaxUint32], and the error of r when r fails.
//
// # Allocation contract
//
// One allocation for the ID that r fills, because r is an interface and
// the buffer escapes to it.
func NewChunkHeader(r rand.Rand, chunkSize int) (ChunkHeader, error) {
	if chunkSize < 1 || uint64(chunkSize) > math.MaxUint32 {
		return ChunkHeader{}, ErrChunkSize
	}

	h := ChunkHeader{ChunkSize: uint32(chunkSize)}
	if _, err := r.Read(h.MessageID[:]); err != nil {
		return ChunkHeader{}, err //nolint:wrapcheck // returned as the source produced it
	}

	return h, nil
}

// AppendBinary appends the [ChunkHeaderSize] bytes of h to dst: the
// version, the chunk size as 4 big-endian bytes and the message ID.
// Implements [encoding.BinaryAppender].
//
// Returns dst unchanged and [ErrChunkSize] when h.ChunkSize is 0.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for ChunkHeaderSize more bytes.
func (h ChunkHeader) AppendBinary(dst []byte) ([]byte, error) {
	if h.ChunkSize == 0 {
		return dst, ErrChunkSize
	}

	dst = append(dst, ChunkDomainVersion)
	dst = binary.BigEndian.AppendUint32(dst, h.ChunkSize)

	return append(dst, h.MessageID[:]...), nil
}

// MarshalBinary returns the encoding that [ChunkHeader.AppendBinary]
// appends. Implements [encoding.BinaryMarshaler].
//
// Returns nil and [ErrChunkSize] when h.ChunkSize is 0.
//
// # Allocation contract
//
// One allocation for the returned slice.
func (h ChunkHeader) MarshalBinary() ([]byte, error) {
	out, err := h.AppendBinary(make([]byte, 0, ChunkHeaderSize))
	if err != nil {
		return nil, err
	}

	return out, nil
}

// UnmarshalBinary decodes the encoding that [ChunkHeader.AppendBinary]
// writes. On an error h is unchanged. Implements
// [encoding.BinaryUnmarshaler].
//
// Returns [ErrChunkHeader] for an input that is not [ChunkHeaderSize]
// bytes long, names a version other than [ChunkDomainVersion], or has a
// chunk size of 0.
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (h *ChunkHeader) UnmarshalBinary(data []byte) error {
	if len(data) != ChunkHeaderSize || data[0] != ChunkDomainVersion {
		return ErrChunkHeader
	}

	size := binary.BigEndian.Uint32(data[chunkSizeOffset:chunkIDOffset])
	if size == 0 {
		return ErrChunkHeader
	}

	h.ChunkSize = size
	copy(h.MessageID[:], data[chunkIDOffset:])

	return nil
}

// fits reports whether a chunk of n bytes at index obeys the size rules
// of h:
//
//   - A chunk that is not last has ChunkSize bytes.
//   - The last chunk has 1 to ChunkSize bytes.
//   - The chunk of an empty message is empty. It is the last chunk, at
//     index 0.
func (h ChunkHeader) fits(n int, index uint64, last bool) bool {
	if h.ChunkSize == 0 {
		return false
	}
	if !last {
		return int64(n) == int64(h.ChunkSize)
	}
	if n == 0 {
		return index == 0
	}

	return int64(n) <= int64(h.ChunkSize)
}

// chunkAt names one chunk of a chunked message: the message's header,
// the chunk's index and whether it is the message's last chunk.
type chunkAt struct {
	index uint64
	h     ChunkHeader
	last  bool
}

// appendChunkFrame appends the associated data of chunk c to dst: the
// chunk domain, the message ID, the chunk size, the index, the
// last-chunk flag and the caller's aad.
func appendChunkFrame(dst []byte, c *chunkAt, aad []byte) []byte {
	flag := [1]byte{chunkNotLast}
	if c.last {
		flag[0] = chunkLast
	}

	f := NewFramer(dst, Domain{Name: ChunkDomainName, Version: ChunkDomainVersion})
	f.Fixed(c.h.MessageID[:])
	f.Uint32(c.h.ChunkSize)
	f.Uint64(c.index)
	f.Fixed(flag[:])
	f.Bytes(aad)

	return f.Frame()
}

// AppendSealChunk appends the sealed envelope of chunk index of the
// message that h identifies to dst, and returns the extended slice. last
// reports whether the chunk is the message's final chunk. aad binds the
// message to the caller's context.
//
// The envelope has the layout [AppendSeal] writes, so [PeekAlgorithm]
// reads it. Its associated data is the chunk frame: the header, index,
// last and aad, framed under [ChunkDomainName].
//
// Every chunk is one AEAD message and counts toward the AEAD's message
// bound: 2^32 chunks per key for AES-GCM with random nonces.
//
// Returns [ErrChunkSize] for a header with a chunk size of 0, a chunk
// that is not last and not h.ChunkSize bytes long, a chunk longer than
// h.ChunkSize, an empty final chunk at an index other than 0, and an
// envelope whose length is not [SealedSize] of the chunk. Returns the
// errors of AppendSeal otherwise. Every error returns nil.
//
// # Allocation contract
//
// Zero alloc under the conditions of [AppendSeal]. The chunk frame is
// built inside the envelope's frame, in the one scratch buffer from the
// package pool that AppendSeal uses.
func AppendSealChunk(
	dst []byte, a AEAD, r rand.Rand, h ChunkHeader, index uint64, last bool, chunk, aad []byte,
) ([]byte, error) {
	if !h.fits(len(chunk), index, last) {
		return nil, ErrChunkSize
	}

	start := len(dst)

	out, err := appendSeal(dst, a, r, chunk, aad, &chunkAt{h: h, index: index, last: last})
	if err != nil {
		return nil, err
	}

	if len(out)-start != SealedSize(a, len(chunk)) {
		return nil, ErrChunkSize
	}

	return out, nil
}

// AppendOpenChunk appends the plaintext of chunk index of the message
// that h identifies to dst, and returns the extended slice. h, index,
// last and aad must be the values the chunk was sealed with.
//
// An envelope sealed under another header, at another index, with
// another last flag or with other aad returns the AEAD's authentication
// error, the same error as a modified ciphertext. A malformed envelope
// header returns the errors of [AppendOpen]. Every error returns nil.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the plaintext.
func AppendOpenChunk(
	dst []byte, a AEAD, h ChunkHeader, sealed []byte, index uint64, last bool, aad []byte,
) ([]byte, error) {
	return appendOpen(dst, a, sealed, aad, &chunkAt{h: h, index: index, last: last})
}
