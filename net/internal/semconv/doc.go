// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package semconv records the measurements of HTTP servers and clients
// under the OpenTelemetry semantic conventions for HTTP.
//
// go.thesmos.sh/core/net/httpserver and go.thesmos.sh/core/net/httpclient
// record the same instruments with the same attributes, so their names,
// the normalisation of a request method and the bound histograms of a
// duration are defined once, here.
//
// # Names
//
// The attribute keys, such as [RequestMethod] and [ResponseStatusCode],
// and the instrument names [ServerDuration] and [ClientDuration] are the
// names of the semantic conventions. [Method] maps a request method
// outside the methods of RFC 9110 and RFC 5789 to [OtherMethod], so a
// client that sends arbitrary methods cannot create a series per method.
//
// # Durations
//
// [Durations] records a duration in seconds into a histogram of the
// buckets that the conventions advise, bound once per attribute set. It
// keeps at most 2,048 attribute sets bound, and releases the set that its
// cache evicts.
//
// # Dependency position
//
// Imports context, net/http and time from the standard library, and
// go.thesmos.sh/core/cache, go.thesmos.sh/core/clock and
// go.thesmos.sh/core/telemetry from this module.
package semconv
