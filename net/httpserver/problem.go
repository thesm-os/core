// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.thesmos.sh/core/errs"
)

// The headers of a problem details response.
const (
	headerContentLength = "Content-Length"
	headerContentType   = "Content-Type"
	headerNoSniff       = "X-Content-Type-Options"
	headerRetryAfter    = "Retry-After"

	// problemContentType is the media type of RFC 9457.
	problemContentType = "application/problem+json"

	// noSniff keeps a browser from reading the body as another type.
	noSniff = "nosniff"

	// blankType is the problem type of RFC 9457 for a problem that the
	// status describes.
	blankType = "about:blank"
)

// problem is a problem details object of RFC 9457 of the type
// about:blank, whose title is the text of its status.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
}

// problems maps every status that the server writes as problem details to
// its body, encoded once, so a response encodes nothing.
var problems = problemBodies(
	http.StatusBadRequest,
	http.StatusForbidden,
	http.StatusNotFound,
	http.StatusConflict,
	http.StatusRequestEntityTooLarge,
	http.StatusInternalServerError,
	http.StatusNotImplemented,
	http.StatusServiceUnavailable,
)

// problemBodies returns the encoded problem details of each status.
func problemBodies(statuses ...int) map[int][]byte {
	bodies := make(map[int][]byte, len(statuses))
	for _, status := range statuses {
		// json.Marshal of two strings and an int returns no error.
		body, _ := json.Marshal(problem{
			Type:   blankType,
			Title:  http.StatusText(status),
			Status: status,
		})
		bodies[status] = body
	}

	return bodies
}

// Error writes the problem details of RFC 9457 for err, with the status of
// its class. The response contains no text of err, because the text can
// contain what a client must not see. The status of each class:
//
//   - [errs.Invalid]: 400 Bad Request.
//   - [errs.Denied]: 403 Forbidden.
//   - [errs.NotFound]: 404 Not Found.
//   - [errs.Conflict]: 409 Conflict.
//   - [errs.Unsupported]: 501 Not Implemented.
//   - [errs.Transient]: 503 Service Unavailable, with a Retry-After header
//     of the delay that [errs.RetryAfter] reports, rounded up to seconds.
//   - [errs.Integrity] and [errs.Unspecified]: 500 Internal Server Error.
//
// An error that wraps an *http.MaxBytesError, the error of a body beyond
// the limit of [WithMaxBodyBytes], is 413 Content Too Large whatever its
// class.
//
// For a request of a [Server], Error records err, and the log record of the
// request contains it, at slog.LevelError for a status of 500 and above. A
// span of a status of 500 and above ends with err. When the handler has
// written the header of the response already, Error only records err. A
// request of another server receives the response, and nothing records
// err.
//
// Error is for the goroutine that serves r, as the ResponseWriter is.
//
// # Allocation contract
//
// Zero alloc of its own for a request of a Server: the values of the
// headers are in the request's exchange, and the bodies are encoded once.
// The header map of net/http allocates its storage at its first entry, as
// for any header that a handler sets. For a request of another server,
// Error allocates the storage of the header values.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := r.Context().Value(exchangeKey{}).(*exchange); ok {
		// The first error of the request is the one that its response
		// reports.
		e.mu.Lock()
		//dokimi:mutate-skip lcr-left: the chain reads err after it marks the request done, and no test can order a later Error between the two
		if e.err == nil && !e.done {
			e.err = err
		}
		e.mu.Unlock()

		if e.status != 0 {
			return
		}
	}

	status := statusOf(err)

	var delay time.Duration
	if status == http.StatusServiceUnavailable {
		delay, _ = errs.RetryAfter(err)
	}

	writeProblem(w, r, status, delay)
}

// statusOf returns the status that Error writes for err.
func statusOf(err error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge
	}

	switch errs.Classify(err) {
	case errs.Invalid:
		return http.StatusBadRequest
	case errs.Denied:
		return http.StatusForbidden
	case errs.NotFound:
		return http.StatusNotFound
	case errs.Conflict:
		return http.StatusConflict
	case errs.Unsupported:
		return http.StatusNotImplemented
	case errs.Transient:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// writeProblem writes the problem details of status to w for r, with a
// Retry-After header of retryAfter rounded up to seconds when retryAfter is
// positive. It removes a Content-Length that the handler set for another
// body, as http.Error does. status is a status of problems.
//
// The values of the headers are in the exchange of r. Each header refers to
// a value of its own with a capacity of 1, so a handler that adds a value
// to the header allocates a slice of its own and leaves the others intact.
// A request whose context contains no exchange, of another server or with a
// context that a middleware replaced, gets new storage.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, retryAfter time.Duration) {
	var values *[3]string
	if e, ok := r.Context().Value(exchangeKey{}).(*exchange); ok {
		values = &e.header
	} else {
		values = new([3]string)
	}

	values[0], values[1] = problemContentType, noSniff

	// The keys are canonical, so the map takes them as they are.
	h := w.Header()
	h.Del(headerContentLength)
	h[headerContentType] = values[0:1:1]
	h[headerNoSniff] = values[1:2:2]

	if retryAfter > 0 {
		// Whole seconds, rounded up, without the overflow of adding a
		// second to the longest duration. strconv formats a number below
		// 100 without an allocation.
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second != 0 {
			seconds++
		}

		values[2] = strconv.FormatInt(seconds, 10)
		h[headerRetryAfter] = values[2:3:3]
	}

	w.WriteHeader(status)
	_, _ = w.Write(problems[status]) //nolint:errcheck // the client went away, and the status is written
}
