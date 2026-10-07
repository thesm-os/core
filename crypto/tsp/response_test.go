// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"bytes"
	"crypto/x509"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/internal/der"
)

// parseContract is the contract of parsesResponse, which TestResponse and
// FuzzParseResponse check.
const parseContract = "ParseResponse must return a token that is a slice of the response"

// sha256Content is the content of the OID of SHA-256 in DER.
var sha256Content = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}

func TestResponse(t *testing.T) {
	t.Parallel()

	a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
	token := stamp(t, a)

	t.Run("ParseResponse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the token of a granted response", func(t *testing.T) {
			t.Parallel()
			resp, err := a.Respond(request(t))
			assert.NoError(t, err, "the authority must respond")

			got, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
			assert.NoError(t, err, "ParseResponse must return the token")
			assert.NotEmpty(t, got, "the token must have octets")
			expect.HasSuffix(t, resp, string(got), "the token must be the last element of the response")
			expect.Equal(t, got[0], byte(der.TagSequence), "the token must be a ContentInfo")
		})

		t.Run("returns the token of a response granted with modifications", func(t *testing.T) {
			t.Parallel()
			got, err := tsp.ParseResponse(response(1, token), tsp.SHA256, imprint, nonce, x509.OID{})
			assert.NoError(t, err, "ParseResponse must accept grantedWithMods")
			assert.Equal(t, got, token, "ParseResponse must return the token")
		})

		t.Run("returns the token of the requested policy", func(t *testing.T) {
			t.Parallel()
			req, err := tsp.AppendRequest(nil, tsp.SHA256, imprint, nonce, policyID)
			assert.NoError(t, err, "AppendRequest must encode the request")
			resp, err := a.Respond(req)
			assert.NoError(t, err, "the authority must respond")

			_, err = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, policyID)
			assert.NoError(t, err, "ParseResponse must accept the requested policy")
		})

		t.Run("returns a token that is a slice of the response", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesResponse)
		})

		statuses := []struct {
			name     string
			resp     []byte
			status   int
			failInfo uint32
			text     string
			class    errs.Class
		}{
			{
				name: "returns a StatusError of the status of a rejection",
				resp: tsptest.StatusResponse(
					tsp.StatusRejection,
					[]int{tsp.FailUnacceptedPolicy},
					"the policy is not supported",
				),
				status:   tsp.StatusRejection,
				failInfo: 1 << tsp.FailUnacceptedPolicy,
				text:     "the policy is not supported",
				class:    errs.Invalid,
			},
			{
				name:   "returns a StatusError that joins the texts of the statusString",
				resp:   tsptest.StatusResponse(tsp.StatusRejection, nil, "first", "second"),
				status: tsp.StatusRejection,
				text:   "first; second",
				class:  errs.Invalid,
			},
			{
				name:   "returns a StatusError of a response that has only a status",
				resp:   tsptest.StatusResponse(tsp.StatusWaiting, nil),
				status: tsp.StatusWaiting,
				class:  errs.Transient,
			},
			{
				name:     "returns a StatusError with the failure bits below 32",
				resp:     tsptest.StatusResponse(tsp.StatusRejection, []int{tsp.FailBadAlg, 31, 32, 40}),
				status:   tsp.StatusRejection,
				failInfo: 1<<tsp.FailBadAlg | 1<<31,
				class:    errs.Invalid,
			},
			{
				name:   "returns a StatusError for the largest status",
				resp:   tsptest.StatusResponse(math.MaxInt32, nil),
				status: math.MaxInt32,
				class:  errs.Unspecified,
			},
		}
		for _, tt := range statuses {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tsp.ParseResponse(tt.resp, tsp.SHA256, imprint, nonce, x509.OID{})
				se := assert.ErrorAs[*tsp.StatusError](t, err, "ParseResponse must return a StatusError")
				expect.Equal(t, se.Status, tt.status, "Status must be the PKIStatus")
				expect.Equal(t, se.FailInfo, tt.failInfo, "FailInfo must contain the bits of failInfo")
				expect.Equal(t, se.Text, tt.text, "Text must be the statusString")
				expect.Equal(t, errs.Classify(err), tt.class, "the StatusError must classify by its status")
			})
		}

		statusInfo := element(der.TagSequence, element(der.TagInteger, []byte{0}))
		malformed := []struct {
			name string
			resp []byte
		}{
			{name: "returns ErrMalformed for a response that is not a SEQUENCE", resp: element(der.TagSet)},
			{name: "returns ErrMalformed for octets after the response", resp: append(response(0, token), 0x05, 0x00)},
			{name: "returns ErrMalformed for a response without a status", resp: element(der.TagSequence)},
			{
				name: "returns ErrMalformed for a status without its INTEGER",
				resp: element(der.TagSequence, element(der.TagSequence), token),
			},
			{
				name: "returns ErrMalformed for a negative status",
				resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{0xff})), token),
			},
			{
				name: "returns ErrMalformed for a status above the largest int32",
				resp: tsptest.StatusResponse(math.MaxInt32+1, nil),
			},
			{
				name: "returns ErrMalformed for a statusString element that is not a UTF8String",
				resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{0}),
					element(der.TagSequence, element(der.TagOctetString, []byte("text")))), token),
			},
			{
				name: "returns ErrMalformed for a statusString that is not UTF-8",
				resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{0}),
					element(der.TagSequence, element(der.TagUTF8String, []byte{0xff}))), token),
			},
			{
				name: "returns ErrMalformed for a failInfo that is not a BIT STRING in DER",
				resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{0}),
					element(der.TagBitString, []byte{0x01, 0x01})), token),
			},
			{
				name: "returns ErrMalformed for an element after the failInfo",
				resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{0}),
					element(der.TagBitString, []byte{0x00}), element(der.TagNull)), token),
			},
			{
				name: "returns ErrMalformed for a granted response without a token",
				resp: element(der.TagSequence, statusInfo),
			},
			{
				name: "returns ErrMalformed for an element after the token",
				resp: element(der.TagSequence, statusInfo, token, element(der.TagNull)),
			},
			{
				name: "returns ErrMalformed for a token that is not a SignedData",
				resp: response(0, element(der.TagSequence)),
			},
			{
				name: "returns ErrMalformed for a token whose TSTInfo does not parse",
				resp: response(0, mustToken(t, a, tsptest.Spec{TSTInfo: element(der.TagSequence)})),
			},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tsp.ParseResponse(tt.resp, tsp.SHA256, imprint, nonce, x509.OID{})
				expect.ErrorIs(t, err, tsp.ErrMalformed, "ParseResponse must refuse the response")
				expect.Equal(t, errs.Classify(err), errs.Integrity, "ErrMalformed must classify as Integrity")
			})
		}

		// A TimeStampReq without a nonce, and one whose hashAlgorithm has
		// parameters other than NULL, which AppendRequest does not build. The
		// authority copies the messageImprint of a request into its token.
		withoutNonce := element(der.TagSequence, element(der.TagInteger, []byte{1}),
			element(der.TagSequence, element(der.TagSequence, element(der.TagOID, sha256Content)),
				element(der.TagOctetString, imprint.Bytes())))
		withParams := element(der.TagSequence, element(der.TagInteger, []byte{1}),
			element(der.TagSequence,
				element(der.TagSequence, element(der.TagOID, sha256Content), element(der.TagSequence)),
				element(der.TagOctetString, imprint.Bytes())),
			element(der.TagInteger, []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}))
		other := crypto.NewDigest256([32]byte{1})
		tests := []struct {
			name    string
			resp    []byte
			hash    tsp.Hash
			imprint crypto.Digest
			nonce   uint64
			policy  x509.OID
			want    error
		}{
			{
				name:    "returns ErrImprint for another digest",
				resp:    response(0, token),
				hash:    tsp.SHA256,
				imprint: other,
				nonce:   nonce,
				want:    tsp.ErrImprint,
			},
			{
				name:    "returns ErrImprint for another hash",
				resp:    response(0, token),
				hash:    tsp.Hash{OID: tsp.SHA384.OID, Size: crypto.DigestSize256},
				imprint: imprint,
				nonce:   nonce,
				want:    tsp.ErrImprint,
			},
			{
				name:    "returns ErrImprint for a hash with parameters other than NULL",
				resp:    response(0, mustToken(t, a, tsptest.Spec{TSTInfo: mustTSTInfo(t, a, withParams)})),
				hash:    tsp.SHA256,
				imprint: imprint,
				nonce:   nonce,
				want:    tsp.ErrImprint,
			},
			{
				name:    "returns ErrNonce for another nonce",
				resp:    response(0, token),
				hash:    tsp.SHA256,
				imprint: imprint,
				nonce:   nonce + 1,
				want:    tsp.ErrNonce,
			},
			{
				name:    "returns ErrNonce for a token without a nonce",
				resp:    response(0, mustToken(t, a, tsptest.Spec{TSTInfo: mustTSTInfo(t, a, withoutNonce)})),
				hash:    tsp.SHA256,
				imprint: imprint,
				want:    tsp.ErrNonce,
			},
			{
				name:    "returns ErrPolicy for a token under another policy than the requested one",
				resp:    response(0, token),
				hash:    tsp.SHA256,
				imprint: imprint,
				nonce:   nonce,
				policy:  otherID,
				want:    tsp.ErrPolicy,
			},
			{
				name:    "returns ErrHash for a Hash without an OID",
				resp:    response(0, token),
				hash:    tsp.Hash{Size: crypto.DigestSize256},
				imprint: imprint,
				want:    tsp.ErrHash,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tsp.ParseResponse(tt.resp, tt.hash, tt.imprint, tt.nonce, tt.policy)
				assert.ErrorIs(t, err, tt.want, "ParseResponse must refuse the token")
			})
		}
	})
}

// TestResponseAllocs checks the allocation contract of ParseResponse.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestResponseAllocs(t *testing.T) {
	a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
	resp, err := a.Respond(request(t))
	assert.NoError(t, err, "the authority must respond")

	t.Run("ParseResponse", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, err = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{}) }, 0,
			"ParseResponse must not allocate for a response that grants a token")
		assert.NoError(t, err, "the test must measure a response that grants a token")
		assert.NotEmpty(t, got, "the test must measure the token of the response")
	})
}

// BenchmarkResponse reports the cost of ParseResponse for a response that
// grants a token, and fails when it allocates.
func BenchmarkResponse(b *testing.B) {
	b.Run("ParseResponse", func(b *testing.B) {
		a := newAuthority(b, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
		resp, err := a.Respond(request(b))
		assert.NoError(b, err, "the authority must respond")

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
		}

		assert.NoError(b, err, "the benchmark must measure a response that grants a token")
		assert.NotEmpty(b, got, "the benchmark must measure the token of the response")
	})
}

// FuzzParseResponse checks the contract of parsesResponse on the inputs
// that a fuzzer finds. The seeds are a granted response and a rejection,
// each after the two octets of its length that the bridge reads first.
func FuzzParseResponse(f *testing.F) {
	a := newAuthority(f, tsptest.Config{
		Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Accuracy: 1500 * time.Microsecond,
	})
	resp, err := a.Respond(request(f))
	assert.NoError(f, err, "the authority must respond")

	for _, seed := range [][]byte{
		resp,
		tsptest.StatusResponse(tsp.StatusRejection, []int{tsp.FailBadAlg, tsp.FailSystemFailure}, "text"),
	} {
		f.Add(append(binary.LittleEndian.AppendUint16(nil, uint16(len(seed))), seed...))
	}

	prop.Fuzz(f, parseContract, parsesResponse)
}

// parsesResponse checks that ParseResponse refuses a drawn response, or
// returns a token that is a slice of it.
func parsesResponse(c *prop.Case) {
	resp := c.Draw(prop.Bytes(), "response")

	got, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
	if err == nil {
		assert.True(c, bytes.Contains(resp, got), "the token must be a slice of the response")
	}
}

// response returns the DER of a TimeStampResp of status with the element
// token.
func response(status uint64, token []byte) []byte {
	b := der.NewBuilder(nil)
	resp := b.Open(der.TagSequence)
	info := b.Open(der.TagSequence)
	b.AddUint64(status)
	b.Close(info)
	b.AddElement(token)
	b.Close(resp)

	return b.Bytes()
}

// mustTSTInfo returns the TSTInfo that a signs for req, and fails tb when a
// refuses req.
func mustTSTInfo(tb assert.TB, a *tsptest.Authority, req []byte) []byte {
	tb.Helper()

	info, err := a.TSTInfo(req)
	assert.NoError(tb, err, "the authority must accept the request")

	return info
}

// mustToken returns the token of s, and fails tb when a refuses s.
func mustToken(tb assert.TB, a *tsptest.Authority, s tsptest.Spec) []byte {
	tb.Helper()

	token, err := a.Token(s)
	assert.NoError(tb, err, "the authority must build the token")

	return token
}

// element returns the DER of the element of tag whose content is the
// concatenation of contents.
func element(tag der.Tag, contents ...[]byte) []byte {
	b := der.NewBuilder(nil)
	b.Add(tag, bytes.Join(contents, nil))

	return b.Bytes()
}
