// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"bytes"
	"crypto/x509"
	"math"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/internal/der"
)

// sha256Content is the content of the OID of SHA-256 in DER.
var sha256Content = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}

func TestParseResponse(t *testing.T) {
	t.Parallel()

	a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
	token := stamp(t, a)

	t.Run("returns the token of a granted response", func(t *testing.T) {
		t.Parallel()
		resp, err := a.Respond(request(t))
		testkit.NoError(t, err, "the authority must respond")

		got, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
		testkit.NoError(t, err, "ParseResponse must return the token")
		testkit.True(t, bytes.HasSuffix(resp, got), "the token must be the last element of the response")
		testkit.Equal(t, got[0], byte(der.TagSequence), "the token must be a ContentInfo")
	})

	t.Run("returns the token of a response granted with modifications", func(t *testing.T) {
		t.Parallel()
		got, err := tsp.ParseResponse(response(1, token), tsp.SHA256, imprint, nonce, x509.OID{})
		testkit.NoError(t, err, "ParseResponse must accept grantedWithMods")
		testkit.Equal(t, got, token, "ParseResponse must return the token")
	})

	t.Run("returns the token of the requested policy", func(t *testing.T) {
		t.Parallel()
		req, err := tsp.AppendRequest(nil, tsp.SHA256, imprint, nonce, policyID)
		testkit.NoError(t, err, "AppendRequest must encode the request")
		resp, err := a.Respond(req)
		testkit.NoError(t, err, "the authority must respond")

		_, err = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, policyID)
		testkit.NoError(t, err, "ParseResponse must accept the requested policy")
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
			name: "returns a StatusError with the status, the failure bits and the text of a rejection",
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
			name:   "returns a StatusError without text or failure bits",
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
			se := testkit.ErrorAs[*tsp.StatusError](t, err, "ParseResponse must return a StatusError")
			testkit.Equal(t, se.Status, tt.status, "Status must be the PKIStatus")
			testkit.Equal(t, se.FailInfo, tt.failInfo, "FailInfo must hold the bits of failInfo")
			testkit.Equal(t, se.Text, tt.text, "Text must be the statusString")
			testkit.Equal(t, errs.Classify(err), tt.class, "the StatusError must classify by its status")
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
			resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{2}),
				element(der.TagSequence, element(der.TagOctetString, []byte("text"))))),
		},
		{
			name: "returns ErrMalformed for a statusString that is not UTF-8",
			resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{2}),
				element(der.TagSequence, element(der.TagUTF8String, []byte{0xff})))),
		},
		{
			name: "returns ErrMalformed for a failInfo that is not a BIT STRING in DER",
			resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{2}),
				element(der.TagBitString, []byte{0x01, 0x01}))),
		},
		{
			name: "returns ErrMalformed for an element after the failInfo",
			resp: element(der.TagSequence, element(der.TagSequence, element(der.TagInteger, []byte{2}),
				element(der.TagBitString, []byte{0x00}), element(der.TagNull))),
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
			testkit.ErrorIs(t, err, tsp.ErrMalformed, "ParseResponse must refuse the response")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "ErrMalformed must classify as Integrity")
		})
	}

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
			name:    "returns ErrNonce for another nonce",
			resp:    response(0, token),
			hash:    tsp.SHA256,
			imprint: imprint,
			nonce:   nonce + 1,
			want:    tsp.ErrNonce,
		},
		{
			name:    "returns ErrNonce for a token without a nonce",
			resp:    response(0, mustToken(t, a, tsptest.Spec{TSTInfo: mustTSTInfo(t, a, requestWithoutNonce())})),
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
			testkit.ErrorIs(t, err, tt.want, "ParseResponse must refuse the token")
		})
	}
}

func BenchmarkParseResponse(b *testing.B) {
	a := newAuthority(b, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
	resp, err := a.Respond(request(b))
	testkit.NoError(b, err, "the authority must respond")

	tspAllocs(b, 0, func() { sinkBytes, errSink = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{}) })
	testkit.NoError(b, errSink, "the benchmark must measure a response that grants a token")
}

func FuzzParseResponse(f *testing.F) {
	a := newAuthority(f, tsptest.Config{
		Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Accuracy: 1500 * time.Microsecond,
	})
	resp, err := a.Respond(request(f))
	testkit.NoError(f, err, "the authority must respond")

	f.Add(resp)
	f.Add(tsptest.StatusResponse(tsp.StatusRejection, []int{tsp.FailBadAlg, tsp.FailSystemFailure}, "text"))
	f.Fuzz(func(t *testing.T, resp []byte) {
		token, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
		if err == nil {
			testkit.True(t, bytes.Contains(resp, token), "the token must be a slice of the response")
		}
	})
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

// requestWithoutNonce returns the DER of a TimeStampReq of imprint without a
// nonce, which AppendRequest does not build.
func requestWithoutNonce() []byte {
	return element(der.TagSequence, element(der.TagInteger, []byte{1}),
		element(der.TagSequence, element(der.TagSequence, element(der.TagOID, sha256Content)),
			element(der.TagOctetString, imprint.Bytes())))
}

// mustTSTInfo returns the TSTInfo that a signs for req, and fails tb when a
// refuses req.
func mustTSTInfo(tb testing.TB, a *tsptest.Authority, req []byte) []byte {
	tb.Helper()

	info, err := a.TSTInfo(req)
	testkit.NoError(tb, err, "the authority must accept the request")

	return info
}

// mustToken returns the token of s, and fails tb when a refuses s.
func mustToken(tb testing.TB, a *tsptest.Authority, s tsptest.Spec) []byte {
	tb.Helper()

	token, err := a.Token(s)
	testkit.NoError(tb, err, "the authority must build the token")

	return token
}

// element returns the DER of the element of tag whose content is the
// concatenation of contents.
func element(tag der.Tag, contents ...[]byte) []byte {
	b := der.NewBuilder(nil)
	b.Add(tag, bytes.Join(contents, nil))

	return b.Bytes()
}
