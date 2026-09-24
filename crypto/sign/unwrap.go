// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

// AsStreamingSigner returns the first [StreamingSigner] in the chain
// that starts at s and follows each decorator's Unwrap, and reports
// whether it found one.
//
// A decorator that wraps a [Signer] implements Unwrap() Signer, and a
// decorator that wraps a [Verifier] implements Unwrap() Verifier. The
// As functions follow either, so a decorator does not hide the
// capability of the value it wraps. A decorator that implements the
// capability itself is found before the value it wraps. A decorator's
// Unwrap must not return a value earlier in its own chain.
//
// # Allocation contract
//
// Zero alloc.
func AsStreamingSigner(s Signer) (StreamingSigner, bool) {
	return find[StreamingSigner](s)
}

// AsStreamingVerifier returns the first [StreamingVerifier] in the
// chain that starts at v and follows each decorator's Unwrap, and
// reports whether it found one. It follows the rules of
// [AsStreamingSigner].
//
// # Allocation contract
//
// Zero alloc.
func AsStreamingVerifier(v Verifier) (StreamingVerifier, bool) {
	return find[StreamingVerifier](v)
}

// AsContextSigner returns the first [ContextSigner] in the chain that
// starts at s and follows each decorator's Unwrap, and reports whether
// it found one. It follows the rules of [AsStreamingSigner].
//
// # Allocation contract
//
// Zero alloc.
func AsContextSigner(s Signer) (ContextSigner, bool) {
	return find[ContextSigner](s)
}

// find returns the first value of type T in the chain that starts at v
// and follows Unwrap() Signer or Unwrap() Verifier.
func find[T any](v Verifier) (T, bool) {
	for v != nil {
		if t, ok := v.(T); ok {
			return t, true
		}

		switch u := v.(type) {
		case interface{ Unwrap() Signer }:
			v = u.Unwrap()
		case interface{ Unwrap() Verifier }:
			v = u.Unwrap()
		default:
			v = nil
		}
	}

	var zero T

	return zero, false
}
