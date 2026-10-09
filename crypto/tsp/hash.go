// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/x509"

	"go.thesmos.sh/core/crypto"
)

// Hash is the hash algorithm of a message imprint: the OID that a
// TimeStampReq and a TSTInfo name it by, the [crypto.Algorithm] of the
// caller's digests, and the size of a digest in bytes.
//
// A caller passes the Hash of its imprint to [AppendRequest],
// [ParseResponse] and [Verifier.Verify]. This package keeps no table of
// hashes, so a caller with another algorithm, such as the hash of a
// national suite, writes a Hash with that algorithm's OID. A token names
// its hash by OID alone, so two Hash values with one OID and two
// Algorithms verify the same tokens.
//
// # Allocation contract
//
// A Hash is a value. The functions that take one do not allocate for an
// OID of at most 64 octets in DER.
type Hash struct {
	// Algorithm is the name of the algorithm in core.
	Algorithm crypto.Algorithm

	// OID is the object identifier of the algorithm.
	OID x509.OID

	// Size is the size of a digest in bytes: 32, 48 or 64 for a
	// [crypto.Digest].
	Size int
}

// The Hash values of the SHA-2 hashes that RFC 3161 time-stamp
// authorities support. RFC 5754 section 2 assigns their OIDs.
var (
	// SHA256 is SHA-256, id-sha256, 2.16.840.1.101.3.4.2.1.
	SHA256 = Hash{OID: oidOf(oidSHA256), Algorithm: crypto.AlgSHA256, Size: crypto.DigestSize256}

	// SHA384 is SHA-384, id-sha384, 2.16.840.1.101.3.4.2.2.
	SHA384 = Hash{OID: oidOf(oidSHA384), Algorithm: crypto.AlgSHA384, Size: crypto.DigestSize384}

	// SHA512 is SHA-512, id-sha512, 2.16.840.1.101.3.4.2.3.
	SHA512 = Hash{OID: oidOf(oidSHA512), Algorithm: crypto.AlgSHA512, Size: crypto.DigestSize512}
)

// matches reports whether imprint is a digest of h: h has an OID, and the
// size of imprint is h.Size.
func (h Hash) matches(imprint crypto.Digest) bool {
	var buf [64]byte

	return len(appendOID(buf[:0], h.OID)) > 0 && imprint.Size() == h.Size
}

// names reports whether a, the hashAlgorithm of a message imprint, is h:
// the OID of h, with no parameters or NULL parameters.
func (h Hash) names(a algorithmIdentifier) bool {
	var buf [64]byte

	return bytes.Equal(a.oid, appendOID(buf[:0], h.OID)) && a.noParams()
}

// appendOID appends the content of oid in DER to dst, and returns the
// extended slice. The content of the zero OID is empty.
func appendOID(dst []byte, oid x509.OID) []byte {
	b, _ := oid.AppendBinary(dst)

	return b
}

// oidOf returns the OID whose content in DER is content, a valid
// identifier of this package.
func oidOf(content []byte) x509.OID {
	var oid x509.OID
	_ = oid.UnmarshalBinary(content)

	return oid
}
