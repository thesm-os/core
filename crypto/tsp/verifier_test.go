// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"bytes"
	"crypto/fips140"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
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

// benchRuns is the number of calls over which a benchmark averages the
// allocations that it checks.
const benchRuns = 100

// nonce is the nonce of the requests of the cases.
const nonce = 0x0123456789abcdef

// childTimeout bounds the child process of TestFIPSOnlyMode, which the
// parent's context ends too.
const childTimeout = 60 * time.Second

// origin is the time of the fake clock of every case.
var origin = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// Sinks keep the results of the benchmarks alive.
var (
	sinkInfo  tsp.Info
	sinkBytes []byte
	errSink   error
)

// policyID is the policy of the authorities of the cases, and otherID a
// policy that no authority uses.
var (
	policyID = mustOID("1.3.6.1.4.1.99999.1")
	otherID  = mustOID("1.3.6.1.4.1.99999.2")
)

// imprint is the digest that the cases time-stamp.
var imprint = crypto.NewDigest256(sha256.Sum256([]byte("the checkpoint")))

// The contents of the OIDs that the cases write into tokens, in DER.
var (
	contentTypeOID          = appendOID(mustOID("1.2.840.113549.1.9.3"))
	signingCertificateOID   = appendOID(mustOID("1.2.840.113549.1.9.16.2.12"))
	signingCertificateV2OID = appendOID(mustOID("1.2.840.113549.1.9.16.2.47"))
	dataOID                 = appendOID(mustOID("1.2.840.113549.1.7.1"))
	sha1OID                 = appendOID(mustOID("1.3.14.3.2.26"))
	unknownOID              = appendOID(mustOID("1.2.3"))
)

// malformedElement is a SEQUENCE whose length is in the long form below
// 128, which DER forbids.
var malformedElement = []byte{0x30, 0x81, 0x01, 0x00}

func TestVerifier(t *testing.T) {
	t.Parallel()

	t.Run("NewVerifier", func(t *testing.T) {
		t.Parallel()

		roots := x509.NewCertPool()
		tests := []struct {
			name string
			give tsp.VerifierConfig
		}{
			{
				name: "returns ErrConfig for nil Roots",
				give: tsp.VerifierConfig{Policies: []tsp.Policy{{ID: policyID, Accuracy: time.Second}}},
			},
			{name: "returns ErrConfig for no Policies", give: tsp.VerifierConfig{Roots: roots}},
			{
				name: "returns ErrConfig for a Policy without an ID",
				give: tsp.VerifierConfig{Roots: roots, Policies: []tsp.Policy{{Accuracy: time.Second}}},
			},
			{
				name: "returns ErrConfig for a Policy of zero Accuracy",
				give: tsp.VerifierConfig{Roots: roots, Policies: []tsp.Policy{{ID: policyID}}},
			},
			{
				name: "returns ErrConfig for a Policy of negative Accuracy",
				give: tsp.VerifierConfig{Roots: roots, Policies: []tsp.Policy{{ID: policyID, Accuracy: -time.Second}}},
			},
			{
				name: "returns ErrConfig for two Policies of one ID",
				give: tsp.VerifierConfig{Roots: roots, Policies: []tsp.Policy{
					{ID: policyID, Accuracy: time.Second},
					{ID: otherID, Accuracy: time.Second},
					{ID: policyID, Accuracy: 2 * time.Second},
				}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := tsp.NewVerifier(tt.give)
				testkit.ErrorIs(t, err, tsp.ErrConfig, "NewVerifier must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a Verifier for one Policy of an accuracy of 1 ns", func(t *testing.T) {
			t.Parallel()
			_, err := tsp.NewVerifier(tsp.VerifierConfig{
				Roots: roots, Policies: []tsp.Policy{{ID: policyID, Accuracy: time.Nanosecond}},
			})
			testkit.NoError(t, err, "a positive accuracy must be valid")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		keys := []struct {
			name   string
			key    tsptest.Key
			digest tsptest.Digest
		}{
			{name: "Ed25519 with SHA-512", key: tsptest.KeyEd25519, digest: tsptest.DigestSHA512},
			{name: "ECDSA P-256 with SHA-256", key: tsptest.KeyECDSAP256, digest: tsptest.DigestSHA256},
			{name: "ECDSA P-384 with SHA-384", key: tsptest.KeyECDSAP384, digest: tsptest.DigestSHA384},
			{name: "ECDSA P-384 with SHA-512", key: tsptest.KeyECDSAP384, digest: tsptest.DigestSHA512},
			{name: "RSA PKCS #1 v1.5 with SHA-256", key: tsptest.KeyRSAPKCS1, digest: tsptest.DigestSHA256},
			{name: "RSA PKCS #1 v1.5 with SHA-384", key: tsptest.KeyRSAPKCS1, digest: tsptest.DigestSHA384},
			{name: "RSA PKCS #1 v1.5 with SHA-512", key: tsptest.KeyRSAPKCS1, digest: tsptest.DigestSHA512},
			{name: "RSA PSS with SHA-256", key: tsptest.KeyRSAPSS, digest: tsptest.DigestSHA256},
			{name: "RSA PSS with SHA-512", key: tsptest.KeyRSAPSS, digest: tsptest.DigestSHA512},
			{name: "ML-DSA-44 with SHA-256", key: tsptest.KeyMLDSA44, digest: tsptest.DigestSHA256},
			{name: "ML-DSA-65 with SHA-384", key: tsptest.KeyMLDSA65, digest: tsptest.DigestSHA384},
			{name: "ML-DSA-87 with SHA-512", key: tsptest.KeyMLDSA87, digest: tsptest.DigestSHA512},
		}
		for _, k := range keys {
			t.Run("returns the TSTInfo of a token signed with "+k.name, func(t *testing.T) {
				t.Parallel()
				a := newAuthority(t, tsptest.Config{Key: k.key, Digest: k.digest})
				v := newVerifier(t, a, nil)

				info, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
				testkit.NoError(t, err, "Verify must accept the token")
				testkit.True(t, info.Time.Time.Equal(origin), "Info.Time must be genTime")
				testkit.Equal(t, info.Time.MaxError, time.Second, "a token without accuracy must take the policy's")
				testkit.True(t, info.Time.Synced, "a verified time must be synced")
				testkit.True(t, info.Policy.ID.Equal(policyID), "Info.Policy must be the accepted policy")
			})
		}

		configs := []struct {
			name string
			cfg  tsptest.Config
		}{
			{name: "verifies a token with a SigningCertificate", cfg: tsptest.Config{ESS: tsptest.ESSV1}},
			{
				name: "verifies a token with both signing-certificate attributes",
				cfg:  tsptest.Config{ESS: tsptest.ESSBoth},
			},
			{
				name: "verifies a token whose certificate identifiers have an issuerSerial",
				cfg:  tsptest.Config{ESS: tsptest.ESSBoth, IssuerSerial: true},
			},
			{
				name: "verifies a token whose signer is its subject key identifier",
				cfg:  tsptest.Config{SubjectKeyID: true},
			},
			{
				name: "verifies a token of a SigningCertificateV2 of SHA-256",
				cfg:  tsptest.Config{Digest: tsptest.DigestSHA256},
			},
		}
		for _, tt := range configs {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := tt.cfg
				cfg.Key = tsptest.KeyECDSAP256
				if cfg.Digest == 0 {
					cfg.Digest = tsptest.DigestSHA384
					cfg.Key = tsptest.KeyECDSAP384
				}

				a := newAuthority(t, cfg)
				_, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
				testkit.NoError(t, err, "Verify must accept the token")
			})
		}

		t.Run("returns the serial and the nonce of the token", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

			info, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token")
			testkit.Equal(t, info.Serial, []byte{0x01}, "Serial must be the content of the serialNumber")
			testkit.Equal(t, info.Nonce, []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
				"Nonce must be the content of the nonce")
			testkit.True(t, info.TSA == nil, "a token without a tsa must return nil")
			testkit.False(t, info.Ordering, "ordering must default to FALSE")
		})

		t.Run("returns the accuracy, the ordering and the tsa of the token", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{
				Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
				Accuracy: 2*time.Millisecond + 500*time.Microsecond, Ordering: true, TSA: true,
			})

			info, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token")
			testkit.Equal(t, info.Time.MaxError, 2*time.Millisecond+500*time.Microsecond,
				"Info.Time.MaxError must be the token's accuracy")
			testkit.True(t, info.Ordering, "Ordering must be the token's")
			testkit.Equal(t, info.TSA, element(der.ContextConstructed(4), a.Leaf().RawSubject),
				"TSA must be the directoryName of the authority")
		})

		t.Run("returns the verified chain of the authority", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

			info, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token")
			testkit.Len(t, info.Chain, 3, "the chain must run from the authority to the root")
			testkit.True(t, info.Chain[0].Equal(a.Leaf()), "the chain must start at the authority's certificate")
			testkit.True(t, info.Chain[1].Equal(a.Intermediate()), "the chain must pass the intermediate")
			testkit.NoError(t, info.Chain[2].CheckSignatureFrom(info.Chain[2]),
				"the chain must end at the self-signed root")
		})

		t.Run("returns the chain that the Verifier keeps for a known certificate", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			v := newVerifier(t, a, nil)

			first, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the first token")
			second, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the second token")

			testkit.True(t, &first.Chain[0] == &second.Chain[0],
				"two tokens of one certificate must share the chain that the Verifier keeps")
		})

		for _, k := range keys {
			name := "verifies a second token signed with " + k.name + " after the caller clears the first"
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				a := newAuthority(t, tsptest.Config{Key: k.key, Digest: k.digest})
				v := newVerifier(t, a, nil)

				// A caller with pooled buffers reuses the buffer of a token
				// once Verify returns.
				first := stamp(t, a)
				_, err := v.Verify(first, tsp.SHA256, imprint)
				testkit.NoError(t, err, "Verify must accept the first token")
				clear(first)

				info, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
				testkit.NoError(t, err, "Verify must accept the second token")
				testkit.True(
					t,
					info.Chain[0].Equal(a.Leaf()),
					"the kept chain must start at the authority's certificate",
				)
				testkit.True(t, info.Chain[1].Equal(a.Intermediate()), "the kept chain must pass the intermediate")
			})
		}

		t.Run("returns the Policy of a token under the second policy", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Policy: otherID})
			v := verifierOf(t, tsp.VerifierConfig{Roots: a.Roots(), Policies: []tsp.Policy{
				{ID: policyID, Accuracy: time.Second}, {ID: otherID, Accuracy: time.Minute},
			}})

			info, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept a token of any accepted policy")
			testkit.True(t, info.Policy.ID.Equal(otherID), "Info.Policy must be the token's policy")
			testkit.Equal(t, info.Time.MaxError, time.Minute, "the accuracy must be that of the token's policy")
		})

		t.Run("verifies a token of a known certificate again", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			v := newVerifier(t, a, nil)

			for i := range 3 {
				_, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
				testkit.NoError(t, err, "Verify must accept token "+strconv.Itoa(i))
			}
		})

		t.Run("verifies a token whose intermediate is in the configuration", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			intermediates := x509.NewCertPool()
			intermediates.AddCert(a.Intermediate())
			v := verifierOf(t, tsp.VerifierConfig{
				Roots:         a.Roots(),
				Intermediates: intermediates,
				Policies:      []tsp.Policy{{ID: policyID, Accuracy: time.Second}},
			})

			_, err := v.Verify(
				token(t, a, func(s *tsptest.Spec) { s.Certificates = [][]byte{a.Leaf().Raw} }),
				tsp.SHA256,
				imprint,
			)
			testkit.NoError(t, err, "Verify must chain through the configured intermediates")
		})

		t.Run("reads past certificates that serve no chain", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

			tok := token(t, a, func(s *tsptest.Spec) {
				s.Certificates = [][]byte{
					element(der.ContextConstructed(1)), a.Leaf().Raw, element(der.TagSequence, element(der.TagNull)),
					a.Intermediate().Raw, malformedElement,
				}
			})
			_, err := newVerifier(t, a, nil).Verify(tok, tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must read past other choices and certificates that do not parse")
		})

		t.Run("reads past certificates of other choices for a SigningCertificate", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(
				t,
				tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, ESS: tsptest.ESSV1},
			)

			tok := token(t, a, func(s *tsptest.Spec) {
				s.Certificates = [][]byte{element(der.ContextConstructed(1)), a.Leaf().Raw, a.Intermediate().Raw}
			})
			_, err := newVerifier(t, a, nil).Verify(tok, tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must find the certificate by its SHA-1 digest")
		})

		t.Run("returns ErrCertificate for a token after the expiry of the authority's certificate", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			a := newAuthority(
				t,
				tsptest.Config{Clock: clk, Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Validity: time.Hour},
			)
			clk.Advance(2 * time.Hour)

			_, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrCertificate, "a certificate must be valid at genTime")
		})

		t.Run("returns ErrCertificate for a token of a known certificate after its expiry", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			a := newAuthority(
				t,
				tsptest.Config{Clock: clk, Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Validity: time.Hour},
			)
			v := newVerifier(t, a, nil)

			_, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token within the validity")

			clk.Advance(2 * time.Hour)
			_, err = v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrCertificate, "a known chain must be valid at genTime")
		})

		t.Run("returns ErrCertificate for a token of a known certificate before its validity", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			a := newAuthority(t, tsptest.Config{Clock: clk, Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			v := newVerifier(t, a, nil)

			_, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token within the validity")

			clk.Set(origin.Add(-2 * time.Hour))
			_, err = v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrCertificate, "a known chain must be valid at genTime")
		})

		t.Run("returns ErrCertificate for a certificate without a chain to the roots", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			stranger := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

			_, err := newVerifier(t, stranger, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrCertificate, "the chain must end at a root of the Verifier")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "ErrCertificate must classify as Integrity")
		})

		refused := []struct {
			name  string
			cfg   tsptest.Config
			token func(tb testing.TB, a *tsptest.Authority) []byte
			want  error
			class errs.Class
		}{
			{
				name:  "returns ErrMalformed for a token that is not DER",
				token: func(testing.TB, *tsptest.Authority) []byte { return malformedElement },
				want:  tsp.ErrMalformed,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrMalformed for a TSTInfo that does not parse",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) { s.TSTInfo = element(der.TagSequence) }),
				want:  tsp.ErrMalformed,
				class: errs.Integrity,
			},
			{
				name: "returns ErrUnsupported for a digest algorithm that this package does not compute",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) {
					s.DigestAlgorithm = element(der.TagSequence, element(der.TagOID, sha1OID))
				}),
				want:  tsp.ErrUnsupported,
				class: errs.Unsupported,
			},
			{
				name: "returns ErrUnsupported for a signature algorithm that this package does not verify",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) {
					s.SignatureAlgorithm = element(der.TagSequence, element(der.TagOID, unknownOID))
				}),
				want:  tsp.ErrUnsupported,
				class: errs.Unsupported,
			},
			{
				name:  "returns ErrUnsupported for an Ed25519 signature with another digest than SHA-512",
				cfg:   tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA256},
				token: stamp,
				want:  tsp.ErrUnsupported,
				class: errs.Unsupported,
			},
			{
				name: "returns ErrUnsupported for a SigningCertificateV2 of a hash that this package does not compute",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					attrs := a.Attributes(s.TSTInfo)
					s.Attributes = [][]byte{
						attrs[0],
						attrs[1],
						tsptest.Attribute(
							signingCertificateV2OID,
							signingCertificate(
								element(der.TagSequence, element(der.TagOID, unknownOID)),
								make([]byte, 20),
								nil,
							),
						),
					}
				}),
				want:  tsp.ErrUnsupported,
				class: errs.Unsupported,
			},
			{
				name: "returns ErrMalformed for signed attributes without a message digest",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Attributes = a.Attributes(s.TSTInfo)[:1]
				}),
				want:  tsp.ErrMalformed,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a content type other than id-ct-TSTInfo",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Attributes = a.Attributes(s.TSTInfo)
					s.Attributes[0] = tsptest.Attribute(contentTypeOID, element(der.TagOID, dataOID))
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a message digest of other content",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Attributes = a.Attributes(element(der.TagSequence))
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrSignature for a token without a signing-certificate attribute",
				cfg:   tsptest.Config{ESS: tsptest.ESSNone},
				token: stamp,
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a SigningCertificate of another certificate",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Attributes = append(a.Attributes(s.TSTInfo),
						tsptest.Attribute(signingCertificateOID, signingCertificate(nil, make([]byte, 20), nil)))
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for an issuerSerial of another serial number",
				cfg:  tsptest.Config{Key: tsptest.KeyECDSAP256, Digest: tsptest.DigestSHA256},
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					sum := sha256.Sum256(a.Leaf().Raw)
					attrs := a.Attributes(s.TSTInfo)
					s.Attributes = [][]byte{attrs[0], attrs[1], tsptest.Attribute(signingCertificateV2OID,
						signingCertificate(nil, sum[:], issuerSerial(a.Leaf().RawIssuer, 99)))}
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a signer of another serial number",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.SignerIdentifier = element(
						der.TagSequence,
						a.Leaf().RawIssuer,
						element(der.TagInteger, []byte{99}),
					)
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a subject key identifier of another key",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) {
					s.SignerIdentifier = element(der.Context(0), []byte{9, 9, 9})
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrSignature for a signature that does not verify",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) { s.Signature = make([]byte, 64) }),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name: "returns ErrSignature for a tsa field that names another certificate",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					r := der.NewReader(s.TSTInfo)
					content, _ := r.Read(der.TagSequence)
					s.TSTInfo = element(der.TagSequence, content, element(der.ContextConstructed(0),
						element(der.ContextConstructed(4), a.Intermediate().RawSubject)))
				}),
				want:  tsp.ErrSignature,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrCertificate for a token without certificates",
				token: spec(func(_ *tsptest.Authority, s *tsptest.Spec) { s.NoCertificates = true }),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for a token without the certificate that it names",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Certificates = [][]byte{a.Intermediate().Raw}
				}),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for certificates that are not DER before the one that it names",
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Certificates = [][]byte{malformedElement, a.Leaf().Raw}
				}),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for a token without the certificate that its SigningCertificate names",
				cfg:  tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, ESS: tsptest.ESSV1},
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Certificates = [][]byte{a.Intermediate().Raw}
				}),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for certificates that are not DER before the one of a SigningCertificate",
				cfg:  tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, ESS: tsptest.ESSV1},
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					s.Certificates = [][]byte{malformedElement, a.Leaf().Raw}
				}),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for a named certificate that does not parse",
				cfg:  tsptest.Config{Key: tsptest.KeyECDSAP256, Digest: tsptest.DigestSHA256},
				token: spec(func(a *tsptest.Authority, s *tsptest.Spec) {
					bogus := element(der.TagSequence, element(der.TagNull))
					sum := sha256.Sum256(bogus)
					attrs := a.Attributes(s.TSTInfo)
					s.Attributes = [][]byte{
						attrs[0], attrs[1],
						tsptest.Attribute(signingCertificateV2OID, signingCertificate(nil, sum[:], nil)),
					}
					s.Certificates = [][]byte{bogus}
				}),
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name: "returns ErrCertificate for an extended key usage that is not critical",
				cfg: tsptest.Config{
					Key:    tsptest.KeyEd25519,
					Digest: tsptest.DigestSHA512,
					Usage:  tsptest.UsageNotCritical,
				},
				token: stamp,
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrCertificate for a usage beside id-kp-timeStamping",
				cfg:   tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Usage: tsptest.UsageExtra},
				token: stamp,
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
			{
				name:  "returns ErrCertificate for a certificate without an extended key usage",
				cfg:   tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Usage: tsptest.UsageNone},
				token: stamp,
				want:  tsp.ErrCertificate,
				class: errs.Integrity,
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := tt.cfg
				if cfg.Key == 0 {
					cfg.Key, cfg.Digest = tsptest.KeyEd25519, tsptest.DigestSHA512
				}

				a := newAuthority(t, cfg)
				_, err := newVerifier(t, a, nil).Verify(tt.token(t, a), tsp.SHA256, imprint)
				testkit.ErrorIs(t, err, tt.want, "Verify must refuse the token")
				testkit.Equal(t, errs.Classify(err), tt.class, "the error must classify by its sentinel")
			})
		}

		arguments := []struct {
			name    string
			hash    tsp.Hash
			imprint crypto.Digest
			want    error
			class   errs.Class
		}{
			{
				name:    "returns ErrImprint for another digest",
				hash:    tsp.SHA256,
				imprint: crypto.NewDigest256(sha256.Sum256([]byte("another checkpoint"))),
				want:    tsp.ErrImprint,
				class:   errs.Integrity,
			},
			{
				name:    "returns ErrImprint for another hash",
				hash:    tsp.Hash{OID: tsp.SHA384.OID, Size: crypto.DigestSize256},
				imprint: imprint,
				want:    tsp.ErrImprint,
				class:   errs.Integrity,
			},
			{
				name:    "returns ErrHash for a Hash without an OID",
				hash:    tsp.Hash{Size: crypto.DigestSize256},
				imprint: imprint,
				want:    tsp.ErrHash,
				class:   errs.Invalid,
			},
		}
		for _, tt := range arguments {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

				_, err := newVerifier(t, a, nil).Verify(stamp(t, a), tt.hash, tt.imprint)
				testkit.ErrorIs(t, err, tt.want, "Verify must refuse the imprint")
				testkit.Equal(t, errs.Classify(err), tt.class, "the error must classify by its sentinel")
			})
		}

		t.Run("returns ErrPolicy for a policy that the Verifier does not accept", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, Policy: otherID})

			_, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrPolicy, "a token under another policy must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "ErrPolicy must classify as Integrity")
		})

		t.Run("returns the error of Check wrapped", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			refused := errors.New("not in the trusted list")
			v := newVerifier(t, a, func(tsp.Info) error { return refused })

			_, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, refused, "the error of Check must fail the verification")
		})

		t.Run("passes Check the Info with the chain of the authority", func(t *testing.T) {
			t.Parallel()
			a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})

			var calls atomic.Int32
			v := newVerifier(t, a, func(info tsp.Info) error {
				calls.Add(1)
				testkit.Len(t, info.Chain, 3, "the chain must run from the authority to the root")
				testkit.True(t, info.Chain[0].Equal(a.Leaf()), "the chain must start at the authority's certificate")
				testkit.True(t, info.Time.Time.Equal(origin), "Check must receive the Info")

				return nil
			})

			_, err := v.Verify(stamp(t, a), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token")
			testkit.Equal(t, calls.Load(), int32(1), "Check must run once per token")
		})
	})
}

// TestFIPSOnlyMode checks the SHA-1 certificate identifier under
// GODEBUG=fips140=only. The mode is fixed when a process starts, so the
// test runs itself again in a child process with the mode set, and the
// child builds its tokens with SHA-1 inside fips140.WithoutEnforcement.
func TestFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()

			args := []string{"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout=" + childTimeout.String()}
			if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
				// The child writes its coverage counters where the parent's
				// profile merges them.
				args = append(args, "-test.gocoverdir="+dir.Value.String())
			}

			//nolint:gosec // G204: the child is this test binary, run again with a fixed pattern.
			cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			testkit.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			testkit.True(
				t,
				bytes.Contains(out, []byte("returns_ErrUnsupported_for_a_token_with_only_a_SigningCertificate")),
				"the child must run the FIPS checks, not skip them",
			)
		})

		return
	}

	t.Run("returns ErrUnsupported for a token with only a SigningCertificate", func(t *testing.T) {
		t.Parallel()

		var a *tsptest.Authority

		var tok []byte

		fips140.WithoutEnforcement(func() {
			a = newAuthority(
				t,
				tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, ESS: tsptest.ESSV1},
			)
			tok = stamp(t, a)
		})

		_, err := newVerifier(t, a, nil).Verify(tok, tsp.SHA256, imprint)
		testkit.ErrorIs(t, err, tsp.ErrUnsupported, "FIPS 140-only mode must refuse a SHA-1 identifier alone")
		testkit.Equal(t, errs.Classify(err), errs.Unsupported, "ErrUnsupported must classify as Unsupported")
	})

	t.Run("verifies a token with both attributes without the SHA-1 identifier", func(t *testing.T) {
		t.Parallel()

		var a *tsptest.Authority

		var tok []byte

		fips140.WithoutEnforcement(func() {
			a = newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			tok = token(t, a, func(s *tsptest.Spec) {
				s.Attributes = append(a.Attributes(s.TSTInfo),
					tsptest.Attribute(signingCertificateOID, signingCertificate(nil, make([]byte, 20), nil)))
			})
		})

		_, err := newVerifier(t, a, nil).Verify(tok, tsp.SHA256, imprint)
		testkit.NoError(t, err, "FIPS 140-only mode must not check the SHA-1 identifier beside a SigningCertificateV2")
	})
}

// BenchmarkVerifier measures Verify for a token of a known certificate.
// Ed25519 and ML-DSA allocate nothing. The ceilings of ECDSA and RSA are the
// allocations of crypto/ecdsa and crypto/rsa in Go 1.27.1, which convert the
// public key and allocate their big numbers on each verification. A memory
// profile attributes no allocation of those paths to this package.
func BenchmarkVerifier(b *testing.B) {
	keys := []struct {
		name   string
		key    tsptest.Key
		digest tsptest.Digest
		allocs float64
	}{
		{name: "Verify of an Ed25519 token", key: tsptest.KeyEd25519, digest: tsptest.DigestSHA512, allocs: 0},
		{name: "Verify of an ML-DSA-65 token", key: tsptest.KeyMLDSA65, digest: tsptest.DigestSHA512, allocs: 0},
		{name: "Verify of an ECDSA P-256 token", key: tsptest.KeyECDSAP256, digest: tsptest.DigestSHA256, allocs: 9},
		{name: "Verify of an ECDSA P-384 token", key: tsptest.KeyECDSAP384, digest: tsptest.DigestSHA384, allocs: 17},
		{
			name:   "Verify of an RSA PKCS #1 v1.5 token",
			key:    tsptest.KeyRSAPKCS1,
			digest: tsptest.DigestSHA256,
			allocs: 9,
		},
		{name: "Verify of an RSA PSS token", key: tsptest.KeyRSAPSS, digest: tsptest.DigestSHA256, allocs: 13},
	}
	for _, k := range keys {
		b.Run(k.name, func(b *testing.B) {
			a := newAuthority(b, tsptest.Config{Key: k.key, Digest: k.digest, TSA: true})
			v := newVerifier(b, a, nil)
			tok := stamp(b, a)

			_, err := v.Verify(tok, tsp.SHA256, imprint)
			testkit.NoError(b, err, "the first Verify must accept the token")

			tspAllocs(b, k.allocs, func() { sinkInfo, errSink = v.Verify(tok, tsp.SHA256, imprint) })
			testkit.NoError(b, errSink, "the benchmark must measure a token that verifies")
		})
	}
}

func FuzzVerifier(f *testing.F) {
	a := newAuthority(f, tsptest.Config{
		Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512, ESS: tsptest.ESSBoth, IssuerSerial: true,
		Accuracy: time.Second, Ordering: true, TSA: true,
	})
	v := newVerifier(f, a, nil)

	f.Add(stamp(f, a))
	f.Fuzz(func(t *testing.T, tok []byte) {
		info, err := v.Verify(tok, tsp.SHA256, imprint)
		if err == nil {
			testkit.True(t, bytes.Contains(tok, info.Serial), "the Info must refer to the token")
		}
	})
}

// tspAllocs fails b when call allocates more than ceiling times per call,
// averaged over benchRuns calls, and then reports the time and the
// allocations of call per iteration.
func tspAllocs(b *testing.B, ceiling float64, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs > ceiling {
		b.Fatalf("allocates %v times per call, want at most %v", allocs, ceiling)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}

// mustOID returns the OID of dotted, a valid identifier.
func mustOID(dotted string) x509.OID {
	oid, err := x509.ParseOID(dotted)
	if err != nil {
		// The fixtures are package variables, and a panic at their
		// initialisation names the bad literal.
		panic(err) //nolint:forbidigo // see above
	}

	return oid
}

// x509OID returns id as an OID of crypto/x509, and fails tb when id does
// not convert.
func x509OID(tb testing.TB, id asn1.ObjectIdentifier) x509.OID {
	tb.Helper()

	arcs := make([]uint64, len(id))
	for i, arc := range id {
		arcs[i] = uint64(arc) //nolint:gosec // G115: the identifiers of the cases have small positive arcs
	}

	oid, err := x509.OIDFromInts(arcs)
	testkit.NoError(tb, err, "the identifier must convert")

	return oid
}

// appendOID returns the content of oid in DER.
func appendOID(oid x509.OID) []byte {
	b, _ := oid.AppendBinary(nil)

	return b
}

// newAuthority returns an Authority of cfg, with the fake clock at origin
// and policyID unless cfg sets them, and fails tb when New refuses cfg.
func newAuthority(tb testing.TB, cfg tsptest.Config) *tsptest.Authority {
	tb.Helper()

	if cfg.Clock == nil {
		cfg.Clock = fake.New(origin)
	}

	if len(appendOID(cfg.Policy)) == 0 {
		cfg.Policy = policyID
	}

	a, err := tsptest.New(cfg)
	testkit.NoError(tb, err, "tsptest.New must accept the configuration")

	return a
}

// request returns the DER of a TimeStampReq of imprint, nonce and no
// policy, and fails tb when AppendRequest refuses it.
func request(tb testing.TB) []byte {
	tb.Helper()

	req, err := tsp.AppendRequest(nil, tsp.SHA256, imprint, nonce, x509.OID{})
	testkit.NoError(tb, err, "AppendRequest must encode the request")

	return req
}

// stamp returns a token of a for the request of request, and fails tb when
// the response does not grant one.
func stamp(tb testing.TB, a *tsptest.Authority) []byte {
	tb.Helper()

	resp, err := a.Respond(request(tb))
	testkit.NoError(tb, err, "the authority must respond")

	tok, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
	testkit.NoError(tb, err, "ParseResponse must return the token")

	return tok
}

// token returns a token of a for the request of request, whose Spec edit
// changes, and fails tb when a refuses the Spec.
func token(tb testing.TB, a *tsptest.Authority, edit func(*tsptest.Spec)) []byte {
	tb.Helper()

	s := tsptest.Spec{TSTInfo: mustTSTInfo(tb, a, request(tb))}
	edit(&s)

	return mustToken(tb, a, s)
}

// spec returns a function that builds the token of token for the Spec that
// edit changes, with the Authority at hand.
func spec(edit func(a *tsptest.Authority, s *tsptest.Spec)) func(testing.TB, *tsptest.Authority) []byte {
	return func(tb testing.TB, a *tsptest.Authority) []byte {
		tb.Helper()

		return token(tb, a, func(s *tsptest.Spec) { edit(a, s) })
	}
}

// newVerifier returns a Verifier of a's roots that accepts policyID with an
// accuracy of a second, with check as its Check, and fails tb when
// NewVerifier refuses it.
func newVerifier(tb testing.TB, a *tsptest.Authority, check func(tsp.Info) error) *tsp.Verifier {
	tb.Helper()

	return verifierOf(tb, tsp.VerifierConfig{
		Roots:    a.Roots(),
		Policies: []tsp.Policy{{ID: policyID, Accuracy: time.Second}},
		Check:    check,
	})
}

// verifierOf returns the Verifier of cfg, and fails tb when NewVerifier
// refuses cfg.
func verifierOf(tb testing.TB, cfg tsp.VerifierConfig) *tsp.Verifier {
	tb.Helper()

	v, err := tsp.NewVerifier(cfg)
	testkit.NoError(tb, err, "NewVerifier must accept the configuration")

	return v
}

// signingCertificate returns the DER of a SigningCertificate or
// SigningCertificateV2 with one identifier: the hashAlgorithm element alg,
// none when nil, certHash, and the IssuerSerial element is, none when nil.
func signingCertificate(alg, certHash, is []byte) []byte {
	return element(der.TagSequence, element(der.TagSequence,
		element(der.TagSequence, alg, element(der.TagOctetString, certHash), is)))
}

// issuerSerial returns the DER of an IssuerSerial of the directoryName
// issuer and the serial number serial.
func issuerSerial(issuer []byte, serial uint64) []byte {
	b := der.NewBuilder(nil)
	is := b.Open(der.TagSequence)
	names := b.Open(der.TagSequence)
	name := b.Open(der.ContextConstructed(4))
	b.AddElement(issuer)
	b.Close(name)
	b.Close(names)
	b.AddUint64(serial)
	b.Close(is)

	return b.Bytes()
}
