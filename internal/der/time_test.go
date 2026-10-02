// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

func TestTime(t *testing.T) {
	t.Parallel()

	t.Run("GeneralizedTime", func(t *testing.T) {
		t.Parallel()

		valid := []struct {
			name string
			give string
			want time.Time
		}{
			{
				name: "returns a time without a fraction",
				give: "19920521000000Z",
				want: time.Date(1992, 5, 21, 0, 0, 0, 0, time.UTC),
			},
			{
				name: "returns a time with one fractional digit",
				give: "19920722132100.3Z",
				want: time.Date(1992, 7, 22, 13, 21, 0, 300_000_000, time.UTC),
			},
			{
				name: "returns a time with nine fractional digits",
				give: "20261002235959.123456789Z",
				want: time.Date(2026, 10, 2, 23, 59, 59, 123_456_789, time.UTC),
			},
			{
				name: "returns the 29th of February of a leap year",
				give: "20240229120000Z",
				want: time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			},
		}
		for _, tt := range valid {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := der.GeneralizedTime([]byte(tt.give))
				testkit.True(t, ok, "GeneralizedTime must accept the DER form")
				testkit.True(t, got.Equal(tt.want), "GeneralizedTime must return "+tt.want.String())
				testkit.True(t, got.Location() == time.UTC, "GeneralizedTime must return UTC")
			})
		}

		malformed := []struct {
			name string
			give string
		}{
			{name: "reports false for a time without Z", give: "19920521000000"},
			{name: "reports false for a time without seconds", give: "199205210000Z"},
			{name: "reports false for a letter among the digits", give: "1992052100000aZ"},
			{name: "reports false for a full stop without digits", give: "19920521000000.Z"},
			{name: "reports false for a trailing zero of the fraction", give: "19920521000000.30Z"},
			{name: "reports false for a fraction of zero", give: "19920521000000.0Z"},
			{name: "reports false for ten fractional digits", give: "19920521000000.1234567891Z"},
			{name: "reports false for a comma before the fraction", give: "19920521000000,3Z"},
			{name: "reports false for a letter in the fraction", give: "19920521000000.3aZ"},
			{name: "reports false for month 13", give: "19921321000000Z"},
			{name: "reports false for month 0", give: "19920021000000Z"},
			{name: "reports false for the 30th of February", give: "20260230000000Z"},
			{name: "reports false for day 0", give: "19920500000000Z"},
			{name: "reports false for hour 24", give: "19920521240000Z"},
			{name: "reports false for minute 60", give: "19920521006000Z"},
			{name: "reports false for second 60", give: "19920521000060Z"},
			{name: "reports false for an offset instead of Z", give: "19920521000000+0000"},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := der.GeneralizedTime([]byte(tt.give))
				testkit.False(t, ok, "GeneralizedTime must refuse "+tt.give)
			})
		}
	})

	t.Run("AddGeneralizedTime", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give time.Time
			want string
		}{
			{
				name: "appends a whole second without a fraction",
				give: time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC),
				want: "20261002083000Z",
			},
			{
				name: "appends a fraction without trailing zeros",
				give: time.Date(2026, 10, 2, 8, 30, 0, 250_000_000, time.UTC),
				want: "20261002083000.25Z",
			},
			{
				name: "appends a time of another zone in UTC",
				give: time.Date(2026, 10, 2, 10, 30, 0, 0, time.FixedZone("CEST", 2*3600)),
				want: "20261002083000Z",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := der.NewBuilder(nil)
				b.AddGeneralizedTime(tt.give)

				r := der.NewReader(b.Bytes())
				content, ok := r.Read(der.TagGeneralizedTime)
				testkit.True(t, ok, "the element must be a GeneralizedTime")
				testkit.Equal(t, string(content), tt.want, "AddGeneralizedTime must append the DER form")

				got, ok := der.GeneralizedTime(content)
				testkit.True(t, ok && got.Equal(tt.give), "GeneralizedTime must read the time back")
			})
		}
	})
}

func BenchmarkTime(b *testing.B) {
	content := []byte("20261002235959.123456789Z")

	b.Run("GeneralizedTime", func(b *testing.B) {
		var sink time.Time

		derAllocs(b, func() { sink, sinkOK = der.GeneralizedTime(content) })
		testkit.True(b, sinkOK && sink.Nanosecond() == 123_456_789, "the benchmark must measure a valid time")
	})
}
