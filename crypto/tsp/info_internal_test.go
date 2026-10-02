// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/x509"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

// tstGenTime is the genTime of the TSTInfo of the cases, and tstDigest the
// hashedMessage of its imprint.
var (
	tstGenTime = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tstDigest  = bytes.Repeat([]byte{0xab}, 32)
)

// The extensions of the cases of Info.Extension: an extension that is
// critical, one that states FALSE, and one without the flag.
var (
	extCritical = el(der.TagSequence, el(der.TagOID, []byte{0x2a, 0x01}), el(der.TagBoolean, []byte{0xff}),
		el(der.TagOctetString, []byte("critical")))
	extFalse = el(der.TagSequence, el(der.TagOID, []byte{0x2a, 0x02}), el(der.TagBoolean, []byte{0x00}),
		el(der.TagOctetString, []byte("false")))
	extPlain = el(der.TagSequence, el(der.TagOID, []byte{0x2a, 0x03}), el(der.TagOctetString, []byte("plain")))
)

func TestTSTInfo(t *testing.T) {
	t.Parallel()

	t.Run("parseTSTInfo", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the fields of a TSTInfo without optional fields", func(t *testing.T) {
			t.Parallel()
			info, ok := parseTSTInfo(tstInfoOf())
			testkit.True(t, ok, "parseTSTInfo must accept the TSTInfo")
			testkit.Equal(t, info.policy, oidUnknown, "policy must be the content of the OID")
			testkit.Equal(t, info.hash.oid, oidSHA256, "hash must be the hashAlgorithm")
			testkit.Equal(t, info.imprint, tstDigest, "imprint must be the hashedMessage")
			testkit.Equal(t, info.serial, []byte{0x07}, "serial must be the content of the INTEGER")
			testkit.True(t, info.genTime.Equal(tstGenTime), "genTime must be the GeneralizedTime")
			testkit.False(t, info.stated, "a TSTInfo without accuracy states none")
			testkit.False(t, info.ordering, "ordering must default to FALSE")
			testkit.True(t, info.nonce == nil && info.tsa == nil && info.extensions == nil,
				"absent fields must be nil")
		})

		t.Run("returns the optional fields of a TSTInfo", func(t *testing.T) {
			t.Parallel()
			info, ok := parseTSTInfo(tstInfoOf(func(p *tstParts) {
				p.accuracy = el(der.TagSequence, el(der.TagInteger, []byte{2}), el(der.Context(0), []byte{3}),
					el(der.Context(1), []byte{4}))
				p.ordering = el(der.TagBoolean, []byte{0xff})
				p.nonce = el(der.TagInteger, []byte{0x01, 0x02})
				p.tsa = el(der.ContextConstructed(0), el(der.ContextConstructed(4), el(der.TagSequence)))
				p.extensions = el(der.ContextConstructed(1), extPlain)
			}))
			testkit.True(t, ok, "parseTSTInfo must accept the TSTInfo")
			testkit.True(t, info.stated, "the TSTInfo states an accuracy")
			testkit.Equal(t, info.accuracy, 2*time.Second+3*time.Millisecond+4*time.Microsecond,
				"accuracy must add seconds, millis and micros")
			testkit.True(t, info.ordering, "ordering must be TRUE")
			testkit.Equal(t, info.nonce, []byte{0x01, 0x02}, "nonce must be the content of the INTEGER")
			testkit.Equal(t, info.tsa, el(der.ContextConstructed(4), el(der.TagSequence)),
				"tsa must be the GeneralName")
			testkit.Equal(t, info.extensions, extPlain, "extensions must be the content of the field")
		})

		accuracies := []struct {
			name string
			give []byte
			want time.Duration
		}{
			{name: "returns an empty accuracy as zero", give: el(der.TagSequence), want: 0},
			{
				name: "returns an accuracy of seconds zero",
				give: el(der.TagSequence, el(der.TagInteger, []byte{0})),
				want: 0,
			},
			{
				name: "returns an accuracy of one millisecond",
				give: el(der.TagSequence, el(der.Context(0), []byte{1})),
				want: time.Millisecond,
			},
			{
				name: "returns an accuracy of one microsecond",
				give: el(der.TagSequence, el(der.Context(1), []byte{1})),
				want: time.Microsecond,
			},
			{
				name: "returns the largest accuracy",
				give: el(der.TagSequence, el(der.TagInteger, []byte{0x02, 0x25, 0xc1, 0x7d, 0x03}),
					el(der.Context(0), []byte{0x03, 0xe7}), el(der.Context(1), []byte{0x03, 0xe7})),
				want: maxAccuracySeconds*time.Second + 999*time.Millisecond + 999*time.Microsecond,
			},
		}
		for _, tt := range accuracies {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				info, ok := parseTSTInfo(tstInfoOf(func(p *tstParts) { p.accuracy = tt.give }))
				testkit.True(t, ok, "parseTSTInfo must accept the accuracy")
				testkit.True(t, info.stated, "the TSTInfo states an accuracy")
				testkit.Equal(t, info.accuracy, tt.want, "accuracy must be the sum of its fields")
			})
		}

		t.Run("returns ordering FALSE when the TSTInfo states it", func(t *testing.T) {
			t.Parallel()
			info, ok := parseTSTInfo(tstInfoOf(func(p *tstParts) { p.ordering = el(der.TagBoolean, []byte{0x00}) }))
			testkit.True(t, ok, "parseTSTInfo must accept ordering FALSE")
			testkit.False(t, info.ordering, "ordering must be FALSE")
		})

		tests := []struct {
			name string
			give func(*tstParts)
		}{
			{
				name: "reports false for a version other than 1",
				give: func(p *tstParts) { p.version = el(der.TagInteger, []byte{2}) },
			},
			{
				name: "reports false for a version that is not an INTEGER in DER",
				give: func(p *tstParts) { p.version = el(der.TagInteger, []byte{0x00, 0x01}) },
			},
			{name: "reports false for a TSTInfo without a version", give: func(p *tstParts) { p.version = nil }},
			{name: "reports false for a TSTInfo without a policy", give: func(p *tstParts) { p.policy = nil }},
			{
				name: "reports false for a policy that is not an OID in DER",
				give: func(p *tstParts) { p.policy = el(der.TagOID, []byte{0x80, 0x01}) },
			},
			{name: "reports false for a TSTInfo without a messageImprint", give: func(p *tstParts) { p.imprint = nil }},
			{
				name: "reports false for a messageImprint without a hashAlgorithm",
				give: func(p *tstParts) { p.imprint = el(der.TagSequence, el(der.TagOctetString, tstDigest)) },
			},
			{
				name: "reports false for a messageImprint without a hashedMessage",
				give: func(p *tstParts) { p.imprint = el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidSHA256))) },
			},
			{
				name: "reports false for an element after the hashedMessage",
				give: func(p *tstParts) {
					p.imprint = el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidSHA256)),
						el(der.TagOctetString, tstDigest), el(der.TagNull))
				},
			},
			{name: "reports false for a TSTInfo without a serialNumber", give: func(p *tstParts) { p.serial = nil }},
			{
				name: "reports false for a serialNumber that is not an INTEGER in DER",
				give: func(p *tstParts) { p.serial = el(der.TagInteger, []byte{0x00, 0x07}) },
			},
			{name: "reports false for a TSTInfo without a genTime", give: func(p *tstParts) { p.genTime = nil }},
			{
				name: "reports false for a genTime that is not DER",
				give: func(p *tstParts) { p.genTime = el(der.TagGeneralizedTime, []byte("20261002120000.10Z")) },
			},
			{
				name: "reports false for an accuracy that is not DER",
				give: func(p *tstParts) { p.accuracy = cat([]byte{byte(der.TagSequence)}, notDER) },
			},
			{
				name: "reports false for seconds above the largest accuracy",
				give: func(p *tstParts) {
					p.accuracy = el(der.TagSequence, el(der.TagInteger, []byte{0x02, 0x25, 0xc1, 0x7d, 0x04}))
				},
			},
			{
				name: "reports false for negative seconds",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, el(der.TagInteger, []byte{0xff})) },
			},
			{
				name: "reports false for seconds that are not DER",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, cat([]byte{byte(der.TagInteger)}, notDER)) },
			},
			{
				name: "reports false for millis of zero",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, el(der.Context(0), []byte{0})) },
			},
			{
				name: "reports false for millis of 1000",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, el(der.Context(0), []byte{0x03, 0xe8})) },
			},
			{
				name: "reports false for micros of zero",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, el(der.Context(1), []byte{0})) },
			},
			{
				name: "reports false for micros of 1000",
				give: func(p *tstParts) { p.accuracy = el(der.TagSequence, el(der.Context(1), []byte{0x03, 0xe8})) },
			},
			{
				name: "reports false for an element after the micros",
				give: func(p *tstParts) {
					p.accuracy = el(der.TagSequence, el(der.Context(1), []byte{1}), el(der.TagNull))
				},
			},
			{
				name: "reports false for an ordering that is not a BOOLEAN in DER",
				give: func(p *tstParts) { p.ordering = el(der.TagBoolean, []byte{0x01}) },
			},
			{
				name: "reports false for an ordering element that is not DER",
				give: func(p *tstParts) { p.ordering = cat([]byte{byte(der.TagBoolean)}, notDER) },
			},
			{
				name: "reports false for a nonce that is not an INTEGER in DER",
				give: func(p *tstParts) { p.nonce = el(der.TagInteger, []byte{0x00, 0x01}) },
			},
			{
				name: "reports false for a nonce element that is not DER",
				give: func(p *tstParts) { p.nonce = cat([]byte{byte(der.TagInteger)}, notDER) },
			},
			{
				name: "reports false for a tsa without a GeneralName",
				give: func(p *tstParts) { p.tsa = el(der.ContextConstructed(0)) },
			},
			{
				name: "reports false for a tsa of two elements",
				give: func(p *tstParts) { p.tsa = el(der.ContextConstructed(0), el(der.TagNull), el(der.TagNull)) },
			},
			{
				name: "reports false for a tsa that is not DER",
				give: func(p *tstParts) { p.tsa = cat([]byte{byte(der.ContextConstructed(0))}, notDER) },
			},
			{
				name: "reports false for extensions that are not DER",
				give: func(p *tstParts) { p.extensions = cat([]byte{byte(der.ContextConstructed(1))}, notDER) },
			},
			{
				name: "reports false for an extension that is not a SEQUENCE",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSet, el(der.TagOID, oidUnknown)))
				},
			},
			{
				name: "reports false for an extension without an extnID",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOctetString)))
				},
			},
			{
				name: "reports false for an extnID that is not an OID in DER",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1),
						el(der.TagSequence, el(der.TagOID, []byte{0x80, 0x01}), el(der.TagOctetString)))
				},
			},
			{
				name: "reports false for a critical flag that is not a BOOLEAN in DER",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidUnknown),
						el(der.TagBoolean, []byte{0x01}), el(der.TagOctetString)))
				},
			},
			{
				name: "reports false for a critical element that is not DER",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidUnknown),
						cat([]byte{byte(der.TagBoolean)}, notDER)))
				},
			},
			{
				name: "reports false for an extension without an extnValue",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidUnknown)))
				},
			},
			{
				name: "reports false for an element after the extnValue",
				give: func(p *tstParts) {
					p.extensions = el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidUnknown),
						el(der.TagOctetString), el(der.TagNull)))
				},
			},
			{
				name: "reports false for an element after the extensions",
				give: func(p *tstParts) { p.extra = el(der.TagNull) },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseTSTInfo(tstInfoOf(tt.give))
				testkit.False(t, ok, "parseTSTInfo must refuse the TSTInfo")
			})
		}

		t.Run("reports false for a TSTInfo that is not a SEQUENCE", func(t *testing.T) {
			t.Parallel()
			_, ok := parseTSTInfo(el(der.TagSet))
			testkit.False(t, ok, "parseTSTInfo must refuse a SET")
		})

		t.Run("reports false for octets after the TSTInfo", func(t *testing.T) {
			t.Parallel()
			_, ok := parseTSTInfo(cat(tstInfoOf(), el(der.TagNull)))
			testkit.False(t, ok, "parseTSTInfo must refuse trailing octets")
		})
	})

	t.Run("Extension", func(t *testing.T) {
		t.Parallel()

		info := Info{extensions: cat(extCritical, extFalse, extPlain)}
		tests := []struct {
			name         string
			id           x509.OID
			value        []byte
			critical, ok bool
		}{
			{
				name:     "returns a critical extension",
				id:       oidOf([]byte{0x2a, 0x01}),
				value:    []byte("critical"),
				critical: true,
				ok:       true,
			},
			{
				name:  "returns an extension that states FALSE",
				id:    oidOf([]byte{0x2a, 0x02}),
				value: []byte("false"),
				ok:    true,
			},
			{
				name:  "returns an extension without the flag",
				id:    oidOf([]byte{0x2a, 0x03}),
				value: []byte("plain"),
				ok:    true,
			},
			{name: "reports false for an extension that the token lacks", id: oidOf([]byte{0x2a, 0x04})},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				value, critical, ok := info.Extension(tt.id)
				testkit.Equal(t, value, tt.value, "Extension must return the extnValue")
				testkit.Equal(t, critical, tt.critical, "Extension must report the critical flag")
				testkit.Equal(t, ok, tt.ok, "Extension must report whether the token has the extension")
			})
		}
	})
}

// tstParts is the elements of a TSTInfo that tstInfoOf writes, in their
// order. A nil element is left out. extra follows the extensions.
type tstParts struct {
	version, policy, imprint, serial, genTime         []byte
	accuracy, ordering, nonce, tsa, extensions, extra []byte
}

// tstInfoOf returns the DER of a TSTInfo that parseTSTInfo accepts, after
// edits change its parts.
func tstInfoOf(edits ...func(*tstParts)) []byte {
	p := tstParts{
		version: el(der.TagInteger, []byte{1}),
		policy:  el(der.TagOID, oidUnknown),
		imprint: el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidSHA256)), el(der.TagOctetString, tstDigest)),
		serial:  el(der.TagInteger, []byte{0x07}),
		genTime: el(der.TagGeneralizedTime, []byte("20261002120000Z")),
	}
	for _, edit := range edits {
		edit(&p)
	}

	return el(der.TagSequence, p.version, p.policy, p.imprint, p.serial, p.genTime,
		p.accuracy, p.ordering, p.nonce, p.tsa, p.extensions, p.extra)
}
