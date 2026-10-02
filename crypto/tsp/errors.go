// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"errors"
	"strconv"
	"strings"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package. Every error of a token or a
// response that fails a check classifies as
// [go.thesmos.sh/core/errs.Integrity], because the data failed
// verification and the same data fails again. [ErrHash] and [ErrConfig]
// classify as Invalid, because the caller passed an argument that never
// works. [ErrUnsupported] classifies as Unsupported.
var (
	// ErrMalformed reports a response or a token that is not the DER
	// encoding of its structure: a TimeStampResp, a ContentInfo of
	// SignedData, or a TSTInfo, with the fields that RFC 3161 and RFC
	// 5652 require. Classifies as Integrity.
	ErrMalformed = errs.WithClass(errors.New("tsp: malformed time-stamp response or token"), errs.Integrity)

	// ErrImprint reports a token whose message imprint differs from the
	// caller's: another hash algorithm, or another digest. Classifies as
	// Integrity.
	ErrImprint = errs.WithClass(errors.New("tsp: message imprint differs from the caller's"), errs.Integrity)

	// ErrNonce reports a response whose token lacks the nonce of the
	// request, or repeats another one. Classifies as Integrity.
	ErrNonce = errs.WithClass(errors.New("tsp: nonce differs from the request's"), errs.Integrity)

	// ErrPolicy reports a token under a policy that the verifier does not
	// accept, or a response under a policy other than the requested one.
	// Classifies as Integrity.
	ErrPolicy = errs.WithClass(errors.New("tsp: policy not accepted"), errs.Integrity)

	// ErrSignature reports a token whose signer information fails a
	// check: its content-type or message-digest attribute, its
	// signing-certificate attribute, its signer identifier, its
	// signature, or a tsa field that does not name the signer's
	// certificate. Classifies as Integrity.
	ErrSignature = errs.WithClass(errors.New("tsp: token signature does not verify"), errs.Integrity)

	// ErrCertificate reports a token whose signer certificate fails a
	// check: absent from the token, without the one critical extended key
	// usage id-kp-timeStamping, or without a chain to the verifier's
	// roots that is valid at the token's time. Classifies as Integrity.
	ErrCertificate = errs.WithClass(errors.New("tsp: authority certificate does not verify"), errs.Integrity)

	// ErrUnsupported reports a token that uses an algorithm that this
	// package does not verify: a digest other than SHA-256, SHA-384 and
	// SHA-512, a signature algorithm outside RSA, ECDSA, Ed25519 and
	// ML-DSA, or a SHA-1 certificate identifier in FIPS 140-only mode.
	// Classifies as Unsupported.
	ErrUnsupported = errs.WithClass(errors.New("tsp: algorithm not supported"), errs.Unsupported)

	// ErrHash reports a Hash without an OID or with a Size of its own,
	// and an imprint whose size is not the Hash's Size. Classifies as
	// Invalid.
	ErrHash = errs.WithClass(errors.New("tsp: imprint does not match its hash"), errs.Invalid)

	// ErrConfig reports a VerifierConfig without Roots or Policies, or
	// with a Policy without an ID, with an Accuracy that is not positive,
	// or with the ID of another Policy. Classifies as Invalid.
	ErrConfig = errs.WithClass(errors.New("tsp: invalid verifier configuration"), errs.Invalid)
)

// The values of PKIStatus, RFC 3161 section 2.4.2.
const (
	// StatusGranted grants the token as requested.
	StatusGranted = 0

	// StatusGrantedWithMods grants a token with modifications.
	StatusGrantedWithMods = 1

	// StatusRejection rejects the request, for the reasons of FailInfo.
	StatusRejection = 2

	// StatusWaiting has the authority still working on the request.
	StatusWaiting = 3

	// StatusRevocationWarning warns of a revocation that is imminent.
	StatusRevocationWarning = 4

	// StatusRevocationNotification notifies of a revocation that has
	// occurred.
	StatusRevocationNotification = 5
)

// The bits of PKIFailureInfo, RFC 3161 section 2.4.2. Bit n of
// [StatusError.FailInfo] is FailInfo&(1<<n).
const (
	// FailBadAlg reports an unrecognized or unsupported algorithm.
	FailBadAlg = 0

	// FailBadRequest reports a transaction that is not permitted or not
	// supported.
	FailBadRequest = 2

	// FailBadDataFormat reports data of the wrong format.
	FailBadDataFormat = 5

	// FailTimeNotAvailable reports that the authority's time source is not
	// available.
	FailTimeNotAvailable = 14

	// FailUnacceptedPolicy reports a requested policy that the authority
	// does not support.
	FailUnacceptedPolicy = 15

	// FailUnacceptedExtension reports a requested extension that the
	// authority does not support.
	FailUnacceptedExtension = 16

	// FailAddInfoNotAvailable reports additional information that the
	// authority could not understand or provide.
	FailAddInfoNotAvailable = 17

	// FailSystemFailure reports a request that a system failure stopped.
	FailSystemFailure = 25
)

// StatusError is the error of a TimeStampResp whose status grants no
// token: the status, the failure information and the text of the
// authority. [ParseResponse] returns it.
//
// # Allocation contract
//
// [ParseResponse] allocates one StatusError and its Text for a response
// that grants no token.
type StatusError struct {
	// Text is the statusString of the response, its texts joined by "; ",
	// and empty when the response has none.
	Text string

	// Status is the PKIStatus of the response, such as
	// [StatusRejection].
	Status int

	// FailInfo contains the bits of the response's failInfo below 32: bit n
	// is FailInfo&(1<<n), as [FailTimeNotAvailable] names bit 14.
	FailInfo uint32
}

// Error returns the status, the failure bits and the text, as in
// "tsp: authority returned status 2, failure bits 0x4000: time source
// unavailable".
func (e *StatusError) Error() string {
	var b strings.Builder

	b.WriteString("tsp: authority returned status ")
	b.WriteString(strconv.Itoa(e.Status))

	if e.FailInfo != 0 {
		b.WriteString(", failure bits 0x")
		b.WriteString(strconv.FormatUint(uint64(e.FailInfo), 16))
	}

	if e.Text != "" {
		b.WriteString(": ")
		b.WriteString(e.Text)
	}

	return b.String()
}

// Class returns the class of the status under
// [go.thesmos.sh/core/errs.Classify]:
//
//   - Transient for [StatusWaiting], and for a rejection whose failure is
//     [FailTimeNotAvailable] or [FailSystemFailure]: the same request may
//     succeed later.
//   - Invalid for every other rejection: the authority refuses the
//     request itself.
//   - Denied for [StatusRevocationWarning] and
//     [StatusRevocationNotification]: the authority's certificate is
//     revoked, and the caller turns to another authority.
//   - Unspecified for a status that RFC 3161 does not define.
func (e *StatusError) Class() errs.Class {
	switch e.Status {
	case StatusWaiting:
		return errs.Transient
	case StatusRejection:
		if e.FailInfo&(1<<FailTimeNotAvailable|1<<FailSystemFailure) != 0 {
			return errs.Transient
		}

		return errs.Invalid
	case StatusRevocationWarning, StatusRevocationNotification:
		return errs.Denied
	default:
		return errs.Unspecified
	}
}
