// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa

import (
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
// context string. Safe for concurrent use.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm] and
// [Verifier.Verify] are zero-alloc.
type Verifier struct {
	pub   *stdmldsa.PublicKey
	opts  *stdmldsa.Options
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
// context. An ML-DSA verifier needs both, so the entry binds them, and
// the Resolver's key for it is p.Algorithm().
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

// unavailable reports that the FIPS 140-3 module in use does not
// provide ML-DSA.
func unavailable(err error) error {
	return errs.WithClass(fmt.Errorf("mldsa: %w", err), errs.Unsupported)
}

// newVerifier builds a Verifier over a parsed key and a checked
// context.
func newVerifier(p Params, pk *stdmldsa.PublicKey, context string) *Verifier {
	enc := pk.Bytes()

	return &Verifier{
		pub:   pk,
		opts:  &stdmldsa.Options{Context: context},
		enc:   enc,
		keyID: KeyIDFromPub(enc),
		p:     p,
	}
}

// KeyID returns SHA-256 of the encoded public key, truncated to 16
// bytes.
func (v *Verifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the FIPS 204 encoding of the public key. The
// returned slice aliases internal storage; callers must treat it as
// immutable.
func (v *Verifier) PublicKey() []byte { return v.enc }

// Algorithm returns the parameter set's long-term name.
func (v *Verifier) Algorithm() crypto.Algorithm { return v.p.Algorithm() }

// Verify reports whether sig is a valid signature over msg under v's
// public key and context. It returns false for a signature made under
// another context, another key, or for malformed bytes.
func (v *Verifier) Verify(msg, sig []byte) bool {
	return stdmldsa.Verify(v.pub, msg, sig, v.opts) == nil
}

// Signer signs with one ML-DSA private key under one context string.
// Every Signer is a [Verifier] for the same key and context. Safe for
// concurrent use.
//
// # Allocation contract
//
// [Signer.Sign] allocates the signature once.
type Signer struct {
	*Verifier

	priv *stdmldsa.PrivateKey
}

// Compile-time interface check.
var _ sign.Signer = (*Signer)(nil)

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
// Allocates the signature once.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return s.priv.Sign(nil, msg, s.opts) //nolint:wrapcheck // returned as the standard library produced it
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
// Zero-alloc.
func KeyIDFromPub(pub []byte) sign.KeyID {
	h := sha256.Sum256(pub)

	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])

	return id
}
