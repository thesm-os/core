// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package seeded

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"go.thesmos.sh/core/crypto"
	cryptohmacsha256 "go.thesmos.sh/core/crypto/hmac/sha256"
	"go.thesmos.sh/core/rand"
)

// blockSize is the HMAC-SHA-256 output size in bytes; one HMAC
// invocation refills this many bytes.
const blockSize = 32

// Rand implements [rand.Rand] as a deterministic CSPRNG built on
// HMAC-SHA-256 over a 64-bit counter. Same seed produces identical
// byte streams across runs, processes, and Go versions; the
// underlying primitive is in the standard library so behaviour
// does not depend on a third-party crypto build.
//
// Rand is safe for concurrent use; an internal mutex serialises
// access to the HMAC state.
//
// # Allocation contract
//
// All scratch (counter bytes, output buffer, HMAC state) lives on
// the receiver, so [Rand.Read] and [Rand.Uint64] are zero-alloc
// after construction.
type Rand struct {
	stream  crypto.Stream
	seed    rand.Seed
	counter uint64
	ctr     [8]byte
	buf     [blockSize]byte
	bufHead int
	bufLen  int
	mu      sync.Mutex
}

// Compile-time interface check.
var _ rand.Rand = (*Rand)(nil)

// New returns a Rand keyed by seed. Two instances with the same
// seed produce bit-identical byte streams.
//
// The HMAC key is the 8-byte big-endian encoding of seed. The
// remaining keying material is empty; HMAC tolerates short keys
// per RFC 2104. The construction is part of the public contract
// — changing the key derivation would change the output stream
// and break the cross-version determinism guarantee.
func New(seed rand.Seed) *Rand {
	var key [8]byte
	// int64 → uint64 is a bit-pattern reinterpretation; the
	// resulting key uses the entire 64-bit value range as the
	// HMAC key seed material.
	binary.BigEndian.PutUint64(key[:], uint64(seed))
	return &Rand{
		stream: cryptohmacsha256.New(key[:]).NewStream(),
		seed:   seed,
	}
}

// NewFromBytes returns a Rand keyed by an arbitrary-length byte
// seed. The seed is hashed to a fixed-size HMAC key so callers can
// pass any length without altering the construction. Two instances
// with equal byte seeds produce bit-identical byte streams.
//
// The returned Rand's Seed reports [rand.SeedUnspecified] — there
// is no single int64 that recovers a byte-string seed.
func NewFromBytes(seed []byte) *Rand {
	digest := sha256.Sum256(seed)
	return &Rand{
		stream: cryptohmacsha256.New(digest[:]).NewStream(),
		seed:   rand.SeedUnspecified,
	}
}

// Uint64 returns the next 64 bits of the keyed-counter stream as
// a uint64. Equivalent to Read(buf[0:8]) followed by a
// little-endian decode, but avoids the user-facing buffer.
//
// # Allocation contract
//
// Zero alloc.
func (r *Rand) Uint64() uint64 {
	var b [8]byte
	r.mu.Lock()
	r.readLocked(b[:])
	r.mu.Unlock()
	return binary.LittleEndian.Uint64(b[:])
}

// Read fills p with bytes from the keyed-counter stream. Always
// returns (len(p), nil) — HMAC-SHA-256 cannot fail.
//
// # Allocation contract
//
// Zero alloc per call (all scratch is on the receiver).
func (r *Rand) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.readLocked(p)
	r.mu.Unlock()
	return len(p), nil
}

// Seed returns the seed used to construct this Rand. Returns
// [rand.SeedUnspecified] for instances created via [NewFromBytes].
func (r *Rand) Seed() rand.Seed {
	return r.seed
}

// readLocked fills p from the buffered HMAC output, refilling as
// needed. Caller holds r.mu.
func (r *Rand) readLocked(p []byte) {
	written := 0
	for written < len(p) {
		if r.bufLen == 0 {
			r.refillLocked()
		}
		take := min(len(p)-written, r.bufLen)
		copy(p[written:written+take], r.buf[r.bufHead:r.bufHead+take])
		written += take
		r.bufHead += take
		r.bufLen -= take
	}
}

// refillLocked computes one HMAC block from the next counter and
// stages it in the receiver buffer. Caller holds r.mu.
func (r *Rand) refillLocked() {
	binary.BigEndian.PutUint64(r.ctr[:], r.counter)
	r.counter++
	r.stream.Reset()
	// crypto.Stream.Write never returns a non-nil error
	// (HMAC-SHA-256 has no IO failure path); ignoring is safe.
	_, _ = r.stream.Write(r.ctr[:])
	digest := r.stream.Sum()
	copy(r.buf[:], digest.Bytes())
	r.bufHead = 0
	r.bufLen = blockSize
}
