// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"time"

	"go.thesmos.sh/core/clock"
)

// ESS names the signing-certificate attributes of an authority's tokens,
// RFC 5035 and RFC 5816. The zero ESS is ESSV2.
type ESS uint8

// The signing-certificate attributes of an Authority.
const (
	// ESSV2 writes a SigningCertificateV2 that hashes the authority's
	// certificate with the Digest.
	ESSV2 ESS = 0

	// ESSV1 writes a SigningCertificate, whose identifier is the SHA-1
	// digest of the authority's certificate.
	ESSV1 ESS = 1

	// ESSBoth writes a SigningCertificateV2 and a SigningCertificate.
	ESSBoth ESS = 2

	// ESSNone writes neither, which RFC 3161 forbids.
	ESSNone ESS = 3
)

// Valid reports whether e is one of the ESS values of this package.
func (e ESS) Valid() bool {
	return e <= ESSNone
}

// v2 reports whether e writes a SigningCertificateV2.
func (e ESS) v2() bool {
	return e == ESSV2 || e == ESSBoth
}

// v1 reports whether e writes a SigningCertificate.
func (e ESS) v1() bool {
	return e == ESSV1 || e == ESSBoth
}

// Usage names the extended key usage of an authority's certificate. The
// zero Usage is UsageCritical.
type Usage uint8

// The extended key usages of an Authority.
const (
	// UsageCritical is one critical id-kp-timeStamping, as RFC 3161
	// section 2.3 requires.
	UsageCritical Usage = 0

	// UsageNotCritical is id-kp-timeStamping in an extension that is not
	// critical.
	UsageNotCritical Usage = 1

	// UsageExtra is id-kp-timeStamping and id-kp-serverAuth in a critical
	// extension.
	UsageExtra Usage = 2

	// UsageNone is no extended key usage.
	UsageNone Usage = 3
)

// Valid reports whether u is one of the Usages of this package.
func (u Usage) Valid() bool {
	return u <= UsageNone
}

// apply sets the extended key usage of u, a valid Usage, on leaf, the
// template of an authority's certificate.
//
// Error modes: the error of encoding/asn1, wrapped.
func (u Usage) apply(leaf *x509.Certificate) error {
	switch u {
	case UsageNotCritical:
		leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}
	case UsageExtra:
		return critical(leaf, oidTimeStamping, oidServerAuth)
	case UsageNone:
	default:
		return critical(leaf, oidTimeStamping)
	}

	return nil
}

// critical adds a critical extended key usage of usages to leaf, which
// crypto/x509 writes only as an extension that is not critical.
//
// Error modes: the error of encoding/asn1, wrapped.
func critical(leaf *x509.Certificate, usages ...asn1.ObjectIdentifier) error {
	value, err := asn1.Marshal(usages)
	if err != nil {
		return fmt.Errorf("tsptest: encode the extended key usage: %w", err)
	}

	leaf.ExtraExtensions = append(
		leaf.ExtraExtensions,
		pkix.Extension{Id: oidExtKeyUsage, Critical: true, Value: value},
	)

	return nil
}

// Config configures an [Authority]. Clock, Policy, Key and Digest are
// required. The other fields default to their zero values, which give a
// token as RFC 3161 requires: a SigningCertificateV2, one critical
// id-kp-timeStamping, and no optional field of the TSTInfo.
type Config struct {
	// Clock is the source of genTime, and of the validity of the
	// certificates, which starts an hour before its time at New.
	Clock clock.Clock

	// Policy is the TSAPolicyId of the tokens.
	Policy x509.OID

	// Extensions are the extensions of each TSTInfo, none when nil. The
	// Authority writes the critical flag only when it is set, as DER
	// requires of a DEFAULT FALSE.
	Extensions []pkix.Extension

	// Accuracy is the accuracy of each TSTInfo, in whole microseconds.
	// Zero writes none.
	Accuracy time.Duration

	// Validity is the validity of the authority's certificate from the
	// clock's time at New. Zero is a year.
	Validity time.Duration

	// Key is the key of the authority's certificate.
	Key Key

	// Digest is the digest algorithm of the SignerInfo.
	Digest Digest

	// ESS is the signing-certificate attributes of the tokens.
	ESS ESS

	// Usage is the extended key usage of the authority's certificate.
	Usage Usage

	// IssuerSerial writes the issuerSerial of each certificate identifier
	// of the signing-certificate attributes.
	IssuerSerial bool

	// SubjectKeyID identifies the signer by its subject key identifier
	// instead of its issuer and serial number.
	SubjectKeyID bool

	// Ordering writes the ordering field TRUE.
	Ordering bool

	// TSA writes the tsa field: the subject of the authority's certificate
	// as a directoryName.
	TSA bool
}
