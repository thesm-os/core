// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest_test

import (
	"crypto/sha256"
	"crypto/x509"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/internal/der"
)

// nonce is the nonce of the requests of the cases.
const nonce = 42

// origin is the time of the fake clock of the cases.
var origin = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// policyID is the policy of the authorities of the cases, and otherID a
// policy that none of them has. x509.OIDFromInts fails only for a first
// arc above 2 or a second arc above 39 under a first arc below 2, which
// these literals do not have.
var (
	policyID, _ = x509.OIDFromInts([]uint64{1, 3, 6, 1, 4, 1, 99999, 1})
	otherID, _  = x509.OIDFromInts([]uint64{1, 3, 6, 1, 4, 1, 99999, 2})
)

// imprint is the digest that the cases time-stamp.
var imprint = crypto.NewDigest256(sha256.Sum256([]byte("the checkpoint")))

func TestAuthority(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		valid := config()
		tests := []struct {
			name string
			edit func(*tsptest.Config)
		}{
			{name: "returns ErrConfig for a Config without a Clock", edit: func(c *tsptest.Config) { c.Clock = nil }},
			{
				name: "returns ErrConfig for a Config without a Policy",
				edit: func(c *tsptest.Config) { c.Policy = x509.OID{} },
			},
			{name: "returns ErrConfig for a Key that is not Valid", edit: func(c *tsptest.Config) { c.Key = 0 }},
			{name: "returns ErrConfig for a Digest that is not Valid", edit: func(c *tsptest.Config) { c.Digest = 4 }},
			{name: "returns ErrConfig for an ESS that is not Valid", edit: func(c *tsptest.Config) { c.ESS = 4 }},
			{name: "returns ErrConfig for a Usage that is not Valid", edit: func(c *tsptest.Config) { c.Usage = 4 }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := valid
				tt.edit(&cfg)

				_, err := tsptest.New(cfg)
				testkit.ErrorIs(t, err, tsptest.ErrConfig, "New must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns an Authority whose chain ends at its Roots", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())

			intermediates := x509.NewCertPool()
			intermediates.AddCert(a.Intermediate())
			_, err := a.Leaf().Verify(x509.VerifyOptions{
				Roots: a.Roots(), Intermediates: intermediates, CurrentTime: origin,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
			})
			testkit.NoError(t, err, "the authority's certificate must chain to the root for time stamping")
		})

		t.Run("returns a certificate valid from an hour before the clock's time", func(t *testing.T) {
			t.Parallel()
			cfg := config()
			cfg.Validity = 2 * time.Hour
			a := authority(t, cfg)

			testkit.True(t, a.Leaf().NotBefore.Equal(origin.Add(-time.Hour)), "the validity must start an hour before")
			testkit.True(t, a.Leaf().NotAfter.Equal(origin.Add(2*time.Hour)), "the validity must last the Config's")
		})
	})

	t.Run("Respond", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a token that a Verifier of its Roots accepts", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())

			info, err := verifier(t, a).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "the token must verify")
			testkit.True(t, info.Time.Time.Equal(origin), "genTime must be the clock's time")
		})

		t.Run("returns tokens of consecutive serial numbers", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())
			v := verifier(t, a)

			for _, want := range []byte{1, 2} {
				info, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
				testkit.NoError(t, err, "the token must verify")
				testkit.Equal(t, info.Serial, []byte{want}, "the serial number must count the tokens")
			}
		})

		t.Run("returns a token without certificates for a request without certReq", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())

			resp, err := a.Respond(rawRequest(false, false))
			testkit.NoError(t, err, "the authority must respond")
			tok, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
			testkit.NoError(t, err, "the response must grant a token")

			_, err = verifier(t, a).Verify(tok, tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrCertificate, "a token without certificates must not verify")
		})

		rejections := []struct {
			name string
			req  func(tb testing.TB) []byte
			bit  int
		}{
			{
				name: "returns the rejection unacceptedPolicy for another policy",
				req: func(tb testing.TB) []byte {
					tb.Helper()
					req, err := tsp.AppendRequest(nil, tsp.SHA256, imprint, nonce, otherID)
					testkit.NoError(tb, err, "AppendRequest must encode the request")

					return req
				},
				bit: tsp.FailUnacceptedPolicy,
			},
			{
				name: "returns the rejection unacceptedExtension for a request with extensions",
				req:  func(testing.TB) []byte { return rawRequest(true, true) },
				bit:  tsp.FailUnacceptedExtension,
			},
		}
		for _, tt := range rejections {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				resp, err := authority(t, config()).Respond(tt.req(t))
				testkit.NoError(t, err, "the authority must respond")

				_, err = tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
				se := testkit.ErrorAs[*tsp.StatusError](t, err, "the response must grant no token")
				testkit.Equal(t, se.Status, tsp.StatusRejection, "the status must be rejection")
				testkit.Equal(t, se.FailInfo, uint32(1)<<tt.bit, "the failure bit must name the reason")
			})
		}

		t.Run("returns ErrRequest for a request that is not DER", func(t *testing.T) {
			t.Parallel()
			_, err := authority(t, config()).Respond([]byte{0x30, 0x81, 0x01, 0x00})
			testkit.ErrorIs(t, err, tsptest.ErrRequest, "Respond must refuse the request")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrRequest must classify as Invalid")
		})
	})

	t.Run("TSTInfo", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the TSTInfo that a token of Token carries", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())
			tst, err := a.TSTInfo(request(t))
			testkit.NoError(t, err, "TSTInfo must accept the request")
			tok, err := a.Token(tsptest.Spec{TSTInfo: tst})
			testkit.NoError(t, err, "Token must build the token")

			_, err = verifier(t, a).Verify(tok, tsp.SHA256, imprint)
			testkit.NoError(t, err, "the token must verify")
		})

		t.Run("returns ErrRequest for a request without a messageImprint", func(t *testing.T) {
			t.Parallel()
			b := der.NewBuilder(nil)
			seq := b.Open(der.TagSequence)
			b.AddUint64(1)
			b.Close(seq)

			_, err := authority(t, config()).TSTInfo(b.Bytes())
			testkit.ErrorIs(t, err, tsptest.ErrRequest, "TSTInfo must refuse the request")
		})
	})
}

// config returns a valid Config of Ed25519 and SHA-512, with the fake clock
// at origin and policyID.
func config() tsptest.Config {
	return tsptest.Config{
		Clock: fake.New(origin), Policy: policyID, Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
	}
}

// authority returns the Authority of cfg, and fails tb when New refuses
// cfg.
func authority(tb testing.TB, cfg tsptest.Config) *tsptest.Authority {
	tb.Helper()

	a, err := tsptest.New(cfg)
	testkit.NoError(tb, err, "New must accept the configuration")

	return a
}

// verifier returns a Verifier of a's roots that accepts policyID, and
// fails tb when NewVerifier refuses it.
func verifier(tb testing.TB, a *tsptest.Authority) *tsp.Verifier {
	tb.Helper()

	v, err := tsp.NewVerifier(tsp.VerifierConfig{
		Roots: a.Roots(), Policies: []tsp.Policy{{ID: policyID, Accuracy: time.Second}},
	})
	testkit.NoError(tb, err, "NewVerifier must accept the configuration")

	return v
}

// request returns the DER of a TimeStampReq of imprint and nonce, and
// fails tb when AppendRequest refuses it.
func request(tb testing.TB) []byte {
	tb.Helper()

	req, err := tsp.AppendRequest(nil, tsp.SHA256, imprint, nonce, x509.OID{})
	testkit.NoError(tb, err, "AppendRequest must encode the request")

	return req
}

// stamp returns the token of a for the request of request, and fails tb
// when the response grants none.
func stamp(tb testing.TB, a *tsptest.Authority) []byte {
	tb.Helper()

	resp, err := a.Respond(request(tb))
	testkit.NoError(tb, err, "the authority must respond")

	tok, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
	testkit.NoError(tb, err, "the response must grant a token")

	return tok
}

// rawRequest returns the DER of a TimeStampReq of imprint and nonce that
// AppendRequest does not build: with certReq when certReq is set, and with
// an empty extensions field when extensions is set.
func rawRequest(certReq, extensions bool) []byte {
	b := der.NewBuilder(nil)
	req := b.Open(der.TagSequence)
	b.AddUint64(1)
	messageImprint := b.Open(der.TagSequence)
	hashAlgorithm := b.Open(der.TagSequence)
	b.Add(der.TagOID, []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01})
	b.Close(hashAlgorithm)
	b.Add(der.TagOctetString, imprint.Bytes())
	b.Close(messageImprint)
	b.AddUint64(nonce)

	if certReq {
		b.AddBoolean(true)
	}

	if extensions {
		b.Close(b.Open(der.ContextConstructed(0)))
	}

	b.Close(req)

	return b.Bytes()
}
