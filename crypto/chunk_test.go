// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/fips140"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	randcrypto "go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/seeded"
)

// chunkVectorsPath is the file of recorded chunk vectors.
const chunkVectorsPath = "testdata/chunk_vectors.txt"

// The keywords and values of the chunk vector file.
const (
	vectorComment = "#"
	vectorMessage = "message"
	vectorKey     = "key"
	vectorAAD     = "aad"
	vectorHeader  = "header"
	vectorChunk   = "chunk"
	vectorLast    = "1"
	vectorEmpty   = "-"
)

// testChunkSize is the chunk size of the messages these tests seal.
const testChunkSize = 16

// lastChunkFlag pins the value of the last-chunk flag in the chunk
// frame. Every other chunk has 0x00.
const lastChunkFlag = 0x01

// benchChunkSize is the chunk size of the allocation tests and the
// benchmarks: 64 KiB, the chunk size of age.
const benchChunkSize = 64 << 10

// chunkChildTimeout is the -test.timeout of the child process of
// TestChunkFIPSOnlyMode. The child runs one subtest in milliseconds.
// The bound ends a child whose parent has died, which no context of the
// parent can cancel.
const chunkChildTimeout = 30 * time.Second

// chunkAAD is the caller's associated data of the messages these tests
// seal.
var chunkAAD = []byte("vectors/object")

// chunkHeaders generates a header with any message ID and any chunk size
// that NewChunkHeader accepts.
var chunkHeaders = prop.Composite(func(c *prop.Case) crypto.ChunkHeader {
	h := crypto.ChunkHeader{ChunkSize: c.Draw(prop.Integer[uint32](1, math.MaxUint32), "chunk size")}
	copy(h.MessageID[:], c.Draw(prop.Bytes(prop.MinSize(16), prop.MaxSize(16)), "message ID"))

	return h
})

// badChunkSizes generates the chunk sizes that NewChunkHeader refuses:
// every int below 1 or above MaxUint32.
var badChunkSizes = prop.OneOf(prop.Integer(math.MinInt, 0), prop.Integer(math.MaxUint32+1, math.MaxInt))

// overstated reports an Overhead one byte larger than its ciphertext
// uses, as an AEAD whose Overhead is a maximum can.
type overstated struct{ crypto.AEAD }

// Overhead returns the Overhead of the wrapped AEAD plus one.
func (o overstated) Overhead() int { return o.AEAD.Overhead() + 1 }

// chunkVector is one recorded message: the seed of its random source,
// its chunk size, key, aad, encoded header and chunks.
type chunkVector struct {
	key, aad, header []byte
	chunks           []chunkLine
	seed             rand.Seed
	size             int
}

// chunkLine is one recorded chunk.
type chunkLine struct {
	nonce, plaintext, sealed []byte
	index                    uint64
	last                     bool
}

func TestChunk(t *testing.T) {
	t.Parallel()

	a := newAEAD(t, aesgcm.KeySize256)
	vectors := loadChunkVectors(t)

	t.Run("NewChunkHeader", func(t *testing.T) {
		t.Parallel()

		t.Run("reads the message ID from the source", func(t *testing.T) {
			t.Parallel()
			h, err := crypto.NewChunkHeader(constant.New(0x0706050403020100), testChunkSize)
			assert.NoError(t, err, "NewChunkHeader must succeed")
			assert.Equal(t, h.MessageID, [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 0, 1, 2, 3, 4, 5, 6, 7},
				"the message ID must be the bytes that the source returned")
		})

		t.Run("returns a header of the chunk size", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) uint32 {
				h, _ := crypto.NewChunkHeader(randcrypto.New(), n)

				return h.ChunkSize
			}, func(n int) uint32 { return uint32(n) }, "NewChunkHeader must keep every chunk size from 1 to MaxUint32",
				prop.Using(prop.Integer(1, math.MaxUint32)), prop.Example(1), prop.Example(math.MaxUint32))
		})

		t.Run("returns two IDs for two messages", func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, newHeader(t).MessageID, newHeader(t).MessageID,
				"two headers from a random source must not share an ID")
		})

		t.Run("returns ErrChunkSize for a chunk size outside 1 to MaxUint32", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := crypto.NewChunkHeader(randcrypto.New(), n)

				return err
			}, crypto.ErrChunkSize, "NewChunkHeader must refuse a chunk size that a uint32 above 0 cannot express",
				prop.Using(badChunkSizes), prop.Example(-1), prop.Example(0), prop.Example(math.MaxUint32+1))
		})

		t.Run("returns an error of class Invalid for a chunk size outside 1 to MaxUint32", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.NewChunkHeader(randcrypto.New(), 0)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrChunkSize must classify as Invalid")
		})

		t.Run("returns the zero header for a chunk size outside 1 to MaxUint32", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) crypto.ChunkHeader {
				h, _ := crypto.NewChunkHeader(randcrypto.New(), n)

				return h
			}, func(int) crypto.ChunkHeader { return crypto.ChunkHeader{} },
				"a refused header must be the zero header", prop.Using(badChunkSizes))
		})

		failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.NewChunkHeader(failing, testChunkSize)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "NewChunkHeader must return the error of the source")
		})

		t.Run("returns the zero header with the error of the source", func(t *testing.T) {
			t.Parallel()
			h, err := crypto.NewChunkHeader(failing, testChunkSize)
			assert.HasError(t, err, "the test must read from a source that fails")
			assert.Equal(t, h, crypto.ChunkHeader{}, "a failed header must be the zero header")
		})
	})

	t.Run("ChunkHeader", func(t *testing.T) {
		t.Parallel()

		h := crypto.ChunkHeader{
			MessageID: [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
			ChunkSize: 0x00010000,
		}
		encoded, err := h.MarshalBinary()
		assert.NoError(t, err, "MarshalBinary must succeed")
		zero := crypto.ChunkHeader{MessageID: h.MessageID}

		t.Run("AppendBinary", func(t *testing.T) {
			t.Parallel()

			t.Run("appends the version then the chunk size then the message ID", func(t *testing.T) {
				t.Parallel()
				want, err := hex.DecodeString("01" + // version
					"00010000" + // chunk size, big-endian
					"000102030405060708090a0b0c0d0e0f") // message ID
				assert.NoError(t, err, "the recorded header must be hexadecimal")
				got, err := h.AppendBinary([]byte("prefix"))
				assert.NoError(t, err, "AppendBinary must succeed")
				assert.Equal(t, got, append([]byte("prefix"), want...), "the header must follow the bytes of dst")
			})

			t.Run("returns ErrChunkSize for a chunk size of 0", func(t *testing.T) {
				t.Parallel()
				_, err := zero.AppendBinary(nil)
				assert.ErrorIs(t, err, crypto.ErrChunkSize, "AppendBinary must refuse a chunk size of 0")
			})

			t.Run("returns dst unchanged for a chunk size of 0", func(t *testing.T) {
				t.Parallel()
				got, _ := zero.AppendBinary([]byte("prefix"))
				assert.Equal(t, string(got), "prefix", "AppendBinary must return dst unchanged with its error")
			})
		})

		t.Run("MarshalBinary", func(t *testing.T) {
			t.Parallel()

			// No generated header has a chunk size of 0, so neither call
			// returns an error.
			t.Run("returns the bytes that AppendBinary appends", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(h crypto.ChunkHeader) []byte {
					out, _ := h.MarshalBinary()

					return out
				}, func(h crypto.ChunkHeader) []byte {
					out, _ := h.AppendBinary(nil)

					return out
				}, "MarshalBinary must return the encoding of AppendBinary", prop.Using(chunkHeaders))
			})

			t.Run("returns ErrChunkSize for a chunk size of 0", func(t *testing.T) {
				t.Parallel()
				_, err := zero.MarshalBinary()
				assert.ErrorIs(t, err, crypto.ErrChunkSize, "MarshalBinary must refuse a chunk size of 0")
			})

			t.Run("returns nil for a chunk size of 0", func(t *testing.T) {
				t.Parallel()
				out, _ := zero.MarshalBinary()
				assert.Nil(t, out, "MarshalBinary must return no bytes with its error")
			})
		})

		t.Run("UnmarshalBinary", func(t *testing.T) {
			t.Parallel()

			t.Run("decodes the encoding that MarshalBinary returns", func(t *testing.T) {
				t.Parallel()
				prop.RoundTrip(t, crypto.ChunkHeader.MarshalBinary, func(b []byte) (crypto.ChunkHeader, error) {
					var got crypto.ChunkHeader
					err := got.UnmarshalBinary(b)

					return got, err
				}, "UnmarshalBinary must invert MarshalBinary", prop.Using(chunkHeaders))
			})

			withVersion := bytes.Clone(encoded)
			withVersion[0] = crypto.ChunkDomainVersion + 1
			malformed := []struct {
				name string
				give []byte
			}{
				{name: "returns ErrChunkHeader for a header one byte short", give: encoded[:crypto.ChunkHeaderSize-1]},
				{name: "returns ErrChunkHeader for a header one byte long", give: append(bytes.Clone(encoded), 0)},
				{name: "returns ErrChunkHeader for no bytes", give: nil},
				{name: "returns ErrChunkHeader for version 0", give: append([]byte{0}, encoded[1:]...)},
				{name: "returns ErrChunkHeader for the next version", give: withVersion},
				{
					name: "returns ErrChunkHeader for a chunk size of 0",
					give: append([]byte{crypto.ChunkDomainVersion, 0, 0, 0, 0}, h.MessageID[:]...),
				},
				{name: "returns ErrChunkHeader for a header of zeroes", give: make([]byte, crypto.ChunkHeaderSize)},
			}
			for _, tt := range malformed {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					var got crypto.ChunkHeader
					err := got.UnmarshalBinary(tt.give)
					assert.ErrorIs(t, err, crypto.ErrChunkHeader, "a malformed header must be refused")
				})
			}

			t.Run("returns an error of class Invalid for a malformed header", func(t *testing.T) {
				t.Parallel()
				var got crypto.ChunkHeader
				assert.Equal(t, errs.Classify(got.UnmarshalBinary(nil)), errs.Invalid,
					"ErrChunkHeader must classify as Invalid")
			})

			t.Run("leaves h unchanged for every header that it refuses", func(t *testing.T) {
				t.Parallel()
				for _, tt := range malformed {
					got := h
					_ = got.UnmarshalBinary(tt.give)
					expect.Equal(t, got, h, "a refused header must leave the receiver unchanged: "+tt.name)
				}
			})
		})
	})

	t.Run("AppendSealChunk", func(t *testing.T) {
		t.Parallel()

		h := newHeader(t)
		full := make([]byte, testChunkSize)
		failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
		refused := []struct {
			name  string
			aead  crypto.AEAD
			r     rand.Rand
			h     crypto.ChunkHeader
			index uint64
			last  bool
			chunk []byte
			want  error
		}{
			{
				name: "returns ErrChunkSize for a short chunk that is not last",
				aead: a, r: randcrypto.New(), h: h, chunk: full[:testChunkSize-1], want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for a long chunk that is not last",
				aead: a, r: randcrypto.New(), h: h, chunk: make([]byte, testChunkSize+1), want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for an empty chunk that is not last",
				aead: a, r: randcrypto.New(), h: h, want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for a final chunk longer than the chunk size",
				aead: a, r: randcrypto.New(), h: h, last: true, chunk: make([]byte, testChunkSize+1),
				want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for an empty final chunk at index 1",
				aead: a, r: randcrypto.New(), h: h, index: 1, last: true, want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for a header with a chunk size of 0",
				aead: a, r: randcrypto.New(), last: true, want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrChunkSize for an envelope whose length is not SealedSize",
				aead: overstated{a}, r: randcrypto.New(), h: h, last: true, chunk: full, want: crypto.ErrChunkSize,
			},
			{
				name: "returns ErrAlgorithmSize for an algorithm name that the header cannot express",
				aead: relabelled{AEAD: a, name: ""}, r: randcrypto.New(), h: h, last: true, chunk: full,
				want: crypto.ErrAlgorithmSize,
			},
			{
				name: "returns the error of the source",
				aead: a, r: failing, h: h, last: true, chunk: full, want: io.ErrUnexpectedEOF,
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := crypto.AppendSealChunk(nil, tt.aead, tt.r, tt.h, tt.index, tt.last, tt.chunk, nil)
				assert.ErrorIs(t, err, tt.want, "AppendSealChunk must refuse the chunk")
			})
		}

		t.Run("returns nil with every error", func(t *testing.T) {
			t.Parallel()
			for _, tt := range refused {
				got, _ := crypto.AppendSealChunk([]byte("prefix"), tt.aead, tt.r, tt.h, tt.index, tt.last,
					tt.chunk, nil)
				expect.Nil(t, got, "a refused chunk must return nil: "+tt.name)
			}
		})

		allowed := []struct {
			name  string
			index uint64
			last  bool
			chunk []byte
		}{
			{name: "seals a full chunk that is not last", index: 3, chunk: full},
			{name: "seals a full final chunk", index: 3, last: true, chunk: full},
			{name: "seals a final chunk of one byte", index: 3, last: true, chunk: full[:1]},
			{name: "seals the empty chunk of an empty message", last: true},
		}
		for _, tt := range allowed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, tt.index, tt.last, tt.chunk, nil)
				assert.NoError(t, err, "the size rules must allow the chunk")
				assert.Length(t, sealed, crypto.SealedSize(a, len(tt.chunk)), "the envelope must have SealedSize bytes")
			})
		}

		t.Run("appends the envelope after the bytes of dst", func(t *testing.T) {
			t.Parallel()
			dst := append(make([]byte, 0, 256), "keep me"...)
			dst, err := crypto.AppendSealChunk(dst, a, randcrypto.New(), h, 0, true, full, nil)
			assert.NoError(t, err, "AppendSealChunk must succeed")
			assert.HasPrefix(t, string(dst), "keep me", "AppendSealChunk must keep the bytes of dst")
			got, err := crypto.AppendOpenChunk(nil, a, h, dst[len("keep me"):], 0, true, nil)
			assert.NoError(t, err, "the appended chunk must open")
			assert.Equal(t, got, full, "the appended chunk must open to the chunk")
		})

		t.Run("authenticates the documented chunk frame", func(t *testing.T) {
			t.Parallel()
			const index = 7
			chunk := []byte("the final chunk")
			sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, index, true, chunk, chunkAAD)
			assert.NoError(t, err, "AppendSealChunk must succeed")

			// The chunk frame, byte by byte as the layout documents it.
			frame := binary.BigEndian.AppendUint64(nil, uint64(len(crypto.ChunkDomainName)))
			frame = append(frame, crypto.ChunkDomainName...)
			frame = binary.BigEndian.AppendUint16(frame, crypto.ChunkDomainVersion)
			frame = append(frame, h.MessageID[:]...)
			frame = binary.BigEndian.AppendUint32(frame, h.ChunkSize)
			frame = binary.BigEndian.AppendUint64(frame, index)
			frame = append(frame, lastChunkFlag)
			frame = binary.BigEndian.AppendUint64(frame, uint64(len(chunkAAD)))
			frame = append(frame, chunkAAD...)

			// The associated data of the envelope frames the algorithm name
			// and the chunk frame.
			alg := string(a.Algorithm())
			ad := binary.BigEndian.AppendUint64(nil, uint64(len(crypto.EnvelopeDomainName)))
			ad = append(ad, crypto.EnvelopeDomainName...)
			ad = binary.BigEndian.AppendUint16(ad, crypto.EnvelopeVersion)
			ad = binary.BigEndian.AppendUint64(ad, uint64(len(alg)))
			ad = append(ad, alg...)
			ad = binary.BigEndian.AppendUint64(ad, uint64(len(frame)))
			ad = append(ad, frame...)

			block, err := aes.NewCipher(aeadKey)
			assert.NoError(t, err, "aes.NewCipher must accept the key")
			gcm, err := cipher.NewGCM(block)
			assert.NoError(t, err, "cipher.NewGCM must succeed")

			// The envelope header and the nonce, which the seal drew, are
			// taken from the chunk. The rest is sealed again here.
			start := 2 + int(sealed[1]) + gcm.NonceSize()
			want := gcm.Seal(bytes.Clone(sealed[:start]), sealed[start-gcm.NonceSize():start], chunk, ad)
			assert.Equal(t, sealed, want, "the chunk must be the documented envelope over the documented frame")
		})

		t.Run("writes the envelope header that PeekAlgorithm reads", func(t *testing.T) {
			t.Parallel()
			sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, 0, true, []byte("chunk"), chunkAAD)
			assert.NoError(t, err, "AppendSealChunk must succeed")
			alg, err := crypto.PeekAlgorithm(sealed)
			assert.NoError(t, err, "PeekAlgorithm must read the header of the chunk")
			assert.Equal(t, alg, a.Algorithm(), "the chunk must name the algorithm of the AEAD")
		})

		t.Run("seals a chunk that Open opens with the chunk frame as associated data", func(t *testing.T) {
			t.Parallel()
			sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, 0, true, []byte("chunk"), chunkAAD)
			assert.NoError(t, err, "AppendSealChunk must succeed")

			f := crypto.NewFramer(nil, crypto.Domain{Name: crypto.ChunkDomainName, Version: crypto.ChunkDomainVersion})
			f.Fixed(h.MessageID[:])
			f.Uint32(h.ChunkSize)
			f.Uint64(0)
			f.Fixed([]byte{lastChunkFlag})
			f.Bytes(chunkAAD)

			got, err := crypto.Open(a, sealed, f.Frame())
			assert.NoError(t, err, "Open must open the chunk with the chunk frame as its associated data")
			assert.Equal(t, string(got), "chunk", "Open must return the chunk")
		})

		// The chunk header and the chunk frame are frozen, so a change to
		// any recorded byte breaks every stored message.
		for _, v := range vectors {
			name := "reproduces the recorded chunks of message " + strconv.FormatInt(int64(v.seed), 10)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				va, err := aesgcm.New(v.key)
				assert.NoError(t, err, "aesgcm.New must accept the key")

				r := seeded.New(v.seed)
				vh, err := crypto.NewChunkHeader(r, v.size)
				assert.NoError(t, err, "NewChunkHeader must succeed")
				encoded, err := vh.MarshalBinary()
				assert.NoError(t, err, "MarshalBinary must succeed")
				assert.Equal(t, encoded, v.header, "the header must match its record")

				for _, c := range v.chunks {
					sealed, err := crypto.AppendSealChunk(nil, va, r, vh, c.index, c.last, c.plaintext, v.aad)
					assert.NoError(t, err, "AppendSealChunk must succeed")
					start := 2 + int(sealed[1])
					expect.Equal(t, sealed[start:start+va.NonceSize()], c.nonce, "the nonce must match its record")
					expect.Equal(t, sealed, c.sealed, "the chunk must match its record")
				}
			})
		}
	})

	t.Run("AppendOpenChunk", func(t *testing.T) {
		t.Parallel()

		lengths := []struct {
			name string
			n    int
		}{
			{name: "opens the chunks of an empty message", n: 0},
			{name: "opens the chunks of a message of one byte", n: 1},
			{name: "opens the chunks of a message one byte short of the chunk size", n: testChunkSize - 1},
			{name: "opens the chunks of a message of the chunk size", n: testChunkSize},
			{name: "opens the chunks of a message one byte past the chunk size", n: testChunkSize + 1},
			{name: "opens the chunks of a message of three chunk sizes", n: 3 * testChunkSize},
		}
		for _, tt := range lengths {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := newHeader(t)
				msg := bytes.Repeat([]byte{0x5A}, tt.n)
				got, _, err := openMessage(a, h, sealMessage(t, a, h, msg, chunkAAD), chunkAAD)
				assert.NoError(t, err, "every chunk must open")
				assert.Equal(t, got, msg, "the chunks must open to the message", assert.EquateEmpty())
			})
		}

		h := newHeader(t)
		chunks := sealMessage(t, a, h, bytes.Repeat([]byte{0x5A}, 3*testChunkSize+5), chunkAAD)
		assert.Length(t, chunks, 4, "the message must have four chunks")
		// An earlier version of the same object, sealed under the same key
		// and aad with its own header.
		earlier := sealMessage(t, a, newHeader(t), bytes.Repeat([]byte{0x5A}, 3*testChunkSize+5), chunkAAD)
		truncated := chunks[3][:len(chunks[3])-3]
		changes := []struct {
			name   string
			chunks [][]byte
			failed int
		}{
			{
				name:   "fails at the second chunk of a message with two chunks swapped",
				chunks: [][]byte{chunks[0], chunks[2], chunks[1], chunks[3]},
				failed: 1,
			},
			{
				name:   "fails at the second chunk of a message with a chunk dropped",
				chunks: [][]byte{chunks[0], chunks[2], chunks[3]},
				failed: 1,
			},
			{
				name:   "fails at the third chunk of a message truncated at a chunk boundary",
				chunks: chunks[:3],
				failed: 2,
			},
			{
				name:   "fails at the last chunk of a message truncated inside a chunk",
				chunks: [][]byte{chunks[0], chunks[1], chunks[2], truncated},
				failed: 3,
			},
			{
				name:   "fails at the last chunk of a message extended past its final chunk",
				chunks: [][]byte{chunks[0], chunks[1], chunks[2], chunks[3], chunks[3]},
				failed: 3,
			},
			{
				name:   "fails at a chunk of an earlier version of the message",
				chunks: [][]byte{chunks[0], earlier[1], chunks[2], chunks[3]},
				failed: 1,
			},
			{
				name:   "fails at the last chunk of an earlier version of the message",
				chunks: [][]byte{chunks[0], chunks[1], chunks[2], earlier[3]},
				failed: 3,
			},
		}
		for _, tt := range changes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, failed, _ := openMessage(a, h, tt.chunks, chunkAAD)
				assert.Equal(t, failed, tt.failed, "the open must fail at the chunk that the change names")
			})
		}

		otherID := h
		otherID.MessageID[0] ^= 0xFF
		otherSize := h
		otherSize.ChunkSize++
		headers := []struct {
			name string
			give crypto.ChunkHeader
		}{
			{name: "returns an error for every chunk under another message ID", give: otherID},
			{name: "returns an error for every chunk under another chunk size", give: otherSize},
		}
		for _, tt := range headers {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				for i, sealed := range chunks {
					_, err := crypto.AppendOpenChunk(nil, a, tt.give, sealed, uint64(i), i == len(chunks)-1, chunkAAD)
					expect.HasError(t, err, "chunk "+strconv.Itoa(i)+" must not open under a changed header")
				}
			})
		}

		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, 2, false,
			make([]byte, testChunkSize), chunkAAD)
		assert.NoError(t, err, "AppendSealChunk must succeed")

		t.Run("opens a chunk with the values that sealed it", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.AppendOpenChunk(nil, a, h, sealed, 2, false, chunkAAD)
			assert.NoError(t, err, "the chunk must open with the values it was sealed with")
		})

		others := []struct {
			name  string
			index uint64
			last  bool
			aad   []byte
		}{
			{name: "returns an error for another index", index: 3, aad: chunkAAD},
			{name: "returns an error for another last flag", index: 2, last: true, aad: chunkAAD},
			{name: "returns an error for other aad", index: 2, aad: []byte("vectors/other")},
		}
		for _, tt := range others {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := crypto.AppendOpenChunk(nil, a, h, sealed, tt.index, tt.last, tt.aad)
				assert.HasError(t, err, "the chunk must not open with values other than the sealed ones")
			})
		}

		malformed := []byte{crypto.EnvelopeVersion + 1, 1, 'x'}

		t.Run("returns the errors of AppendOpen for a malformed envelope", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.AppendOpenChunk(nil, a, h, malformed, 0, true, nil)
			assert.ErrorIs(t, err, crypto.ErrEnvelopeVersion, "an unknown envelope version must be refused")
		})

		t.Run("returns nil for a malformed envelope", func(t *testing.T) {
			t.Parallel()
			got, _ := crypto.AppendOpenChunk([]byte("prefix"), a, h, malformed, 0, true, nil)
			assert.Nil(t, got, "a refused chunk must return nil")
		})

		for _, v := range vectors {
			t.Run("opens the recorded chunks of message "+strconv.FormatInt(int64(v.seed), 10), func(t *testing.T) {
				t.Parallel()
				va, err := aesgcm.New(v.key)
				assert.NoError(t, err, "aesgcm.New must accept the key")
				var vh crypto.ChunkHeader
				assert.NoError(t, vh.UnmarshalBinary(v.header), "the recorded header must decode")

				for _, c := range v.chunks {
					got, err := crypto.AppendOpenChunk(nil, va, vh, c.sealed, c.index, c.last, v.aad)
					expect.NoError(t, err, "the recorded chunk must open")
					expect.Equal(t, got, c.plaintext, "the chunk must open to its recorded plaintext",
						assert.EquateEmpty())
				}
			})
		}
	})
}

// TestChunkAllocs checks the allocation contract of each function and
// method of the chunk format, in both nonce modes where the mode matters.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestChunkAllocs(t *testing.T) {
	h := crypto.ChunkHeader{ChunkSize: benchChunkSize}
	chunk := bytes.Repeat([]byte{0x5A}, benchChunkSize)
	// Converted once, so no measured call boxes the source.
	var r rand.Rand = randcrypto.New()
	modes := []struct {
		name string
		aead crypto.AEAD
	}{
		{name: "with a caller nonce", aead: newAEAD(t, aesgcm.KeySize256)},
		{name: "with a module nonce", aead: newModuleNonceAEAD(t)},
	}

	t.Run("NewChunkHeader", func(t *testing.T) {
		var got crypto.ChunkHeader
		expect.MaxAllocs(t, func() { got, _ = crypto.NewChunkHeader(r, benchChunkSize) }, 1,
			"NewChunkHeader must allocate only the ID that the source fills")
		assert.Equal(t, got.ChunkSize, uint32(benchChunkSize), "the test must measure a header")
	})

	t.Run("ChunkHeader", func(t *testing.T) {
		t.Run("AppendBinary", func(t *testing.T) {
			dst := make([]byte, 0, crypto.ChunkHeaderSize)
			expect.MaxAllocs(t, func() { dst, _ = h.AppendBinary(dst[:0]) }, 0,
				"AppendBinary must not allocate when dst has capacity")
			assert.Length(t, dst, crypto.ChunkHeaderSize, "the test must measure an encoding")
		})

		t.Run("MarshalBinary", func(t *testing.T) {
			var out []byte
			expect.MaxAllocs(t, func() { out, _ = h.MarshalBinary() }, 1,
				"MarshalBinary must allocate only the returned slice")
			assert.Length(t, out, crypto.ChunkHeaderSize, "the test must measure an encoding")
		})

		t.Run("UnmarshalBinary", func(t *testing.T) {
			encoded, err := h.MarshalBinary()
			assert.NoError(t, err, "MarshalBinary must succeed")
			var got crypto.ChunkHeader
			expect.MaxAllocs(t, func() { err = got.UnmarshalBinary(encoded) }, 0,
				"UnmarshalBinary must not allocate")
			assert.NoError(t, err, "the test must measure a decode that succeeds")
		})
	})

	t.Run("AppendSealChunk", func(t *testing.T) {
		for _, mode := range modes {
			t.Run(mode.name, func(t *testing.T) {
				dst := make([]byte, 0, crypto.SealedSize(mode.aead, benchChunkSize))
				var err error
				expect.MaxAllocs(t, func() {
					dst, err = crypto.AppendSealChunk(dst[:0], mode.aead, r, h, 0, false, chunk, chunkAAD)
				}, 0, "AppendSealChunk must not allocate when dst has capacity")
				assert.NoError(t, err, "the test must measure a seal that succeeds")
			})
		}
	})

	t.Run("AppendOpenChunk", func(t *testing.T) {
		for _, mode := range modes {
			t.Run(mode.name, func(t *testing.T) {
				sealed, err := crypto.AppendSealChunk(nil, mode.aead, r, h, 0, false, chunk, chunkAAD)
				assert.NoError(t, err, "AppendSealChunk must succeed")
				dst := make([]byte, 0, benchChunkSize)
				expect.MaxAllocs(t, func() {
					dst, err = crypto.AppendOpenChunk(dst[:0], mode.aead, h, sealed, 0, false, chunkAAD)
				}, 0, "AppendOpenChunk must not allocate when dst has capacity")
				assert.NoError(t, err, "the test must measure an open that succeeds")
			})
		}
	})
}

// BenchmarkChunk reports the cost of the chunk functions over chunks of
// 64 KiB in both nonce modes, and of the header encoding, and fails when
// one of them allocates more than TestChunkAllocs allows.
func BenchmarkChunk(b *testing.B) {
	h := crypto.ChunkHeader{ChunkSize: benchChunkSize}
	chunk := bytes.Repeat([]byte{0x5A}, benchChunkSize)
	var r rand.Rand = randcrypto.New()
	modes := []struct {
		name string
		aead crypto.AEAD
	}{
		{name: "with a caller nonce", aead: newAEAD(b, aesgcm.KeySize256)},
		{name: "with a module nonce", aead: newModuleNonceAEAD(b)},
	}

	b.Run("NewChunkHeader", func(b *testing.B) {
		var got crypto.ChunkHeader

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = crypto.NewChunkHeader(r, benchChunkSize)
		}

		assert.Equal(b, got.ChunkSize, uint32(benchChunkSize), "the benchmark must measure a header")
	})

	b.Run("ChunkHeader", func(b *testing.B) {
		b.Run("AppendBinary", func(b *testing.B) {
			dst := make([]byte, 0, crypto.ChunkHeaderSize)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				dst, _ = h.AppendBinary(dst[:0])
			}

			assert.Length(b, dst, crypto.ChunkHeaderSize, "the benchmark must measure an encoding")
		})

		b.Run("UnmarshalBinary", func(b *testing.B) {
			encoded, err := h.MarshalBinary()
			assert.NoError(b, err, "MarshalBinary must succeed")
			var got crypto.ChunkHeader

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = got.UnmarshalBinary(encoded)
			}

			assert.NoError(b, err, "the benchmark must measure a decode that succeeds")
		})
	})

	b.Run("AppendSealChunk", func(b *testing.B) {
		for _, mode := range modes {
			b.Run(mode.name, func(b *testing.B) {
				dst := make([]byte, 0, crypto.SealedSize(mode.aead, benchChunkSize))
				var err error

				b.SetBytes(benchChunkSize)
				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					dst, err = crypto.AppendSealChunk(dst[:0], mode.aead, r, h, 0, false, chunk, chunkAAD)
				}

				assert.NoError(b, err, "the benchmark must measure a seal that succeeds")
			})
		}
	})

	b.Run("AppendOpenChunk", func(b *testing.B) {
		for _, mode := range modes {
			b.Run(mode.name, func(b *testing.B) {
				sealed, err := crypto.AppendSealChunk(nil, mode.aead, r, h, 0, false, chunk, chunkAAD)
				assert.NoError(b, err, "AppendSealChunk must succeed")
				dst := make([]byte, 0, benchChunkSize)

				b.SetBytes(benchChunkSize)
				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					dst, err = crypto.AppendOpenChunk(dst[:0], mode.aead, h, sealed, 0, false, chunkAAD)
				}

				assert.NoError(b, err, "the benchmark must measure an open that succeeds")
			})
		}
	})
}

// TestChunkFIPSOnlyMode checks that the chunk functions work in Go's
// FIPS 140-only mode with the module's nonces. The mode is fixed when
// the process starts, so the test runs itself again in a child process
// with GODEBUG=fips140=only.
func TestChunkFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()
			//nolint:gosec // G204: the child is this test binary, run again with a fixed pattern.
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestChunkFIPSOnlyMode$", "-test.v", "-test.timeout="+chunkChildTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			assert.Contains(t, string(out), "round-trips_a_message", "the child must run the FIPS checks")
		})

		return
	}

	t.Run("round-trips a message", func(t *testing.T) {
		t.Parallel()
		a := newModuleNonceAEAD(t)
		h := newHeader(t)
		msg := bytes.Repeat([]byte{0x5A}, 3*testChunkSize+5)

		got, _, err := openMessage(a, h, sealMessage(t, a, h, msg, chunkAAD), chunkAAD)
		assert.NoError(t, err, "every chunk must open in FIPS 140-only mode")
		assert.Equal(t, got, msg, "the chunks must open to the message")
	})
}

// newHeader returns a header with a random message ID and
// testChunkSize. It fails tb when NewChunkHeader fails.
func newHeader(tb testing.TB) crypto.ChunkHeader {
	tb.Helper()

	h, err := crypto.NewChunkHeader(randcrypto.New(), testChunkSize)
	assert.NoError(tb, err, "NewChunkHeader must succeed")

	return h
}

// sealMessage splits msg into chunks of h.ChunkSize and seals each one
// with its index, marking the final chunk as last. It fails tb when a
// seal fails.
func sealMessage(tb testing.TB, a crypto.AEAD, h crypto.ChunkHeader, msg, aad []byte) [][]byte {
	tb.Helper()

	size := int(h.ChunkSize)
	n := max(1, (len(msg)+size-1)/size)
	chunks := make([][]byte, n)
	for i := range n {
		end := min((i+1)*size, len(msg))
		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, uint64(i), i == n-1,
			msg[i*size:end], aad)
		assert.NoError(tb, err, "AppendSealChunk must succeed")
		chunks[i] = sealed
	}

	return chunks
}

// openMessage opens chunks in order as the chunks of one message, the
// final one as last. It returns the plaintext, or the position of the
// first chunk that fails and its error.
func openMessage(a crypto.AEAD, h crypto.ChunkHeader, chunks [][]byte, aad []byte) ([]byte, int, error) {
	var msg []byte
	for i, sealed := range chunks {
		var err error
		msg, err = crypto.AppendOpenChunk(msg, a, h, sealed, uint64(i), i == len(chunks)-1, aad)
		if err != nil {
			return nil, i, err
		}
	}

	return msg, -1, nil
}

// loadChunkVectors reads the chunk vector file. It fails tb when the file
// does not read or a field does not parse.
func loadChunkVectors(tb testing.TB) []chunkVector {
	tb.Helper()

	f, err := os.Open(chunkVectorsPath)
	assert.NoError(tb, err, "the vectors must open")
	defer f.Close()

	decode := func(s string) []byte {
		if s == vectorEmpty {
			return nil
		}
		b, err := hex.DecodeString(s)
		assert.NoError(tb, err, "a vector field must be hex")

		return b
	}
	number := func(s string) int64 {
		n, err := strconv.ParseInt(s, 10, 64)
		assert.NoError(tb, err, "a vector number must parse")

		return n
	}

	var vectors []chunkVector
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], vectorComment) {
			continue
		}

		if fields[0] == vectorMessage {
			vectors = append(vectors, chunkVector{seed: rand.Seed(number(fields[1])), size: int(number(fields[2]))})

			continue
		}

		assert.NotEmpty(tb, vectors, "a vector line must follow a message line")
		v := &vectors[len(vectors)-1]
		switch fields[0] {
		case vectorKey:
			v.key = decode(fields[1])
		case vectorAAD:
			v.aad = decode(fields[1])
		case vectorHeader:
			v.header = decode(fields[1])
		case vectorChunk:
			v.chunks = append(v.chunks, chunkLine{
				index:     uint64(number(fields[1])),
				last:      fields[2] == vectorLast,
				nonce:     decode(fields[3]),
				plaintext: decode(fields[4]),
				sealed:    decode(fields[5]),
			})
		}
	}
	assert.NoError(tb, lines.Err(), "the vectors must read")
	assert.NotEmpty(tb, vectors, "the file must contain vectors")

	return vectors
}
