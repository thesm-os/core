// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package semconv

import "net/http"

// The attribute keys of the semantic conventions for HTTP that the server
// and the client record.
const (
	// RequestMethod is the method of a request, as [Method] normalises it.
	RequestMethod = "http.request.method"

	// ResponseStatusCode is the status code of a response.
	ResponseStatusCode = "http.response.status_code"

	// Route is the template of the path that a server matched, such as
	// /items/{id}.
	Route = "http.route"

	// URLScheme is the scheme of a request that a server received: http or
	// https.
	URLScheme = "url.scheme"

	// ServerAddress is the host that a client called.
	ServerAddress = "server.address"

	// ServerPort is the port that a client called.
	ServerPort = "server.port"

	// ErrorType is the type of the error of a request that failed. The
	// client records the class of the error, such as Transient.
	ErrorType = "error.type"
)

// OtherMethod is the value of [RequestMethod] for a method that [Method]
// does not return as it is.
const OtherMethod = "_OTHER"

// Method returns m when it is a method of RFC 9110 or PATCH of RFC 5789,
// and [OtherMethod] otherwise. Methods are case-sensitive, so "get" is
// [OtherMethod]. A server records the method that a client sent, so the
// conventions bound the values of the attribute to these ten.
//
// # Allocation contract
//
// Zero alloc.
func Method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	default:
		return OtherMethod
	}
}
