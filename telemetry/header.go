// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"maps"
	"slices"
)

// keyBuffer is the length of the longest key whose canonical form
// [HeaderCarrier.Get] and [HeaderCarrier.Set] build on the stack. No header
// of a propagator in this module is longer. A longer key costs one more
// allocation per call.
const keyBuffer = 64

// interned maps the canonical keys of the headers of the W3C Trace Context,
// which a propagator writes for every request, to strings that
// [HeaderCarrier.Set] stores without building a key of its own.
var interned = map[string]string{
	"Traceparent": "Traceparent",
	"Tracestate":  "Tracestate",
}

// HeaderCarrier is a [Carrier] over the headers of an HTTP message. Its type
// is the type of net/http's Header, so a header converts to it without a
// copy:
//
//	parent, ok := propagator.Extract(ctx, telemetry.HeaderCarrier(r.Header))
//
// Each method canonicalises its key as net/textproto's
// CanonicalMIMEHeaderKey does: the first letter and every letter after a
// hyphen in upper case, the others in lower case. "traceparent" therefore
// reads and writes the header that net/http stores under "Traceparent". A
// key that contains a byte outside the token characters of RFC 9110, such
// as a space or a colon, is used as it is, as CanonicalMIMEHeaderKey
// returns it.
//
// HeaderCarrier does not import net/http, so a package that propagates a
// trace over another protocol does not link the HTTP stack.
//
// # Concurrency
//
// Not safe for concurrent use, as the map under it is not. Get is safe for
// concurrent use while nothing writes to the map.
//
// # Allocation contract
//
//   - [HeaderCarrier.Get] does not allocate for a key of at most 64 bytes.
//   - [HeaderCarrier.Set] allocates the slice of the value, and the string of
//     a key that is not canonical, apart from traceparent and tracestate,
//     whose canonical keys it interns.
//   - [HeaderCarrier.Keys] allocates the slice that it returns.
type HeaderCarrier map[string][]string

// Compile-time proof that HeaderCarrier satisfies the seam.
var _ Carrier = HeaderCarrier(nil)

// Get returns the first value of the header key, or the empty string when
// the carrier has no value for it. A nil HeaderCarrier has no values.
//
// Get canonicalises a key that is not canonical in a buffer on its stack,
// and looks it up without converting it to a string.
func (c HeaderCarrier) Get(key string) string {
	var (
		buf    [keyBuffer]byte
		values []string
	)

	if k, ok := canonical(key, buf[:0]); ok {
		values = c[string(k)]
	} else {
		values = c[key]
	}

	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// Set replaces every value of the header key with value, under the
// canonical form of key. It looks the canonical form up among the interned
// keys without converting it to a string, and builds a string only for a
// key that is not interned.
//
// Set on a nil HeaderCarrier panics, as a write to a nil map does. A
// propagator writes into the headers of a request that net/http built,
// which are never nil.
func (c HeaderCarrier) Set(key, value string) {
	var buf [keyBuffer]byte

	if k, ok := canonical(key, buf[:0]); ok {
		if s, found := interned[string(k)]; found {
			key = s
		} else {
			key = string(k)
		}
	}

	c[key] = []string{value}
}

// Keys returns the key of every header in the carrier, in unspecified
// order, and nil for a carrier without headers.
func (c HeaderCarrier) Keys() []string { return slices.Collect(maps.Keys(c)) }

// canonical appends the canonical form of key to dst in one pass, returns
// the result, and reports whether it differs from key. It reports false for
// a key that is canonical already, and for a key with a byte outside the
// token characters of RFC 9110, which the carrier uses as it is. The token
// characters are the letters, the digits and !#$%&'*+-.^_`|~.
func canonical(key string, dst []byte) ([]byte, bool) {
	changed, upper := false, true
	for i := range len(key) {
		c := key[i]
		lower, capital := 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z'

		token := lower || capital || '0' <= c && c <= '9'
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			token = true
		}

		if !token {
			return dst, false
		}

		if upper && lower {
			c -= 'a' - 'A'
			changed = true
		} else if !upper && capital {
			c += 'a' - 'A'
			changed = true
		}

		dst = append(dst, c)
		upper = c == '-'
	}

	return dst, changed
}
