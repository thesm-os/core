// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math"
	"math/big"
	"slices"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

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

// imprints are the hashes of the requests that the encoding property
// draws, each with an imprint of its size.
var imprints = []struct {
	hash    tsp.Hash
	imprint crypto.Digest
}{
	{hash: tsp.SHA256, imprint: imprint},
	{hash: tsp.SHA512, imprint: imprint512},
}

// timeStampReq is a TimeStampReq, RFC 3161 section 2.4.1, as encoding/asn1
// decodes it. The property decodes the requests of AppendRequest with it
// and encodes the result again, so a second encoder checks every octet.
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

func TestRequest(t *testing.T) {
	t.Parallel()

	t.Run("AppendRequest", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the one DER encoding of the TimeStampReq", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendRequest must encode each field of the request in DER", func(c *prop.Case) {
				p := c.Draw(prop.SampledFrom(imprints...), "hash")
				policy := c.Draw(prop.SampledFrom(x509.OID{}, policyID, longPolicy), "policy")
				n := c.Draw(prop.Integer[uint64](0, math.MaxUint64), "nonce")

				req, err := tsp.AppendRequest(nil, p.hash, p.imprint, n, policy)
				assert.NoError(c, err, "AppendRequest must encode the request")

				var got timeStampReq
				rest, err := asn1.Unmarshal(req, &got)
				assert.NoError(c, err, "the request must be a TimeStampReq")
				assert.Empty(c, rest, "the request must have no octets after the TimeStampReq")
				assert.Equal(c, got.Version, 1, "the version must be 1")
				assert.Equal(c, got.MessageImprint.HashAlgorithm.Algorithm, asn1OID(c, p.hash.OID),
					"the hashAlgorithm must be the OID of the Hash")
				assert.Empty(c, got.MessageImprint.HashAlgorithm.Parameters.FullBytes,
					"the hashAlgorithm must have no parameters")
				assert.Equal(c, got.MessageImprint.HashedMessage, p.imprint.Bytes(),
					"the hashedMessage must be the imprint")
				assert.Equal(c, got.ReqPolicy, asn1OID(c, policy), "the reqPolicy must be the policy")
				assert.NotNil(c, got.Nonce, "the request must have a nonce")
				assert.Equal(c, got.Nonce.Uint64(), n, "the nonce must be the nonce of the caller")
				assert.True(c, got.CertReq, "certReq must be TRUE")
				assert.Empty(c, got.Extensions, "the request must have no extensions")

				assert.RoundTrip(c, func(b []byte) (timeStampReq, error) {
					var v timeStampReq
					_, err := asn1.Unmarshal(b, &v)

					return v, err
				}, func(v timeStampReq) ([]byte, error) { return asn1.Marshal(v) }, req,
					"the request must be the one DER encoding of its value")
			})
		})

		t.Run("appends the request to dst", func(t *testing.T) {
			t.Parallel()
			want := request(t)
			prop.ForAll(t, "AppendRequest must keep dst and append the request", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(16)), "dst")
				prefix := slices.Clone(dst)

				got, err := tsp.AppendRequest(dst, tsp.SHA256, imprint, nonce, x509.OID{})
				assert.NoError(c, err, "AppendRequest must encode the request")
				assert.Equal(c, got, slices.Concat(prefix, want), "the request must follow the octets of dst")
			})
		})

		t.Run("returns the largest request in its room", func(t *testing.T) {
			t.Parallel()
			req, err := tsp.AppendRequest(nil, tsp.SHA512, imprint512, math.MaxUint64, longPolicy)
			assert.NoError(t, err, "AppendRequest must encode the request")
			assert.Length(t, req, largestRequest, "the largest request must fill its room exactly")
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
			{
				name:    "returns ErrHash for an imprint of another size than the Hash's",
				hash:    tsp.SHA384,
				imprint: imprint,
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tsp.AppendRequest([]byte("dst"), tt.hash, tt.imprint, nonce, x509.OID{})
				expect.ErrorIs(t, err, tsp.ErrHash, "AppendRequest must refuse the imprint")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrHash must classify as Invalid")
				expect.Equal(t, got, []byte("dst"), "AppendRequest must return dst unchanged")
			})
		}
	})
}

// TestRequestAllocs checks the allocation contract of AppendRequest.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestRequestAllocs(t *testing.T) {
	t.Run("AppendRequest", func(t *testing.T) {
		dst := make([]byte, 0, largestRequest)

		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() {
			got, err = tsp.AppendRequest(dst[:0], tsp.SHA512, imprint512, math.MaxUint64, longPolicy)
		}, 0, "AppendRequest must not allocate when dst has room for the request")
		assert.NoError(t, err, "the test must measure a request that encodes")
		assert.Length(t, got, largestRequest, "the test must measure the largest request")
	})
}

// BenchmarkRequest reports the cost of AppendRequest into a slice with
// room, and fails when it allocates.
func BenchmarkRequest(b *testing.B) {
	b.Run("AppendRequest", func(b *testing.B) {
		dst := make([]byte, 0, largestRequest)

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = tsp.AppendRequest(dst[:0], tsp.SHA512, imprint512, math.MaxUint64, longPolicy)
		}

		assert.NoError(b, err, "the benchmark must measure a request that encodes")
		assert.Length(b, got, largestRequest, "the benchmark must measure the largest request")
	})
}

// asn1OID returns oid as an encoding/asn1 identifier, nil for the zero OID.
// It fails tb when oid does not convert.
func asn1OID(tb assert.TB, oid x509.OID) asn1.ObjectIdentifier {
	tb.Helper()

	content, err := oid.AppendBinary(nil)
	assert.NoError(tb, err, "the OID must encode")

	if len(content) == 0 {
		return nil
	}

	var id asn1.ObjectIdentifier
	_, err = asn1.Unmarshal(append([]byte{0x06, byte(len(content))}, content...), &id)
	assert.NoError(tb, err, "the OID must decode")

	return id
}
