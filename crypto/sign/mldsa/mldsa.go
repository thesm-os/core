// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa

import (
	"context"
	stdmldsa "crypto/mldsa"
	"crypto/sha256"
	"fmt"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
)

// Params selects a FIPS 204 parameter set. The zero value is not a
// parameter set.
type Params uint8

const (
	// MLDSA44 is ML-DSA-44: 1,312-byte public keys and 2,420-byte
	// signatures, NIST security category 2.
	MLDSA44 Params = 1

	// MLDSA65 is ML-DSA-65: 1,952-byte public keys and 3,309-byte
	// signatures, NIST security category 3.
	MLDSA65 Params = 2

	// MLDSA87 is ML-DSA-87: 2,592-byte public keys and 4,627-byte
	// signatures, NIST security category 5. CNSA 2.0 requires it.
	MLDSA87 Params = 3
)

// SeedSize is the length of a private-key seed, the same for every
// parameter set.
const SeedSize = stdmldsa.PrivateKeySize

// maxContext is the longest context string FIPS 204 admits.
const maxContext = 255

// Algorithm returns the parameter set's long-term name:
// [crypto.AlgMLDSA44], [crypto.AlgMLDSA65] or [crypto.AlgMLDSA87]. It
// returns the empty name for any other value.
func (p Params) Algorithm() crypto.Algorithm {
	switch p {
	case MLDSA44:
		return crypto.AlgMLDSA44
	case MLDSA65:
		return crypto.AlgMLDSA65
	case MLDSA87:
		return crypto.AlgMLDSA87
	default:
		return ""
	}
}

// std returns the standard library's parameter set for p, or
// [ErrParams].
func (p Params) std() (stdmldsa.Parameters, error) {
	switch p {
	case MLDSA44:
		return stdmldsa.MLDSA44(), nil
	case MLDSA65:
		return stdmldsa.MLDSA65(), nil
	case MLDSA87:
		return stdmldsa.MLDSA87(), nil
	default:
		return stdmldsa.Parameters{}, ErrParams
	}
}

// Verifier verifies ML-DSA signatures under one public key and one
// context string.
//
// # Concurrency
//
// A Verifier is safe for concurrent use.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm] and
// [Verifier.Verify] do not allocate. [NewVerifier] allocates the
// Verifier, its copy of the encoded key, and the key that
// [crypto/mldsa.NewPublicKey] parses.
type Verifier struct {
	pub *stdmldsa.PublicKey

	// opts contains the context string. Verify and the Sign of a Signer
	// pass its address, which points into the Verifier, so the options
	// are no allocation of their own.
	opts stdmldsa.Options

	enc   []byte
	keyID sign.KeyID
	p     Params
}

// Compile-time interface check.
var _ sign.Verifier = (*Verifier)(nil)

// NewVerifier returns a Verifier for the encoded public key pub under
// context. pub is copied, so the caller may reuse its buffer.
//
// Returns [ErrParams] for an unknown parameter set, [ErrContext] for a
// context longer than 255 bytes, and [ErrPublicKey] when pub is not a
// public key of p. Returns an error classified [errs.Unsupported] when
// the FIPS 140-3 module in use does not provide ML-DSA, as module
// v1.0.0 does not.
func NewVerifier(p Params, pub []byte, context string) (*Verifier, error) {
	return parseVerifier(p, pub, context, stdmldsa.NewPublicKey)
}

// parseVerifier is NewVerifier with the standard library's parser
// passed in, so a test can exercise the path where the module does not
// provide ML-DSA.
func parseVerifier(
	p Params, pub []byte, context string,
	parse func(stdmldsa.Parameters, []byte) (*stdmldsa.PublicKey, error),
) (*Verifier, error) {
	params, err := p.std()
	if err != nil {
		return nil, err
	}

	if len(context) > maxContext {
		return nil, ErrContext
	}

	// Every encoding of the right length is a public key, so the
	// parser can fail only when the module does not provide ML-DSA.
	if len(pub) != params.PublicKeySize() {
		return nil, ErrPublicKey
	}

	pk, err := parse(params, pub)
	if err != nil {
		return nil, unavailable(err)
	}

	return newVerifier(p, pk, context), nil
}

// Resolver returns the [sign.Resolver] entry for parameter set p under
// context. An ML-DSA verifier needs both, so the entry binds them. The
// key of the entry in a Resolver is p.Algorithm().
//
// The entry returns the errors [NewVerifier] returns, with a nil
// Verifier. An invalid p or context fails on every call.
func Resolver(p Params, context string) func(pub []byte) (sign.Verifier, error) {
	return func(pub []byte) (sign.Verifier, error) {
		v, err := NewVerifier(p, pub, context)
		if err != nil {
			return nil, err
		}

		return v, nil
	}
}

// unavailable returns err under the package name, classified
// [errs.Unsupported]. The callers pass the error of a FIPS 140-3 module
// that does not provide ML-DSA.
func unavailable(err error) error {
	return errs.WithClass(fmt.Errorf("mldsa: %w", err), errs.Unsupported)
}

// newVerifier builds a Verifier over a parsed key and a checked
// context.
func newVerifier(p Params, pk *stdmldsa.PublicKey, context string) *Verifier {
	enc := pk.Bytes()

	return &Verifier{
		pub:   pk,
		opts:  stdmldsa.Options{Context: context},
		enc:   enc,
		keyID: KeyIDFromPub(enc),
		p:     p,
	}
}

// KeyID returns SHA-256 of the encoded public key, truncated to 16
// bytes.
func (v *Verifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the FIPS 204 encoding of the public key. The
// returned slice aliases the storage of the Verifier. Callers must treat
// it as immutable.
func (v *Verifier) PublicKey() []byte { return v.enc }

// Algorithm returns the parameter set's long-term name.
func (v *Verifier) Algorithm() crypto.Algorithm { return v.p.Algorithm() }

// Context returns the FIPS 204 context string under which v verifies,
// and under which a [Signer] of the same key signs. A caller that
// accepts a signer only under one context checks it here, because a
// signature under another context verifies under no other.
//
// # Allocation contract
//
// Context does not allocate.
func (v *Verifier) Context() string { return v.opts.Context }

// Verify reports whether sig is a valid signature over msg under v's
// public key and context. It returns false for a signature made under
// another context, another key, or for malformed bytes.
func (v *Verifier) Verify(msg, sig []byte) bool {
	return stdmldsa.Verify(v.pub, msg, sig, &v.opts) == nil
}

// Signer signs with one ML-DSA private key under one context string.
// Every Signer is a [Verifier] for the same key and context.
//
// # Concurrency
//
// A Signer is safe for concurrent use.
//
// # Allocation contract
//
// [Signer.Sign] allocates the signature once.
type Signer struct {
	*Verifier

	priv *stdmldsa.PrivateKey
}

// Compile-time interface checks.
var (
	_ sign.Signer       = (*Signer)(nil)
	_ sign.AppendSigner = (*Signer)(nil)
)

// New returns a Signer for the private key derived from the 32-byte
// FIPS 204 seed, under context. The standard library copies seed into
// the key, so the caller may zero its own copy afterwards.
//
// Returns [ErrParams] for an unknown parameter set, [ErrSeed] for a
// seed that is not [SeedSize] bytes, and [ErrContext] for a context
// longer than 255 bytes. Returns an error classified [errs.Unsupported]
// when the FIPS 140-3 module in use does not provide ML-DSA.
func New(p Params, seed []byte, context string) (*Signer, error) {
	return expandSigner(p, seed, context, stdmldsa.NewPrivateKey)
}

// expandSigner is New with the standard library's key expansion passed
// in, so a test can exercise the path where the module does not
// provide ML-DSA.
func expandSigner(
	p Params, seed []byte, context string,
	expand func(stdmldsa.Parameters, []byte) (*stdmldsa.PrivateKey, error),
) (*Signer, error) {
	params, err := p.std()
	if err != nil {
		return nil, err
	}

	if len(seed) != SeedSize {
		return nil, ErrSeed
	}

	if len(context) > maxContext {
		return nil, ErrContext
	}

	// The seed has the right length, so expansion can fail only when
	// the module does not provide ML-DSA.
	sk, err := expand(params, seed)
	if err != nil {
		return nil, unavailable(err)
	}

	return &Signer{Verifier: newVerifier(p, sk.PublicKey(), context), priv: sk}, nil
}

// Generate returns a Signer for a fresh key whose seed is read from r,
// under context. A seeded r gives a reproducible key, which tests use.
// When r fills the seed, Generate zeroes the buffer before it returns,
// so only the key contains the seed.
//
// Returns the errors [New] returns, and the error r returns when it
// cannot fill the seed.
func Generate(p Params, r rand.Rand, context string) (*Signer, error) {
	seed := make([]byte, SeedSize)
	if _, err := r.Read(seed); err != nil {
		return nil, err //nolint:wrapcheck // returned as the source produced it
	}

	s, err := New(p, seed, context)
	clear(seed)

	return s, err
}

// Sign returns a hedged ML-DSA signature over msg under s's context.
//
// # Allocation contract
//
// Sign allocates the signature once.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return s.priv.Sign(nil, msg, &s.opts) //nolint:wrapcheck // returned as the standard library produced it
}

// AppendSign appends a hedged ML-DSA signature over msg under s's
// context to dst and returns the extended slice. It implements
// [sign.AppendSigner].
//
// Returns dst unchanged and context.Cause(ctx) when ctx has ended, and
// dst unchanged and the error of [Signer.Sign]. The signature is
// computed in process, so the check before signing is the only one.
//
// # Allocation contract
//
// AppendSign allocates the signature once, inside
// [crypto/mldsa.PrivateKey.Sign], which returns a new slice. It also
// allocates when dst has no room for the signature.
func (s *Signer) AppendSign(ctx context.Context, dst, msg []byte) ([]byte, error) {
	if err := context.Cause(ctx); err != nil {
		return dst, err
	}

	sig, err := s.Sign(msg)

	return appendSignature(dst, sig, err)
}

// appendSignature appends sig to dst, or returns dst unchanged and err
// when Sign failed. It is a function of its own so that a test can run
// the error branch. [crypto/mldsa.PrivateKey.Sign] fails only for the
// zero private key, which [New] never builds.
func appendSignature(dst, sig []byte, err error) ([]byte, error) {
	if err != nil {
		return dst, err
	}

	return append(dst, sig...), nil
}

// Seed returns a copy of the private key's 32-byte seed, for storage
// in a custodian. The caller zeroes the copy when it no longer needs
// it.
func (s *Signer) Seed() []byte { return s.priv.Bytes() }

// KeyIDFromPub returns SHA-256 of the encoded public key, truncated to
// [sign.KeyIDSize] bytes. The same key gives the same KeyID across
// builds and verifier services.
//
// # Allocation contract
//
// KeyIDFromPub does not allocate.
func KeyIDFromPub(pub []byte) sign.KeyID {
	h := sha256.Sum256(pub)

	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])

	return id
}
