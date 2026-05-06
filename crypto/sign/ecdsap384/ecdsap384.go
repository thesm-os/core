// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// ErrNilKey is returned when a constructor receives a nil
// private or public key.
var ErrNilKey = errors.New("crypto/sign/ecdsap384: nil key")

// ErrWrongCurve is returned when a constructor receives a key
// whose curve is not [elliptic.P384].
var ErrWrongCurve = errors.New("crypto/sign/ecdsap384: key curve is not P-384")

// ErrInvalidPublicKey is returned when public-key bytes cannot
// be parsed as a PKIX-encoded ECDSA P-384 public key.
var ErrInvalidPublicKey = errors.New(
	"crypto/sign/ecdsap384: public-key bytes are not PKIX-encoded ECDSA P-384",
)

// Verifier verifies ECDSA P-384 + SHA-384 signatures against a
// fixed public key. Safe for concurrent use.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm]
// are zero-alloc. [Verifier.Verify] allocates because
// [crypto/ecdsa.VerifyASN1] performs big.Int arithmetic — a
// stdlib constraint, distinct from Ed25519's zero-alloc Verify.
type Verifier struct {
	pub      *ecdsa.PublicKey
	pubBytes []byte // PKIX (X.509 SubjectPublicKeyInfo) DER
	keyID    sign.KeyID
}

// Compile-time interface checks.
var (
	_ sign.Verifier          = (*Verifier)(nil)
	_ sign.StreamingVerifier = (*Verifier)(nil)
)

// NewVerifier wraps an existing P-384 public key in a
// [Verifier]. Returns [ErrNilKey] for nil input and
// [ErrWrongCurve] for keys not on [elliptic.P384].
func NewVerifier(pub *ecdsa.PublicKey) (*Verifier, error) {
	if pub == nil {
		return nil, ErrNilKey
	}
	if pub.Curve != elliptic.P384() {
		return nil, ErrWrongCurve
	}
	// x509.MarshalPKIXPublicKey on a *ecdsa.PublicKey with
	// curve already validated as P-384 has no error path:
	// the stdlib's failure modes are for unsupported key
	// types or malformed-curve keys, neither of which
	// applies here.
	pubBytes, _ := x509.MarshalPKIXPublicKey(pub)
	keyID, err := KeyIDFromPub(pub)
	if err != nil {
		// Off-curve point — propagate so callers see the real cause.
		return nil, err
	}
	return &Verifier{pub: pub, pubBytes: pubBytes, keyID: keyID}, nil
}

// NewVerifierFromPKIX parses PKIX-encoded public-key bytes
// (X.509 SubjectPublicKeyInfo DER, the format returned by
// [Verifier.PublicKey]) and wraps the result in a [Verifier].
// Returns [ErrInvalidPublicKey] for bytes that are not a valid
// ECDSA P-384 PKIX encoding.
func NewVerifierFromPKIX(pubBytes []byte) (*Verifier, error) {
	parsed, err := x509.ParsePKIXPublicKey(pubBytes)
	if err != nil {
		return nil, ErrInvalidPublicKey
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, ErrInvalidPublicKey
	}
	// Delegate curve validation + KeyID derivation to
	// NewVerifier; both functions then share one tested
	// implementation. Errors include [ErrWrongCurve] (P-256
	// or other non-P-384 curves) and any propagated
	// off-curve detection from KeyIDFromPub.
	v, err := NewVerifier(pub)
	if err != nil {
		return nil, err
	}
	// Override v.pubBytes with the supplied source bytes
	// (defensive copy). NewVerifier marshals its own PKIX,
	// which is byte-identical to the source for canonical
	// inputs but may diverge for non-canonical PKIX —
	// preserving the caller's bytes is the conservative
	// choice.
	keep := make([]byte, len(pubBytes))
	copy(keep, pubBytes)
	v.pubBytes = keep
	return v, nil
}

// KeyID returns the canonical key identifier
// SHA-256(SEC 1 uncompressed point)[:16].
func (v *Verifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the PKIX-encoded public-key bytes (X.509
// SubjectPublicKeyInfo DER). The returned slice aliases
// internal storage; callers must treat it as immutable.
func (v *Verifier) PublicKey() []byte { return v.pubBytes }

// Algorithm returns [crypto.AlgECDSAP384].
func (*Verifier) Algorithm() crypto.Algorithm { return crypto.AlgECDSAP384 }

// Verify reports whether sig is a valid ECDSA P-384 + SHA-384
// signature over msg under v's public key. Returns false for
// any reason verification cannot succeed (wrong key, malformed
// DER signature, length mismatch, cryptographic invalidity).
func (v *Verifier) Verify(msg, sig []byte) bool {
	digest := sha512.Sum384(msg)
	return ecdsa.VerifyASN1(v.pub, digest[:], sig)
}

// NewVerifyStream returns a fresh [sign.VerifyStream] backed by
// a SHA-384 hash. Bytes written to the stream are absorbed into
// the hash; [sign.VerifyStream.Verify] finalises the digest and
// runs the ECDSA verification on it. The stream is single-use.
func (v *Verifier) NewVerifyStream() sign.VerifyStream {
	return &verifyStream{h: sha512.New384(), pub: v.pub}
}

// Signer signs messages with a fixed P-384 private key. Safe
// for concurrent use.
type Signer struct {
	*Verifier
	priv *ecdsa.PrivateKey
}

// Compile-time interface checks.
var (
	_ sign.Signer          = (*Signer)(nil)
	_ sign.StreamingSigner = (*Signer)(nil)
)

// New wraps an existing P-384 private key in a [Signer].
// Returns [ErrNilKey] for nil input and [ErrWrongCurve] for
// keys not on [elliptic.P384].
func New(priv *ecdsa.PrivateKey) (*Signer, error) {
	if priv == nil {
		return nil, ErrNilKey
	}
	if priv.Curve != elliptic.P384() {
		return nil, ErrWrongCurve
	}
	// NewVerifier on priv.PublicKey can't fail: priv.Curve is
	// validated above (so the curve check inside NewVerifier
	// passes), and priv.PublicKey is on-curve by construction
	// for any [ecdsa.PrivateKey] that survived stdlib's
	// generation / parsing path (so KeyIDFromPub succeeds).
	v, _ := NewVerifier(&priv.PublicKey)
	return &Signer{Verifier: v, priv: priv}, nil
}

// Generate creates a fresh ECDSA P-384 keypair drawing from
// the runtime's secure entropy source.
//
// Unlike [crypto/sign/ed25519.Generate], this function does not
// take a [go.thesmos.sh/core/rand.Rand] parameter. Go 1.26's
// [crypto/ecdsa.GenerateKey] ignores any supplied
// [io.Reader] and uses the runtime's internal secure RNG
// regardless (unless `GODEBUG=cryptocustomrand=1` is set).
// Accepting a [go.thesmos.sh/core/rand.Rand] would be a misleading API shape —
// the parameter would silently do nothing. Tests requiring
// deterministic ECDSA key generation use
// [testing/cryptotest.SetGlobalRandom].
func Generate() (*Signer, error) {
	// Wrapped through wrapGenerate so the error path is
	// reachable from internal tests via synthetic input —
	// Go 1.26's [ecdsa.GenerateKey] uses an internal secure
	// RNG and cannot fail in default mode.
	return wrapGenerate(ecdsa.GenerateKey(elliptic.P384(), stdrand.Reader))
}

// wrapGenerate finalises a fresh keypair into a [Signer],
// wrapping any stdlib-supplied error with package context.
// Extracted so tests can drive the error branch directly with
// synthetic input.
func wrapGenerate(priv *ecdsa.PrivateKey, err error) (*Signer, error) {
	if err != nil {
		return nil, fmt.Errorf("crypto/sign/ecdsap384: generate: %w", err)
	}
	return New(priv)
}

// Sign returns the ECDSA P-384 + SHA-384 signature for msg in
// ASN.1 DER encoding. Per Go 1.26's [crypto/ecdsa.SignASN1]
// contract, the entropy source is selected internally by the
// stdlib (ignoring any caller-supplied reader unless
// `GODEBUG=cryptocustomrand=1`); we pass [crypto/rand.Reader]
// for compatibility with the function signature.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	digest := sha512.Sum384(msg)
	// Wrapped through wrapSign so the error branch is
	// reachable from internal tests — Go 1.26's
	// [ecdsa.SignASN1] uses an internal secure RNG and
	// cannot fail in default mode.
	return wrapSign(ecdsa.SignASN1(stdrand.Reader, s.priv, digest[:]))
}

// wrapSign attaches package context to a stdlib-supplied
// signing error. Extracted so tests can drive the error branch
// directly with synthetic input.
func wrapSign(sig []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, fmt.Errorf("crypto/sign/ecdsap384: sign: %w", err)
	}
	return sig, nil
}

// NewSignStream returns a fresh [sign.SignStream] backed by a
// SHA-384 hash. Bytes written to the stream are absorbed into
// the hash; [sign.SignStream.SignAndReset] finalises the digest,
// signs it, and resets the underlying hash for reuse with the
// next message.
func (s *Signer) NewSignStream() sign.SignStream {
	return &signStream{h: sha512.New384(), priv: s.priv}
}

// KeyIDFromPub derives the canonical [sign.KeyID] from a P-384
// public key: SHA-256(SEC 1 uncompressed point)[:16].
//
// SEC 1 uncompressed encoding is:
//
//	0x04 || X(48 byte big-endian) || Y(48 byte big-endian)
//
// (97 bytes total). Fixed-width big-endian coordinates make the
// encoding deterministic across builds and languages — distinct
// from the variable-length PKIX bytes returned by
// [Verifier.PublicKey].
//
// The encoding goes through [crypto/ecdsa.PublicKey.Bytes],
// which validates that (X, Y) lies on the curve. Off-curve
// points return an error (Go 1.25+ deprecated direct
// X / Y access for cryptographic encoding).
func KeyIDFromPub(pub *ecdsa.PublicKey) (sign.KeyID, error) {
	canonical, err := pub.Bytes()
	if err != nil {
		return sign.KeyID{}, fmt.Errorf("crypto/sign/ecdsap384: encode public key: %w", err)
	}
	h := sha256.Sum256(canonical)
	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])
	return id, nil
}

// signStream wraps a SHA-384 [hash.Hash] for [sign.SignStream].
// The receiver-owned digest buffer keeps the hash-finalise path
// zero-alloc; the returned signature itself is allocated by
// [crypto/ecdsa.SignASN1] (stdlib constraint).
type signStream struct {
	h    hash.Hash
	priv *ecdsa.PrivateKey
}

// Compile-time interface check.
var _ sign.SignStream = (*signStream)(nil)

// Write feeds p into the underlying SHA-384 hash. The stdlib
// [hash.Hash] contract guarantees no error path, so Write
// always reports (len(p), nil).
func (ss *signStream) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	n, _ := ss.h.Write(p)
	return n, nil
}

// SignAndReset finalises the SHA-384 digest, signs it under the
// stream's private key, and resets the hash so the stream can
// be reused for the next message.
func (ss *signStream) SignAndReset() ([]byte, error) {
	var digest [sha512.Size384]byte
	ss.h.Sum(digest[:0])
	ss.h.Reset()
	return wrapSign(ecdsa.SignASN1(stdrand.Reader, ss.priv, digest[:]))
}

// verifyStream wraps a SHA-384 [hash.Hash] for [sign.VerifyStream].
type verifyStream struct {
	h   hash.Hash
	pub *ecdsa.PublicKey
}

// Compile-time interface check.
var _ sign.VerifyStream = (*verifyStream)(nil)

// Write feeds p into the underlying SHA-384 hash. The stdlib
// [hash.Hash] contract guarantees no error path, so Write
// always reports (len(p), nil).
func (vs *verifyStream) Write(p []byte) (int, error) {
	// hash.Hash.Write never returns a non-nil error per the
	// stdlib contract; ignoring is safe.
	n, _ := vs.h.Write(p)
	return n, nil
}

// Verify finalises the SHA-384 digest and reports whether sig
// is a valid signature over the absorbed bytes. The stream is
// single-use; consumers create a new one via
// [Verifier.NewVerifyStream] for the next message.
func (vs *verifyStream) Verify(sig []byte) bool {
	var digest [sha512.Size384]byte
	vs.h.Sum(digest[:0])
	return ecdsa.VerifyASN1(vs.pub, digest[:], sig)
}
