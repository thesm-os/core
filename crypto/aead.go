// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"crypto/cipher"
	"slices"

	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/rand"
)

// AEAD is an authenticated-encryption primitive with associated data.
// It embeds the standard library contract verbatim and adds identity.
//
// Callers persist [AEAD.Algorithm] alongside the ciphertext and select
// the matching implementation at open time, exactly as they persist
// [Hasher.Algorithm] alongside a digest. That is what lets a
// ciphertext written today be opened by a build that does not yet
// exist, by an implementation chosen at run time rather than compile
// time.
//
// The embedded [cipher.AEAD] is available for callers managing their
// own nonces — a counter under a per-message key, for instance, which
// is both correct and cheaper than randomness. Everyone else uses
// [Seal] and [Open], which manage the nonce.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type AEAD interface {
	// Seal and Open panic when nonce is not NonceSize() bytes, as
	// [cipher.AEAD] documents. That is inherited, not chosen: this
	// seam embeds the standard library contract so a cipher.AEAD
	// from anywhere satisfies it without an adapter, and the panic
	// comes with it.
	//
	// It is also the only panicking surface in this module. Use
	// [Seal] and [Open], which construct the nonce themselves and
	// cannot call the panicking path. Use these methods only when
	// managing nonces deliberately, and validate the length first.
	cipher.AEAD

	// ID is the build-local implementation identifier.
	ID() ID

	// Algorithm is the long-term, cross-build name. Persist it
	// alongside every ciphertext.
	Algorithm() Algorithm
}

// Sealed-envelope constants. A verifier in another language rebuilds
// the authenticated bytes from these.
const (
	// EnvelopeVersion is the envelope layout this build writes, and the
	// only one it opens.
	//
	// It appears twice: as the envelope's first byte, and as the
	// [Domain.Version] bound into what the tag covers. The byte gives a
	// reader a clean [ErrEnvelopeVersion] before any key is used, which
	// is what an operator migrating formats needs to see. The domain
	// version makes that rejection unforgeable — bytes written under
	// one layout cannot authenticate under another however the plain
	// byte is rewritten.
	EnvelopeVersion = 1

	// EnvelopeDomainName separates this envelope's authenticated bytes
	// from every other framed sequence in a deployment.
	EnvelopeDomainName = "thesmos.crypto.aead"

	// EnvelopeVersionSize is the width of the leading version byte, and
	// so the offset of the algorithm length within a sealed envelope.
	EnvelopeVersionSize = 1

	// EnvelopeAlgorithmLenSize is the width of the algorithm-length
	// byte. A caller computing offsets into an envelope adds this to
	// [EnvelopeVersionSize] and the algorithm's own length to find the
	// nonce.
	EnvelopeAlgorithmLenSize = 1

	// envelopeHeaderFixed is the part of the header whose width does
	// not depend on the algorithm: [EnvelopeVersionSize] and
	// [EnvelopeAlgorithmLenSize], one byte each. It is a literal, because
	// an operator in a const declaration has no coverage counter and a
	// mutation tool never runs a mutant of it.
	envelopeHeaderFixed = 2

	// maxAlgorithmLen is what the single length byte can express.
	maxAlgorithmLen = 255
)

// aadPool pools the scratch the associated-data frame is built in. The
// frame never escapes the call that builds it, so a per-call
// allocation would be the whole cost of these helpers.
var aadPool = pool.NewPool(func() *[]byte {
	b := make([]byte, 0, 128)

	return &b
})

// envelopeSize is the exact length [Seal] will produce, so its buffer
// is sized once rather than regrown as each field is appended.
func envelopeSize(a AEAD, plaintext []byte) int {
	return envelopeHeaderFixed + len(a.Algorithm()) +
		a.NonceSize() + len(plaintext) + a.Overhead()
}

// frameAAD builds the bytes a sealed envelope authenticates: the
// domain tag, the algorithm name, then the caller's own associated
// data. Both fields are length-prefixed, so no caller-supplied aad can
// imitate a longer algorithm name.
func frameAAD(dst, name, aad []byte) []byte {
	f := NewFramer(dst, Domain{Name: EnvelopeDomainName, Version: EnvelopeVersion})
	f.Bytes(name)
	f.Bytes(aad)

	return f.Frame()
}

// splitEnvelope parses a sealed envelope's header, returning the
// algorithm name it declares and the bytes that follow it.
func splitEnvelope(sealed []byte) (name, rest []byte, err error) {
	if len(sealed) < envelopeHeaderFixed {
		return nil, nil, ErrCiphertextShort
	}

	// The version is checked before anything is measured: an unknown
	// layout means the fields below may not be where they look.
	if sealed[0] != EnvelopeVersion {
		return nil, nil, ErrEnvelopeVersion
	}

	algLen := int(sealed[1])
	if algLen == 0 {
		return nil, nil, ErrAlgorithmSize
	}

	if len(sealed) < envelopeHeaderFixed+algLen {
		return nil, nil, ErrCiphertextShort
	}

	return sealed[envelopeHeaderFixed : envelopeHeaderFixed+algLen],
		sealed[envelopeHeaderFixed+algLen:], nil
}

// Seal draws a fresh random nonce from r, encrypts plaintext under a
// with aad as associated data, and returns a sealed envelope:
//
//	version || algLen || algorithm || nonce || ciphertext || tag
//
// An AEAD whose NonceSize is 0 generates the nonce itself and prepends
// it to its own output, as
// [go.thesmos.sh/core/crypto/aesgcm.NewRandomNonce] does. Seal then
// reads nothing from r, which may be nil, and the envelope has the
// same layout.
//
// The algorithm and the nonce are written into the envelope, because
// both are needed to open it and neither is secret. A value stored
// apart from its ciphertext can be lost, and losing either makes the
// ciphertext unrecoverable.
//
// The header is authenticated, not merely prepended: it is bound into
// what the tag covers, so an attacker cannot rewrite the algorithm to
// steer an open towards a different primitive.
//
// Use this unless you have a specific reason to manage nonces
// yourself. Reusing a nonce under one key breaks AES-GCM completely:
// it discloses the XOR of the two plaintexts and permits tag forgery
// for every message under that key. Passing a deterministic
// [rand.Rand] — a fixed or seeded source — reuses the nonce by
// construction and must never be done outside a test asserting that
// property.
//
// Random 96-bit nonces are safe for any message volume a single key
// will realistically see; the birthday bound is roughly 2^32 messages
// per key for a 2^-32 collision probability.
//
// Returns [ErrAlgorithmSize] when a reports a name the header cannot
// express, and whatever r returns when entropy fails. No output is
// returned alongside an error.
//
// # Allocation contract
//
// Allocates the returned envelope once, sized up front rather than
// regrown per field. [AppendSeal] reuses a caller's buffer and
// allocates nothing.
func Seal(a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error) {
	return AppendSeal(make([]byte, 0, envelopeSize(a, plaintext)), a, r, plaintext, aad)
}

// AppendSeal appends the sealed envelope to dst and returns the
// extended slice, as [Seal] does for a fresh one.
//
// dst is appended to, not overwritten; pass buf[:0] to reuse a
// buffer's capacity. On failure dst is returned unmodified as nil, and
// the caller's own slice header is untouched.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the finished envelope and r's
// Read does not allocate, as [go.thesmos.sh/core/rand/crypto.Rand]'s
// does not. The nonce is drawn into dst, and the associated-data frame
// is scratch drawn from a package pool.
//
// A caller that passes r as a concrete type wider than a pointer pays
// one allocation per call to box it into the [rand.Rand] interface.
// Converting r once, outside the loop, avoids it.
func AppendSeal(dst []byte, a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error) {
	alg := a.Algorithm()
	if len(alg) == 0 || len(alg) > maxAlgorithmLen {
		return nil, ErrAlgorithmSize
	}

	nonceSize := a.NonceSize()
	body := len(dst)

	//nolint:gosec // G115: bounded by the maxAlgorithmLen check above.
	dst = append(dst, EnvelopeVersion, byte(len(alg)))
	dst = append(dst, alg...)

	// Grown rather than appended from a temporary, so the nonce is
	// drawn straight into the envelope it belongs to.
	dst = slices.Grow(dst, nonceSize)
	dst = dst[:len(dst)+nonceSize]
	nonce := dst[len(dst)-nonceSize:]

	// An AEAD with no nonce of its own generates one inside Seal, so r
	// is neither read nor required.
	if nonceSize > 0 {
		if _, err := r.Read(nonce); err != nil {
			return nil, err //nolint:wrapcheck // returned as the source produced it
		}
	}

	// Taken after every append to dst, so no growth can have moved
	// them out from under the frame.
	name := dst[body+envelopeHeaderFixed : body+envelopeHeaderFixed+len(alg)]

	scratch := aadPool.Get()
	framed := frameAAD((*scratch)[:0], name, aad)
	out := a.Seal(dst, nonce, plaintext, framed)
	*scratch = framed[:0]
	aadPool.Put(scratch)

	return out, nil
}

// Open parses a sealed envelope, checks that it names a's algorithm,
// and decrypts it with aad as the caller's associated data.
//
// Returns [ErrEnvelopeVersion] for a layout this build does not know,
// [ErrCiphertextShort] for an envelope shorter than its header
// declares, [ErrAlgorithmSize] for one naming nothing, and
// [ErrAlgorithmMismatch] when it names an algorithm other than a's.
// All four are decided from the header before any key is used.
//
// Every other failure — a modified ciphertext, a modified tag, a
// rewritten algorithm, mismatched associated data, the wrong key —
// returns the underlying [cipher.AEAD] error unwrapped and
// indistinguishable from the others. That is deliberate: a caller must
// not be able to branch on why authentication failed.
//
// There is no fallback to a bare nonce-prefixed ciphertext. A reader
// that falls back is one an attacker forces into the weaker mode by
// stripping bytes, and the algorithm binding then covers nothing.
//
// A successfully opened empty plaintext returns nil rather than an
// empty non-nil slice, following [cipher.AEAD.Open]. Callers compare
// contents or length, not nil-ness; guaranteeing non-nil would cost
// an allocation to carry a distinction the payload does not have.
//
// # Allocation contract
//
// One allocation for the returned plaintext. Use [AppendOpen] to reuse
// a buffer and pay none.
func Open(a AEAD, sealed, aad []byte) ([]byte, error) {
	return AppendOpen(nil, a, sealed, aad)
}

// AppendOpen appends the recovered plaintext to dst and returns the
// extended slice, as [Open] does for a fresh one.
//
// dst is appended to, not overwritten; pass buf[:0] to reuse a
// buffer's capacity. On failure nil is returned and dst is not
// written to, so a caller cannot mistake a partial result for a
// plaintext.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the plaintext.
func AppendOpen(dst []byte, a AEAD, sealed, aad []byte) ([]byte, error) {
	name, rest, err := splitEnvelope(sealed)
	if err != nil {
		return nil, err
	}

	// Compared without converting either side to a new string: the
	// compiler recognises this shape and allocates nothing.
	if string(name) != string(a.Algorithm()) {
		return nil, ErrAlgorithmMismatch
	}

	nonceSize := a.NonceSize()
	if len(rest) < nonceSize {
		return nil, ErrCiphertextShort
	}

	scratch := aadPool.Get()
	framed := frameAAD((*scratch)[:0], name, aad)
	plaintext, err := a.Open(dst, rest[:nonceSize], rest[nonceSize:], framed)
	*scratch = framed[:0]
	aadPool.Put(scratch)

	if err != nil {
		return nil, err //nolint:wrapcheck // authentication failures must remain indistinguishable
	}

	return plaintext, nil
}

// PeekAlgorithm reports the algorithm a sealed envelope names, without
// a key and without authenticating anything.
//
// A caller with several implementations uses it to choose which to
// open with, so the dispatch key comes from the ciphertext and not
// from a side channel that has to be kept in step with it.
//
// The name is attacker-supplied and is only ever a hint for selecting
// a key. It does not show whether the bytes are genuine. The tag
// decides that, and the algorithm is bound into what the tag covers,
// so a rewritten name fails to authenticate. Treat a name you do not
// recognise as a rejection, never as a reason to try something else.
//
// Returns [ErrCiphertextShort], [ErrEnvelopeVersion], or
// [ErrAlgorithmSize] for an envelope whose header does not parse.
//
// # Allocation contract
//
// One allocation for the returned string.
func PeekAlgorithm(sealed []byte) (Algorithm, error) {
	name, _, err := splitEnvelope(sealed)
	if err != nil {
		return "", err
	}

	return Algorithm(name), nil
}
