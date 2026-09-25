---
rfc: 0041
title: Chunked Authenticated Encryption
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-09-25
updated: 2026-09-25
discussion: none
supersedes: none
superseded-by: none
produces-adr: tbd
---

# RFC-0041: Chunked Authenticated Encryption

## Summary

We propose a chunk header and two functions in `crypto`,
`AppendSealChunk` and `AppendOpenChunk`, that seal a message as a
sequence of chunks. The header contains a random 16-byte message ID and
the chunk size, and the caller stores it once per message. Each chunk
is a sealed envelope in the existing layout. Its associated data binds
the header, the chunk's index, a flag for the message's last chunk, and
the caller's associated data. A reader opens any chunk on its own. A
chunk moved to another index, a dropped chunk, a truncated message and
a chunk from another message each fail to open. The construction works
over any `crypto.AEAD`, including in FIPS 140-only mode.

## Motivation

`crypto.Seal` and `crypto.Open` handle one message at a time. A large
object sealed as one envelope has two costs:

- A reader of a range must open the whole envelope, because the tag
  covers every byte. A read of 64 KiB from a 50 MB object decrypts
  50 MB.
- A streaming reader cannot return plaintext before it has read and
  authenticated the last byte. Its memory and its latency grow with the
  object.

Sealing each chunk as an independent envelope removes both costs, and
it loses the order of the chunks. An attacker who can rewrite the stored
object can drop, reorder, truncate or extend chunks, and each chunk
still authenticates. STREAM prevents this by deriving each segment's nonce
from the segment's index and a final-segment flag, and age and Tink use
it.

Core cannot put the index into the nonce:

- Go's FIPS 140-only mode refuses GCM with caller-supplied nonces.
  Measured with Go 1.27.1 under `GODEBUG=fips140=only`: `aesgcm.New`
  fails with "use of GCM with arbitrary IVs is not allowed in FIPS
  140-only mode", and `aesgcm.NewRandomNonce` succeeds.
- `crypto.Seal` takes no nonce, so a caller cannot derive one.

A caller that needs chunks has to frame the index and the flag into the
associated data itself, and each caller writes a different framing. The
index and the flag do not separate messages either. The caller also has
to stop a chunk of one message from opening as a chunk of another
message under the same key.

## Detailed design

### The chunk header

```go
// ChunkDomainName separates the associated data of a chunk from every
// other framed sequence in a deployment.
const ChunkDomainName = "thesmos.crypto.chunk"

// ChunkDomainVersion is the layout of the chunk header and of a chunk's
// associated data that this build writes and opens. It is the first
// byte of an encoded ChunkHeader and the version of the chunk frame's
// domain.
const ChunkDomainVersion = 1

// ChunkHeaderSize is the length of an encoded ChunkHeader.
const ChunkHeaderSize = 21

// ChunkHeader identifies one chunked message and fixes its chunk size.
// The caller stores the encoded header once per message and passes the
// header to every seal and open of the message's chunks.
//
// A header belongs to one message. The chunks of a second message
// sealed under the same header, key and aad open as chunks of the
// first.
type ChunkHeader struct {
    // MessageID is the random identifier that NewChunkHeader reads from
    // r.
    MessageID [16]byte

    // ChunkSize is the length of every chunk except the final one. The
    // final chunk is 1 to ChunkSize bytes long. Only an empty message
    // has an empty final chunk.
    ChunkSize uint32
}

// NewChunkHeader returns the header of a new message, with a message ID
// read from r and the given chunk size.
//
// Returns ErrChunkSize for a chunkSize below 1 or above math.MaxUint32,
// and the error of r when r fails.
func NewChunkHeader(r rand.Rand, chunkSize int) (ChunkHeader, error)

// AppendBinary appends the ChunkHeaderSize bytes of h to dst: the
// version, the chunk size as 4 big-endian bytes, and the message ID.
// Implements encoding.BinaryAppender.
//
// Returns dst unchanged and ErrChunkSize when h.ChunkSize is 0.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for ChunkHeaderSize more bytes.
func (h ChunkHeader) AppendBinary(dst []byte) ([]byte, error)

// MarshalBinary returns the encoding that AppendBinary appends.
// Implements encoding.BinaryMarshaler.
//
// Returns nil and ErrChunkSize when h.ChunkSize is 0.
func (h ChunkHeader) MarshalBinary() ([]byte, error)

// UnmarshalBinary decodes the encoding that AppendBinary writes. On an
// error h is unchanged. Implements encoding.BinaryUnmarshaler.
//
// Returns ErrChunkHeader for an input that is not ChunkHeaderSize bytes
// long, names a version other than ChunkDomainVersion, or has a chunk
// size of 0.
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (h *ChunkHeader) UnmarshalBinary(data []byte) error

// ErrChunkSize is returned when a chunk breaks the size rules of its
// header, when a chunk size is out of range, and when an AEAD seals a
// chunk to a length other than SealedSize.
var ErrChunkSize = errors.New("crypto: chunk size does not match its header")

// ErrChunkHeader is returned when an encoded ChunkHeader is malformed.
var ErrChunkHeader = errors.New("crypto: malformed chunk header")
```

### Sealing and opening chunks

```go
// SealedSize returns the length of the envelope that AppendSeal writes
// for n bytes of plaintext under a.
//
// The result is exact for an AEAD whose ciphertext is always n +
// a.Overhead() bytes long, as the ciphertext of AES-GCM is.
// cipher.AEAD documents Overhead as a maximum.
func SealedSize(a AEAD, n int) int

// AppendSealChunk appends the sealed envelope of chunk index of the
// message that h identifies to dst, and returns the extended slice.
// last reports whether the chunk is the message's final chunk. aad
// binds the message to the caller's context.
//
// The envelope has the layout AppendSeal writes, so PeekAlgorithm reads
// it. Its associated data is the chunk frame: the header, index, last
// and aad, framed under ChunkDomainName.
//
// Every chunk is one AEAD message and counts toward the AEAD's message
// bound: 2^32 chunks per key for AES-GCM with random nonces.
//
// Returns ErrChunkSize for a header with a chunk size of 0, a chunk
// that is not last and not h.ChunkSize bytes long, a chunk longer than
// h.ChunkSize, an empty final chunk at an index other than 0, and an
// envelope whose length is not SealedSize(a, len(chunk)). Returns the
// errors of AppendSeal otherwise. Every error returns nil.
//
// # Allocation contract
//
// Zero alloc under the conditions of AppendSeal.
func AppendSealChunk(dst []byte, a AEAD, r rand.Rand, h ChunkHeader, index uint64, last bool, chunk, aad []byte) ([]byte, error)

// AppendOpenChunk appends the plaintext of chunk index of the message
// that h identifies to dst, and returns the extended slice. h, index,
// last and aad must be the values the chunk was sealed with.
//
// An envelope sealed under another header, at another index, with
// another last flag or with other aad returns the AEAD's
// authentication error, the same error as a modified ciphertext. A
// malformed envelope header returns the errors of AppendOpen.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the plaintext.
func AppendOpenChunk(dst []byte, a AEAD, h ChunkHeader, sealed []byte, index uint64, last bool, aad []byte) ([]byte, error)
```

Both functions build the chunk frame inside the envelope's frame, in
the one pooled scratch buffer that `AppendSeal` uses, so neither
allocates on the warm path.

`AppendSealChunk` compares the length of every envelope with
`SealedSize` and returns `ErrChunkSize` when they differ. Every sealed
chunk then has the length `SealedSize` reports, whatever AEAD the
caller supplies. A reader computes the offset of any chunk without
opening another chunk.

### Byte layouts

An encoded `ChunkHeader` is 21 bytes:

| Field | Bytes |
|---|---|
| Version | 1, value 1 |
| Chunk size | 4, big-endian |
| Message ID | 16 |

The chunk frame is the associated data that `AppendSealChunk` passes to
`AppendSeal`. It is a `Framer` sequence:

| Field | Bytes |
|---|---|
| Domain name length | 8, big-endian, value 20 |
| Domain name | `thesmos.crypto.chunk` |
| Domain version | 2, big-endian, value 1 |
| Message ID | 16 |
| Chunk size | 4, big-endian |
| Index | 8, big-endian |
| Last flag | 1: `0x01` for the last chunk, `0x00` for every other chunk |
| aad length | 8, big-endian |
| aad | the caller's bytes |

The frame binds every field of the header, so the header does not need
a tag of its own. A changed message ID or chunk size changes the frame of
every chunk, and every chunk then fails to open.

The envelope around the frame is unchanged: the version byte, the
length of the algorithm name, the name, the nonce, the ciphertext and
the tag. Its associated data frames the algorithm name and the chunk
frame under `thesmos.crypto.aead` version 1.

A chunk of n bytes seals to n + 41 bytes with `aes-256-gcm`: 1 byte of
version, 1 of name length, 11 of name, 12 of nonce and 16 of tag.

### Message rules

`AppendSealChunk` and `AppendOpenChunk` seal and open one chunk each.
The caller splits the message and places the header and the chunks. It
follows these rules:

1. Each message has its own header from `NewChunkHeader`. The caller
   stores the encoded header with the message, for example at offset 0
   of the object.
2. Chunk i contains the bytes from i·S up to (i+1)·S of the message, or
   up to its end, where S is the header's chunk size.
3. A message of L bytes has ⌈L/S⌉ chunks, and at least one. Every chunk
   except the final one is S bytes long. The final chunk is empty only
   when the message is empty. `AppendSealChunk` refuses a chunk that
   breaks this rule.
4. A reader takes each chunk's index from its position and the number
   of chunks from the stored length. It opens the final chunk with
   `last` set, and every other chunk without it.
5. `aad` binds the message to the caller's context, such as the name it
   is stored under. It does not have to be unique per message, because
   the message ID separates messages.

Under these rules each change to a stored message fails at a chunk the
reader can name:

| Change to the stored message | Fails at |
|---|---|
| Two chunks swapped | The first of them: it was sealed at another index |
| A chunk dropped | The chunk that moves into the dropped position |
| Truncated at a chunk boundary | The new final chunk: it was sealed without `last` |
| Truncated inside a chunk | That chunk: its tag no longer verifies |
| Extended past the final chunk | The old final chunk: it was sealed with `last` |
| A chunk of another message inserted, including a chunk of an earlier version of the same object under the same key and `aad` | That chunk: it was sealed under another message ID |
| The header's message ID or chunk size changed | Every chunk: each was sealed under the original header |

A reader of a range detects a change only when the range contains the
chunk the table names. Every chunk that opens belongs to this message
at this index.

### Reading a range

Consider an object that stores the encoded header at offset 0, followed
by the sealed chunks. Its store reads a byte range under a version
precondition, as S3 `GetObject` does with `Range` and `If-Match`. A
reader with the data encryption key (DEK) reads the plaintext bytes
from p up to q in three steps:

1. Read bytes 0 to 20 with no precondition, and decode them with
   `UnmarshalBinary`. Keep the object's version v and its stored length
   T from the same response.
2. Compute the offsets. With F = SealedSize(a, S), the message has
   n = ⌈(T − 21)/F⌉ chunks, and chunk i starts at byte 21 + i·F. The
   range needs chunks ⌊p/S⌋ to ⌊(q − 1)/S⌋. The message is
   T − 21 − n·(F − S) bytes long.
3. Read those chunks with the precondition that the version is v. Open
   chunk i with `last` set when i is n − 1. A failed precondition means
   that the object changed after step 1, and the reader starts again at
   step 1.

A reader that keeps the header, or stores it in an index, skips step 1
for later ranges of the same version.

### Test vectors

A new vector file under `crypto/testdata` records AES-256-GCM messages
sealed through `aesgcm.New` with a seeded random source. Each vector
lists the key, the encoded header, the aad, and for each chunk the
nonce, the index, the flag, the plaintext and the sealed envelope. The
tests reproduce each header with `AppendBinary` and each envelope with
`AppendSealChunk`, and open each envelope with `AppendOpenChunk`. An
implementation in another language reproduces the same bytes from the
same inputs.

### Tests

- Messages of 0, 1, S−1, S, S+1 and 3S bytes seal chunk by chunk, open
  chunk by chunk and reassemble to the original. Each has ⌈L/S⌉ chunks,
  and the empty message has one.
- Each row of the table of changes fails at the chunk the row names.
- A chunk opened under another header, index, flag or aad fails.
- `AppendSealChunk` returns `ErrChunkSize` for a short chunk that is
  not last, a chunk longer than S, an empty final chunk at index 1, and
  a header with a chunk size of 0.
- `AppendSealChunk` returns `ErrChunkSize` for a stub AEAD whose
  ciphertext is shorter than its `Overhead`.
- Each chunk that the tests seal has the length `SealedSize` reports.
- A header round-trips through `AppendBinary` and `UnmarshalBinary`.
  `UnmarshalBinary` returns `ErrChunkHeader` for 20 and 22 bytes,
  another version and a chunk size of 0. `NewChunkHeader` returns
  `ErrChunkSize` for 0 and for `math.MaxUint32` + 1.
- A chunk is a valid envelope. `PeekAlgorithm` returns the AEAD's
  algorithm. `crypto.Open` opens the chunk with the chunk frame as its
  associated data, which the test builds with `NewFramer`.
- `BenchmarkChunk` reports the allocations of both functions for both
  nonce modes: none when `dst` has capacity.
- Both functions work under `GODEBUG=fips140=only` with
  `aesgcm.NewRandomNonce`. The test runs in a child process, as the
  FIPS test of `aesgcm` does.

### Migration

None. The functions are additive and leave the existing envelope
unchanged. Core freezes every persisted byte layout from the first
release that contains it, and the header and the chunk frame are fixed
from that release.

## Alternatives considered

### A. The index in the nonce

age builds each 12-byte ChaCha20-Poly1305 nonce from an 11-byte
big-endian counter and a final-chunk byte. Tink's AES-GCM-HKDF streaming
AEAD builds each IV from a nonce prefix, a 4-byte segment number and a
last-segment byte. It passes empty associated data to each segment.
Both formats bind the position of a chunk through its nonce, so they do
not store a nonce or a frame per chunk.

**Why not:** Go's FIPS 140-only mode refuses GCM with caller-supplied
nonces. `crypto.Seal` does not let a caller choose a nonce either. The
construction would work only outside FIPS 140-only mode and only over an
AEAD whose nonce the caller controls.

### B. A key per message

Tink derives each stream's key with HKDF from the key, a salt in the
stream's header and the associated data. age derives the payload key
with HKDF-SHA-256 from the file key, with a nonce as the salt. A key per
message separates messages without a message ID. It also gives each
message its own 2^32 message bound.

**Why not:** HKDF needs the key bytes. A `crypto.AEAD` does not expose
them. The construction would take a raw key and build AES-GCM itself,
and it would no longer work over an arbitrary `crypto.AEAD`. Building
the cipher also allocates once per message. A caller that gives each
message its own DEK already gets a bound of 2^32 chunks per message. At 64 KiB per chunk that bound is 256 TiB.

### C. The caller's `aad` as the message identity

The construction would not bind a message ID. It would instead require
`aad` to be unique per message under a key. A caller would pass the
name the message is stored under. A reader would then read a range with
one request and without a header.

**Why not:** a name identifies a message only until the object is
overwritten. A caller that overwrites an object under the same DEK
produces two messages with the same `aad`. An attacker who can write to
storage can then move chunks of the old version into the new version at
the same indexes. Every chunk still opens. The production formats
generate the separation instead of trusting the caller:

- The AWS Encryption SDK binds a random message ID into every frame.
  Its message format states that the ID "provides a mechanism to
  securely reuse a data key with multiple encrypted messages" and
  "protects against accidental reuse of a data key or the wearing out
  of keys".
- Tink derives a key per stream from a salt in the stream's header.
- age begins each payload with "a 16-byte nonce generated by the sender
  from a CSPRNG", and derives the payload key from it.

### D. A streaming writer and reader

A type that splits a stream into chunks and seals them, and a type that
opens them in order.

**Why not:** both types fix the storage layout, which belongs to the
caller. A reader of ranges needs the per-chunk functions anyway. The
streaming types can be built on the two functions without changing
them.

### E. An empty final chunk after a full one

The AWS Encryption SDK lets a message whose length is a multiple of the
frame length end with a full final frame, or with a regular frame
followed by a final frame of zero length. A streaming writer can then
seal each full chunk as soon as it fills, and seal an empty final chunk
when the stream ends.

**Why not:** a plaintext of k·S bytes then has two valid chunkings, of
k and of k + 1 chunks. A reader that knows the plaintext length, for
example from an index, cannot compute the number of chunks or the
stored length from it. age requires that the final chunk "MUST NOT be
empty unless the whole payload is empty", and rule 3 takes the same
rule. One plaintext, header and random source then produce exactly one
stored message, which the test vectors depend on.

## Drawbacks

- Each message stores a 21-byte header. A reader that does not keep the
  header makes a second request per range to read it.
- Each chunk costs 41 bytes with `aes-256-gcm`. That is 0.06% of a
  64 KiB chunk.
- Each chunk counts toward the AEAD's message bound of 2^32 chunks per
  key with random nonces. At 64 KiB per chunk the bound is 256 TiB of
  messages under one key.
- A header belongs to one message. A caller that seals a second message
  under the same header, key and `aad` loses the separation between the
  two messages, and the functions cannot detect it.
- A streaming writer that does not know the message length seals a full
  chunk only after it has seen the next byte, because rule 3 decides
  whether that chunk is the last.
- The caller places the chunks and follows rule 4 when it reads. The
  functions enforce rule 3 only when they seal.
- The header and the chunk frame are new persisted layouts: 3 fields in
  the header, and 5 fields after the domain in the frame.
- `crypto` gains one type with three methods, four functions, three
  constants and two errors.

## Open questions

None.

## Unresolved / future work

- A streaming writer and reader over the functions, as Alternative D
  describes.
- A key per message, as Alternative B describes, if the AEAD seam gains
  a way to derive a key.

## References

- RFC-0016, framed domain separation.
- RFC-0017, authenticated encryption.
- RFC-0040, ranged reads on named object storage.
- ADR-0016, persisted encodings are frozen.
- V. T. Hoang, R. Reyhanitabar, P. Rogaway and D. Vizár, "Online
  Authenticated-Encryption and its Nonce-Reuse Misuse-Resistance",
  Cryptology ePrint Archive, Paper 2015/189,
  <https://eprint.iacr.org/2015/189>.
- C2SP, the age format, payload section, <https://c2sp.org/age>.
- Tink, AES-GCM-HKDF streaming AEAD,
  <https://developers.google.com/tink/streaming-aead/aes_gcm_hkdf_streaming>.
- AWS Encryption SDK, message format reference,
  <https://docs.aws.amazon.com/encryption-sdk/latest/developer-guide/message-format.html>.
- AWS Encryption SDK, body AAD reference,
  <https://docs.aws.amazon.com/encryption-sdk/latest/developer-guide/body-aad-reference.html>.
- Amazon S3 API Reference, `GetObject`,
  <https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html>.
- Go 1.27.1: `crypto/cipher.NewGCMWithRandomNonce`,
  `crypto/cipher.AEAD`.
