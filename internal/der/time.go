// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der

import "time"

// The shape of a GeneralizedTime in DER: YYYYMMDDhhmmss, an optional
// fraction of a second, and Z.
const (
	// wholeSeconds is the number of digits up to the seconds.
	wholeSeconds = 14

	// maxFraction is the largest number of digits of the fraction, one per
	// decimal place down to a nanosecond.
	maxFraction = 9

	// generalizedLayout is the layout of [time.Time.AppendFormat] that
	// writes the DER form: the fraction without trailing zeros, and none
	// for a whole second.
	generalizedLayout = "20060102150405.999999999Z"
)

// GeneralizedTime returns the time of content, the content of a
// GeneralizedTime in DER, in UTC. DER allows one form: YYYYMMDDhhmmss in
// digits, an optional fraction of a second of one to nine digits after a
// full stop, without a trailing zero, and the letter Z. GeneralizedTime
// reports false for any other content, and for a date or time that does
// not exist, such as 20260230 or a second of 60.
func GeneralizedTime(content []byte) (time.Time, bool) {
	if len(content) < wholeSeconds+1 || content[len(content)-1] != 'Z' {
		return time.Time{}, false
	}

	digits, ok := number(content[:wholeSeconds])
	if !ok {
		return time.Time{}, false
	}

	nanos, ok := fraction(content[wholeSeconds : len(content)-1])
	if !ok {
		return time.Time{}, false
	}

	year, month, day := digits/1e10, digits/1e8%100, digits/1e6%100
	hour, minute, second := digits/1e4%100, digits/1e2%100, digits%100

	// time.Date normalizes a field out of range, such as the 30th of
	// February to the 2nd of March, so a field that differs afterwards
	// names a date or time that does not exist.
	t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC)
	same := t.Year() == year && int(t.Month()) == month && t.Day() == day &&
		t.Hour() == hour && t.Minute() == minute && t.Second() == second
	if !same {
		return time.Time{}, false
	}

	return t, true
}

// number returns the value of digits, decimal digits only. It reports false
// for any other octet.
func number(digits []byte) (int, bool) {
	v := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}

		v = 10*v + int(c-'0')
	}

	return v, true
}

// fraction returns the nanoseconds of f, the fraction of a GeneralizedTime
// between its seconds and its Z: empty, or a full stop and one to nine
// digits whose last digit is not zero.
func fraction(f []byte) (int, bool) {
	if len(f) == 0 {
		return 0, true
	}

	if f[0] != '.' || len(f) == 1 || len(f) > maxFraction+1 || f[len(f)-1] == '0' {
		return 0, false
	}

	v, ok := number(f[1:])
	for range maxFraction + 1 - len(f) {
		v *= 10
	}

	return v, ok
}

// AddGeneralizedTime appends a GeneralizedTime of t in UTC, in the DER form
// that [GeneralizedTime] reads. t has a year from 0 to 9999.
func (b *Builder) AddGeneralizedTime(t time.Time) {
	at := b.Open(TagGeneralizedTime)
	b.b = t.UTC().AppendFormat(b.b, generalizedLayout)
	b.Close(at)
}
