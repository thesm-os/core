// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"crypto/ecdsa"
	stded25519 "crypto/ed25519"
	"crypto/sha512"
	"hash"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// The messages that the checks of the sign suites sign.
var (
	signPangram      = []byte("the quick brown fox jumps over the lazy dog")
	signOther        = []byte("another message")
	signTampered     = []byte("verify-me")
	signAny          = []byte("any")
	signSplit        = []byte("agentic-context-payload-streamed-across-many-writes")
	signFirst        = []byte("first")
	signSecond       = []byte("second")
	signBody         = []byte("body")
	signPayload      = []byte("payload")
	signEmptyMarker  = []byte{0}
	signWrongLengths = [][]byte{nil, {}, make([]byte, 1), make([]byte, 16)}
)

// SignerAlgorithmIsStable checks that Algorithm returns the same name on every
// call.
//
//dokimi:check Signer.Algorithm defect=signerAlternatesItsAlgorithm
func SignerAlgorithmIsStable(h *SignerHandle) {
	assert.Deterministic(h, func(s sign.Signer) (crypto.Algorithm, error) { return s.Algorithm(), nil }, h.Subject(),
		h.Statement())
}

// SignerKeyIDIsStable checks that KeyID returns the same key ID on every call.
//
//dokimi:check Signer.KeyID defect=signerAlternatesItsKeyID
func SignerKeyIDIsStable(h *SignerHandle) {
	assert.Deterministic(h, func(s sign.Signer) (sign.KeyID, error) { return s.KeyID(), nil }, h.Subject(),
		h.Statement())
}

// SignerPublicKeyIsStable checks that PublicKey returns the same bytes on every
// call.
//
//dokimi:check Signer.PublicKey defect=signerAlternatesItsPublicKey
func SignerPublicKeyIsStable(h *SignerHandle) {
	assert.Deterministic(h, func(s sign.Signer) ([]byte, error) { return s.PublicKey(), nil }, h.Subject(),
		h.Statement())
}

// SignerSignaturesVerify checks that Verify accepts the signature of Sign over
// a message.
//
//dokimi:check Signer.Sign defect=signerSignsAnotherMessage
func SignerSignaturesVerify(h *SignerHandle) {
	s := h.Subject()
	assert.True(h, s.Verify(signPangram, signWith(h, s, signPangram)), h.Statement())
}

// SignerSignsAnEmptyMessage checks that Verify accepts the signature of Sign
// over a nil message, for a nil and for an empty message.
//
//dokimi:check Signer.Sign defect=signerRefusesAnEmptyMessage
func SignerSignsAnEmptyMessage(h *SignerHandle) {
	s := h.Subject()
	sig := signWith(h, s, nil)
	assert.True(h, s.Verify(nil, sig), h.Statement())
	assert.True(h, s.Verify([]byte{}, sig), h.Statement())
}

// SignerRefusesAFlippedSignatureBit checks that Verify refuses a signature of
// Sign whose first bit changed.
//
//dokimi:check Signer.Verify defect=signerRestoresTheFirstSignatureByte
func SignerRefusesAFlippedSignatureBit(h *SignerHandle) {
	s := h.Subject()
	sig := signWith(h, s, signTampered)
	sig[0] ^= 0x01
	assert.False(h, s.Verify(signTampered, sig), h.Statement())
}

// SignerRefusesAFlippedMessageBit checks that Verify refuses a signature of
// Sign for its message with the first bit changed.
//
//dokimi:check Signer.Verify defect=signerRestoresTheFirstMessageByte
func SignerRefusesAFlippedMessageBit(h *SignerHandle) {
	s := h.Subject()
	sig := signWith(h, s, signTampered)
	tampered := bytes.Clone(signTampered)
	tampered[0] ^= 0x01
	assert.False(h, s.Verify(tampered, sig), h.Statement())
}

// SignerRefusesASignatureOfAWrongLength checks that Verify refuses a nil, an
// empty, a 1-byte and a 16-byte signature.
//
//dokimi:check Signer.Verify defect=signerAcceptsANilSignature
func SignerRefusesASignatureOfAWrongLength(h *SignerHandle) {
	s := h.Subject()
	for _, sig := range signWrongLengths {
		assert.False(h, s.Verify(signAny, sig), h.Statement())
	}
}

// VerifierAlgorithmIsStable checks that Algorithm returns the same name on
// every call.
//
//dokimi:check Verifier.Algorithm defect=verifierAlternatesItsAlgorithm
func VerifierAlgorithmIsStable(h *VerifierHandle) {
	assert.Deterministic(h, func(v sign.Verifier) (crypto.Algorithm, error) { return v.Algorithm(), nil },
		h.Subject(), h.Statement())
}

// VerifierKeyIDIsStable checks that KeyID returns the same key ID on every
// call.
//
//dokimi:check Verifier.KeyID defect=verifierAlternatesItsKeyID
func VerifierKeyIDIsStable(h *VerifierHandle) {
	assert.Deterministic(h, func(v sign.Verifier) (sign.KeyID, error) { return v.KeyID(), nil }, h.Subject(),
		h.Statement())
}

// VerifierPublicKeyIsStable checks that PublicKey returns the same bytes on
// every call.
//
//dokimi:check Verifier.PublicKey defect=verifierAlternatesItsPublicKey
func VerifierPublicKeyIsStable(h *VerifierHandle) {
	assert.Deterministic(h, func(v sign.Verifier) ([]byte, error) { return v.PublicKey(), nil }, h.Subject(),
		h.Statement())
}

// VerifierRefusesANilSignature checks that Verify refuses a nil signature.
//
//dokimi:check Verifier.Verify defect=verifierAcceptsANilSignature
func VerifierRefusesANilSignature(h *VerifierHandle) {
	assert.False(h, h.Subject().Verify(signAny, nil), h.Statement())
}

// VerifierRefusesAnEmptySignature checks that Verify refuses an empty
// signature.
//
//dokimi:check Verifier.Verify defect=verifierAcceptsAnEmptySignature
func VerifierRefusesAnEmptySignature(h *VerifierHandle) {
	assert.False(h, h.Subject().Verify(signAny, []byte{}), h.Statement())
}

// VerifierAcceptsASignatureOfItsKey checks that Verify accepts the signature of
// the Signer of its key. The second subject of an application is that Signer.
//
//dokimi:check Verifier.Verify defect=verifierRefusesEverySignature second=verifierSigner
func VerifierAcceptsASignatureOfItsKey(h *VerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signPangram)
	assert.True(h, h.Subject().Verify(signPangram, sig), h.Statement())
}

// VerifierRefusesAFlippedSignatureBit checks that Verify refuses a signature of
// the Signer of its key whose first bit changed.
//
//dokimi:check Verifier.Verify defect=verifierRestoresTheFirstSignatureByte second=verifierSigner
func VerifierRefusesAFlippedSignatureBit(h *VerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signTampered)
	sig[0] ^= 0x01
	assert.False(h, h.Subject().Verify(signTampered, sig), h.Statement())
}

// VerifierRefusesAFlippedMessageBit checks that Verify refuses a signature of
// the Signer of its key for its message with the first bit changed.
//
//dokimi:check Verifier.Verify defect=verifierRestoresTheFirstMessageByte second=verifierSigner
func VerifierRefusesAFlippedMessageBit(h *VerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signTampered)
	tampered := bytes.Clone(signTampered)
	tampered[0] ^= 0x01
	assert.False(h, h.Subject().Verify(tampered, sig), h.Statement())
}

// StreamingSignerStreamSignatureVerifies checks that Verify accepts the
// signature of a SignStream over the message that the stream absorbed.
//
//dokimi:check StreamingSigner.NewSignStream defect=signStreamSignsAnotherMessageForThePangram
func StreamingSignerStreamSignatureVerifies(h *StreamingSignerHandle) {
	s := h.Subject()
	st := s.NewSignStream()
	streamWrite(h, st, signPangram)
	assert.True(h, s.Verify(signPangram, signAndReset(h, st)), h.Statement())
}

// StreamingSignerStreamAbsorbsSplitWrites checks that a SignStream signs the
// message of three Writes of its parts.
//
//dokimi:check StreamingSigner.NewSignStream defect=signStreamAbsorbsTheFirstWriteOnly
func StreamingSignerStreamAbsorbsSplitWrites(h *StreamingSignerHandle) {
	s := h.Subject()
	st := s.NewSignStream()
	streamWrite(h, st, signSplit[:10])
	streamWrite(h, st, signSplit[10:25])
	streamWrite(h, st, signSplit[25:])
	assert.True(h, s.Verify(signSplit, signAndReset(h, st)), h.Statement())
}

// StreamingSignerSignAndResetClearsTheStream checks that the second
// SignAndReset of a SignStream signs only the message after the first.
//
//dokimi:check StreamingSigner.NewSignStream defect=signStreamKeepsItsInputOnSignAndReset
func StreamingSignerSignAndResetClearsTheStream(h *StreamingSignerHandle) {
	s := h.Subject()
	st := s.NewSignStream()
	streamWrite(h, st, signFirst)
	signAndReset(h, st)
	streamWrite(h, st, signSecond)
	assert.True(h, s.Verify(signSecond, signAndReset(h, st)), h.Statement())
}

// StreamingSignerStreamIgnoresEmptyWrites checks that a nil and an empty Write
// do not change the message that a SignStream signs.
//
//dokimi:check StreamingSigner.NewSignStream defect=signStreamAbsorbsAMarkerForAnEmptyWrite
func StreamingSignerStreamIgnoresEmptyWrites(h *StreamingSignerHandle) {
	s := h.Subject()
	st := s.NewSignStream()
	streamWrite(h, st, nil)
	streamWrite(h, st, []byte{})
	streamWrite(h, st, signBody)
	assert.True(h, s.Verify(signBody, signAndReset(h, st)), h.Statement())
}

// StreamingVerifierStreamAcceptsASignatureOfItsKey checks that a VerifyStream
// accepts the signature of the Signer of its key over the message that the
// stream absorbed. The second subject of an application is that Signer.
//
//dokimi:check StreamingVerifier.NewVerifyStream defect=verifyStreamRefusesThePangram second=streamingVerifierSigner
func StreamingVerifierStreamAcceptsASignatureOfItsKey(h *StreamingVerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signPangram)
	vs := h.Subject().NewVerifyStream()
	streamWrite(h, vs, signPangram)
	assert.True(h, vs.Verify(sig), h.Statement())
}

// StreamingVerifierStreamAbsorbsSplitWrites checks that a VerifyStream accepts
// the signature of a message that it absorbed in two Writes.
//
//dokimi:check StreamingVerifier.NewVerifyStream defect=verifyStreamAbsorbsTheFirstWriteOnly second=streamingVerifierSigner
func StreamingVerifierStreamAbsorbsSplitWrites(h *StreamingVerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signSplit)
	vs := h.Subject().NewVerifyStream()
	streamWrite(h, vs, signSplit[:20])
	streamWrite(h, vs, signSplit[20:])
	assert.True(h, vs.Verify(sig), h.Statement())
}

// StreamingVerifierStreamRefusesAFlippedSignatureBit checks that a
// VerifyStream refuses a signature whose first bit changed.
//
//dokimi:check StreamingVerifier.NewVerifyStream defect=verifyStreamRestoresTheFirstSignatureByte second=streamingVerifierSigner
func StreamingVerifierStreamRefusesAFlippedSignatureBit(h *StreamingVerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signTampered)
	sig[0] ^= 0x01
	vs := h.Subject().NewVerifyStream()
	streamWrite(h, vs, signTampered)
	assert.False(h, vs.Verify(sig), h.Statement())
}

// StreamingVerifierStreamIgnoresEmptyWrites checks that a nil and an empty
// Write do not change the message that a VerifyStream verifies.
//
//dokimi:check StreamingVerifier.NewVerifyStream defect=verifyStreamAbsorbsAMarkerForAnEmptyWrite second=streamingVerifierSigner
func StreamingVerifierStreamIgnoresEmptyWrites(h *StreamingVerifierHandle) {
	sig := signWith(h, secondSigner(h, h.Second()), signBody)
	vs := h.Subject().NewVerifyStream()
	streamWrite(h, vs, nil)
	streamWrite(h, vs, signBody)
	streamWrite(h, vs, []byte{})
	assert.True(h, vs.Verify(sig), h.Statement())
}

// StreamingVerifierStreamRefusesANilSignature checks that a VerifyStream
// refuses a nil signature.
//
//dokimi:check StreamingVerifier.NewVerifyStream defect=verifyStreamAcceptsANilSignature
func StreamingVerifierStreamRefusesANilSignature(h *StreamingVerifierHandle) {
	vs := h.Subject().NewVerifyStream()
	streamWrite(h, vs, signAny)
	assert.False(h, vs.Verify(nil), h.Statement())
}

// signWith signs msg with s, and stops the check when Sign fails.
func signWith(h assert.TB, s sign.Signer, msg []byte) []byte {
	h.Helper()

	sig, err := s.Sign(msg)
	assert.NoError(h, err, "Sign signs the message")

	return sig
}

// secondSigner returns the second subject of a check as a Signer, and stops the
// check when the second subject is no Signer.
func secondSigner(h assert.TB, second any) sign.Signer {
	h.Helper()

	s, ok := second.(sign.Signer)
	assert.True(h, ok, "the second subject of the application is the Signer of the key")

	return s
}

// streamWrite writes p to w, and stops the check when Write fails or reports
// fewer bytes than p has.
func streamWrite(h assert.TB, w interface{ Write(p []byte) (int, error) }, p []byte) {
	h.Helper()

	n, err := w.Write(p)
	assert.NoError(h, err, "Write absorbs p")
	assert.Equal(h, n, len(p), "Write absorbs every byte of p")
}

// signAndReset returns the signature of SignAndReset of st, and stops the
// check when SignAndReset fails.
func signAndReset(h assert.TB, st sign.SignStream) []byte {
	h.Helper()

	sig, err := st.SignAndReset()
	assert.NoError(h, err, "SignAndReset signs the absorbed message")

	return sig
}

// ed25519Spec returns the spec of the signer of the canonical Ed25519 key of
// [NewEd25519Sample]. The planted defects of the suite of Signer change one
// behaviour of the stdlibSigner of the spec.
func ed25519Spec() StdlibSignerSpec {
	fix := NewEd25519Sample()

	return StdlibSignerSpec{
		Algorithm: crypto.AlgEd25519,
		KeyID:     fix.KeyID,
		PublicKey: fix.PublicKey,
		Sign:      func(msg []byte) ([]byte, error) { return stded25519.Sign(fix.StdlibPriv, msg), nil },
		Verify:    func(msg, sig []byte) bool { return stded25519.Verify(fix.PublicKey, msg, sig) },
	}
}

// soundVerifier returns the Ed25519 Verifier of the standard library that the
// planted defects of the suite of Verifier change.
func soundVerifier() *stdlibVerifier {
	spec := ed25519Spec()

	return &stdlibVerifier{spec: StdlibVerifierSpec{
		Algorithm: spec.Algorithm,
		KeyID:     spec.KeyID,
		PublicKey: spec.PublicKey,
		Verify:    spec.Verify,
	}}
}

// stdlibECDSASigner is the ECDSA P-384 signer of the canonical key of
// [NewECDSAP384Sample] over the standard library. It signs the SHA-384 digest
// of a message, so it is a [sign.StreamingSigner] and a
// [sign.StreamingVerifier]. The planted defects of the streaming suites change
// one behaviour of it.
type stdlibECDSASigner struct {
	fix ECDSAP384Fixture
}

// KeyID returns the key ID of the fixture.
func (s stdlibECDSASigner) KeyID() sign.KeyID { return s.fix.KeyID }

// PublicKey returns the PKIX encoding of the public key of the fixture.
func (s stdlibECDSASigner) PublicKey() []byte { return s.fix.PublicKey }

// Algorithm returns crypto.AlgECDSAP384.
func (stdlibECDSASigner) Algorithm() crypto.Algorithm { return crypto.AlgECDSAP384 }

// Sign returns the ASN.1 signature of the SHA-384 digest of message.
func (s stdlibECDSASigner) Sign(message []byte) ([]byte, error) {
	digest := sha512.Sum384(message)

	return ecdsa.SignASN1(randcrypto.New(), s.fix.StdlibPriv, digest[:])
}

// Verify reports whether signature is the ASN.1 signature of the SHA-384
// digest of message.
func (s stdlibECDSASigner) Verify(message, signature []byte) bool {
	digest := sha512.Sum384(message)

	return ecdsa.VerifyASN1(&s.fix.StdlibPriv.PublicKey, digest[:], signature)
}

// NewSignStream returns a stream that signs the SHA-384 digest of its input.
func (s stdlibECDSASigner) NewSignStream() sign.SignStream {
	return &stdlibSignStream{signer: s, digest: sha512.New384()}
}

// NewVerifyStream returns a stream that verifies a signature of the SHA-384
// digest of its input.
func (s stdlibECDSASigner) NewVerifyStream() sign.VerifyStream {
	return &stdlibVerifyStream{signer: s, digest: sha512.New384()}
}

// stdlibSignStream is the SignStream of a stdlibECDSASigner.
type stdlibSignStream struct {
	digest hash.Hash
	signer stdlibECDSASigner
}

// Write absorbs p.
func (s *stdlibSignStream) Write(p []byte) (int, error) { return s.digest.Write(p) }

// SignAndReset signs the digest of the absorbed bytes and resets the stream.
func (s *stdlibSignStream) SignAndReset() ([]byte, error) {
	sum := s.digest.Sum(nil)
	s.digest.Reset()

	return ecdsa.SignASN1(randcrypto.New(), s.signer.fix.StdlibPriv, sum)
}

// stdlibVerifyStream is the VerifyStream of a stdlibECDSASigner.
type stdlibVerifyStream struct {
	digest hash.Hash
	signer stdlibECDSASigner
}

// Write absorbs p.
func (s *stdlibVerifyStream) Write(p []byte) (int, error) { return s.digest.Write(p) }

// Verify reports whether sig is a signature of the digest of the absorbed
// bytes.
func (s *stdlibVerifyStream) Verify(sig []byte) bool {
	return ecdsa.VerifyASN1(&s.signer.fix.StdlibPriv.PublicKey, s.digest.Sum(nil), sig)
}

// signerAlternatesItsAlgorithm returns the planted defect of
// SignerAlgorithmIsStable: a Signer whose Algorithm alternates between two
// names.
func signerAlternatesItsAlgorithm(h *SignerHandle) sign.Signer {
	var calls int

	return NewSignerStub(h, SignerStubDelegateTo(&stdlibSigner{spec: ed25519Spec()}),
		WithSignerAlgorithm(func() crypto.Algorithm {
			calls++
			if calls%2 == 0 {
				return crypto.AlgECDSAP384
			}

			return crypto.AlgEd25519
		}))
}

// signerAlternatesItsKeyID returns the planted defect of SignerKeyIDIsStable: a
// Signer whose KeyID alternates between its key ID and the zero key ID.
func signerAlternatesItsKeyID(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}
	var calls int

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerKeyID(func() sign.KeyID {
		calls++
		if calls%2 == 0 {
			return sign.KeyID{}
		}

		return sound.KeyID()
	}))
}

// signerAlternatesItsPublicKey returns the planted defect of
// SignerPublicKeyIsStable: a Signer whose PublicKey alternates between its key
// and no bytes.
func signerAlternatesItsPublicKey(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}
	var calls int

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerPublicKey(func() []byte {
		calls++
		if calls%2 == 0 {
			return nil
		}

		return sound.PublicKey()
	}))
}

// signerSignsAnotherMessage returns the planted defect of
// SignerSignaturesVerify: a Signer whose Sign of the pangram signs another
// message.
func signerSignsAnotherMessage(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerSign(func(message []byte) ([]byte, error) {
		if bytes.Equal(message, signPangram) {
			return sound.Sign(signOther)
		}

		return sound.Sign(message)
	}))
}

// signerRefusesAnEmptyMessage returns the planted defect of
// SignerSignsAnEmptyMessage: a Signer whose Verify refuses every signature of
// an empty message.
func signerRefusesAnEmptyMessage(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerVerify(func(message, signature []byte) bool {
		return len(message) > 0 && sound.Verify(message, signature)
	}))
}

// signerRestoresTheFirstSignatureByte returns the planted defect of
// SignerRefusesAFlippedSignatureBit: a Signer whose Verify verifies a
// signature again with its first bit changed, when the signature fails.
func signerRestoresTheFirstSignatureByte(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerVerify(func(message, signature []byte) bool {
		return sound.Verify(message, signature) || len(signature) > 0 && sound.Verify(message, flipFirst(signature))
	}))
}

// signerRestoresTheFirstMessageByte returns the planted defect of
// SignerRefusesAFlippedMessageBit: a Signer whose Verify verifies a signature
// again for its message with the first bit changed, when the signature fails.
func signerRestoresTheFirstMessageByte(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerVerify(func(message, signature []byte) bool {
		return sound.Verify(message, signature) || len(message) > 0 && sound.Verify(flipFirst(message), signature)
	}))
}

// signerAcceptsANilSignature returns the planted defect of
// SignerRefusesASignatureOfAWrongLength: a Signer whose Verify accepts a nil
// signature.
func signerAcceptsANilSignature(h *SignerHandle) sign.Signer {
	sound := &stdlibSigner{spec: ed25519Spec()}

	return NewSignerStub(h, SignerStubDelegateTo(sound), WithSignerVerify(func(message, signature []byte) bool {
		return signature == nil || sound.Verify(message, signature)
	}))
}

// verifierAlternatesItsAlgorithm returns the planted defect of
// VerifierAlgorithmIsStable: a Verifier whose Algorithm alternates between two
// names.
func verifierAlternatesItsAlgorithm(h *VerifierHandle) sign.Verifier {
	var calls int

	return NewVerifierStub(h, VerifierStubDelegateTo(soundVerifier()), WithVerifierAlgorithm(func() crypto.Algorithm {
		calls++
		if calls%2 == 0 {
			return crypto.AlgECDSAP384
		}

		return crypto.AlgEd25519
	}))
}

// verifierAlternatesItsKeyID returns the planted defect of
// VerifierKeyIDIsStable: a Verifier whose KeyID alternates between its key ID
// and the zero key ID.
func verifierAlternatesItsKeyID(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()
	var calls int

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierKeyID(func() sign.KeyID {
		calls++
		if calls%2 == 0 {
			return sign.KeyID{}
		}

		return sound.KeyID()
	}))
}

// verifierAlternatesItsPublicKey returns the planted defect of
// VerifierPublicKeyIsStable: a Verifier whose PublicKey alternates between its
// key and no bytes.
func verifierAlternatesItsPublicKey(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()
	var calls int

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierPublicKey(func() []byte {
		calls++
		if calls%2 == 0 {
			return nil
		}

		return sound.PublicKey()
	}))
}

// verifierAcceptsANilSignature returns the planted defect of
// VerifierRefusesANilSignature: a Verifier whose Verify accepts a nil
// signature.
func verifierAcceptsANilSignature(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierVerify(func(message, signature []byte) bool {
		return signature == nil || sound.Verify(message, signature)
	}))
}

// verifierAcceptsAnEmptySignature returns the planted defect of
// VerifierRefusesAnEmptySignature: a Verifier whose Verify accepts an empty
// signature that is not nil.
func verifierAcceptsAnEmptySignature(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierVerify(func(message, signature []byte) bool {
		return signature != nil && len(signature) == 0 || sound.Verify(message, signature)
	}))
}

// verifierRefusesEverySignature returns the planted defect of
// VerifierAcceptsASignatureOfItsKey: a Verifier whose Verify refuses every
// signature.
func verifierRefusesEverySignature(h *VerifierHandle) sign.Verifier {
	return NewVerifierStub(h, VerifierStubDelegateTo(soundVerifier()),
		WithVerifierVerify(func([]byte, []byte) bool { return false }))
}

// verifierRestoresTheFirstSignatureByte returns the planted defect of
// VerifierRefusesAFlippedSignatureBit: a Verifier whose Verify verifies a
// signature again with its first bit changed, when the signature fails.
func verifierRestoresTheFirstSignatureByte(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierVerify(func(message, signature []byte) bool {
		return sound.Verify(message, signature) || len(signature) > 0 && sound.Verify(message, flipFirst(signature))
	}))
}

// verifierRestoresTheFirstMessageByte returns the planted defect of
// VerifierRefusesAFlippedMessageBit: a Verifier whose Verify verifies a
// signature again for its message with the first bit changed, when the
// signature fails.
func verifierRestoresTheFirstMessageByte(h *VerifierHandle) sign.Verifier {
	sound := soundVerifier()

	return NewVerifierStub(h, VerifierStubDelegateTo(sound), WithVerifierVerify(func(message, signature []byte) bool {
		return sound.Verify(message, signature) || len(message) > 0 && sound.Verify(flipFirst(message), signature)
	}))
}

// verifierSigner returns the second subject of the planted defects of the
// suite of Verifier: the Signer of the canonical Ed25519 key.
func verifierSigner(*VerifierHandle) sign.Verifier {
	return &stdlibSigner{spec: ed25519Spec()}
}

// signStreamDefect returns a StreamingSigner over the canonical ECDSA P-384
// key whose SignStreams are the streams that stream builds over a new sound
// stream.
func signStreamDefect(h *StreamingSignerHandle, stream func(sound sign.SignStream) sign.SignStream,
) sign.StreamingSigner {
	sound := stdlibECDSASigner{fix: NewECDSAP384Sample()}

	return NewStreamingSignerStub(h, StreamingSignerStubDelegateTo(sound),
		WithStreamingSignerNewSignStream(func() sign.SignStream { return stream(sound.NewSignStream()) }))
}

// signStreamSignsAnotherMessageForThePangram returns the planted defect of
// StreamingSignerStreamSignatureVerifies: a StreamingSigner whose SignStream
// signs another message when it absorbed the pangram.
func signStreamSignsAnotherMessageForThePangram(h *StreamingSignerHandle) sign.StreamingSigner {
	return signStreamDefect(h, func(sound sign.SignStream) sign.SignStream {
		var absorbed []byte

		return NewSignStreamStub(h, SignStreamStubDelegateTo(sound),
			WithSignStreamWrite(func(p []byte) (int, error) {
				absorbed = append(absorbed, p...)

				return sound.Write(p)
			}),
			WithSignStreamSignAndReset(func() ([]byte, error) {
				pangram := bytes.Equal(absorbed, signPangram)
				absorbed = nil
				if !pangram {
					return sound.SignAndReset()
				}
				if _, err := sound.SignAndReset(); err != nil {
					return nil, err
				}
				if _, err := sound.Write(signOther); err != nil {
					return nil, err
				}

				return sound.SignAndReset()
			}))
	})
}

// signStreamAbsorbsTheFirstWriteOnly returns the planted defect of
// StreamingSignerStreamAbsorbsSplitWrites: a StreamingSigner whose SignStream
// absorbs only the first Write of bytes after its start or its last
// SignAndReset.
func signStreamAbsorbsTheFirstWriteOnly(h *StreamingSignerHandle) sign.StreamingSigner {
	return signStreamDefect(h, func(sound sign.SignStream) sign.SignStream {
		var writes int

		return NewSignStreamStub(h, SignStreamStubDelegateTo(sound),
			WithSignStreamWrite(func(p []byte) (int, error) {
				if len(p) > 0 {
					writes++
				}
				if writes > 1 {
					return len(p), nil
				}

				return sound.Write(p)
			}),
			WithSignStreamSignAndReset(func() ([]byte, error) {
				writes = 0

				return sound.SignAndReset()
			}))
	})
}

// signStreamKeepsItsInputOnSignAndReset returns the planted defect of
// StreamingSignerSignAndResetClearsTheStream: a StreamingSigner whose
// SignStream absorbs its input again after SignAndReset.
func signStreamKeepsItsInputOnSignAndReset(h *StreamingSignerHandle) sign.StreamingSigner {
	return signStreamDefect(h, func(sound sign.SignStream) sign.SignStream {
		var absorbed []byte

		return NewSignStreamStub(h, SignStreamStubDelegateTo(sound),
			WithSignStreamWrite(func(p []byte) (int, error) {
				absorbed = append(absorbed, p...)

				return sound.Write(p)
			}),
			WithSignStreamSignAndReset(func() ([]byte, error) {
				sig, err := sound.SignAndReset()
				if err != nil {
					return nil, err
				}
				if _, err := sound.Write(absorbed); err != nil {
					return nil, err
				}

				return sig, nil
			}))
	})
}

// signStreamAbsorbsAMarkerForAnEmptyWrite returns the planted defect of
// StreamingSignerStreamIgnoresEmptyWrites: a StreamingSigner whose SignStream
// absorbs a zero byte for a Write of no bytes.
func signStreamAbsorbsAMarkerForAnEmptyWrite(h *StreamingSignerHandle) sign.StreamingSigner {
	return signStreamDefect(h, func(sound sign.SignStream) sign.SignStream {
		return NewSignStreamStub(h, SignStreamStubDelegateTo(sound), WithSignStreamWrite(func(p []byte) (int, error) {
			if len(p) == 0 {
				_, err := sound.Write(signEmptyMarker)

				return 0, err
			}

			return sound.Write(p)
		}))
	})
}

// verifyStreamDefect returns a StreamingVerifier over the canonical ECDSA P-384
// key whose VerifyStreams are the streams that stream builds over a new sound
// stream.
func verifyStreamDefect(h *StreamingVerifierHandle, stream func(sound sign.VerifyStream) sign.VerifyStream,
) sign.StreamingVerifier {
	sound := stdlibECDSASigner{fix: NewECDSAP384Sample()}

	return NewStreamingVerifierStub(h, StreamingVerifierStubDelegateTo(sound),
		WithStreamingVerifierNewVerifyStream(func() sign.VerifyStream { return stream(sound.NewVerifyStream()) }))
}

// verifyStreamRefusesThePangram returns the planted defect of
// StreamingVerifierStreamAcceptsASignatureOfItsKey: a StreamingVerifier whose
// VerifyStream refuses every signature when it absorbed the pangram.
func verifyStreamRefusesThePangram(h *StreamingVerifierHandle) sign.StreamingVerifier {
	return verifyStreamDefect(h, func(sound sign.VerifyStream) sign.VerifyStream {
		var absorbed []byte

		return NewVerifyStreamStub(h, VerifyStreamStubDelegateTo(sound),
			WithVerifyStreamWrite(func(p []byte) (int, error) {
				absorbed = append(absorbed, p...)

				return sound.Write(p)
			}),
			WithVerifyStreamVerify(func(sig []byte) bool {
				return !bytes.Equal(absorbed, signPangram) && sound.Verify(sig)
			}))
	})
}

// verifyStreamAbsorbsTheFirstWriteOnly returns the planted defect of
// StreamingVerifierStreamAbsorbsSplitWrites: a StreamingVerifier whose
// VerifyStream absorbs only its first Write of bytes.
func verifyStreamAbsorbsTheFirstWriteOnly(h *StreamingVerifierHandle) sign.StreamingVerifier {
	return verifyStreamDefect(h, func(sound sign.VerifyStream) sign.VerifyStream {
		var writes int

		return NewVerifyStreamStub(h, VerifyStreamStubDelegateTo(sound),
			WithVerifyStreamWrite(func(p []byte) (int, error) {
				if len(p) > 0 {
					writes++
				}
				if writes > 1 {
					return len(p), nil
				}

				return sound.Write(p)
			}))
	})
}

// verifyStreamRestoresTheFirstSignatureByte returns the planted defect of
// StreamingVerifierStreamRefusesAFlippedSignatureBit: a StreamingVerifier whose
// VerifyStream verifies a signature again with its first bit changed, when the
// signature fails.
func verifyStreamRestoresTheFirstSignatureByte(h *StreamingVerifierHandle) sign.StreamingVerifier {
	return verifyStreamDefect(h, func(sound sign.VerifyStream) sign.VerifyStream {
		return NewVerifyStreamStub(h, VerifyStreamStubDelegateTo(sound), WithVerifyStreamVerify(func(sig []byte) bool {
			return sound.Verify(sig) || len(sig) > 0 && sound.Verify(flipFirst(sig))
		}))
	})
}

// verifyStreamAbsorbsAMarkerForAnEmptyWrite returns the planted defect of
// StreamingVerifierStreamIgnoresEmptyWrites: a StreamingVerifier whose
// VerifyStream absorbs a zero byte for a Write of no bytes.
func verifyStreamAbsorbsAMarkerForAnEmptyWrite(h *StreamingVerifierHandle) sign.StreamingVerifier {
	return verifyStreamDefect(h, func(sound sign.VerifyStream) sign.VerifyStream {
		return NewVerifyStreamStub(h, VerifyStreamStubDelegateTo(sound),
			WithVerifyStreamWrite(func(p []byte) (int, error) {
				if len(p) == 0 {
					_, err := sound.Write(signEmptyMarker)

					return 0, err
				}

				return sound.Write(p)
			}))
	})
}

// verifyStreamAcceptsANilSignature returns the planted defect of
// StreamingVerifierStreamRefusesANilSignature: a StreamingVerifier whose
// VerifyStream accepts a nil signature.
func verifyStreamAcceptsANilSignature(h *StreamingVerifierHandle) sign.StreamingVerifier {
	return verifyStreamDefect(h, func(sound sign.VerifyStream) sign.VerifyStream {
		return NewVerifyStreamStub(h, VerifyStreamStubDelegateTo(sound), WithVerifyStreamVerify(func(sig []byte) bool {
			return sig == nil || sound.Verify(sig)
		}))
	})
}

// streamingVerifierSigner returns the second subject of the planted defects of
// the suite of StreamingVerifier: the Signer of the canonical ECDSA P-384 key.
func streamingVerifierSigner(*StreamingVerifierHandle) sign.StreamingVerifier {
	return stdlibECDSASigner{fix: NewECDSAP384Sample()}
}

// flipFirst returns a copy of p with its first bit changed. p is not empty.
func flipFirst(p []byte) []byte {
	flipped := bytes.Clone(p)
	flipped[0] ^= 0x01

	return flipped
}
