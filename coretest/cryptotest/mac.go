// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	stdhmac "crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"hash"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
)

// The sizes of the inputs of the checks of a MAC and of their planted defects.
const (
	// macMaxData is the largest data that a property check of a MAC draws.
	macMaxData = 256

	// macLongData is the size above which the planted defects of
	// MACSignIsDeterministic and MACVerifyAcceptsTheMACOfSign change a call.
	macLongData = 16

	// macLongWrite is the size above which the planted defect of
	// MACStreamSumsLikeSign drops the end of a Write.
	macLongWrite = 40

	// macDroppedWrite is the number of the first Write that the planted defect
	// of MACStreamAbsorbsSplitWrites drops.
	macDroppedWrite = 3
)

// The messages that the checks of a MAC sign, and the keys of the sound MAC of
// the planted defects.
var (
	macPangram  = []byte("the quick brown fox jumps over the lazy dog")
	macSplit    = []byte("agentic-context-payload-streamed-across-many-writes")
	macAlpha    = []byte("alpha")
	macBeta     = []byte("beta")
	macTampered = []byte("verify-me")
	macFirst    = []byte("first")
	macSecond   = []byte("second")
	macPrefix   = []byte("ab")
	macSuffix   = []byte("c")
	macWhole    = []byte("abc")

	// macMarker is the data whose MAC the planted defect of MACSignsNilAsEmpty
	// returns for nil data.
	macMarker = []byte{0}

	// macSoundKey is the key of the sound MAC of the planted defects.
	macSoundKey = []byte("cryptotest/mac-key")

	// macOtherKey is the key of the planted defect of MACSignMatchesTheReference.
	macOtherKey = []byte("cryptotest/another-mac-key")
)

// macSoundSpec is the spec of the HMAC-SHA-256 of the standard library that the
// planted defects of the suite of MAC change.
var macSoundSpec = StdlibMACSpec{
	Algorithm: crypto.AlgHMACSHA256,
	ID:        crypto.ID{'c', 'r', 'y', 'p', 't', 'o', 't', 'e', 's', 't', '/', 'm', 'a', 'c'},
	Size:      crypto.DigestSize256,
	Key:       macSoundKey,
	NewHash:   sha256.New,
}

// StdlibMACSpec is the algorithm, the key and the hash of an HMAC of the
// standard library, which [NewStdlibMACStub] wraps. A test fills it with the
// key of the MAC under test, so the HMAC of the spec is the reference of that
// MAC.
type StdlibMACSpec struct {
	// NewHash returns a hash of the standard library, such as sha256.New,
	// which crypto/hmac keys with Key.
	NewHash func() hash.Hash

	// Algorithm is the name of the algorithm that Algorithm returns.
	Algorithm crypto.Algorithm

	// Key is the key of the HMAC.
	Key []byte

	// ID is the ID that ID returns.
	ID crypto.ID

	// Size is the size of a MAC in bytes: crypto.DigestSize256,
	// crypto.DigestSize384 or crypto.DigestSize512.
	Size int
}

// stdlibMAC is the [crypto.MAC] of a [StdlibMACSpec]: the HMAC of crypto/hmac
// over the hash of the spec, under the key of the spec. The Write of a
// [hash.Hash] never returns an error, so Sign and Verify ignore it.
type stdlibMAC struct {
	spec StdlibMACSpec
}

// ID returns the ID of the spec.
func (m *stdlibMAC) ID() crypto.ID { return m.spec.ID }

// Algorithm returns the algorithm of the spec.
func (m *stdlibMAC) Algorithm() crypto.Algorithm { return m.spec.Algorithm }

// Size returns the size of the spec.
func (m *stdlibMAC) Size() int { return m.spec.Size }

// Sign returns the HMAC of data.
func (m *stdlibMAC) Sign(data []byte) crypto.Digest {
	h := stdhmac.New(m.spec.NewHash, m.spec.Key)
	_, _ = h.Write(data)

	return digestFromBytes(h.Sum(nil))
}

// Verify reports whether expected is the HMAC of data, compared in constant
// time, and reports false for an expected of another size.
func (m *stdlibMAC) Verify(data, expected []byte) bool {
	if len(expected) != m.spec.Size {
		return false
	}
	h := stdhmac.New(m.spec.NewHash, m.spec.Key)
	_, _ = h.Write(data)

	return subtle.ConstantTimeCompare(h.Sum(nil), expected) == 1
}

// NewStream returns a stream of the HMAC.
func (m *stdlibMAC) NewStream() crypto.Stream {
	return &stdlibStream{h: stdhmac.New(m.spec.NewHash, m.spec.Key)}
}

// NewStdlibMACStub returns a [MACStub] that delegates each call to the HMAC of
// spec. The double records the calls, and crypto/hmac computes the MACs.
func NewStdlibMACStub(tb assert.TB, spec StdlibMACSpec) *MACStub {
	return NewMACStub(tb, MACStubDelegateTo(&stdlibMAC{spec: spec}))
}

// MACIDIdentifiesTheImplementation checks that ID returns an ID that is not
// zero, and the same ID on every call.
//
//dokimi:check MAC.ID defect=macHasNoID
func MACIDIdentifiesTheImplementation(h *MACHandle) {
	m := h.Subject()
	assert.NotEqual(h, m.ID(), crypto.ID{}, h.Statement())
	assert.Deterministic(h, func(m crypto.MAC) (crypto.ID, error) { return m.ID(), nil }, m, h.Statement())
}

// MACAlgorithmNamesTheFunction checks that Algorithm returns a name that is not
// empty, and the same name on every call.
//
//dokimi:check MAC.Algorithm defect=macHasNoAlgorithm
func MACAlgorithmNamesTheFunction(h *MACHandle) {
	m := h.Subject()
	assert.NotEqual(h, m.Algorithm(), crypto.Algorithm(""), h.Statement())
	assert.Deterministic(h, func(m crypto.MAC) (crypto.Algorithm, error) { return m.Algorithm(), nil }, m,
		h.Statement())
}

// MACSignedSizeIsTheSize checks that a MAC of Sign has the size that Size
// returns.
//
//dokimi:check MAC.Size defect=macOverstatesItsSize
func MACSignedSizeIsTheSize(h *MACHandle) {
	m := h.Subject()
	assert.Equal(h, m.Sign(macPangram).Size(), m.Size(), h.Statement())
}

// MACSignIsDeterministic checks that Sign returns the same MAC on every call
// with the same data.
//
//dokimi:check MAC.Sign defect=macAlternatesItsMAC
func MACSignIsDeterministic(h *MACHandle, c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(macMaxData)), "data")
	m := h.Subject()
	assert.Deterministic(h, func(d []byte) (crypto.Digest, error) { return m.Sign(d), nil }, data, h.Statement())
}

// MACSignsNilAsEmpty checks that the MAC of nil data is the MAC of empty data.
//
//dokimi:check MAC.Sign defect=macSignsNilAsAMarker
func MACSignsNilAsEmpty(h *MACHandle) {
	m := h.Subject()
	assert.Equal(h, m.Sign(nil), m.Sign([]byte{}), h.Statement())
}

// MACSignDistinguishesInputs checks that the MACs of two different messages
// differ.
//
//dokimi:check MAC.Sign defect=macCollidesTwoInputs
func MACSignDistinguishesInputs(h *MACHandle) {
	m := h.Subject()
	assert.NotEqual(h, m.Sign(macAlpha), m.Sign(macBeta), h.Statement())
}

// MACSignMatchesTheReference checks that Sign returns the MAC that the
// reference of the application returns for the same data.
//
//dokimi:check MAC.Sign defect=macOfAnotherKey reference=macReference
func MACSignMatchesTheReference(h *MACHandle, c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(macMaxData)), "data")
	assert.Equal(h, h.Subject().Sign(data), h.Reference().Sign(data), h.Statement())
}

// MACVerifyAcceptsTheMACOfSign checks that Verify accepts the MAC that Sign
// returns for the same data.
//
//dokimi:check MAC.Verify defect=macRefusesTheMACOfLongData
func MACVerifyAcceptsTheMACOfSign(h *MACHandle, c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(macMaxData)), "data")
	m := h.Subject()
	assert.True(h, m.Verify(data, m.Sign(data).Bytes()), h.Statement())
}

// MACVerifyRefusesAModifiedMAC checks that Verify refuses a MAC of Sign whose
// first bit changed.
//
//dokimi:check MAC.Verify defect=macVerifyRestoresTheFirstByte
func MACVerifyRefusesAModifiedMAC(h *MACHandle) {
	m := h.Subject()
	tag := m.Sign(macTampered).Bytes()
	tag[0] ^= 0x01
	assert.False(h, m.Verify(macTampered, tag), h.Statement())
}

// MACVerifyRefusesAMACOfAWrongLength checks that Verify refuses a nil MAC, a
// MAC of 16 bytes and a MAC of twice the size that Size returns.
//
//dokimi:check MAC.Verify defect=macAcceptsAnEmptyMAC
func MACVerifyRefusesAMACOfAWrongLength(h *MACHandle) {
	m := h.Subject()
	for _, expected := range [][]byte{nil, make([]byte, 16), make([]byte, 2*m.Size())} {
		assert.False(h, m.Verify(macTampered, expected), h.Statement())
	}
}

// MACStreamSumsLikeSign checks that the Sum of a stream that absorbed a message
// in one Write is the MAC that Sign returns for the message.
//
//dokimi:check MAC.NewStream defect=macStreamDropsTheEndOfALongWrite
func MACStreamSumsLikeSign(h *MACHandle) {
	m := h.Subject()
	s := m.NewStream()
	defer s.Close()
	streamWrite(h, s, macPangram)
	assert.Equal(h, s.Sum(), m.Sign(macPangram), h.Statement())
}

// MACStreamAbsorbsSplitWrites checks that the Sum of a stream that absorbed a
// message in three Writes is the MAC that Sign returns for the message.
//
//dokimi:check MAC.NewStream defect=macStreamDropsTheThirdWrite
func MACStreamAbsorbsSplitWrites(h *MACHandle) {
	m := h.Subject()
	s := m.NewStream()
	defer s.Close()
	streamWrite(h, s, macSplit[:10])
	streamWrite(h, s, macSplit[10:25])
	streamWrite(h, s, macSplit[25:])
	assert.Equal(h, s.Sum(), m.Sign(macSplit), h.Statement())
}

// MACStreamResetRestartsTheMAC checks that a stream that absorbed a message and
// was Reset returns the MAC of the next message alone.
//
//dokimi:check MAC.NewStream defect=macStreamKeepsItsInputOnReset
func MACStreamResetRestartsTheMAC(h *MACHandle) {
	m := h.Subject()
	s := m.NewStream()
	defer s.Close()
	streamWrite(h, s, macFirst)
	_ = s.Sum()
	s.Reset()
	streamWrite(h, s, macSecond)
	assert.Equal(h, s.Sum(), m.Sign(macSecond), h.Statement())
}

// MACStreamSumKeepsTheState checks that a Write after a Sum extends the message
// of a stream.
//
//dokimi:check MAC.NewStream defect=macStreamResetsOnSum
func MACStreamSumKeepsTheState(h *MACHandle) {
	m := h.Subject()
	s := m.NewStream()
	defer s.Close()
	streamWrite(h, s, macPrefix)
	_ = s.Sum()
	streamWrite(h, s, macSuffix)
	assert.Equal(h, s.Sum(), m.Sign(macWhole), h.Statement())
}

// macReference returns the reference of the planted defect of
// MACSignMatchesTheReference: the sound MAC.
func macReference(h *MACHandle) crypto.MAC {
	return NewStdlibMACStub(h, macSoundSpec)
}

// macHasNoID returns the planted defect of MACIDIdentifiesTheImplementation: a
// MAC whose ID returns the zero ID.
func macHasNoID(h *MACHandle) crypto.MAC {
	return NewMACStub(h, MACStubDelegateTo(&stdlibMAC{spec: macSoundSpec}),
		WithMACID(func() crypto.ID { return crypto.ID{} }))
}

// macHasNoAlgorithm returns the planted defect of MACAlgorithmNamesTheFunction:
// a MAC whose Algorithm returns the empty name.
func macHasNoAlgorithm(h *MACHandle) crypto.MAC {
	return NewMACStub(h, MACStubDelegateTo(&stdlibMAC{spec: macSoundSpec}),
		WithMACAlgorithm(func() crypto.Algorithm { return "" }))
}

// macOverstatesItsSize returns the planted defect of MACSignedSizeIsTheSize: a
// MAC whose Size is one byte more than its MAC.
func macOverstatesItsSize(h *MACHandle) crypto.MAC {
	return NewMACStub(h, MACStubDelegateTo(&stdlibMAC{spec: macSoundSpec}),
		WithMACSize(func() int { return macSoundSpec.Size + 1 }))
}

// macAlternatesItsMAC returns the planted defect of MACSignIsDeterministic: a
// MAC whose every second Sign of data longer than 16 bytes returns the MAC of
// the data and a zero byte.
func macAlternatesItsMAC(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}
	var calls int

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACSign(func(data []byte) crypto.Digest {
		calls++
		if len(data) > macLongData && calls%2 == 0 {
			return sound.Sign(append(bytes.Clone(data), 0))
		}

		return sound.Sign(data)
	}))
}

// macSignsNilAsAMarker returns the planted defect of MACSignsNilAsEmpty: a MAC
// whose Sign of nil data returns the MAC of one zero byte.
func macSignsNilAsAMarker(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACSign(func(data []byte) crypto.Digest {
		if data == nil {
			return sound.Sign(macMarker)
		}

		return sound.Sign(data)
	}))
}

// macCollidesTwoInputs returns the planted defect of MACSignDistinguishesInputs:
// a MAC whose Sign of the message beta returns the MAC of the message alpha.
func macCollidesTwoInputs(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACSign(func(data []byte) crypto.Digest {
		if bytes.Equal(data, macBeta) {
			return sound.Sign(macAlpha)
		}

		return sound.Sign(data)
	}))
}

// macOfAnotherKey returns the planted defect of MACSignMatchesTheReference: the
// HMAC-SHA-256 of the standard library under another key than the reference.
func macOfAnotherKey(h *MACHandle) crypto.MAC {
	spec := macSoundSpec
	spec.Key = macOtherKey

	return NewStdlibMACStub(h, spec)
}

// macRefusesTheMACOfLongData returns the planted defect of
// MACVerifyAcceptsTheMACOfSign: a MAC whose Verify refuses every MAC of data
// longer than 16 bytes.
func macRefusesTheMACOfLongData(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACVerify(func(data, expected []byte) bool {
		return len(data) <= macLongData && sound.Verify(data, expected)
	}))
}

// macVerifyRestoresTheFirstByte returns the planted defect of
// MACVerifyRefusesAModifiedMAC: a MAC whose Verify verifies a MAC again with its
// first bit changed, when the MAC fails.
func macVerifyRestoresTheFirstByte(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACVerify(func(data, expected []byte) bool {
		return sound.Verify(data, expected) || len(expected) > 0 && sound.Verify(data, flipFirst(expected))
	}))
}

// macAcceptsAnEmptyMAC returns the planted defect of
// MACVerifyRefusesAMACOfAWrongLength: a MAC whose Verify accepts a nil or an
// empty MAC.
func macAcceptsAnEmptyMAC(h *MACHandle) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound), WithMACVerify(func(data, expected []byte) bool {
		return len(expected) == 0 || sound.Verify(data, expected)
	}))
}

// macStreamDefect returns a MAC over the sound MAC whose NewStream returns the
// stream that change makes of a stream of the sound MAC.
func macStreamDefect(h *MACHandle, change func(sound crypto.Stream) crypto.Stream) crypto.MAC {
	sound := &stdlibMAC{spec: macSoundSpec}

	return NewMACStub(h, MACStubDelegateTo(sound),
		WithMACNewStream(func() crypto.Stream { return change(sound.NewStream()) }))
}

// macStreamDropsTheEndOfALongWrite returns the planted defect of
// MACStreamSumsLikeSign: a MAC whose stream absorbs the first 40 bytes of a
// longer Write.
func macStreamDropsTheEndOfALongWrite(h *MACHandle) crypto.MAC {
	return macStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamWrite(func(p []byte) (int, error) {
			if len(p) > macLongWrite {
				_, err := sound.Write(p[:macLongWrite])

				return len(p), err
			}

			return sound.Write(p)
		}))
	})
}

// macStreamDropsTheThirdWrite returns the planted defect of
// MACStreamAbsorbsSplitWrites: a MAC whose stream absorbs its first two Writes
// alone.
func macStreamDropsTheThirdWrite(h *MACHandle) crypto.MAC {
	return macStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		var writes int

		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamWrite(func(p []byte) (int, error) {
			writes++
			if writes >= macDroppedWrite {
				return len(p), nil
			}

			return sound.Write(p)
		}))
	})
}

// macStreamKeepsItsInputOnReset returns the planted defect of
// MACStreamResetRestartsTheMAC: a MAC whose stream keeps its absorbed message on
// Reset.
func macStreamKeepsItsInputOnReset(h *MACHandle) crypto.MAC {
	return macStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamReset(func() {}))
	})
}

// macStreamResetsOnSum returns the planted defect of MACStreamSumKeepsTheState:
// a MAC whose stream resets its message after each Sum.
func macStreamResetsOnSum(h *MACHandle) crypto.MAC {
	return macStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamSum(func() crypto.Digest {
			d := sound.Sum()
			sound.Reset()

			return d
		}))
	})
}
