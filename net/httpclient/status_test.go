// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
)

func TestStatus(t *testing.T) {
	t.Parallel()

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the dependency and the status", func(t *testing.T) {
			t.Parallel()

			err := &httpclient.StatusError{Dependency: dependency, Status: http.StatusServiceUnavailable}
			testkit.Equal(t, err.Error(), "httpclient: registry: 503 Service Unavailable", "the message")
		})
	})

	t.Run("Class", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			statuses []int
			want     errs.Class
		}{
			{statuses: []int{400, 405, 406, 411, 413, 414, 415, 422}, want: errs.Invalid},
			{statuses: []int{401, 403}, want: errs.Denied},
			{statuses: []int{404, 410}, want: errs.NotFound},
			{statuses: []int{409, 412}, want: errs.Conflict},
			{statuses: []int{408, 425, 429, 500, 502, 503, 504, 599}, want: errs.Transient},
			{statuses: []int{501}, want: errs.Unsupported},
			{statuses: []int{300, 302, 418, 499, 600}, want: errs.Unspecified},
		}
		for _, tt := range tests {
			for _, status := range tt.statuses {
				t.Run("returns "+tt.want.String()+" for "+strconv.Itoa(status), func(t *testing.T) {
					t.Parallel()

					err := &httpclient.StatusError{Dependency: dependency, Status: status}
					testkit.Equal(t, err.Class(), tt.want, "the class")
					testkit.Equal(t, errs.Classify(err), tt.want, "the class under errs.Classify")
				})
			}
		}
	})

	t.Run("RetryAfter", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			header http.Header
			name   string
			want   time.Duration
		}{
			{
				name:   "returns the delay of a number of seconds",
				header: http.Header{"Retry-After": {"120"}},
				want:   2 * time.Minute,
			},
			{
				name:   "returns zero for a negative number of seconds",
				header: http.Header{"Retry-After": {"-5"}},
			},
			{
				name:   "cuts a number of seconds beyond the longest duration",
				header: http.Header{"Retry-After": {"99999999999"}},
				want:   9223372036 * time.Second,
			},
			{
				name:   "returns zero for a header that is no number and no date",
				header: http.Header{"Retry-After": {"soon"}},
			},
			{
				name: "returns zero without the header",
			},
			{
				name: "returns the delay of a date after the Date header",
				header: http.Header{
					"Date":        {origin.Add(time.Hour).Format(http.TimeFormat)},
					"Retry-After": {origin.Add(time.Hour + 90*time.Second).Format(http.TimeFormat)},
				},
				want: 90 * time.Second,
			},
			{
				name: "returns zero for a date before the Date header",
				header: http.Header{
					"Date":        {origin.Add(time.Hour).Format(http.TimeFormat)},
					"Retry-After": {origin.Format(http.TimeFormat)},
				},
			},
			{
				name: "returns the delay of a date after the clock without a Date header",
				header: http.Header{
					"Date":        nil,
					"Retry-After": {origin.Add(30 * time.Second).Format(http.TimeFormat)},
				},
				want: 30 * time.Second,
			},
			{
				name: "returns the delay of a date after the clock for a malformed Date header",
				header: http.Header{
					"Date":        {"yesterday"},
					"Retry-After": {origin.Add(45 * time.Second).Format(http.TimeFormat)},
				},
				want: 45 * time.Second,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// A nil value, such as the one of Date, removes the header that
				// net/http would add.
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					maps.Copy(w.Header(), tt.header)
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback)
				testkit.NoError(t, err, "New must accept the options")

				_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				se := testkit.ErrorAs[*httpclient.StatusError](t, err, "Fetch must return a StatusError")
				testkit.Equal(t, se.RetryAfter(), tt.want, "the delay")

				delay, _ := errs.RetryAfter(err)
				testkit.Equal(t, delay, tt.want, "the delay under errs.RetryAfter")
			})
		}
	})
}
