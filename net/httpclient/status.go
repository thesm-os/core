// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"net/http"
	"strconv"
	"time"

	"go.thesmos.sh/core/errs"
)

// The headers that the classification reads.
const (
	headerRetryAfter = "Retry-After"
	headerDate       = "Date"
)

// maxDelaySeconds is the longest delay of a Retry-After header in seconds
// that a time.Duration represents, math.MaxInt64 nanoseconds in whole
// seconds. A longer delay is cut to it.
const maxDelaySeconds = 9223372036

// StatusError is the error of a response with a status other than 2xx,
// under the default classification of a [Client]. Its class follows the
// status:
//
//   - 400, 405, 406, 411, 413, 414, 415 and 422: Invalid.
//   - 401 and 403: Denied.
//   - 404 and 410: NotFound.
//   - 409 and 412: Conflict.
//   - 408, 425 and 429: Transient.
//   - 501: Unsupported.
//   - Every other status of 500 to 599: Transient.
//   - Any other status: Unspecified.
//
// [StatusError.RetryAfter] returns the delay of the response's Retry-After
// header, which [go.thesmos.sh/core/errs.RetryAfter] reads, so a retrier
// waits for it.
//
// # Allocation contract
//
// A response that the classification refuses allocates its StatusError.
// [StatusError.Error] allocates the message.
type StatusError struct {
	// Dependency is the name of the client that received the response.
	Dependency string

	// Body is the first 1 KiB of the body of the response, which
	// [Client.Fetch] reads. [Client.Do] leaves the body to its caller, and
	// its errors have no Body.
	Body []byte

	// Status is the status code of the response.
	Status int

	// retryAfter is the delay of the Retry-After header of the response,
	// and 0 without one.
	retryAfter time.Duration
}

// Error returns the dependency and the status of e, such as
// "httpclient: registry: 503 Service Unavailable". It contains no text of
// the body.
func (e *StatusError) Error() string {
	return "httpclient: " + e.Dependency + ": " + strconv.Itoa(e.Status) + " " + http.StatusText(e.Status)
}

// Class returns the class of the status of e, as the documentation of
// StatusError states.
func (e *StatusError) Class() errs.Class {
	switch e.Status {
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusNotAcceptable,
		http.StatusLengthRequired, http.StatusRequestEntityTooLarge, http.StatusRequestURITooLong,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return errs.Invalid
	case http.StatusUnauthorized, http.StatusForbidden:
		return errs.Denied
	case http.StatusNotFound, http.StatusGone:
		return errs.NotFound
	case http.StatusConflict, http.StatusPreconditionFailed:
		return errs.Conflict
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return errs.Transient
	case http.StatusNotImplemented:
		return errs.Unsupported
	}

	if e.Status >= http.StatusInternalServerError && e.Status < 600 {
		return errs.Transient
	}

	return errs.Unspecified
}

// RetryAfter returns the delay of the Retry-After header of the response,
// and 0 without a header that delays.
func (e *StatusError) RetryAfter() time.Duration { return e.retryAfter }

// retryAfter returns the delay of the Retry-After header of h, in both
// forms of RFC 9110: a number of seconds, and an HTTP-date. A date counts
// from the Date header of h, so the clocks of the client and the server
// need not agree, and from now without one. It returns 0 for an absent or
// malformed header and for a date that has passed, and cuts a delay of
// seconds that a time.Duration cannot represent.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := h.Get(headerRetryAfter)
	if v == "" {
		return 0
	}

	if s, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Duration(min(max(s, 0), maxDelaySeconds)) * time.Second
	}

	at, err := http.ParseTime(v)
	if err != nil {
		return 0
	}

	if date, err := http.ParseTime(h.Get(headerDate)); err == nil {
		now = date
	}

	return max(at.Sub(now), 0)
}
