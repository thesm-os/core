// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate go tool kanon -type=Key -canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"go.thesmos.sh/core/pool"
)

const (
	// keySeparator separates the name, the key ID and the encoded type and
	// public key of a verifier key. No key name contains it.
	keySeparator = '+'

	// keyIDSize is the length of a key ID in bytes.
	keyIDSize = 4

	// hexIDSize is the length of a key ID in hexadecimal digits.
	hexIDSize = 8

	// idSeparator separates the key name from the type in the bytes that
	// a key ID hashes.
	idSeparator = '\n'

	// newlines are the characters that base64 decoding skips, which a
	// verifier key does not contain.
	newlines = "\r\n"

	// padding is the character that ends padded base64.
	padding = '='

	// stringBuffer is the length of the longest verifier key that
	// [Key.String] writes on the stack.
	stringBuffer = 256

	// base64Group is the number of bytes that 4 base64 characters encode.
	base64Group = 3

	// base64Head is the number of bytes that appendBase64 copies to the
	// stack at most: a head of 257 bytes, the longest type, and 2 bytes of
	// the body.
	base64Head = 259

	// decodeChunk is the number of base64 characters that appendDecode
	// copies to the stack at a time. It is a multiple of 4, so that each
	// chunk decodes on its own.
	decodeChunk = 256
)

// strictBase64 decodes padded standard base64, and refuses an encoding
// whose spare bits are not zero. It skips carriage returns and newlines,
// so its callers refuse them first.
var strictBase64 = base64.StdEncoding.Strict()

// keyBuffers contains the buffers into which [Key.Set] and
// [Key.UnmarshalText] decode the type and the public key before they copy
// them into the Key.
var keyBuffers = pool.NewPool(func() *[]byte { return new([]byte) })

// Key is a signed-note verifier key: the name, the signature type and the
// public key of the key that makes the signatures of a signature line.
//
// The zero Key is not valid.
//
// # Encoding
//
// [ParseKey] and [Key.AppendText] read and write the verifier key, the
// text form that signed-note specifies. kanon generates the canonical
// codec of Key, the form of a Key in a kanon record, with the field
// numbers Name 1, Type 2 and PublicKey 3. Its decode accepts only the
// encoding that its encode writes, and returns an error that wraps
// [go.thesmos.sh/kanon.ErrNotCanonical] for any other input that the wire
// format lets a decoder accept. The methods of the codec have pointer
// receivers, so encoding/gob encodes a Key only when it can take its
// address.
type Key struct {
	// Name is the key name.
	Name Name

	// Type is the signature type.
	Type Type

	// PublicKey is the public key in the encoding that the
	// [sign.Verifier] of the type returns from PublicKey and that its
	// [sign.Resolver] entry takes: 32 bytes for Ed25519, the FIPS 204
	// encoding for ML-DSA, and PKIX DER for ECDSA P-384.
	PublicKey []byte
}

// ParseKey parses a verifier key: the name, '+', the key ID in 8
// lowercase hexadecimal digits, '+', and the type followed by the public
// key in padded standard base64. It parses vkey into a new Key, as
// [Key.Set] parses it. The name of the returned Key is a substring of
// vkey, and its public key does not alias vkey.
//
// Returns [ErrKey], classified [errs.Invalid], for a vkey without two
// '+', an invalid name, a key ID that is not 8 lowercase hexadecimal
// digits, base64 that is not the padded standard encoding of its bytes, an
// invalid type, a type without a public key after it, and a key ID that
// differs from [Key.ID].
//
// # Allocation contract
//
// Allocates the public key once, a Type without an assigned byte once
// more, and the error of a key that it refuses. It decodes into a pooled
// buffer.
func ParseKey(vkey string) (Key, error) {
	var k Key
	if err := k.Set(vkey); err != nil {
		return Key{}, err
	}

	return k, nil
}

// Valid reports whether k has a valid name, a valid type and a public key.
//
// # Allocation contract
//
// Zero-alloc.
func (k Key) Valid() bool {
	return k.Name.Valid() && k.Type.Valid() && len(k.PublicKey) > 0
}

// ID returns the key ID of k: the first four bytes of SHA-256(name ‖ 0x0A
// ‖ type ‖ public key), read as a big-endian integer. It does not check
// that k is Valid.
//
// # Allocation contract
//
// Zero-alloc. It copies the name and the type to the hash through a
// buffer on the stack, in the function that creates the hash, so that the
// compiler keeps the hash and the buffer on the stack.
func (k Key) ID() uint32 {
	h := sha256.New()

	var chunk [hashChunk]byte

	for _, s := range [...]string{string(k.Name), string(idSeparator), string(k.Type)} {
		for s != "" {
			n := copy(chunk[:], s)
			h.Write(chunk[:n])
			s = s[n:]
		}
	}

	h.Write(k.PublicKey)

	var sum [sha256.Size]byte

	return binary.BigEndian.Uint32(h.Sum(sum[:0]))
}

// AppendText appends the verifier key of k to b and returns the extended
// slice. It implements [encoding.TextAppender].
//
// Returns b unchanged and [ErrKey], classified [errs.Invalid], for a Key
// that is not Valid.
//
// # Allocation contract
//
// Zero-alloc when b has room for the verifier key.
func (k Key) AppendText(b []byte) ([]byte, error) {
	if !k.Valid() {
		return b, fmt.Errorf("%w: a key needs a valid name, a valid type and a public key", ErrKey)
	}

	var id [keyIDSize]byte
	binary.BigEndian.PutUint32(id[:], k.ID())

	b = append(b, k.Name...)
	b = append(b, keySeparator)
	b = hex.AppendEncode(b, id[:])
	b = append(b, keySeparator)

	return appendBase64(b, string(k.Type), k.PublicKey), nil
}

// MarshalText returns the verifier key of k. It implements
// [encoding.TextMarshaler].
//
// Returns nil and the error of [Key.AppendText] for a Key that is not
// Valid.
//
// # Allocation contract
//
// Allocates the returned verifier key once, at its exact length.
func (k Key) MarshalText() ([]byte, error) {
	b, err := k.AppendText(make([]byte, 0, k.textLen()))
	if err != nil {
		return nil, err
	}

	return b, nil
}

// Set sets k to the key of the verifier key vkey, as [ParseKey] parses it,
// and reuses the memory of k: its name and its type when they equal those
// of vkey, and the capacity of its public key, into which it copies the
// public key. A name that differs from the name of k is a substring of
// vkey. With [Key.String], Set implements [flag.Value], so a command line
// takes a verifier key as a flag.
//
// Returns the errors of ParseKey, and leaves k unchanged, for a vkey that
// is not a verifier key.
//
// # Allocation contract
//
// Zero-alloc when the type of vkey is a type of one byte or the type of
// k, and the public key of k has room for the public key of vkey, as when
// k holds the key of vkey. Otherwise allocates the new type and the
// public key. It decodes into a pooled buffer, and copies the base64 to
// the stack in chunks, so that the string vkey costs no conversion.
func (k *Key) Set(vkey string) error {
	name, t, pub, err := parseKey(vkey, k.Name, k.Type, k.PublicKey)
	if err != nil {
		return err
	}

	k.Name, k.Type, k.PublicKey = name, t, pub

	return nil
}

// UnmarshalText sets k to the key of the verifier key text, as [Key.Set]
// sets it, and reuses the memory of k as Set does. A name that differs
// from the name of k is a new string. It implements
// [encoding.TextUnmarshaler], so a configuration in JSON or YAML decodes a
// list of verifier keys into a []Key.
//
// Returns the errors of [ParseKey], and leaves k unchanged, for a text
// that is not a verifier key.
//
// # Allocation contract
//
// Zero-alloc when the name and the type of text equal those of k and the
// public key of k has room for its public key, as when k holds the key of
// text. Otherwise allocates the new name, a new type without an assigned
// byte, and the public key.
func (k *Key) UnmarshalText(text []byte) error {
	name, t, pub, err := parseKey(text, k.Name, k.Type, k.PublicKey)
	if err != nil {
		return err
	}

	k.Name, k.Type, k.PublicKey = name, t, pub

	return nil
}

// String returns the verifier key of k, or the empty string for a Key that
// is not Valid. It implements [fmt.Stringer].
//
// # Allocation contract
//
// Allocates the returned string, and the verifier key of more than 256
// bytes, such as that of an ML-DSA key, once more. [Key.AppendText] is the
// form without an allocation.
func (k Key) String() string {
	var buf [stringBuffer]byte

	b, err := k.AppendText(buf[:0])
	if err != nil {
		return ""
	}

	return string(b)
}

// textLen returns the length of the verifier key of k: the name, the key
// ID in hexadecimal between two '+', and the base64 of the type and the
// public key.
func (k Key) textLen() int {
	return len(k.Name) + hexIDSize + 2 + base64.StdEncoding.EncodedLen(len(k.Type)+len(k.PublicKey))
}

// parseKey returns the name, the type and the public key of vkey, a
// verifier key in a string or a byte slice, as [Key.Set] documents. It
// returns name when the name of vkey equals it, and t when the type of
// vkey equals it, and copies the public key into the capacity of pub. It
// takes and returns values, and no pointer to a Key, so that the Key of a
// caller in another package stays on the stack of that caller.
func parseKey[S ~string | ~[]byte](vkey S, name Name, t Type, pub []byte) (Name, Type, []byte, error) {
	part, rest, ok := cut(vkey, keySeparator)
	if !ok {
		return "", "", nil, fmt.Errorf("%w: no '+' after the name", ErrKey)
	}

	hexID, encoded, ok := cut(rest, keySeparator)
	if !ok {
		return "", "", nil, fmt.Errorf("%w: no '+' after the key ID", ErrKey)
	}

	if string(part) != string(name) {
		name = Name(part)
	}

	if !name.Valid() {
		return "", "", nil, fmt.Errorf("%w: invalid name %q", ErrKey, string(name))
	}

	id, ok := parseKeyID(hexID)
	if !ok {
		return "", "", nil, fmt.Errorf("%w: key ID %q is not 8 lowercase hexadecimal digits", ErrKey, string(hexID))
	}

	buf := keyBuffers.Get()
	defer keyBuffers.Put(buf)

	raw, ok := appendDecode((*buf)[:0], encoded)
	*buf = raw[:0]

	if !ok {
		return "", "", nil, fmt.Errorf("%w: the type and the public key are not padded standard base64", ErrKey)
	}

	t, key, ok := splitType(raw, t)
	if !ok {
		return "", "", nil, fmt.Errorf("%w: an invalid type, or no public key after the type", ErrKey)
	}

	if got := (Key{Name: name, Type: t, PublicKey: key}).ID(); got != id {
		return "", "", nil, fmt.Errorf("%w: key ID %08x differs from %08x, the key ID of the key", ErrKey, id, got)
	}

	return name, t, append(pub[:0], key...), nil
}

// cut slices s around its first byte sep, and returns the bytes before
// and after it and true, or s, an empty tail and false when s has no sep.
// It is strings.Cut and bytes.Cut for a string or a byte slice.
func cut[S ~string | ~[]byte](s S, sep byte) (before, after S, found bool) {
	for i := range len(s) {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}

	return s, s[len(s):], false
}

// parseKeyID returns the key ID that s spells in 8 lowercase hexadecimal
// digits, and reports whether s spells one.
func parseKeyID[S ~string | ~[]byte](s S) (uint32, bool) {
	if len(s) != hexIDSize {
		return 0, false
	}

	var digits [hexIDSize]byte
	copy(digits[:], s)

	var id [keyIDSize]byte
	if _, err := hex.Decode(id[:], digits[:]); err != nil {
		return 0, false
	}

	var lower [hexIDSize]byte
	if !bytes.Equal(hex.AppendEncode(lower[:0], id[:]), digits[:]) {
		return 0, false
	}

	return binary.BigEndian.Uint32(id[:]), true
}

// appendDecode appends the bytes that s spells in padded standard base64
// to dst, and returns the extended slice and whether s spells bytes. It
// copies s to the stack in chunks of 256 characters and decodes each
// chunk, so that a string s costs no conversion to bytes. Padding may end
// only the last chunk, so the chunks decode to the bytes that s spells as
// a whole. It refuses spare bits that are not zero, and the carriage
// returns and newlines that the decoder skips, so that every byte string
// has one encoding.
func appendDecode[S ~string | ~[]byte](dst []byte, s S) ([]byte, bool) {
	var chunk [decodeChunk]byte

	for len(s) != 0 {
		n := copy(chunk[:], s)
		part := chunk[:n]
		s = s[n:]

		if bytes.ContainsAny(part, newlines) || len(s) != 0 && bytes.IndexByte(part, padding) != -1 {
			return dst, false
		}

		var err error
		if dst, err = strictBase64.AppendDecode(dst, part); err != nil {
			return dst, false
		}
	}

	return dst, true
}

// splitType splits the decoded bytes of a verifier key into the type and
// the public key. It returns reuse as the type when the bytes of the type
// equal it, so that a known type costs no allocation. It reports false
// when the bytes end before the end of the type or at it, so that no
// public key follows, and when the type is not Valid. The public key
// aliases raw.
func splitType(raw []byte, reuse Type) (Type, []byte, bool) {
	n := 1
	if len(raw) > 1 && raw[0] == typeOther {
		n = 2 + int(raw[1])
	}

	if n >= len(raw) {
		return "", nil, false
	}

	t := reuse
	if string(raw[:n]) != string(t) {
		t = Type(raw[:n])
	}

	return t, raw[n:], t.Valid()
}

// appendBase64 appends the padded standard base64 of head ‖ body to dst
// and returns the extended slice, for a head of at most 257 bytes. The
// base64 of a sequence of byte strings is the sequence of their encodings
// when the length of each one but the last is a multiple of 3. It
// therefore copies head and the first bytes of body, up to a multiple of
// 3 bytes, to a buffer on the stack, and encodes the rest of body where it
// is.
func appendBase64(dst []byte, head string, body []byte) []byte {
	var buf [base64Head]byte

	n := copy(buf[:], head)
	n += copy(buf[n:n+(base64Group-n%base64Group)%base64Group], body)
	dst = base64.StdEncoding.AppendEncode(dst, buf[:n])

	return base64.StdEncoding.AppendEncode(dst, body[n-len(head):])
}
