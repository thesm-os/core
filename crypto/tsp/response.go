// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/x509"
	"math"
	"strings"
	"unicode/utf8"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/internal/der"
)

// failInfoBits is the number of bits of a failInfo that a [StatusError]
// keeps. RFC 3161 names bits up to 25.
const failInfoBits = 32

// ParseResponse returns the TimeStampToken of resp, the DER of a
// TimeStampResp, RFC 3161 section 2.4.2, that responds to the request that
// [AppendRequest] built from the same h, imprint, nonce and policy. The
// token is a slice of resp, and the caller copies it to keep it past resp.
//
// ParseResponse checks that the status grants a token, and that the
// token's TSTInfo repeats the request's message imprint and nonce, and the
// requested policy unless policy is the zero OID. It does not verify the
// token's signature or certificate: [Verifier.Verify] does, offline, every
// time the token is used.
//
// Error modes:
//
//   - A status other than granted and grantedWithMods returns a
//     [*StatusError] with the status, the failure bits and the text of the
//     authority, which classifies by its status.
//   - A response or a token that is not DER, and a granted response
//     without a token, return [ErrMalformed].
//   - A token with another imprint returns [ErrImprint], one without the
//     request's nonce [ErrNonce], and one under another policy than the
//     requested one [ErrPolicy].
//   - A Hash without an OID, and an imprint whose size is not h.Size,
//     return [ErrHash].
//
// Every error but ErrHash, which classifies as
// [go.thesmos.sh/core/errs.Invalid], and the StatusError classifies as
// Integrity.
//
// # Allocation contract
//
// Zero alloc for a response that grants a token. A response that grants
// none allocates its StatusError and its text.
func ParseResponse(resp []byte, h Hash, imprint crypto.Digest, nonce uint64, policy x509.OID) ([]byte, error) {
	if !h.matches(imprint) {
		return nil, ErrHash
	}

	r := der.NewReader(resp)

	seq, ok := r.Read(der.TagSequence)
	if !ok || !r.Empty() {
		return nil, ErrMalformed
	}

	s := der.NewReader(seq)

	status, ok := s.Read(der.TagSequence)
	if !ok {
		return nil, ErrMalformed
	}

	if err := granted(status); err != nil {
		return nil, err
	}

	tok, _, ok := s.ReadElement(der.TagSequence)
	if !ok || !s.Empty() {
		return nil, ErrMalformed
	}

	t, ok := parseToken(tok)
	if !ok {
		return nil, ErrMalformed
	}

	info, ok := parseTSTInfo(t.content)
	if !ok {
		return nil, ErrMalformed
	}

	return tok, repeats(info, h, imprint, nonce, policy)
}

// repeats returns nil when info repeats the imprint under h, the nonce, and
// the policy unless it is the zero OID, and the error of the first field
// that it does not repeat.
func repeats(info tstInfo, h Hash, imprint crypto.Digest, nonce uint64, policy x509.OID) error {
	if !h.names(info.hash) || !bytes.Equal(info.imprint, imprint.Bytes()) {
		return ErrImprint
	}

	if got, ok := der.Uint64(info.nonce); !ok || got != nonce {
		return ErrNonce
	}

	var buf [64]byte
	if want := appendOID(buf[:0], policy); len(want) > 0 && !bytes.Equal(info.policy, want) {
		return ErrPolicy
	}

	return nil
}

// granted returns nil when status, the content of a PKIStatusInfo, grants a
// token, a *StatusError when it grants none, and [ErrMalformed] when it is
// not a PKIStatusInfo: a status INTEGER that fits an int32, an optional
// statusString of UTF8Strings, and an optional failInfo BIT STRING.
func granted(status []byte) error {
	r := der.NewReader(status)

	content, ok := r.Read(der.TagInteger)
	if !ok {
		return ErrMalformed
	}

	code, ok := der.Uint64(content)
	if !ok || code > math.MaxInt32 {
		return ErrMalformed
	}

	texts, _, ok := r.Optional(der.TagSequence)
	if !ok || !validTexts(texts) {
		return ErrMalformed
	}

	failInfo, _, ok := r.Optional(der.TagBitString)
	if !ok || !r.Empty() {
		return ErrMalformed
	}

	bits, _, ok := der.BitString(failInfo)
	if failInfo != nil && !ok {
		return ErrMalformed
	}

	if code == StatusGranted || code == StatusGrantedWithMods {
		return nil
	}

	e := &StatusError{Status: int(code), Text: joinTexts(texts)}
	for n := range failInfoBits {
		if der.Bit(bits, n) {
			e.FailInfo |= 1 << n
		}
	}

	return e
}

// validTexts reports whether texts, the content of a PKIFreeText, is a
// sequence of UTF8Strings of valid UTF-8. An absent statusString is valid.
func validTexts(texts []byte) bool {
	r := der.NewReader(texts)
	for !r.Empty() {
		text, ok := r.Read(der.TagUTF8String)
		if !ok || !utf8.Valid(text) {
			return false
		}
	}

	return true
}

// joinTexts returns the UTF8Strings of texts, a PKIFreeText that
// validTexts accepted, joined by "; ".
func joinTexts(texts []byte) string {
	var b strings.Builder

	r := der.NewReader(texts)
	for !r.Empty() {
		text, _ := r.Read(der.TagUTF8String)
		if b.Len() > 0 {
			b.WriteString("; ")
		}

		b.Write(text)
	}

	return b.String()
}
