// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"crypto/sha3"
	"errors"
	"io"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
)

// xofLongRead is the length above which the planted defect of
// XOFLongerReadExtendsTheStream squeezes another stream.
const xofLongRead = 64

// The inputs that the checks of an XOF absorb.
var (
	xofInput      = []byte("absorb me")
	xofAlpha      = []byte("alpha")
	xofBeta       = []byte("beta")
	xofDiscarded  = []byte("discard me")
	xofLate       = []byte("too late")
	xofOtherInput = []byte("another stream")
)

// xofSoundID is the ID of the SHAKE128 XOF of the standard library that the
// planted defects of the suite of XOF change.
var xofSoundID = crypto.ID{'c', 'r', 'y', 'p', 't', 'o', 't', 'e', 's', 't', '/', 'x', 'o', 'f'}

// XOFOutputDependsOnTheInput checks that the streams of two inputs squeeze
// different bytes.
//
//dokimi:check XOF.NewXOFStream defect=xofIgnoresItsInput
func XOFOutputDependsOnTheInput(h *XOFHandle) {
	x := h.Subject()
	assert.NotEqual(h, xofSqueeze(h, x, xofAlpha, 32), xofSqueeze(h, x, xofBeta, 32), h.Statement())
}

// XOFLongerReadExtendsTheStream checks that a Read of 96 bytes begins with the
// 32 bytes of a Read of 32 bytes of the same input.
//
//dokimi:check XOF.NewXOFStream defect=xofSqueezesAnotherStreamForALongRead
func XOFLongerReadExtendsTheStream(h *XOFHandle) {
	x := h.Subject()
	short := xofSqueeze(h, x, xofInput, 32)
	long := xofSqueeze(h, x, xofInput, 96)
	assert.Equal(h, long[:32], short, h.Statement())
}

// XOFSuccessiveReadsContinueOneStream checks that two Reads of 32 bytes return
// the bytes of one Read of 64 bytes.
//
//dokimi:check XOF.NewXOFStream defect=xofRestartsEachRead
func XOFSuccessiveReadsContinueOneStream(h *XOFHandle) {
	x := h.Subject()
	s := x.NewXOFStream()
	xofAbsorb(h, s, xofInput)
	first := xofRead(h, s, 32)
	second := xofRead(h, s, 32)
	assert.Equal(h, append(first, second...), xofSqueeze(h, x, xofInput, 64), h.Statement())
}

// XOFAbsorbsInPieces checks that two Writes of the parts of an input squeeze
// the bytes of one Write of the input.
//
//dokimi:check XOF.NewXOFStream defect=xofDropsALaterWrite
func XOFAbsorbsInPieces(h *XOFHandle) {
	x := h.Subject()
	s := x.NewXOFStream()
	xofAbsorb(h, s, xofInput[:6])
	xofAbsorb(h, s, xofInput[6:])
	assert.Equal(h, xofRead(h, s, 64), xofSqueeze(h, x, xofInput, 64), h.Statement())
}

// XOFSqueezesAnEmptyInput checks that a stream that absorbed no bytes squeezes
// 32 bytes.
//
//dokimi:check XOF.NewXOFStream defect=xofFailsAfterAnEmptyWrite
func XOFSqueezesAnEmptyInput(h *XOFHandle) {
	s := h.Subject().NewXOFStream()
	xofAbsorb(h, s, nil)
	n, err := io.ReadFull(s, make([]byte, 32))
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, n, 32, h.Statement())
}

// XOFWriteAfterReadReturnsErrXOFSqueezing checks that a Write after a Read
// returns an error that wraps crypto.ErrXOFSqueezing.
//
//dokimi:check XOF.NewXOFStream defect=xofAbsorbsAfterARead
func XOFWriteAfterReadReturnsErrXOFSqueezing(h *XOFHandle) {
	s := h.Subject().NewXOFStream()
	xofAbsorb(h, s, xofInput)
	xofRead(h, s, 8)
	_, err := s.Write(xofLate)
	assert.ErrorIs(h, err, crypto.ErrXOFSqueezing, h.Statement())
}

// XOFResetRestartsTheOutput checks that a stream that squeezed 8 bytes and was
// Reset absorbs again and squeezes the bytes of a new stream.
//
//dokimi:check XOF.NewXOFStream defect=xofKeepsTheSqueezePositionOnReset
func XOFResetRestartsTheOutput(h *XOFHandle) {
	x := h.Subject()
	s := x.NewXOFStream()
	xofRead(h, s, 8)
	s.Reset()
	_, err := s.Write(xofInput)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, xofRead(h, s, 64), xofSqueeze(h, x, xofInput, 64), h.Statement())
}

// XOFResetDiscardsAbsorbedInput checks that the bytes that a stream absorbed
// before a Reset do not change its output.
//
//dokimi:check XOF.NewXOFStream defect=xofKeepsTheInputOnReset
func XOFResetDiscardsAbsorbedInput(h *XOFHandle) {
	x := h.Subject()
	s := x.NewXOFStream()
	xofAbsorb(h, s, xofDiscarded)
	s.Reset()
	xofAbsorb(h, s, xofInput)
	assert.Equal(h, xofRead(h, s, 32), xofSqueeze(h, x, xofInput, 32), h.Statement())
}

// XOFStreamsAreIndependent checks that the output of a stream does not depend
// on the input of another stream that absorbs at the same time.
//
//dokimi:check XOF.NewXOFStream defect=xofSharesTheInputOfAnAbsorbingStream
func XOFStreamsAreIndependent(h *XOFHandle) {
	x := h.Subject()
	a, b := x.NewXOFStream(), x.NewXOFStream()
	xofAbsorb(h, a, xofAlpha)
	xofAbsorb(h, b, xofBeta)
	assert.Equal(h, xofRead(h, a, 32), xofSqueeze(h, x, xofAlpha, 32), h.Statement())
}

// XOFAlgorithmNamesTheFunction checks that Algorithm returns a name that is not
// empty, and the same name on every call.
//
//dokimi:check XOF.Algorithm defect=xofHasNoAlgorithm
func XOFAlgorithmNamesTheFunction(h *XOFHandle) {
	x := h.Subject()
	assert.NotEqual(h, x.Algorithm(), crypto.Algorithm(""), h.Statement())
	assert.Deterministic(h, func(x crypto.XOF) (crypto.Algorithm, error) { return x.Algorithm(), nil }, x,
		h.Statement())
}

// XOFIDIdentifiesTheImplementation checks that ID returns an ID that is not
// zero, and the same ID on every call.
//
//dokimi:check XOF.ID defect=xofHasNoID
func XOFIDIdentifiesTheImplementation(h *XOFHandle) {
	x := h.Subject()
	assert.NotEqual(h, x.ID(), crypto.ID{}, h.Statement())
	assert.Deterministic(h, func(x crypto.XOF) (crypto.ID, error) { return x.ID(), nil }, x, h.Statement())
}

// xofSqueeze absorbs data into a new stream of x and returns the first n bytes
// that the stream squeezes. It stops the check when Write or Read fails.
func xofSqueeze(h assert.TB, x crypto.XOF, data []byte, n int) []byte {
	h.Helper()

	s := x.NewXOFStream()
	xofAbsorb(h, s, data)

	return xofRead(h, s, n)
}

// xofAbsorb writes p to s, and stops the check when Write fails or absorbs
// fewer bytes than p has.
func xofAbsorb(h assert.TB, s crypto.XOFStream, p []byte) {
	h.Helper()

	n, err := s.Write(p)
	assert.NoError(h, err, "Write absorbs while the stream absorbs")
	assert.Equal(h, n, len(p), "Write absorbs every byte of p")
}

// xofRead squeezes n bytes from s, and stops the check when the stream returns
// fewer bytes or an error.
func xofRead(h assert.TB, s crypto.XOFStream, n int) []byte {
	h.Helper()

	out := make([]byte, n)
	_, err := io.ReadFull(s, out)
	assert.NoError(h, err, "Read fills its buffer, because an XOF stream never ends")

	return out
}

// stdlibXOF is SHAKE128 of the standard library behind crypto.XOF. The planted
// defects of the suite of XOF change one behaviour of it.
type stdlibXOF struct{}

// ID returns xofSoundID.
func (stdlibXOF) ID() crypto.ID { return xofSoundID }

// Algorithm returns crypto.AlgSHAKE128.
func (stdlibXOF) Algorithm() crypto.Algorithm { return crypto.AlgSHAKE128 }

// NewXOFStream returns a new absorbing stream.
func (stdlibXOF) NewXOFStream() crypto.XOFStream {
	return &stdlibXOFStream{shake: sha3.NewSHAKE128()}
}

// stdlibXOFStream is a SHAKE128 stream that returns crypto.ErrXOFSqueezing for
// a Write after a Read, where the stream of the standard library panics.
type stdlibXOFStream struct {
	shake     *sha3.SHAKE
	squeezing bool
}

// Write absorbs p, or returns crypto.ErrXOFSqueezing once Read has run.
func (s *stdlibXOFStream) Write(p []byte) (int, error) {
	if s.squeezing {
		return 0, crypto.ErrXOFSqueezing
	}

	return s.shake.Write(p)
}

// Read squeezes p.
func (s *stdlibXOFStream) Read(p []byte) (int, error) {
	s.squeezing = true

	return s.shake.Read(p)
}

// Reset returns the stream to absorbing and discards its input.
func (s *stdlibXOFStream) Reset() {
	s.shake.Reset()
	s.squeezing = false
}

// xofOfStreams returns an XOF whose NewXOFStream returns the stream that
// stream builds over a new sound stream.
func xofOfStreams(h *XOFHandle, stream func(sound crypto.XOFStream) crypto.XOFStream) crypto.XOF {
	return NewXOFStub(h, XOFStubDelegateTo(stdlibXOF{}),
		WithXOFNewXOFStream(func() crypto.XOFStream { return stream(stdlibXOF{}.NewXOFStream()) }))
}

// xofIgnoresItsInput returns the planted defect of XOFOutputDependsOnTheInput:
// an XOF whose streams absorb no byte that Write receives.
func xofIgnoresItsInput(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound), WithXOFStreamWrite(func(p []byte) (int, error) {
			if _, err := sound.Write(nil); err != nil {
				return 0, err
			}

			return len(p), nil
		}))
	})
}

// xofSqueezesAnotherStreamForALongRead returns the planted defect of
// XOFLongerReadExtendsTheStream: an XOF whose streams fill a Read of more than
// 64 bytes from the stream of another input.
func xofSqueezesAnotherStreamForALongRead(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound), WithXOFStreamRead(func(p []byte) (int, error) {
			if len(p) <= xofLongRead {
				return sound.Read(p)
			}
			other := sha3.NewSHAKE128()
			if _, err := other.Write(xofOtherInput); err != nil {
				return 0, err
			}

			return other.Read(p)
		}))
	})
}

// xofRestartsEachRead returns the planted defect of
// XOFSuccessiveReadsContinueOneStream: an XOF whose streams squeeze every Read
// from the start of the output.
func xofRestartsEachRead(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		var absorbed []byte

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamWrite(func(p []byte) (int, error) {
				n, err := sound.Write(p)
				absorbed = append(absorbed, p[:n]...)

				return n, err
			}),
			WithXOFStreamRead(func(p []byte) (int, error) {
				if _, err := sound.Read(nil); err != nil {
					return 0, err
				}
				fresh := sha3.NewSHAKE128()
				if _, err := fresh.Write(absorbed); err != nil {
					return 0, err
				}

				return fresh.Read(p)
			}),
			WithXOFStreamReset(func() {
				absorbed = nil
				sound.Reset()
			}))
	})
}

// xofDropsALaterWrite returns the planted defect of XOFAbsorbsInPieces: an XOF
// whose streams absorb only the first Write after their start or their last
// Reset.
func xofDropsALaterWrite(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		var writes int

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamWrite(func(p []byte) (int, error) {
				writes++
				if writes == 1 {
					return sound.Write(p)
				}
				if _, err := sound.Write(nil); err != nil {
					return 0, err
				}

				return len(p), nil
			}),
			WithXOFStreamReset(func() {
				writes = 0
				sound.Reset()
			}))
	})
}

// xofFailsAfterAnEmptyWrite returns the planted defect of
// XOFSqueezesAnEmptyInput: an XOF whose streams end every Read with io.EOF
// after a Write of no bytes.
func xofFailsAfterAnEmptyWrite(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		var empty bool

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamWrite(func(p []byte) (int, error) {
				empty = empty || len(p) == 0

				return sound.Write(p)
			}),
			WithXOFStreamRead(func(p []byte) (int, error) {
				if empty {
					return 0, io.EOF
				}

				return sound.Read(p)
			}))
	})
}

// xofAbsorbsAfterARead returns the planted defect of
// XOFWriteAfterReadReturnsErrXOFSqueezing: an XOF whose streams report a Write
// after a Read as absorbed.
func xofAbsorbsAfterARead(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound), WithXOFStreamWrite(func(p []byte) (int, error) {
			n, err := sound.Write(p)
			if errors.Is(err, crypto.ErrXOFSqueezing) {
				return len(p), nil
			}

			return n, err
		}))
	})
}

// xofKeepsTheSqueezePositionOnReset returns the planted defect of
// XOFResetRestartsTheOutput: an XOF whose streams skip the bytes that they
// squeezed before a Reset.
func xofKeepsTheSqueezePositionOnReset(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		var squeezed, skip int

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamRead(func(p []byte) (int, error) {
				if skip > 0 {
					if _, err := sound.Read(make([]byte, skip)); err != nil {
						return 0, err
					}
					skip = 0
				}
				n, err := sound.Read(p)
				squeezed += n

				return n, err
			}),
			WithXOFStreamReset(func() {
				skip, squeezed = squeezed, 0
				sound.Reset()
			}))
	})
}

// xofKeepsTheInputOnReset returns the planted defect of
// XOFResetDiscardsAbsorbedInput: an XOF whose streams absorb their input again
// after a Reset.
func xofKeepsTheInputOnReset(h *XOFHandle) crypto.XOF {
	return xofOfStreams(h, func(sound crypto.XOFStream) crypto.XOFStream {
		var absorbed []byte

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamWrite(func(p []byte) (int, error) {
				n, err := sound.Write(p)
				absorbed = append(absorbed, p[:n]...)

				return n, err
			}),
			WithXOFStreamReset(func() {
				sound.Reset()
				_, _ = sound.Write(absorbed)
			}))
	})
}

// xofSharesTheInputOfAnAbsorbingStream returns the planted defect of
// XOFStreamsAreIndependent: an XOF whose new stream also writes its input into
// the last stream while that stream absorbs.
func xofSharesTheInputOfAnAbsorbingStream(h *XOFHandle) crypto.XOF {
	var (
		lastSound    crypto.XOFStream
		lastSqueezed *bool
	)

	return NewXOFStub(h, XOFStubDelegateTo(stdlibXOF{}), WithXOFNewXOFStream(func() crypto.XOFStream {
		sound, squeezed := stdlibXOF{}.NewXOFStream(), new(bool)
		previousSound, previousSqueezed := lastSound, lastSqueezed
		lastSound, lastSqueezed = sound, squeezed

		return NewXOFStreamStub(h, XOFStreamStubDelegateTo(sound),
			WithXOFStreamWrite(func(p []byte) (int, error) {
				if previousSound != nil && !*previousSqueezed {
					_, _ = previousSound.Write(p)
				}

				return sound.Write(p)
			}),
			WithXOFStreamRead(func(p []byte) (int, error) {
				*squeezed = true

				return sound.Read(p)
			}))
	}))
}

// xofHasNoAlgorithm returns the planted defect of XOFAlgorithmNamesTheFunction:
// an XOF whose Algorithm returns the empty name.
func xofHasNoAlgorithm(h *XOFHandle) crypto.XOF {
	return NewXOFStub(h, XOFStubDelegateTo(stdlibXOF{}), WithXOFAlgorithm(func() crypto.Algorithm { return "" }))
}

// xofHasNoID returns the planted defect of XOFIDIdentifiesTheImplementation: an
// XOF whose ID returns the zero ID.
func xofHasNoID(h *XOFHandle) crypto.XOF {
	return NewXOFStub(h, XOFStubDelegateTo(stdlibXOF{}), WithXOFID(func() crypto.ID { return crypto.ID{} }))
}
