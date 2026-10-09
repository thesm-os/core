// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/internal/der"
)

// The Unix seconds of the first and the last second that a GeneralizedTime
// of four year digits encodes: 0000-01-01T00:00:00Z and
// 9999-12-31T23:59:59Z.
const (
	firstSecond = -62167219200
	lastSecond  = 253402300799
)

// utcTimes generates the instants in UTC from the first to the last
// nanosecond that a GeneralizedTime of four year digits encodes.
var utcTimes = prop.Composite(func(c *prop.Case) time.Time {
	seconds := c.Draw(prop.Integer[int64](firstSecond, lastSecond), "second")
	nanos := c.Draw(prop.Integer[int64](0, int64(time.Second)-1), "nanosecond")

	return time.Unix(seconds, nanos).UTC()
})

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
			{
				name: "returns the first second of a year",
				give: "20260101000000Z",
				want: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				name: "returns the last second of a year",
				give: "20261231235959Z",
				want: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			},
		}
		for _, tt := range valid {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := der.GeneralizedTime([]byte(tt.give))
				assert.True(t, ok, "GeneralizedTime must accept the DER form")
				assert.Equal(t, got, tt.want, "GeneralizedTime must return the time of the content in UTC")
			})
		}

		malformed := []struct {
			name string
			give string
		}{
			{name: "reports false for a time without Z", give: "19920521000000"},
			{name: "reports false for a lowercase z", give: "19920521000000z"},
			{name: "reports false for a time without seconds", give: "199205210000Z"},
			{name: "reports false for a letter among the digits", give: "1992052100000aZ"},
			{name: "reports false for a slash among the digits", give: "1992052100000/Z"},
			{name: "reports false for a full stop without digits", give: "19920521000000.Z"},
			{name: "reports false for a trailing zero of the fraction", give: "19920521000000.30Z"},
			{name: "reports false for a fraction of zero", give: "19920521000000.0Z"},
			{name: "reports false for ten fractional digits", give: "19920521000000.1234567891Z"},
			{name: "reports false for a comma before the fraction", give: "19920521000000,3Z"},
			{name: "reports false for a letter in the fraction", give: "19920521000000.3aZ"},
			{name: "reports false for month 13", give: "19921321000000Z"},
			{name: "reports false for month 0", give: "19920021000000Z"},
			{name: "reports false for the 30th of February", give: "20260230000000Z"},
			{name: "reports false for the 29th of February of a common year", give: "20260229000000Z"},
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
				assert.False(t, ok, "GeneralizedTime must refuse the content")
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
				assert.True(t, ok, "the element must be a GeneralizedTime")
				assert.Equal(t, string(content), tt.want, "AddGeneralizedTime must append the DER form")
			})
		}

		t.Run("appends a time that GeneralizedTime reads back", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(v time.Time) ([]byte, error) {
				b := der.NewBuilder(nil)
				b.AddGeneralizedTime(v)

				return b.Bytes(), nil
			}, func(element []byte) (time.Time, error) {
				r := der.NewReader(element)
				content, isTime := r.Read(der.TagGeneralizedTime)
				v, valid := der.GeneralizedTime(content)
				if !isTime || !valid {
					return time.Time{}, errRefused
				}

				return v, nil
			}, "GeneralizedTime must read back the time that AddGeneralizedTime appends", prop.Using(utcTimes))
		})
	})
}

// TestTimeAllocs checks the allocation contract of the GeneralizedTime
// decoder and encoder. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
func TestTimeAllocs(t *testing.T) {
	content := []byte("20261002235959.123456789Z")
	instant := time.Date(2026, 10, 2, 23, 59, 59, 123_456_789, time.UTC)
	dst := make([]byte, 0, 64)

	t.Run("GeneralizedTime", func(t *testing.T) {
		var got time.Time
		expect.MaxAllocs(t, func() { got, _ = der.GeneralizedTime(content) }, 0, "GeneralizedTime must not allocate")
		assert.Equal(t, got, instant, "the test must measure the time of the content")
	})

	t.Run("AddGeneralizedTime", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() {
			builder := der.NewBuilder(dst[:0])
			builder.AddGeneralizedTime(instant)
			got = builder.Bytes()
		}, 0, "AddGeneralizedTime into a slice with room must not allocate")
		assert.Length(t, got, 2+len(content), "the test must measure the whole element")
	})
}

// BenchmarkTime reports the cost of the GeneralizedTime decoder and
// encoder, and fails when one allocates.
func BenchmarkTime(b *testing.B) {
	content := []byte("20261002235959.123456789Z")
	instant := time.Date(2026, 10, 2, 23, 59, 59, 123_456_789, time.UTC)
	dst := make([]byte, 0, 64)

	b.Run("GeneralizedTime", func(b *testing.B) {
		var got time.Time

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = der.GeneralizedTime(content)
		}

		assert.Equal(b, got, instant, "the benchmark must measure the time of the content")
	})

	b.Run("AddGeneralizedTime", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			builder := der.NewBuilder(dst[:0])
			builder.AddGeneralizedTime(instant)
			got = builder.Bytes()
		}

		assert.Length(b, got, 2+len(content), "the benchmark must measure the whole element")
	})
}
