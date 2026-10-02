// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"crypto/x509"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/internal/der"
)

// AppendRequest appends the DER of a TimeStampReq, RFC 3161 section
// 2.4.1, to dst, and returns the extended slice. The request has version
// 1, the message imprint of imprint under h, policy unless it is the zero
// OID, nonce, and certReq TRUE, so the authority puts its certificate into
// the token. It has no extensions, and its hashAlgorithm has no
// parameters, as RFC 5754 section 2 asks for a SHA-2 hash.
//
// The caller chooses nonce at random for each request, sends the request to
// the authority, for example as an application/timestamp-query over HTTP,
// RFC 3161 section 3.4, and passes the response with the same h, imprint,
// nonce and policy to [ParseResponse].
//
// Error modes: a Hash without an OID, and an imprint whose size is not
// h.Size, return dst unchanged and [ErrHash], classified
// [go.thesmos.sh/core/errs.Invalid].
//
// # Allocation contract
//
// Zero alloc when dst has room for the request: at most 167 octets for an
// imprint of 64 octets and a policy of 64 octets in DER. AppendRequest
// grows dst without room through append.
func AppendRequest(dst []byte, h Hash, imprint crypto.Digest, nonce uint64, policy x509.OID) ([]byte, error) {
	if !h.matches(imprint) {
		return dst, ErrHash
	}

	var hashOID, policyOID [64]byte

	b := der.NewBuilder(dst)
	req := b.Open(der.TagSequence)
	b.AddUint64(1)

	messageImprint := b.Open(der.TagSequence)
	hashAlgorithm := b.Open(der.TagSequence)
	b.Add(der.TagOID, appendOID(hashOID[:0], h.OID))
	b.Close(hashAlgorithm)
	b.Add(der.TagOctetString, imprint.Bytes())
	b.Close(messageImprint)

	if p := appendOID(policyOID[:0], policy); len(p) > 0 {
		b.Add(der.TagOID, p)
	}

	b.AddUint64(nonce)
	b.AddBoolean(true)
	b.Close(req)

	return b.Bytes(), nil
}
