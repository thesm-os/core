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

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
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
// frame. Every other chunk carries 0x00.
const lastChunkFlag = 0x01

// chunkAAD is the caller's associated data of the messages these tests
// seal.
var chunkAAD = []byte("vectors/object")

// newHeader returns a header with a random message ID and
// [testChunkSize].
func newHeader(tb testing.TB) crypto.ChunkHeader {
	tb.Helper()

	h, err := crypto.NewChunkHeader(randcrypto.New(), testChunkSize)
	testkit.NoError(tb, err, "NewChunkHeader must succeed")

	return h
}

// sealMessage splits msg into chunks of h.ChunkSize and seals each one
// with its index, marking the final chunk as last.
func sealMessage(tb testing.TB, a crypto.AEAD, h crypto.ChunkHeader, msg, aad []byte) [][]byte {
	tb.Helper()

	size := int(h.ChunkSize)
	n := max(1, (len(msg)+size-1)/size)
	chunks := make([][]byte, n)
	for i := range n {
		end := min((i+1)*size, len(msg))
		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, uint64(i), i == n-1,
			msg[i*size:end], aad)
		testkit.NoError(tb, err, "AppendSealChunk must succeed")
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

func TestNewChunkHeader(t *testing.T) {
	t.Parallel()

	t.Run("reads the message ID from the source", func(t *testing.T) {
		t.Parallel()
		h, err := crypto.NewChunkHeader(constant.New(0x0706050403020100), testChunkSize)
		testkit.NoError(t, err, "NewChunkHeader must succeed")
		testkit.Equal(t, h.MessageID[:], testkit.MustDecodeHex(t, "00010203040506070001020304050607"),
			"the message ID must be the bytes the source returned")
		testkit.Equal(t, h.ChunkSize, uint32(testChunkSize), "the chunk size must be the one given")
	})

	t.Run("gives two messages different IDs", func(t *testing.T) {
		t.Parallel()
		testkit.NotEqual(t, newHeader(t).MessageID, newHeader(t).MessageID,
			"two headers from a random source must not share an ID")
	})

	t.Run("accepts the smallest and the largest chunk size", func(t *testing.T) {
		t.Parallel()
		for _, size := range []int{1, math.MaxUint32} {
			h, err := crypto.NewChunkHeader(randcrypto.New(), size)
			testkit.NoError(t, err, "a chunk size from 1 to MaxUint32 must be accepted")
			testkit.Equal(t, int(h.ChunkSize), size, "the chunk size must be the one given")
		}
	})

	t.Run("refuses a chunk size outside 1 to MaxUint32", func(t *testing.T) {
		t.Parallel()
		for _, size := range []int{-1, 0, math.MaxUint32 + 1} {
			h, err := crypto.NewChunkHeader(randcrypto.New(), size)
			testkit.ErrorIs(t, err, crypto.ErrChunkSize, "an unrepresentable chunk size must be refused")
			testkit.Equal(t, h, crypto.ChunkHeader{}, "a refused header must be the zero header")
		}
	})

	t.Run("returns the error of the source", func(t *testing.T) {
		t.Parallel()
		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil),
			Err:    io.ErrUnexpectedEOF,
		})
		h, err := crypto.NewChunkHeader(failing, testChunkSize)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "NewChunkHeader must return the source's error")
		testkit.Equal(t, h, crypto.ChunkHeader{}, "a failed header must be the zero header")
	})
}

func TestChunkHeaderBinary(t *testing.T) {
	t.Parallel()

	h := crypto.ChunkHeader{ChunkSize: 0x00010000}
	copy(h.MessageID[:], testkit.MustDecodeHex(t, "000102030405060708090a0b0c0d0e0f"))

	t.Run("encodes the version, the chunk size and the message ID", func(t *testing.T) {
		t.Parallel()
		got, err := h.MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must succeed")
		testkit.Equal(t, got, testkit.MustDecodeHex(t,
			"01"+ // version
				"00010000"+ // chunk size, big-endian
				"000102030405060708090a0b0c0d0e0f"), // message ID
			"the encoding must match the documented layout")
		testkit.Equal(t, len(got), crypto.ChunkHeaderSize, "the encoding must be ChunkHeaderSize bytes")
	})

	t.Run("appends to dst and leaves its prefix alone", func(t *testing.T) {
		t.Parallel()
		got, err := h.AppendBinary([]byte("prefix"))
		testkit.NoError(t, err, "AppendBinary must succeed")
		testkit.Equal(t, string(got[:len("prefix")]), "prefix", "the prefix must survive")
		testkit.Equal(t, len(got), len("prefix")+crypto.ChunkHeaderSize, "the header must follow the prefix")
	})

	t.Run("round-trips through UnmarshalBinary", func(t *testing.T) {
		t.Parallel()
		encoded, err := h.MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must succeed")

		var got crypto.ChunkHeader
		testkit.NoError(t, got.UnmarshalBinary(encoded), "UnmarshalBinary must accept the encoding")
		testkit.Equal(t, got, h, "the header must round-trip")
	})

	t.Run("refuses to encode a chunk size of 0", func(t *testing.T) {
		t.Parallel()
		zero := crypto.ChunkHeader{MessageID: h.MessageID}

		got, err := zero.AppendBinary([]byte("prefix"))
		testkit.ErrorIs(t, err, crypto.ErrChunkSize, "AppendBinary must refuse a chunk size of 0")
		testkit.Equal(t, string(got), "prefix", "AppendBinary must return dst unchanged")

		out, err := zero.MarshalBinary()
		testkit.ErrorIs(t, err, crypto.ErrChunkSize, "MarshalBinary must refuse a chunk size of 0")
		testkit.Equal(t, out, []byte(nil), "MarshalBinary must return nil with its error")
	})

	t.Run("UnmarshalBinary refuses a malformed header and leaves the receiver alone", func(t *testing.T) {
		t.Parallel()
		valid, err := h.MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must succeed")

		withVersion := func(v byte) []byte {
			b := bytes.Clone(valid)
			b[0] = v

			return b
		}
		zeroSize := append([]byte{crypto.ChunkDomainVersion, 0, 0, 0, 0}, h.MessageID[:]...)

		for name, data := range map[string][]byte{
			"one byte short":     valid[:crypto.ChunkHeaderSize-1],
			"one byte long":      append(bytes.Clone(valid), 0),
			"version 0":          withVersion(0),
			"version 2":          withVersion(crypto.ChunkDomainVersion + 1),
			"a chunk size of 0":  zeroSize,
			"no bytes at all":    nil,
			"a header of zeroes": make([]byte, crypto.ChunkHeaderSize),
		} {
			got := h
			testkit.ErrorIs(t, got.UnmarshalBinary(data), crypto.ErrChunkHeader, name+" must be refused")
			testkit.Equal(t, got, h, name+" must leave the receiver unchanged")
		}
	})
}

func TestAppendSealChunk(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)
	h := newHeader(t)
	full := make([]byte, testChunkSize)

	t.Run("refuses a chunk that breaks the size rules of its header", func(t *testing.T) {
		t.Parallel()
		for name, c := range map[string]struct {
			h     crypto.ChunkHeader
			chunk []byte
			index uint64
			last  bool
		}{
			"a short chunk that is not last":     {h: h, chunk: full[:testChunkSize-1], index: 0},
			"a long chunk that is not last":      {h: h, chunk: make([]byte, testChunkSize+1), index: 0},
			"an empty chunk that is not last":    {h: h, chunk: nil, index: 0},
			"a final chunk longer than the size": {h: h, chunk: make([]byte, testChunkSize+1), last: true},
			"an empty final chunk at index 1":    {h: h, chunk: nil, index: 1, last: true},
			"a header with a chunk size of 0":    {h: crypto.ChunkHeader{}, chunk: nil, last: true},
		} {
			got, err := crypto.AppendSealChunk([]byte("prefix"), a, randcrypto.New(),
				c.h, c.index, c.last, c.chunk, nil)
			testkit.ErrorIs(t, err, crypto.ErrChunkSize, name+" must be refused")
			testkit.Equal(t, got, []byte(nil), name+" must return nil")
		}
	})

	t.Run("accepts every chunk the size rules allow", func(t *testing.T) {
		t.Parallel()
		for name, c := range map[string]struct {
			chunk []byte
			index uint64
			last  bool
		}{
			"a full chunk that is not last":       {chunk: full, index: 3},
			"a full final chunk":                  {chunk: full, index: 3, last: true},
			"a one-byte final chunk":              {chunk: full[:1], index: 3, last: true},
			"the empty chunk of an empty message": {chunk: nil, index: 0, last: true},
		} {
			sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, c.index, c.last, c.chunk, nil)
			testkit.NoError(t, err, name+" must be sealed")
			testkit.Equal(t, len(sealed), crypto.SealedSize(a, len(c.chunk)),
				name+" must seal to SealedSize of its length")
		}
	})

	t.Run("refuses an envelope whose length is not SealedSize", func(t *testing.T) {
		t.Parallel()
		got, err := crypto.AppendSealChunk(nil, overstated{a}, randcrypto.New(), h, 0, true, full, nil)
		testkit.ErrorIs(t, err, crypto.ErrChunkSize,
			"an AEAD whose ciphertext is shorter than its Overhead must be refused")
		testkit.Equal(t, got, []byte(nil), "a refused chunk must return nil")
	})

	t.Run("returns the errors of AppendSeal", func(t *testing.T) {
		t.Parallel()
		nameless := relabelled{AEAD: a, name: ""}
		_, err := crypto.AppendSealChunk(nil, nameless, randcrypto.New(), h, 0, true, full, nil)
		testkit.ErrorIs(t, err, crypto.ErrAlgorithmSize, "an unrepresentable algorithm must be refused")

		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil),
			Err:    io.ErrUnexpectedEOF,
		})
		_, err = crypto.AppendSealChunk(nil, a, failing, h, 0, true, full, nil)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "an entropy failure must be returned")
	})

	t.Run("appends to dst and leaves its prefix alone", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("keep me")
		dst := append(make([]byte, 0, 256), prefix...)

		dst, err := crypto.AppendSealChunk(dst, a, randcrypto.New(), h, 0, true, full, nil)
		testkit.NoError(t, err, "AppendSealChunk must succeed")
		testkit.Equal(t, dst[:len(prefix)], prefix, "the prefix must survive")

		got, err := crypto.AppendOpenChunk(nil, a, h, dst[len(prefix):], 0, true, nil)
		testkit.NoError(t, err, "the appended chunk must open")
		testkit.Equal(t, got, full, "the chunk must round-trip")
	})

	t.Run("matches the documented layout, rebuilt with the standard library", func(t *testing.T) {
		t.Parallel()
		const index = 7

		chunk := []byte("the final chunk")
		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, index, true, chunk, chunkAAD)
		testkit.NoError(t, err, "AppendSealChunk must succeed")

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

		// The envelope's associated data frames the algorithm name and
		// the chunk frame.
		alg := string(a.Algorithm())
		ad := binary.BigEndian.AppendUint64(nil, uint64(len(crypto.EnvelopeDomainName)))
		ad = append(ad, crypto.EnvelopeDomainName...)
		ad = binary.BigEndian.AppendUint16(ad, crypto.EnvelopeVersion)
		ad = binary.BigEndian.AppendUint64(ad, uint64(len(alg)))
		ad = append(ad, alg...)
		ad = binary.BigEndian.AppendUint64(ad, uint64(len(frame)))
		ad = append(ad, frame...)

		block, err := aes.NewCipher(testKey())
		testkit.NoError(t, err, "aes.NewCipher must accept the key")
		gcm, err := cipher.NewGCM(block)
		testkit.NoError(t, err, "cipher.NewGCM must succeed")

		// The envelope header and the nonce, which the seal drew, are
		// taken from the chunk. The rest is sealed again here.
		start := len(header(t, sealed)) + gcm.NonceSize()
		nonce := sealed[start-gcm.NonceSize() : start]
		want := gcm.Seal(bytes.Clone(sealed[:start]), nonce, chunk, ad)
		testkit.Equal(t, sealed, want, "the chunk must be the documented envelope over the documented frame")
	})
}

func TestAppendOpenChunk(t *testing.T) {
	t.Parallel()

	a := newAEAD(t)

	t.Run("seals and opens messages of every length around the chunk size", func(t *testing.T) {
		t.Parallel()
		for _, n := range []int{0, 1, testChunkSize - 1, testChunkSize, testChunkSize + 1, 3 * testChunkSize} {
			h := newHeader(t)
			msg := bytes.Repeat([]byte{0x5A}, n)

			chunks := sealMessage(t, a, h, msg, chunkAAD)
			testkit.Equal(t, len(chunks), max(1, (n+testChunkSize-1)/testChunkSize),
				"a message must have one chunk per started chunk size, and at least one")

			got, failed, err := openMessage(a, h, chunks, chunkAAD)
			testkit.NoError(t, err, "every chunk must open")
			testkit.Equal(t, failed, -1, "no chunk may fail")
			testkit.Equal(t, string(got), string(msg), "the chunks must reassemble to the message")
		}
	})

	t.Run("fails each change to a stored message at the chunk it names", func(t *testing.T) {
		t.Parallel()
		h := newHeader(t)
		msg := bytes.Repeat([]byte{0x5A}, 3*testChunkSize+5)
		chunks := sealMessage(t, a, h, msg, chunkAAD)
		testkit.Equal(t, len(chunks), 4, "the message must have four chunks")

		// An earlier version of the same object, sealed under the same
		// key and aad with its own header.
		earlier := sealMessage(t, a, newHeader(t), msg, chunkAAD)
		truncated := chunks[3][:len(chunks[3])-3]

		for name, c := range map[string]struct {
			chunks [][]byte
			failed int
		}{
			"two chunks swapped":               {[][]byte{chunks[0], chunks[2], chunks[1], chunks[3]}, 1},
			"a chunk dropped":                  {[][]byte{chunks[0], chunks[2], chunks[3]}, 1},
			"truncated at a chunk boundary":    {chunks[:3], 2},
			"truncated inside a chunk":         {[][]byte{chunks[0], chunks[1], chunks[2], truncated}, 3},
			"extended past the final chunk":    {[][]byte{chunks[0], chunks[1], chunks[2], chunks[3], chunks[3]}, 3},
			"a chunk of an earlier version":    {[][]byte{chunks[0], earlier[1], chunks[2], chunks[3]}, 1},
			"the earlier version's last chunk": {[][]byte{chunks[0], chunks[1], chunks[2], earlier[3]}, 3},
		} {
			_, failed, err := openMessage(a, h, c.chunks, chunkAAD)
			testkit.Error(t, err, name+" must fail to open")
			testkit.Equal(t, failed, c.failed, name+" must fail at the chunk it names")
		}
	})

	t.Run("fails every chunk under a changed header", func(t *testing.T) {
		t.Parallel()
		h := newHeader(t)
		chunks := sealMessage(t, a, h, bytes.Repeat([]byte{0x5A}, 2*testChunkSize), chunkAAD)

		otherID := h
		otherID.MessageID[0] ^= 0xFF
		otherSize := h
		otherSize.ChunkSize++

		for name, changed := range map[string]crypto.ChunkHeader{
			"another message ID": otherID,
			"another chunk size": otherSize,
		} {
			for i, sealed := range chunks {
				_, err := crypto.AppendOpenChunk(nil, a, changed, sealed, uint64(i), i == len(chunks)-1, chunkAAD)
				testkit.Error(t, err, "a chunk opened with "+name+" must fail")
			}
		}
	})

	t.Run("fails a chunk opened with another index, flag or aad", func(t *testing.T) {
		t.Parallel()
		h := newHeader(t)
		chunk := make([]byte, testChunkSize)
		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, 2, false, chunk, chunkAAD)
		testkit.NoError(t, err, "AppendSealChunk must succeed")

		_, err = crypto.AppendOpenChunk(nil, a, h, sealed, 2, false, chunkAAD)
		testkit.NoError(t, err, "the chunk must open with the values it was sealed with")

		_, err = crypto.AppendOpenChunk(nil, a, h, sealed, 3, false, chunkAAD)
		testkit.Error(t, err, "another index must fail")
		_, err = crypto.AppendOpenChunk(nil, a, h, sealed, 2, true, chunkAAD)
		testkit.Error(t, err, "another last flag must fail")
		_, err = crypto.AppendOpenChunk(nil, a, h, sealed, 2, false, []byte("vectors/other"))
		testkit.Error(t, err, "other aad must fail")
	})

	t.Run("returns the errors of AppendOpen for a malformed envelope", func(t *testing.T) {
		t.Parallel()
		got, err := crypto.AppendOpenChunk([]byte("prefix"), a, newHeader(t),
			[]byte{crypto.EnvelopeVersion + 1, 1, 'x'}, 0, true, nil)
		testkit.ErrorIs(t, err, crypto.ErrEnvelopeVersion, "an unknown envelope version must be refused")
		testkit.Equal(t, got, []byte(nil), "a refused chunk must return nil")
	})

	t.Run("a chunk is an envelope that Open opens with the chunk frame", func(t *testing.T) {
		t.Parallel()
		h := newHeader(t)
		sealed, err := crypto.AppendSealChunk(nil, a, randcrypto.New(), h, 0, true, []byte("chunk"), chunkAAD)
		testkit.NoError(t, err, "AppendSealChunk must succeed")

		alg, err := crypto.PeekAlgorithm(sealed)
		testkit.NoError(t, err, "PeekAlgorithm must read the chunk's header")
		testkit.Equal(t, alg, a.Algorithm(), "the chunk must name the AEAD's algorithm")

		f := crypto.NewFramer(nil, crypto.Domain{Name: crypto.ChunkDomainName, Version: crypto.ChunkDomainVersion})
		f.Fixed(h.MessageID[:])
		f.Uint32(h.ChunkSize)
		f.Uint64(0)
		f.Fixed([]byte{lastChunkFlag})
		f.Bytes(chunkAAD)

		got, err := crypto.Open(a, sealed, f.Frame())
		testkit.NoError(t, err, "Open must open the chunk with the chunk frame as its associated data")
		testkit.Equal(t, string(got), "chunk", "Open must return the chunk")
	})
}

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

// loadChunkVectors reads the chunk vector file.
func loadChunkVectors(tb testing.TB) []chunkVector {
	tb.Helper()

	f, err := os.Open(chunkVectorsPath)
	testkit.NoError(tb, err, "the vectors must open")
	defer f.Close()

	decode := func(s string) []byte {
		if s == vectorEmpty {
			return nil
		}
		b, err := hex.DecodeString(s)
		testkit.NoError(tb, err, "a vector field must be hex")

		return b
	}
	number := func(s string) int64 {
		n, err := strconv.ParseInt(s, 10, 64)
		testkit.NoError(tb, err, "a vector number must parse")

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

		testkit.True(tb, len(vectors) > 0, "a vector line must follow a message line")
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
	testkit.NoError(tb, lines.Err(), "the vectors must read")
	testkit.True(tb, len(vectors) > 0, "the file must hold vectors")

	return vectors
}

// TestChunkVectors reproduces the recorded messages. The chunk header
// and the chunk frame are frozen, so a change to any recorded byte breaks
// every stored message and must fail this test.
func TestChunkVectors(t *testing.T) {
	t.Parallel()

	for _, v := range loadChunkVectors(t) {
		t.Run("message "+strconv.FormatInt(int64(v.seed), 10)+" seals and opens to its record", func(t *testing.T) {
			t.Parallel()

			a, err := aesgcm.New(v.key)
			testkit.NoError(t, err, "aesgcm.New must accept the key")

			r := seeded.New(v.seed)
			h, err := crypto.NewChunkHeader(r, v.size)
			testkit.NoError(t, err, "NewChunkHeader must succeed")
			encoded, err := h.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must succeed")
			testkit.Equal(t, encoded, v.header, "the header must match its record")

			for _, c := range v.chunks {
				sealed, sealErr := crypto.AppendSealChunk(nil, a, r, h, c.index, c.last, c.plaintext, v.aad)
				testkit.NoError(t, sealErr, "AppendSealChunk must succeed")
				testkit.Equal(t, sealed, c.sealed, "the chunk must match its record")

				start := len(header(t, sealed))
				testkit.Equal(t, sealed[start:start+a.NonceSize()], c.nonce, "the nonce must match its record")

				got, openErr := crypto.AppendOpenChunk(nil, a, h, c.sealed, c.index, c.last, v.aad)
				testkit.NoError(t, openErr, "the recorded chunk must open")
				testkit.Equal(t, string(got), string(c.plaintext), "the chunk must open to its recorded plaintext")
			}
		})
	}
}

// overstated reports an Overhead one byte larger than its ciphertext
// uses, as an AEAD whose Overhead is a maximum can.
type overstated struct{ crypto.AEAD }

func (o overstated) Overhead() int { return o.AEAD.Overhead() + 1 }

// testKey returns the key that [newAEAD] builds its AEAD over.
func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}

	return k
}

// benchChunkSize is the chunk size of the benchmarks: 64 KiB, the chunk
// size of age.
const benchChunkSize = 64 << 10

// BenchmarkChunk reports the cost and the allocations of the chunk
// functions for both nonce modes, and of the header encoding.
func BenchmarkChunk(b *testing.B) {
	h := crypto.ChunkHeader{ChunkSize: benchChunkSize}
	chunk := bytes.Repeat([]byte{0x5A}, benchChunkSize)

	// Converted once, so no call boxes the source into the interface.
	var r rand.Rand = randcrypto.New()

	aeads := []struct {
		a    crypto.AEAD
		name string
	}{
		{newAEAD(b), "caller nonce"},
		{newModuleNonceAEAD(b), "module nonce"},
	}
	for _, tt := range aeads {
		b.Run("AppendSealChunk/"+tt.name, func(b *testing.B) {
			dst := make([]byte, 0, crypto.SealedSize(tt.a, benchChunkSize))

			b.ReportAllocs()
			b.SetBytes(benchChunkSize)
			for b.Loop() {
				dst, _ = crypto.AppendSealChunk(dst[:0], tt.a, r, h, 0, false, chunk, chunkAAD)
			}
			testkit.True(b, dst != nil, "AppendSealChunk must produce output")
		})

		b.Run("AppendOpenChunk/"+tt.name, func(b *testing.B) {
			sealed, err := crypto.AppendSealChunk(nil, tt.a, r, h, 0, false, chunk, chunkAAD)
			testkit.NoError(b, err, "AppendSealChunk must succeed")
			dst := make([]byte, 0, benchChunkSize)

			b.ReportAllocs()
			b.SetBytes(benchChunkSize)
			for b.Loop() {
				dst, _ = crypto.AppendOpenChunk(dst[:0], tt.a, h, sealed, 0, false, chunkAAD)
			}
			testkit.True(b, dst != nil, "AppendOpenChunk must produce output")
		})
	}

	b.Run("NewChunkHeader", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = crypto.NewChunkHeader(r, benchChunkSize)
		}
	})

	b.Run("AppendBinary", func(b *testing.B) {
		dst := make([]byte, 0, crypto.ChunkHeaderSize)

		b.ReportAllocs()
		for b.Loop() {
			dst, _ = h.AppendBinary(dst[:0])
		}
		testkit.Equal(b, len(dst), crypto.ChunkHeaderSize, "AppendBinary must produce a header")
	})

	b.Run("UnmarshalBinary", func(b *testing.B) {
		encoded, err := h.MarshalBinary()
		testkit.NoError(b, err, "MarshalBinary must succeed")
		var decoded crypto.ChunkHeader

		b.ReportAllocs()
		for b.Loop() {
			_ = decoded.UnmarshalBinary(encoded)
		}
		testkit.Equal(b, decoded, h, "UnmarshalBinary must decode the header")
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
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestChunkFIPSOnlyMode$", "-test.v")
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			testkit.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			testkit.True(t, bytes.Contains(out, []byte("seals_and_opens_a_message")),
				"the child must run the FIPS checks, not skip them")
		})

		return
	}

	t.Run("seals and opens a message", func(t *testing.T) {
		t.Parallel()
		a := newModuleNonceAEAD(t)
		h := newHeader(t)
		msg := bytes.Repeat([]byte{0x5A}, 3*testChunkSize+5)

		got, failed, err := openMessage(a, h, sealMessage(t, a, h, msg, chunkAAD), chunkAAD)
		testkit.NoError(t, err, "every chunk must open in FIPS 140-only mode")
		testkit.Equal(t, failed, -1, "no chunk may fail")
		testkit.Equal(t, got, msg, "the chunks must reassemble to the message")
	})
}
