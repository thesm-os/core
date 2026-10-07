// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
)

// listed are the statuses outside 500 to 599 that the documentation of
// StatusError gives a class.
var listed = []int{400, 401, 403, 404, 405, 406, 408, 409, 410, 411, 412, 413, 414, 415, 422, 425, 429}

func TestStatus(t *testing.T) {
	t.Parallel()

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the message of the status of the dependency", func(t *testing.T) {
			t.Parallel()
			err := &httpclient.StatusError{Dependency: dependency, Status: http.StatusServiceUnavailable}
			assert.Equal(t, err.Error(), "httpclient: registry: 503 Service Unavailable",
				"Error must name the package, the dependency and the status")
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
					expect.Equal(t, err.Class(), tt.want, "Class must return the class of the status")
					expect.Equal(t, errs.Classify(err), tt.want, "errs.Classify must return the class of the status")
				})
			}
		}

		t.Run("returns Transient for every status of 5xx but 501", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(status int) errs.Class {
				return (&httpclient.StatusError{Dependency: dependency, Status: status}).Class()
			}, func(int) errs.Class { return errs.Transient }, "a server error other than 501 must be Transient",
				prop.Using(prop.Integer(500, 599).Filter(func(status int) bool { return status != 501 })))
		})

		t.Run("returns Unspecified for every other status", func(t *testing.T) {
			t.Parallel()
			others := prop.Integer(math.MinInt, math.MaxInt).Filter(func(status int) bool {
				return !slices.Contains(listed, status) && (status < 500 || status > 599)
			})
			prop.Equal(t, func(status int) errs.Class {
				return (&httpclient.StatusError{Dependency: dependency, Status: status}).Class()
			}, func(int) errs.Class { return errs.Unspecified }, "a status without a listed class must be Unspecified",
				prop.Using(others))
		})
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
				assert.NoError(t, err, "New must accept the options")

				_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				se := assert.ErrorAs[*httpclient.StatusError](t, err, "Fetch must return a StatusError")
				expect.Equal(t, se.RetryAfter(), tt.want, "RetryAfter must return the delay of the header")

				delay, _ := errs.RetryAfter(err)
				expect.Equal(t, delay, tt.want, "errs.RetryAfter must return the delay of the header")
			})
		}
	})
}
