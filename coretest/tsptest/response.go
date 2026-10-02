// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

import (
	"slices"

	"go.thesmos.sh/core/internal/der"
)

// StatusResponse returns the DER of a TimeStampResp without a token, RFC
// 3161 section 2.4.2: the PKIStatus status, a statusString of texts when
// there are any, and a failInfo whose bits failBits are set when there
// are any. A test of a client builds the responses of an authority that
// grants no token with it.
//
// A negative status or bit number is a misuse that StatusResponse does not
// check: it writes the INTEGER of the status's two's complement, and a bit
// number below zero panics.
func StatusResponse(status int, failBits []int, texts ...string) []byte {
	b := der.NewBuilder(nil)
	resp := b.Open(der.TagSequence)
	info := b.Open(der.TagSequence)
	b.AddUint64(uint64(status)) //nolint:gosec // G115: a test passes a status that is not negative

	if len(texts) > 0 {
		free := b.Open(der.TagSequence)
		for _, t := range texts {
			b.Add(der.TagUTF8String, []byte(t))
		}

		b.Close(free)
	}

	if len(failBits) > 0 {
		b.Add(der.TagBitString, bitString(failBits))
	}

	b.Close(info)
	b.Close(resp)

	return b.Bytes()
}

// grantedResponse returns the DER of a TimeStampResp of the status granted
// and the token tok.
func grantedResponse(tok []byte) []byte {
	b := der.NewBuilder(nil)
	resp := b.Open(der.TagSequence)
	status := b.Open(der.TagSequence)
	b.AddUint64(statusGranted)
	b.Close(status)
	b.AddElement(tok)
	b.Close(resp)

	return b.Bytes()
}

// bitString returns the content of a BIT STRING in DER whose set bits are
// the bit numbers of bits: bit 0 is the most significant bit of the first
// octet, and the last octet contains the highest bit, so the BIT STRING
// has no trailing zero octet.
func bitString(bits []int) []byte {
	last := slices.Max(bits)
	octets := make([]byte, 1+last/8+1)
	octets[0] = byte(7 - last%8)

	for _, n := range bits {
		octets[1+n/8] |= 0x80 >> (n % 8)
	}

	return octets
}
