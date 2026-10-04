// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/x509"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/internal/der"
)

// maxAccuracySeconds is the largest seconds field of an Accuracy that a
// token may state: the most whole seconds that fit a [time.Duration] with
// the milliseconds and microseconds added.
const maxAccuracySeconds = 9_223_372_035

// maxSubsecond is the largest millis or micros field of an Accuracy, RFC
// 3161 section 2.4.2.
const maxSubsecond = 999

// Info is the TSTInfo of a token that [Verifier.Verify] verified, RFC 3161
// section 2.4.2. Its byte fields are slices of the token, so the caller
// keeps the token while it uses them, and copies a field that it keeps
// longer.
//
// # Allocation contract
//
// An Info is a value of fixed size, and Verify returns it without an
// allocation. [Info.Extension] does not allocate.
type Info struct {
	// Time is the genTime of the token as a reading of UTC: its Time is
	// genTime, its MaxError the accuracy that the token states, or the
	// Accuracy of Policy when the token states none, and Synced is true.
	// [clock.UTCReading.Latest] is the time before which the data of the
	// imprint existed.
	Time clock.UTCReading

	// Serial is the content of the serialNumber INTEGER: its octets in
	// big-endian two's complement. RFC 3161 makes the pair of the
	// authority and the serial unique, and asks a verifier to accept
	// serials of up to 160 bits.
	Serial []byte

	// Nonce is the content of the nonce INTEGER, and nil when the token
	// has none.
	Nonce []byte

	// TSA is the DER of the tsa GeneralName, which names the authority,
	// and nil when the token has none. Verify checks that it names the
	// authority's certificate: a directoryName of its subject, or a name
	// of its subjectAltName extension.
	TSA []byte

	// Chain is the verified chain of the authority's certificate, leaf
	// first, as Verify verified it at genTime. Chain[0] is the certificate
	// whose key signed the token. A caller applies a rule of its own per
	// authority with it, such as a date after which it distrusts a key.
	// The Verifier shares the chain with every token of the same
	// certificate, so the caller does not change it.
	Chain []*x509.Certificate

	// extensions is the content of the extensions field, a sequence of
	// Extension elements, and nil when the token has none.
	extensions []byte

	// Policy is the Policy of the Verifier that the token names.
	Policy Policy

	// Ordering reports the ordering field: the authority orders all its
	// tokens by genTime, whatever their accuracy.
	Ordering bool
}

// Extension returns the extnValue of the extension of the token whose
// extnID is id, and reports whether it is critical and whether the token
// has it. The token's TSTInfo is DER, so it has at most one extension of
// each identifier. Verify parsed every extension, so Extension finds the
// extension of a verified token without a parse failure.
//
// A caller with rules of its own, such as the qcStatements extension that
// ETSI EN 319 422 defines for a qualified electronic time-stamp, reads the
// extension in its [VerifierConfig.Check].
//
// # Allocation contract
//
// Zero alloc for an id of at most 64 octets in DER.
func (i Info) Extension(id x509.OID) (value []byte, critical, ok bool) {
	var buf [64]byte

	want := appendOID(buf[:0], id)

	r := der.NewReader(i.extensions)
	for !r.Empty() {
		ext, _ := r.Read(der.TagSequence)

		oid, critical, value := parseExtension(ext)
		if bytes.Equal(oid, want) {
			return value, critical, true
		}
	}

	return nil, false, false
}

// tstInfo is the parts of a TSTInfo, each a slice of the token apart from
// genTime, accuracy and ordering.
type tstInfo struct {
	genTime time.Time

	// policy is the content of the policy OID.
	policy []byte

	// imprint is the content of the hashedMessage of messageImprint, and
	// hash its hashAlgorithm.
	imprint []byte
	hash    algorithmIdentifier

	// serial, nonce, tsa and extensions are the fields of Info.
	serial, nonce, tsa, extensions []byte

	// accuracy is the Accuracy of the token, and stated reports whether
	// the token has one.
	accuracy time.Duration
	stated   bool

	ordering bool
}

// parseTSTInfo returns the parts of b, a TSTInfo in DER, and reports false
// when b is not one: version 1, a policy, a messageImprint, a
// serialNumber, a genTime, and the optional fields in their order. An
// Accuracy has seconds of at most maxAccuracySeconds, and millis and
// micros from 1 to 999. ordering may state its DEFAULT of FALSE. Every
// extension has an extnID, an optional critical flag, and an extnValue.
func parseTSTInfo(b []byte) (tstInfo, bool) {
	var t tstInfo

	r := der.NewReader(b)

	seq, ok := r.Read(der.TagSequence)
	if !ok || !r.Empty() {
		return tstInfo{}, false
	}

	s := der.NewReader(seq)

	version, ok := s.Read(der.TagInteger)
	if !ok || !isOne(version) {
		return tstInfo{}, false
	}

	if t.policy, ok = s.Read(der.TagOID); !ok || !der.ObjectIdentifier(t.policy) {
		return tstInfo{}, false
	}

	imprint, ok := s.Read(der.TagSequence)
	if !ok {
		return tstInfo{}, false
	}

	if t.hash, t.imprint, ok = parseImprint(imprint); !ok {
		return tstInfo{}, false
	}

	if t.serial, ok = s.Read(der.TagInteger); !ok || !der.Integer(t.serial) {
		return tstInfo{}, false
	}

	genTime, ok := s.Read(der.TagGeneralizedTime)
	if !ok {
		return tstInfo{}, false
	}

	if t.genTime, ok = der.GeneralizedTime(genTime); !ok {
		return tstInfo{}, false
	}

	if !parseOptional(&s, &t) {
		return tstInfo{}, false
	}

	return t, true
}

// parseOptional reads the optional fields of a TSTInfo from s into t:
// accuracy, ordering, nonce, tsa and extensions, in that order, and
// reports false when one is malformed or s has octets after them.
func parseOptional(s *der.Reader, t *tstInfo) bool {
	accuracy, present, ok := s.Optional(der.TagSequence)
	if !ok {
		return false
	}

	if present {
		if t.accuracy, ok = parseAccuracy(accuracy); !ok {
			return false
		}

		t.stated = true
	}

	ordering, present, ok := s.Optional(der.TagBoolean)
	if !ok {
		return false
	}

	if present {
		if t.ordering, ok = der.Boolean(ordering); !ok {
			return false
		}
	}

	if t.nonce, _, ok = s.Optional(der.TagInteger); !ok || t.nonce != nil && !der.Integer(t.nonce) {
		return false
	}

	if t.tsa, _, ok = s.Optional(der.ContextConstructed(0)); !ok || t.tsa != nil && !oneElement(t.tsa) {
		return false
	}

	if t.extensions, _, ok = s.Optional(der.ContextConstructed(1)); !ok || !validExtensions(t.extensions) {
		return false
	}

	return s.Empty()
}

// parseImprint returns the hashAlgorithm and the content of the
// hashedMessage of imprint, the content of a MessageImprint.
func parseImprint(imprint []byte) (algorithmIdentifier, []byte, bool) {
	r := der.NewReader(imprint)

	alg, ok := readAlgorithm(&r)
	if !ok {
		return algorithmIdentifier{}, nil, false
	}

	hashed, ok := r.Read(der.TagOctetString)
	if !ok || !r.Empty() {
		return algorithmIdentifier{}, nil, false
	}

	return alg, hashed, true
}

// parseAccuracy returns the duration of accuracy, the content of an
// Accuracy: seconds, millis [0] and micros [1], each optional and zero
// when absent.
func parseAccuracy(accuracy []byte) (time.Duration, bool) {
	r := der.NewReader(accuracy)

	seconds, ok := optionalUint(&r, der.TagInteger, 0, maxAccuracySeconds)
	if !ok {
		return 0, false
	}

	millis, ok := optionalUint(&r, der.Context(0), 1, maxSubsecond)
	if !ok {
		return 0, false
	}

	micros, ok := optionalUint(&r, der.Context(1), 1, maxSubsecond)
	if !ok || !r.Empty() {
		return 0, false
	}

	//nolint:gosec // G115: each field is at most maxAccuracySeconds, so the sum fits a Duration
	return time.Duration(seconds)*time.Second + time.Duration(millis)*time.Millisecond +
		time.Duration(micros)*time.Microsecond, true
}

// optionalUint reads an optional INTEGER of tag from r, and returns its
// value, 0 when it is absent. It reports false for a value outside lo to
// hi, and for a malformed INTEGER.
func optionalUint(r *der.Reader, tag der.Tag, lo, hi uint64) (uint64, bool) {
	content, present, ok := r.Optional(tag)
	if !ok {
		return 0, false
	}

	if !present {
		return 0, true
	}

	v, ok := der.Uint64(content)

	return v, ok && v >= lo && v <= hi
}

// parseExtension returns the extnID, the critical flag and the content of
// the extnValue of ext, the content of an Extension that validExtensions
// accepted.
func parseExtension(ext []byte) (oid []byte, critical bool, value []byte) {
	r := der.NewReader(ext)
	oid, _ = r.Read(der.TagOID)

	if flag, present, _ := r.Optional(der.TagBoolean); present {
		critical, _ = der.Boolean(flag)
	}

	value, _ = r.Read(der.TagOctetString)

	return oid, critical, value
}

// validExtensions reports whether exts, the content of an Extensions
// field, is a sequence of Extension elements: an OID, an optional BOOLEAN
// critical, and an OCTET STRING extnValue. An absent field is valid.
func validExtensions(exts []byte) bool {
	r := der.NewReader(exts)
	for !r.Empty() {
		ext, ok := r.Read(der.TagSequence)
		if !ok {
			return false
		}

		er := der.NewReader(ext)
		if oid, ok := er.Read(der.TagOID); !ok || !der.ObjectIdentifier(oid) {
			return false
		}

		if flag, present, ok := er.Optional(der.TagBoolean); !ok || present && !validBoolean(flag) {
			return false
		}

		if _, ok := er.Read(der.TagOctetString); !ok || !er.Empty() {
			return false
		}
	}

	return true
}

// validBoolean reports whether flag is the content of a BOOLEAN in DER.
func validBoolean(flag []byte) bool {
	_, ok := der.Boolean(flag)

	return ok
}

// oneElement reports whether b is exactly one DER element.
func oneElement(b []byte) bool {
	r := der.NewReader(b)
	_, _, _, ok := r.Next()

	return ok && r.Empty()
}

// isOne reports whether content is the content of the INTEGER 1.
func isOne(content []byte) bool {
	v, ok := der.Uint64(content)

	return ok && v == 1
}
