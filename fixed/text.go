// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import (
	"strconv"
	"strings"
)

// Parse returns the [Fixed64] denoted by s.
//
// The accepted grammar is exactly:
//
//	decimal = [ "-" ] int [ "." frac ]
//	int     = digit { digit }
//	frac    = digit { digit }
//	digit   = "0" … "9"
//
// Concretely: no leading "+", no exponent, no underscores, no
// surrounding whitespace, and at least one digit on each side of the
// point — ".5" and "1." are both [ErrSyntax]. "-0" parses to [Zero].
// This is a trust boundary, so the accepted language is part of the
// contract rather than a property of the implementation.
//
// A significant digit beyond the eighth decimal place is
// [ErrPrecision] rather than a silent truncation. Trailing zeroes are
// not significant, so "1.000000000" parses and "0.000000001" does
// not.
//
// Anything outside the grammar is [ErrSyntax]; a magnitude beyond
// [Max] is [ErrRange].
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

	// parseWhole guarantees whole <= maxWholeUnits and parseFrac
	// guarantees frac < 10^Scale, so this product and sum cannot wrap
	// a uint64 — the largest reachable raw is 9,223,372,036,899,999,999
	// against a uint64 ceiling of ~1.8e19. What remains reachable is
	// exceeding the DOMAIN bound, which is what is tested.
	raw := whole*scaleFactorU + frac
	if raw > maxRawU {
		return Zero, ErrRange
	}

	if neg {
		return -Fixed64(raw), nil
	}

	return Fixed64(raw), nil
}

// parseWhole reads the integer part as a count of whole units.
//
// The bound is tested after each accumulation, which is what keeps
// the return value inside maxWholeUnits rather than one digit past
// it. Testing before instead lets v reach maxWholeUnits*10+9, and
// that value times 10^Scale wraps a uint64 — so an input of twelve
// digits could wrap back under the domain bound and parse as a small
// number instead of being rejected.
//
// Testing after is also safe against wrapping in the accumulation
// itself: entering an iteration v is at most maxWholeUnits, so
// v*10+9 is at most 922,337,203,689 — nowhere near a uint64.
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
// A non-digit anywhere — including a second "." — is [ErrSyntax],
// which is what keeps the grammar closed without a separate scan.
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

		// Past the eighth place. A zero here carries no information
		// and is accepted; anything else is a digit the caller meant.
		if c != '0' {
			return 0, ErrPrecision
		}
	}

	for ; digits < Scale; digits++ {
		v *= 10
	}

	return v, nil
}

// String returns f rendered at all [Scale] places — "1.00000000",
// never "1".
//
// Rendering every place is what makes the round trip unconditional:
// for every in-domain f, Parse(f.String()) yields f again, and two
// renderings of one number cannot differ. Trimming would make the
// text shorter and the guarantee conditional.
//
// String is total. Unlike [Fixed64.MarshalText] it renders the
// out-of-contract math.MinInt64 rather than refusing it, because a
// diagnostic that fails when a value is unusual fails exactly when it
// is needed.
//
// # Allocation contract
//
// Allocates the result string.
func (f Fixed64) String() string {
	return string(appendDecimal(make([]byte, 0, maxTextLen), f))
}

// AppendText appends the [Fixed64.String] rendering of f to dst.
//
// Returns [ErrRange] for the out-of-contract math.MinInt64, so that
// nothing outside the domain reaches a wire or a log sink that a
// decoder will later reject. Implements [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has capacity for 21 more bytes.
func (f Fixed64) AppendText(dst []byte) ([]byte, error) {
	if f == outOfDomain {
		return dst, ErrRange
	}

	return appendDecimal(dst, f), nil
}

// MarshalText returns the [Fixed64.String] rendering of f.
//
// Because this exists, [encoding/json] encodes a Fixed64 as a JSON
// string rather than a number. That is deliberate: JSON numbers
// decode to float64 by default, so a numeric encoding would hand the
// value back to the type this package exists to displace, at the one
// boundary where it is hardest to notice.
//
// Implements [encoding.TextMarshaler].
func (f Fixed64) MarshalText() ([]byte, error) {
	return f.AppendText(make([]byte, 0, maxTextLen))
}

// UnmarshalText sets f to the value denoted by data.
//
// Accepts exactly what [Parse] accepts and returns exactly its
// errors, sharing one implementation — two decode paths that disagree
// about which inputs are valid is the defect this package exists to
// close. f is left unmodified when data is rejected.
//
// Implements [encoding.TextUnmarshaler].
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
// The fractional digits are emitted right-to-left into a stack array
// because they must be zero-padded to a fixed width, which
// [strconv.AppendUint] on the remainder alone would not do — 0.5 has
// a remainder of 50000000 but 0.00000005 has one of 5.
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
