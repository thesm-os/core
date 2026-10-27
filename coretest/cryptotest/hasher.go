// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha3"
	"fmt"
	"hash"
	"slices"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
)

// The sizes of the inputs of the checks of a Hasher and of their planted defects.
const (
	// hasherMaxData is the largest data that a property check of a Hasher draws.
	hasherMaxData = 256

	// hasherLongData is the size above which the planted defects of
	// HasherHashIsDeterministic, HasherHashTaggedIsTheRoleByteFollowedByTheData and
	// HasherHashTaggedIsDeterministic change a call.
	hasherLongData = 16

	// hasherLongWrite is the size above which the planted defect of
	// HasherStreamSumsLikeHash drops the end of a Write.
	hasherLongWrite = 40

	// hasherDroppedWrite is the number of the first Write that the planted defect
	// of HasherStreamAbsorbsSplitWrites drops.
	hasherDroppedWrite = 3
)

// The roles of the checks of a Hasher. Core assigns no roles, so the checks take
// example roles of each half and the roles on either side of the high bit.
const (
	// hasherLeafRole is the unary role of a leaf.
	hasherLeafRole crypto.Role = 0x01

	// hasherLinkRole is a binary role, such as the role of the link of a chain.
	hasherLinkRole crypto.Role = 0x83

	// hasherNodeRole is a binary role, such as the role of the node of a tree.
	hasherNodeRole crypto.Role = 0x84

	// hasherAccumulatorRole is a binary role, such as the role of the node of an
	// accumulator.
	hasherAccumulatorRole crypto.Role = 0x85

	// hasherLowestUnary is the lowest unary role.
	hasherLowestUnary crypto.Role = 0x00

	// hasherHighestUnary is the highest unary role.
	hasherHighestUnary crypto.Role = 0x7F

	// hasherLowestBinary is the lowest binary role.
	hasherLowestBinary crypto.Role = 0x80

	// hasherHighestBinary is the highest binary role.
	hasherHighestBinary crypto.Role = 0xFF
)

// The messages that the checks of a Hasher hash.
var (
	hasherPangram = []byte("the quick brown fox jumps over the lazy dog")
	hasherSplit   = []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	hasherAlpha   = []byte("alpha")
	hasherBeta    = []byte("beta")
	hasherFirst   = []byte("first")
	hasherSecond  = []byte("second")
	hasherPrefix  = []byte("ab")
	hasherSuffix  = []byte("c")
	hasherWhole   = []byte("abc")

	// hasherLeft and hasherRight are the messages whose digests are the operands
	// of an interior node in the checks of CombineTagged.
	hasherLeft  = []byte("left")
	hasherRight = []byte("right")

	// hasherMarker is the data whose digest the planted defect of
	// HasherHashesNilAsEmpty returns for nil data.
	hasherMarker = []byte{0}
)

// hasherSoundSpec is the spec of the SHA-256 of the standard library that the
// planted defects of the suite of Hasher change.
var hasherSoundSpec = StdlibHasherSpec{
	NewHash:   sha256.New,
	Sum:       func(data []byte) []byte { d := sha256.Sum256(data); return d[:] },
	Algorithm: crypto.AlgSHA256,
	ID:        crypto.ID{'c', 'r', 'y', 'p', 't', 'o', 't', 'e', 's', 't', '/', 'h', 'a', 's', 'h'},
}

// hasherOtherSpec is the spec of the SHA3-256 of the standard library, whose
// digests have the width of the digests of hasherSoundSpec.
var hasherOtherSpec = StdlibHasherSpec{
	NewHash:   func() hash.Hash { return sha3.New256() },
	Sum:       func(data []byte) []byte { d := sha3.Sum256(data); return d[:] },
	Algorithm: crypto.AlgSHA3_256,
	ID:        crypto.ID{'c', 'r', 'y', 'p', 't', 'o', 't', 'e', 's', 't', '/', 's', 'h', 'a', '3'},
}

// StdlibHasherSpec is the algorithm, the ID and the functions of a hash of the
// standard library, which [NewStdlibHasherStub] wraps. A test fills it with the
// hash of the Hasher under test, so the Hasher of the spec is the reference of
// that Hasher.
type StdlibHasherSpec struct {
	// NewHash returns a hash of the standard library, such as sha256.New, for
	// the streams of the Hasher.
	NewHash func() hash.Hash

	// Sum returns the digest of data, as sha256.Sum256 does, for Hash,
	// HashTagged and CombineTagged.
	Sum func(data []byte) []byte

	// Algorithm is the name of the algorithm that Algorithm returns.
	Algorithm crypto.Algorithm

	// ID is the ID that ID returns.
	ID crypto.ID
}

// stdlibHasher is the [crypto.Hasher] of a [StdlibHasherSpec]. It hashes the
// role byte and the operands of a tagged call with the Sum of the spec. It panics
// on a role of the other half, on the zero Digest and on an operand whose width
// is not the width of the digests of Hash, as the contract of [crypto.Hasher]
// states.
type stdlibHasher struct {
	tb   assert.TB
	spec StdlibHasherSpec
}

// ID returns the ID of the spec.
func (h *stdlibHasher) ID() crypto.ID { return h.spec.ID }

// Algorithm returns the algorithm of the spec.
func (h *stdlibHasher) Algorithm() crypto.Algorithm { return h.spec.Algorithm }

// Hash returns the digest of data.
func (h *stdlibHasher) Hash(data []byte) crypto.Digest {
	return digestFromBytes(h.spec.Sum(data))
}

// HashTagged returns the digest of r followed by data. It panics for a binary
// role.
func (h *stdlibHasher) HashTagged(r crypto.Role, data []byte) crypto.Digest {
	if !r.IsUnary() {
		panic(fmt.Sprintf("cryptotest: HashTagged takes a unary role, got %#02x", byte(r)))
	}

	return h.Hash(slices.Concat([]byte{byte(r)}, data))
}

// CombineTagged returns the digest of r followed by left and right. It panics
// for a unary role, for the zero Digest and for an operand whose width is not the
// width of the digests of Hash.
func (h *stdlibHasher) CombineTagged(r crypto.Role, left, right crypto.Digest) crypto.Digest {
	if !r.IsBinary() {
		panic(fmt.Sprintf("cryptotest: CombineTagged takes a binary role, got %#02x", byte(r)))
	}
	if left.IsZero() || right.IsZero() {
		panic("cryptotest: CombineTagged refuses the zero Digest")
	}
	if width := h.spec.NewHash().Size(); left.Size() != width || right.Size() != width {
		panic(fmt.Sprintf("cryptotest: CombineTagged takes operands of %d bytes, got %d and %d",
			width, left.Size(), right.Size()))
	}

	return h.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes()))
}

// NewStream returns a stream over a new hash of the spec.
func (h *stdlibHasher) NewStream() crypto.Stream {
	return NewStdlibStreamStub(h.tb, h.spec.NewHash())
}

// NewStdlibHasherStub returns a [HasherStub] that delegates each call to the
// Hasher of spec. The double records the calls, and the standard library
// computes the digests.
func NewStdlibHasherStub(tb assert.TB, spec StdlibHasherSpec) *HasherStub {
	return NewHasherStub(tb, HasherStubDelegateTo(&stdlibHasher{tb: tb, spec: spec}))
}

// HasherZeroAllocCases returns one call for each operation of [crypto.Hasher]
// and [crypto.Stream] that allocates nothing on the warm path, keyed by name, for
// a test that measures each call with expect.MaxAllocs.
//
// Every call goes through the interfaces, over data. data must be on the heap, as
// a caller that must not allocate passes it. A slice passed through an interface
// escapes at the call site, so data on the stack would measure the caller and
// not the implementation. The stream calls borrow a stream from the pool of h and
// return it, so every call after the first finds the pool warm.
func HasherZeroAllocCases(h crypto.Hasher, data []byte) map[string]func() {
	d := h.Hash(data)

	return map[string]func(){
		"ID and Algorithm": func() {
			_ = h.ID()
			_ = h.Algorithm()
		},
		"Hash":          func() { _ = h.Hash(data) },
		"HashTagged":    func() { _ = h.HashTagged(hasherLeafRole, data) },
		"CombineTagged": func() { _ = h.CombineTagged(hasherNodeRole, d, d) },
		"NewStream, Write, Sum and Close": func() {
			s := h.NewStream()
			_, _ = s.Write(data)
			_ = s.Sum()
			s.Close()
		},
	}
}

// HasherIDIdentifiesTheImplementation checks that ID returns an ID that is not
// zero, and the same ID on every call.
//
//dokimi:check Hasher.ID defect=hasherHasNoID
func HasherIDIdentifiesTheImplementation(h *HasherHandle) {
	s := h.Subject()
	assert.NotEqual(h, s.ID(), crypto.ID{}, h.Statement())
	assert.Deterministic(h, func(s crypto.Hasher) (crypto.ID, error) { return s.ID(), nil }, s, h.Statement())
}

// HasherAlgorithmNamesTheFunction checks that Algorithm returns a name that is
// not empty, and the same name on every call.
//
//dokimi:check Hasher.Algorithm defect=hasherHasNoAlgorithm
func HasherAlgorithmNamesTheFunction(h *HasherHandle) {
	s := h.Subject()
	assert.NotEqual(h, s.Algorithm(), crypto.Algorithm(""), h.Statement())
	assert.Deterministic(h, func(s crypto.Hasher) (crypto.Algorithm, error) { return s.Algorithm(), nil }, s,
		h.Statement())
}

// HasherHashIsDeterministic checks that Hash returns the same digest on every
// call with the same data.
//
//dokimi:check Hasher.Hash defect=hasherAlternatesItsHash
func HasherHashIsDeterministic(h *HasherHandle, c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "data")
	s := h.Subject()
	assert.Deterministic(h, func(d []byte) (crypto.Digest, error) { return s.Hash(d), nil }, data, h.Statement())
}

// HasherHashesNilAsEmpty checks that the digest of nil data is the digest of
// empty data.
//
//dokimi:check Hasher.Hash defect=hasherHashesNilAsAMarker
func HasherHashesNilAsEmpty(h *HasherHandle) {
	s := h.Subject()
	assert.Equal(h, s.Hash(nil), s.Hash([]byte{}), h.Statement())
}

// HasherHashDistinguishesInputs checks that the digests of two different messages
// differ.
//
//dokimi:check Hasher.Hash defect=hasherCollidesTwoInputs
func HasherHashDistinguishesInputs(h *HasherHandle) {
	s := h.Subject()
	assert.NotEqual(h, s.Hash(hasherAlpha), s.Hash(hasherBeta), h.Statement())
}

// HasherHashMatchesTheReference checks that Hash returns the digest that the
// reference of the application returns for the same data.
//
//dokimi:check Hasher.Hash defect=hasherOfAnotherAlgorithm reference=hasherReference
func HasherHashMatchesTheReference(h *HasherHandle, c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "data")
	assert.Equal(h, h.Subject().Hash(data), h.Reference().Hash(data), h.Statement())
}

// HasherHashTaggedIsTheRoleByteFollowedByTheData checks that HashTagged of a
// unary role returns the digest that Hash returns for the role byte followed by
// the data.
//
//dokimi:check Hasher.HashTagged defect=hasherFramesTheTaggedData
func HasherHashTaggedIsTheRoleByteFollowedByTheData(h *HasherHandle, c *prop.Case) {
	r := c.Draw(prop.Integer(hasherLowestUnary, hasherHighestUnary), "role")
	data := c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "data")
	s := h.Subject()
	assert.Equal(h, s.HashTagged(r, data), s.Hash(slices.Concat([]byte{byte(r)}, data)), h.Statement())
}

// HasherHashTaggedHashesNilAsTheRoleByte checks that HashTagged of nil data
// returns the digest that Hash returns for the role byte alone.
//
//dokimi:check Hasher.HashTagged defect=hasherTagsNilAsAZeroByte
func HasherHashTaggedHashesNilAsTheRoleByte(h *HasherHandle) {
	s := h.Subject()
	assert.Equal(h, s.HashTagged(hasherLeafRole, nil), s.Hash([]byte{byte(hasherLeafRole)}), h.Statement())
}

// HasherHashTaggedIsDeterministic checks that HashTagged returns the same digest
// on every call with the same role and data.
//
//dokimi:check Hasher.HashTagged defect=hasherAlternatesItsTaggedHash
func HasherHashTaggedIsDeterministic(h *HasherHandle, c *prop.Case) {
	r := c.Draw(prop.Integer(hasherLowestUnary, hasherHighestUnary), "role")
	data := c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "data")
	s := h.Subject()
	assert.Deterministic(h, func(d []byte) (crypto.Digest, error) { return s.HashTagged(r, d), nil }, data,
		h.Statement())
}

// HasherHashTaggedRefusesABinaryRole checks that HashTagged panics for the lowest
// and the highest binary role, and returns for the highest unary role.
//
//dokimi:check Hasher.HashTagged defect=hasherTagsDataWithABinaryRole
func HasherHashTaggedRefusesABinaryRole(h *HasherHandle) {
	s := h.Subject()
	assert.NotPanics(h, func() { _ = s.HashTagged(hasherHighestUnary, nil) }, h.Statement())
	assert.Panics(h, func() { _ = s.HashTagged(hasherLowestBinary, nil) }, h.Statement())
	assert.Panics(h, func() { _ = s.HashTagged(hasherHighestBinary, hasherAlpha) }, h.Statement())
}

// HasherLeafDiffersFromItsNode checks that HashTagged of the operands of an
// interior node, offered as the data of a leaf, does not return the digest of the
// node.
//
//dokimi:check Hasher.HashTagged defect=hasherHashesACraftedLeafAsItsNode
func HasherLeafDiffersFromItsNode(h *HasherHandle) {
	s := h.Subject()
	left, right := s.Hash(hasherLeft), s.Hash(hasherRight)
	crafted := slices.Concat(left.Bytes(), right.Bytes())
	assert.NotEqual(h, s.HashTagged(hasherLeafRole, crafted), s.CombineTagged(hasherNodeRole, left, right),
		h.Statement())
}

// HasherCombineTaggedIsTheRoleByteFollowedByBothOperands checks that
// CombineTagged of a binary role returns the digest that Hash returns for the
// role byte followed by both operands.
//
//dokimi:check Hasher.CombineTagged defect=hasherFramesTheOperands
func HasherCombineTaggedIsTheRoleByteFollowedByBothOperands(h *HasherHandle, c *prop.Case) {
	r := c.Draw(prop.Integer(hasherLowestBinary, hasherHighestBinary), "role")
	s := h.Subject()
	left := s.Hash(c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "left"))
	right := s.Hash(c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "right"))
	want := s.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes()))
	assert.Equal(h, s.CombineTagged(r, left, right), want, h.Statement())
}

// HasherCombineTaggedIsDeterministic checks that CombineTagged returns the same
// digest on every call with the same role and operands.
//
//dokimi:check Hasher.CombineTagged defect=hasherAlternatesItsCombination
func HasherCombineTaggedIsDeterministic(h *HasherHandle, c *prop.Case) {
	r := c.Draw(prop.Integer(hasherLowestBinary, hasherHighestBinary), "role")
	s := h.Subject()
	left := s.Hash(c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "left"))
	right := s.Hash(c.Draw(prop.Bytes(prop.MaxSize(hasherMaxData)), "right"))
	assert.Deterministic(h, func(d crypto.Digest) (crypto.Digest, error) { return s.CombineTagged(r, left, d), nil },
		right, h.Statement())
}

// HasherCombineTaggedDependsOnTheOrder checks that CombineTagged of two operands
// differs from CombineTagged of the same operands in reverse order.
//
//dokimi:check Hasher.CombineTagged defect=hasherCombinesAReversedPairAsThePair
func HasherCombineTaggedDependsOnTheOrder(h *HasherHandle) {
	s := h.Subject()
	left, right := s.Hash(hasherLeft), s.Hash(hasherRight)
	assert.NotEqual(h, s.CombineTagged(hasherNodeRole, left, right), s.CombineTagged(hasherNodeRole, right, left),
		h.Statement())
}

// HasherCombineTaggedSeparatesTheRoles checks that CombineTagged of one pair of
// operands returns three distinct digests under three binary roles.
//
//dokimi:check Hasher.CombineTagged defect=hasherCombinesTwoRolesAsOne
func HasherCombineTaggedSeparatesTheRoles(h *HasherHandle) {
	s := h.Subject()
	left, right := s.Hash(hasherLeft), s.Hash(hasherRight)
	assert.NoDuplicates(h, func() ([]crypto.Digest, error) {
		return []crypto.Digest{
			s.CombineTagged(hasherLinkRole, left, right),
			s.CombineTagged(hasherNodeRole, left, right),
			s.CombineTagged(hasherAccumulatorRole, left, right),
		}, nil
	}, h.Statement())
}

// HasherCombineTaggedRefusesAUnaryRole checks that CombineTagged panics for the
// lowest and the highest unary role, and returns for the lowest binary role.
//
//dokimi:check Hasher.CombineTagged defect=hasherCombinesWithAUnaryRole
func HasherCombineTaggedRefusesAUnaryRole(h *HasherHandle) {
	s := h.Subject()
	d := s.Hash(nil)
	assert.NotPanics(h, func() { _ = s.CombineTagged(hasherLowestBinary, d, d) }, h.Statement())
	assert.Panics(h, func() { _ = s.CombineTagged(hasherHighestUnary, d, d) }, h.Statement())
	assert.Panics(h, func() { _ = s.CombineTagged(hasherLowestUnary, d, d) }, h.Statement())
}

// HasherCombineTaggedRefusesTheZeroDigest checks that CombineTagged panics when
// its left or its right operand is the zero Digest.
//
//dokimi:check Hasher.CombineTagged defect=hasherCombinesTheZeroDigest
func HasherCombineTaggedRefusesTheZeroDigest(h *HasherHandle) {
	s := h.Subject()
	d := s.Hash(nil)
	assert.Panics(h, func() { _ = s.CombineTagged(hasherNodeRole, crypto.Digest{}, d) }, h.Statement())
	assert.Panics(h, func() { _ = s.CombineTagged(hasherNodeRole, d, crypto.Digest{}) }, h.Statement())
}

// HasherCombineTaggedRefusesAnOperandOfAnotherWidth checks that CombineTagged
// panics when its left or its right operand has another width than the digests
// of Hash.
//
//dokimi:check Hasher.CombineTagged defect=hasherCombinesOperandsOfAnyWidth
func HasherCombineTaggedRefusesAnOperandOfAnotherWidth(h *HasherHandle) {
	s := h.Subject()
	d := s.Hash(nil)
	wrong := otherWidthDigest(d.Size())
	assert.Panics(h, func() { _ = s.CombineTagged(hasherNodeRole, wrong, d) }, h.Statement())
	assert.Panics(h, func() { _ = s.CombineTagged(hasherNodeRole, d, wrong) }, h.Statement())
}

// HasherStreamSumsLikeHash checks that the Sum of a stream that absorbed a
// message in one Write is the digest that Hash returns for the message.
//
//dokimi:check Hasher.NewStream defect=hasherStreamDropsTheEndOfALongWrite
func HasherStreamSumsLikeHash(h *HasherHandle) {
	s := h.Subject()
	st := s.NewStream()
	defer st.Close()
	streamWrite(h, st, hasherPangram)
	assert.Equal(h, st.Sum(), s.Hash(hasherPangram), h.Statement())
}

// HasherStreamAbsorbsSplitWrites checks that the Sum of a stream that absorbed a
// message in three Writes is the digest that Hash returns for the message.
//
//dokimi:check Hasher.NewStream defect=hasherStreamDropsTheThirdWrite
func HasherStreamAbsorbsSplitWrites(h *HasherHandle) {
	s := h.Subject()
	st := s.NewStream()
	defer st.Close()
	streamWrite(h, st, hasherSplit[:10])
	streamWrite(h, st, hasherSplit[10:25])
	streamWrite(h, st, hasherSplit[25:])
	assert.Equal(h, st.Sum(), s.Hash(hasherSplit), h.Statement())
}

// HasherStreamResetRestartsTheHash checks that a stream that absorbed a message
// and was Reset returns the digest of the next message alone.
//
//dokimi:check Hasher.NewStream defect=hasherStreamKeepsItsInputOnReset
func HasherStreamResetRestartsTheHash(h *HasherHandle) {
	s := h.Subject()
	st := s.NewStream()
	defer st.Close()
	streamWrite(h, st, hasherFirst)
	_ = st.Sum()
	st.Reset()
	streamWrite(h, st, hasherSecond)
	assert.Equal(h, st.Sum(), s.Hash(hasherSecond), h.Statement())
}

// HasherStreamSumKeepsTheState checks that a Write after a Sum extends the
// message of a stream.
//
//dokimi:check Hasher.NewStream defect=hasherStreamResetsOnSum
func HasherStreamSumKeepsTheState(h *HasherHandle) {
	s := h.Subject()
	st := s.NewStream()
	defer st.Close()
	streamWrite(h, st, hasherPrefix)
	_ = st.Sum()
	streamWrite(h, st, hasherSuffix)
	assert.Equal(h, st.Sum(), s.Hash(hasherWhole), h.Statement())
}

// hasherReference returns the reference of the planted defect of
// HasherHashMatchesTheReference: the sound Hasher.
func hasherReference(h *HasherHandle) crypto.Hasher {
	return NewStdlibHasherStub(h, hasherSoundSpec)
}

// hasherHasNoID returns the planted defect of HasherIDIdentifiesTheImplementation:
// a Hasher whose ID returns the zero ID.
func hasherHasNoID(h *HasherHandle) crypto.Hasher {
	return NewHasherStub(h, HasherStubDelegateTo(&stdlibHasher{tb: h, spec: hasherSoundSpec}),
		WithHasherID(func() crypto.ID { return crypto.ID{} }))
}

// hasherHasNoAlgorithm returns the planted defect of
// HasherAlgorithmNamesTheFunction: a Hasher whose Algorithm returns the empty
// name.
func hasherHasNoAlgorithm(h *HasherHandle) crypto.Hasher {
	return NewHasherStub(h, HasherStubDelegateTo(&stdlibHasher{tb: h, spec: hasherSoundSpec}),
		WithHasherAlgorithm(func() crypto.Algorithm { return "" }))
}

// hasherAlternatesItsHash returns the planted defect of HasherHashIsDeterministic:
// a Hasher whose every second Hash of data longer than 16 bytes returns the
// digest of the data and a zero byte.
func hasherAlternatesItsHash(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}
	var calls int

	return NewHasherStub(h, HasherStubDelegateTo(sound), WithHasherHash(func(data []byte) crypto.Digest {
		calls++
		if len(data) > hasherLongData && calls%2 == 0 {
			return sound.Hash(slices.Concat(data, []byte{0}))
		}

		return sound.Hash(data)
	}))
}

// hasherHashesNilAsAMarker returns the planted defect of HasherHashesNilAsEmpty: a
// Hasher whose Hash of nil data returns the digest of one zero byte.
func hasherHashesNilAsAMarker(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound), WithHasherHash(func(data []byte) crypto.Digest {
		if data == nil {
			return sound.Hash(hasherMarker)
		}

		return sound.Hash(data)
	}))
}

// hasherCollidesTwoInputs returns the planted defect of
// HasherHashDistinguishesInputs: a Hasher whose Hash of the message beta returns
// the digest of the message alpha.
func hasherCollidesTwoInputs(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound), WithHasherHash(func(data []byte) crypto.Digest {
		if bytes.Equal(data, hasherBeta) {
			return sound.Hash(hasherAlpha)
		}

		return sound.Hash(data)
	}))
}

// hasherOfAnotherAlgorithm returns the planted defect of
// HasherHashMatchesTheReference: the SHA3-256 of the standard library, whose
// digests have the width of the digests of the reference.
func hasherOfAnotherAlgorithm(h *HasherHandle) crypto.Hasher {
	return NewStdlibHasherStub(h, hasherOtherSpec)
}

// hasherFramesTheTaggedData returns the planted defect of
// HasherHashTaggedIsTheRoleByteFollowedByTheData: a Hasher whose HashTagged of
// data longer than 16 bytes hashes a zero byte between the role byte and the
// data.
func hasherFramesTheTaggedData(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherHashTagged(func(r crypto.Role, data []byte) crypto.Digest {
			if r.IsUnary() && len(data) > hasherLongData {
				return sound.Hash(slices.Concat([]byte{byte(r), 0}, data))
			}

			return sound.HashTagged(r, data)
		}))
}

// hasherTagsNilAsAZeroByte returns the planted defect of
// HasherHashTaggedHashesNilAsTheRoleByte: a Hasher whose HashTagged of nil data
// returns the digest of the role byte and a zero byte.
func hasherTagsNilAsAZeroByte(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherHashTagged(func(r crypto.Role, data []byte) crypto.Digest {
			if r.IsUnary() && data == nil {
				return sound.Hash([]byte{byte(r), 0})
			}

			return sound.HashTagged(r, data)
		}))
}

// hasherAlternatesItsTaggedHash returns the planted defect of
// HasherHashTaggedIsDeterministic: a Hasher whose every second HashTagged of data
// longer than 16 bytes returns the tagged digest of the data and a zero byte.
func hasherAlternatesItsTaggedHash(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}
	var calls int

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherHashTagged(func(r crypto.Role, data []byte) crypto.Digest {
			calls++
			if len(data) > hasherLongData && calls%2 == 0 {
				return sound.HashTagged(r, slices.Concat(data, []byte{0}))
			}

			return sound.HashTagged(r, data)
		}))
}

// hasherTagsDataWithABinaryRole returns the planted defect of
// HasherHashTaggedRefusesABinaryRole: a Hasher whose HashTagged hashes the role
// byte and the data for a role of either half.
func hasherTagsDataWithABinaryRole(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherHashTagged(func(r crypto.Role, data []byte) crypto.Digest {
			return sound.Hash(slices.Concat([]byte{byte(r)}, data))
		}))
}

// hasherHashesACraftedLeafAsItsNode returns the planted defect of
// HasherLeafDiffersFromItsNode: a Hasher whose HashTagged of the operands of the
// node of the check returns the digest of the node.
func hasherHashesACraftedLeafAsItsNode(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}
	left, right := sound.Hash(hasherLeft), sound.Hash(hasherRight)
	crafted := slices.Concat(left.Bytes(), right.Bytes())

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherHashTagged(func(r crypto.Role, data []byte) crypto.Digest {
			if bytes.Equal(data, crafted) {
				return sound.CombineTagged(hasherNodeRole, left, right)
			}

			return sound.HashTagged(r, data)
		}))
}

// hasherCombineDefect returns a Hasher over the sound Hasher whose CombineTagged
// of a binary role and two operands of the width of the digests of Hash returns
// the digest that change returns. The sound Hasher refuses every other call of
// CombineTagged.
func hasherCombineDefect(
	h *HasherHandle,
	change func(sound *stdlibHasher, r crypto.Role, left, right crypto.Digest) crypto.Digest,
) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}
	width := hasherSoundSpec.NewHash().Size()

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherCombineTagged(func(r crypto.Role, left, right crypto.Digest) crypto.Digest {
			if !r.IsBinary() || left.Size() != width || right.Size() != width {
				return sound.CombineTagged(r, left, right)
			}

			return change(sound, r, left, right)
		}))
}

// hasherFramesTheOperands returns the planted defect of
// HasherCombineTaggedIsTheRoleByteFollowedByBothOperands: a Hasher whose
// CombineTagged hashes a zero byte between the two operands.
func hasherFramesTheOperands(h *HasherHandle) crypto.Hasher {
	return hasherCombineDefect(h, func(sound *stdlibHasher, r crypto.Role, left, right crypto.Digest) crypto.Digest {
		return sound.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), []byte{0}, right.Bytes()))
	})
}

// hasherAlternatesItsCombination returns the planted defect of
// HasherCombineTaggedIsDeterministic: a Hasher whose every second CombineTagged
// returns the digest of the role byte, both operands and a zero byte.
func hasherAlternatesItsCombination(h *HasherHandle) crypto.Hasher {
	var calls int

	return hasherCombineDefect(h, func(sound *stdlibHasher, r crypto.Role, left, right crypto.Digest) crypto.Digest {
		calls++
		if calls%2 == 0 {
			return sound.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes(), []byte{0}))
		}

		return sound.CombineTagged(r, left, right)
	})
}

// hasherCombinesAReversedPairAsThePair returns the planted defect of
// HasherCombineTaggedDependsOnTheOrder: a Hasher whose CombineTagged of the pair
// of the check in reverse order returns the digest of the pair.
func hasherCombinesAReversedPairAsThePair(h *HasherHandle) crypto.Hasher {
	pairLeft := digestFromBytes(hasherSoundSpec.Sum(hasherLeft))
	pairRight := digestFromBytes(hasherSoundSpec.Sum(hasherRight))

	return hasherCombineDefect(h, func(sound *stdlibHasher, r crypto.Role, left, right crypto.Digest) crypto.Digest {
		if left.Equal(pairRight) && right.Equal(pairLeft) {
			return sound.CombineTagged(r, pairLeft, pairRight)
		}

		return sound.CombineTagged(r, left, right)
	})
}

// hasherCombinesTwoRolesAsOne returns the planted defect of
// HasherCombineTaggedSeparatesTheRoles: a Hasher whose CombineTagged of the pair
// of the check under the role of an accumulator returns the digest under the role
// of a node.
func hasherCombinesTwoRolesAsOne(h *HasherHandle) crypto.Hasher {
	pairLeft := digestFromBytes(hasherSoundSpec.Sum(hasherLeft))
	pairRight := digestFromBytes(hasherSoundSpec.Sum(hasherRight))

	return hasherCombineDefect(h, func(sound *stdlibHasher, r crypto.Role, left, right crypto.Digest) crypto.Digest {
		if r == hasherAccumulatorRole && left.Equal(pairLeft) && right.Equal(pairRight) {
			return sound.CombineTagged(hasherNodeRole, left, right)
		}

		return sound.CombineTagged(r, left, right)
	})
}

// hasherCombinesWithAUnaryRole returns the planted defect of
// HasherCombineTaggedRefusesAUnaryRole: a Hasher whose CombineTagged hashes the
// role byte and both operands for a unary role too.
func hasherCombinesWithAUnaryRole(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherCombineTagged(func(r crypto.Role, left, right crypto.Digest) crypto.Digest {
			if r.IsUnary() {
				return sound.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes()))
			}

			return sound.CombineTagged(r, left, right)
		}))
}

// hasherCombinesTheZeroDigest returns the planted defect of
// HasherCombineTaggedRefusesTheZeroDigest: a Hasher whose CombineTagged hashes the
// role byte and the bytes of both operands when one of them is the zero Digest.
func hasherCombinesTheZeroDigest(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherCombineTagged(func(r crypto.Role, left, right crypto.Digest) crypto.Digest {
			if r.IsBinary() && (left.IsZero() || right.IsZero()) {
				return sound.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes()))
			}

			return sound.CombineTagged(r, left, right)
		}))
}

// hasherCombinesOperandsOfAnyWidth returns the planted defect of
// HasherCombineTaggedRefusesAnOperandOfAnotherWidth: a Hasher whose CombineTagged
// hashes the role byte and two operands of any width that are not the zero
// Digest.
func hasherCombinesOperandsOfAnyWidth(h *HasherHandle) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherCombineTagged(func(r crypto.Role, left, right crypto.Digest) crypto.Digest {
			if r.IsBinary() && !left.IsZero() && !right.IsZero() {
				return sound.Hash(slices.Concat([]byte{byte(r)}, left.Bytes(), right.Bytes()))
			}

			return sound.CombineTagged(r, left, right)
		}))
}

// hasherStreamDefect returns a Hasher over the sound Hasher whose NewStream
// returns the stream that change makes of a stream of the sound Hasher.
func hasherStreamDefect(h *HasherHandle, change func(sound crypto.Stream) crypto.Stream) crypto.Hasher {
	sound := &stdlibHasher{tb: h, spec: hasherSoundSpec}

	return NewHasherStub(h, HasherStubDelegateTo(sound),
		WithHasherNewStream(func() crypto.Stream { return change(sound.NewStream()) }))
}

// hasherStreamDropsTheEndOfALongWrite returns the planted defect of
// HasherStreamSumsLikeHash: a Hasher whose stream absorbs the first 40 bytes of a
// longer Write.
func hasherStreamDropsTheEndOfALongWrite(h *HasherHandle) crypto.Hasher {
	return hasherStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamWrite(func(p []byte) (int, error) {
			if len(p) > hasherLongWrite {
				_, err := sound.Write(p[:hasherLongWrite])

				return len(p), err
			}

			return sound.Write(p)
		}))
	})
}

// hasherStreamDropsTheThirdWrite returns the planted defect of
// HasherStreamAbsorbsSplitWrites: a Hasher whose stream absorbs its first two
// Writes alone.
func hasherStreamDropsTheThirdWrite(h *HasherHandle) crypto.Hasher {
	return hasherStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		var writes int

		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamWrite(func(p []byte) (int, error) {
			writes++
			if writes >= hasherDroppedWrite {
				return len(p), nil
			}

			return sound.Write(p)
		}))
	})
}

// hasherStreamKeepsItsInputOnReset returns the planted defect of
// HasherStreamResetRestartsTheHash: a Hasher whose stream keeps its absorbed
// message on Reset.
func hasherStreamKeepsItsInputOnReset(h *HasherHandle) crypto.Hasher {
	return hasherStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamReset(func() {}))
	})
}

// hasherStreamResetsOnSum returns the planted defect of
// HasherStreamSumKeepsTheState: a Hasher whose stream resets its message after
// each Sum.
func hasherStreamResetsOnSum(h *HasherHandle) crypto.Hasher {
	return hasherStreamDefect(h, func(sound crypto.Stream) crypto.Stream {
		return NewStreamStub(h, StreamStubDelegateTo(sound), WithStreamSum(func() crypto.Digest {
			d := sound.Sum()
			sound.Reset()

			return d
		}))
	})
}

// otherWidthDigest returns a [crypto.Digest] of 0xAB bytes whose width is not
// size. Its bytes are not zero, because CombineTagged refuses the zero Digest for
// a reason of its own.
func otherWidthDigest(size int) crypto.Digest {
	if size == crypto.DigestSize256 {
		return digestFromBytes(bytes.Repeat([]byte{0xAB}, crypto.DigestSize512))
	}

	return digestFromBytes(bytes.Repeat([]byte{0xAB}, crypto.DigestSize256))
}

// digestFromBytes returns the [crypto.Digest] of the output of a hash of the
// standard library. It panics for an output of another size than 32, 48 or 64
// bytes, which no hash of the kit returns.
func digestFromBytes(b []byte) crypto.Digest {
	switch len(b) {
	case crypto.DigestSize256:
		return crypto.NewDigest256([crypto.DigestSize256]byte(b))
	case crypto.DigestSize384:
		return crypto.NewDigest384([crypto.DigestSize384]byte(b))
	case crypto.DigestSize512:
		return crypto.NewDigest512([crypto.DigestSize512]byte(b))
	default:
		panic(fmt.Sprintf("cryptotest: unsupported digest size %d", len(b)))
	}
}
