// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import (
	"strconv"
	"strings"
)

// Parse returns the [Fixed64] that s denotes.
//
// The accepted grammar is exactly:
//
//	decimal = [ "-" ] int [ "." frac ]
//	int     = digit { digit }
//	frac    = digit { digit }
//	digit   = "0" … "9"
//
// The grammar has no leading "+", no exponent, no underscore and no
// surrounding white space, and requires a digit on each side of the
// point, so ".5" and "1." are [ErrSyntax]. "-0" parses to [Zero].
// Parse is a trust boundary, and the grammar is part of its contract.
//
// Returns [ErrPrecision] for a significant digit beyond the eighth
// decimal place, which Parse never truncates. Trailing zeroes are not
// significant, so "1.000000000" parses and "0.000000001" does not.
// Returns [ErrSyntax] for any other text outside the grammar, and
// [ErrRange] for a magnitude beyond [Max].
//
// # Allocation contract
//
// Zero alloc.
func Parse(s string) (Fixed64, error) {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	intPart, fracPart, hasPoint := strings.Cut(s, ".")

	whole, err := parseWhole(intPart)
	if err != nil {
		return Zero, err
	}

	frac, err := parseFrac(fracPart, hasPoint)
	if err != nil {
		return Zero, err
	}

	// parseWhole returns at most maxWholeUnits and parseFrac less than
	// 10^Scale, so the product and the sum do not wrap a uint64. The
	// largest raw value here is 9,223,372,036,899,999,999, against a
	// uint64 maximum of about 1.8e19. The check below tests the bound
	// of the domain.
	raw := whole*scaleFactorU + frac
	if raw > maxRawU {
		return Zero, ErrRange
	}

	if neg {
		return -Fixed64(raw), nil
	}

	return Fixed64(raw), nil
}

// parseWhole reads the integer part as a count of whole units, and
// tests the bound after each digit. Its result is at most
// maxWholeUnits, whose product with 10^Scale fits a uint64. The
// accumulation fits too: v is at most maxWholeUnits when an iteration
// starts, so v*10+9 is at most 922,337,203,689.
func parseWhole(s string) (uint64, error) {
	if s == "" {
		return 0, ErrSyntax
	}

	var v uint64

	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrSyntax
		}

		v = v*10 + uint64(c-'0')
		if v > uint64(maxWholeUnits) {
			return 0, ErrRange
		}
	}

	return v, nil
}

// parseFrac reads the fractional part as a count of 10⁻⁸ units,
// left-aligned and zero-padded to [Scale] digits.
//
// It returns [ErrSyntax] for a non-digit anywhere in s, a second "."
// included, so the grammar does not need a separate scan.
func parseFrac(s string, hasPoint bool) (uint64, error) {
	if !hasPoint {
		return 0, nil
	}

	if s == "" {
		return 0, ErrSyntax
	}

	var (
		v      uint64
		digits int
	)

	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrSyntax
		}

		if digits < Scale {
			v = v*10 + uint64(c-'0')
			digits++

			continue
		}

		// A digit past the eighth place must be a zero. Any other
		// digit is one that the caller wrote, so it fails.
		if c != '0' {
			return 0, ErrPrecision
		}
	}

	for range Scale - digits {
		v *= 10
	}

	return v, nil
}

// String returns f at all [Scale] places, such as "1.00000000" for
// one, and never "1".
//
// Because String writes every place, Parse(f.String()) returns f for
// every f of the domain, and two renderings of one number are
// identical.
//
// String accepts every value, math.MinInt64 included, which
// [Fixed64.MarshalText] rejects, so a diagnostic of a value outside the
// domain still renders it.
//
// # Allocation contract
//
// Allocates the result string.
func (f Fixed64) String() string {
	return string(appendDecimal(make([]byte, 0, maxTextLen), f))
}

// AppendText appends the [Fixed64.String] rendering of f to dst.
//
// Returns [ErrRange] for math.MinInt64, which the domain excludes, so
// no text that a decoder rejects enters a wire format or a log.
// Implements [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for 21 more bytes.
func (f Fixed64) AppendText(dst []byte) ([]byte, error) {
	if err := f.valid(); err != nil {
		return dst, err
	}

	return appendDecimal(dst, f), nil
}

// MarshalText returns the [Fixed64.String] rendering of f.
//
// Through this method, [encoding/json] encodes a Fixed64 as a JSON
// string and not as a number. A JSON number decodes to a float64 by
// default, which would lose the exact value.
//
// Implements [encoding.TextMarshaler].
func (f Fixed64) MarshalText() ([]byte, error) {
	return f.AppendText(make([]byte, 0, maxTextLen))
}

// UnmarshalText sets f to the value that data denotes, through
// [Parse]. It accepts exactly what Parse accepts and returns its
// errors, so the two decode paths agree on every input. On an error f
// is unchanged. Implements [encoding.TextUnmarshaler].
func (f *Fixed64) UnmarshalText(data []byte) error {
	v, err := Parse(string(data))
	if err != nil {
		return err
	}

	*f = v

	return nil
}

// appendDecimal renders f at exactly [Scale] places.
//
// It writes the fractional digits right to left into a stack array,
// which pads them with zeroes to a fixed width: the remainder of 0.5 is
// 50000000, and that of 0.00000005 is 5. [strconv.AppendUint] on the
// remainder does not pad.
func appendDecimal(dst []byte, f Fixed64) []byte {
	if f < Zero {
		dst = append(dst, '-')
	}

	u := magnitude(f)
	dst = strconv.AppendUint(dst, u/scaleFactorU, 10)
	dst = append(dst, '.')

	var frac [Scale]byte

	rem := u % scaleFactorU
	for i := Scale - 1; i >= 0; i-- {
		frac[i] = byte('0' + rem%10)
		rem /= 10
	}

	return append(dst, frac[:]...)
}
