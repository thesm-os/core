// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"bytes"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math"
	"math/big"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
)

// largestRequest is the size of a request for a hash OID of 9 octets, as
// the OIDs of SHA-2 are, an imprint of 64 octets, a policy of 64 octets and
// a nonce of 9 octets in DER: the room that AppendRequest needs to
// allocate nothing.
const largestRequest = 167

// imprint512 is a digest of SHA-512, the largest imprint.
var imprint512 = crypto.NewDigest512(sha512.Sum512([]byte("the checkpoint")))

// longPolicy is a policy whose OID has 64 octets of content: 1.3 and 63
// arcs of 1.
var longPolicy = mustOID("1.3" + strings.Repeat(".1", 63))

// timeStampReq is a TimeStampReq, RFC 3161 section 2.4.1, as encoding/asn1
// decodes it. The cases decode the requests of AppendRequest with it and
// encode the result again, so a second encoder checks every octet.
type timeStampReq struct {
	Version        int
	MessageImprint messageImprint
	ReqPolicy      asn1.ObjectIdentifier `asn1:"optional"`
	Nonce          *big.Int              `asn1:"optional"`
	CertReq        bool                  `asn1:"optional"`
	Extensions     []pkix.Extension      `asn1:"optional,tag:0"`
}

// messageImprint is a MessageImprint, RFC 3161 section 2.4.1.
type messageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

func TestAppendRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hash    tsp.Hash
		imprint crypto.Digest
		nonce   uint64
		policy  x509.OID
	}{
		{name: "encodes a request without a policy for the zero OID", hash: tsp.SHA256, imprint: imprint, nonce: nonce},
		{name: "encodes the policy of a request", hash: tsp.SHA256, imprint: imprint, nonce: nonce, policy: policyID},
		{name: "encodes a nonce of zero", hash: tsp.SHA256, imprint: imprint},
		{name: "encodes a nonce whose top bit is set", hash: tsp.SHA512, imprint: imprint512, nonce: math.MaxUint64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := tsp.AppendRequest(nil, tt.hash, tt.imprint, tt.nonce, tt.policy)
			testkit.NoError(t, err, "AppendRequest must encode the request")

			var got timeStampReq
			rest, err := asn1.Unmarshal(req, &got)
			testkit.NoError(t, err, "the request must be a TimeStampReq")
			testkit.Len(t, rest, 0, "the request must have no octets after the TimeStampReq")
			testkit.Equal(t, got.Version, 1, "the version must be 1")
			testkit.True(t, got.MessageImprint.HashAlgorithm.Algorithm.Equal(asn1OID(t, tt.hash.OID)),
				"the hashAlgorithm must be the Hash's OID")
			testkit.Len(t, got.MessageImprint.HashAlgorithm.Parameters.FullBytes, 0,
				"the hashAlgorithm must have no parameters")
			testkit.Equal(
				t,
				got.MessageImprint.HashedMessage,
				tt.imprint.Bytes(),
				"the hashedMessage must be the imprint",
			)
			testkit.True(t, got.ReqPolicy.Equal(asn1OID(t, tt.policy)), "the reqPolicy must be the policy")
			testkit.Equal(t, got.Nonce.Uint64(), tt.nonce, "the nonce must be the caller's")
			testkit.True(t, got.CertReq, "certReq must be TRUE")
			testkit.Len(t, got.Extensions, 0, "the request must have no extensions")

			again, err := asn1.Marshal(got)
			testkit.NoError(t, err, "the decoded request must encode")
			testkit.Equal(t, req, again, "the request must be the one DER encoding of its value")
		})
	}

	t.Run("appends the request to dst", func(t *testing.T) {
		t.Parallel()
		prefix := []byte("prefix")
		req, err := tsp.AppendRequest(bytes.Clone(prefix), tsp.SHA256, imprint, nonce, x509.OID{})
		testkit.NoError(t, err, "AppendRequest must encode the request")
		testkit.True(t, bytes.HasPrefix(req, prefix), "the request must follow the octets of dst")
		testkit.Equal(t, req[len(prefix):], request(t), "the octets after dst must be the request")
	})

	t.Run("encodes the largest request in its room", func(t *testing.T) {
		t.Parallel()
		req, err := tsp.AppendRequest(nil, tsp.SHA512, imprint512, math.MaxUint64, longPolicy)
		testkit.NoError(t, err, "AppendRequest must encode the request")
		testkit.Len(t, req, largestRequest, "the largest request must fill its room exactly")
	})

	refused := []struct {
		name    string
		hash    tsp.Hash
		imprint crypto.Digest
	}{
		{
			name:    "returns ErrHash for a Hash without an OID",
			hash:    tsp.Hash{Size: crypto.DigestSize256},
			imprint: imprint,
		},
		{name: "returns ErrHash for an imprint of another size than the Hash's", hash: tsp.SHA384, imprint: imprint},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dst := []byte("dst")
			got, err := tsp.AppendRequest(dst, tt.hash, tt.imprint, nonce, x509.OID{})
			testkit.ErrorIs(t, err, tsp.ErrHash, "AppendRequest must refuse the imprint")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrHash must classify as Invalid")
			testkit.Equal(t, got, dst, "AppendRequest must return dst unchanged")
		})
	}
}

func BenchmarkAppendRequest(b *testing.B) {
	dst := make([]byte, 0, largestRequest)
	tspAllocs(b, 0, func() {
		sinkBytes, errSink = tsp.AppendRequest(dst[:0], tsp.SHA512, imprint512, math.MaxUint64, longPolicy)
	})
	testkit.NoError(b, errSink, "the benchmark must measure a request that encodes")
	testkit.Len(b, sinkBytes, largestRequest, "the benchmark must measure the largest request")
}

// asn1OID returns oid as an encoding/asn1 identifier, nil for the zero OID.
func asn1OID(tb testing.TB, oid x509.OID) asn1.ObjectIdentifier {
	tb.Helper()

	content, err := oid.AppendBinary(nil)
	testkit.NoError(tb, err, "the OID must encode")

	if len(content) == 0 {
		return nil
	}

	var id asn1.ObjectIdentifier
	_, err = asn1.Unmarshal(append([]byte{0x06, byte(len(content))}, content...), &id)
	testkit.NoError(tb, err, "the OID must decode")

	return id
}
