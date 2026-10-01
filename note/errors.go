// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// The functions of this package return these errors, wrapped with the
// detail of the failure. [ErrNote], [ErrKey] and [ErrType] classify as
// [errs.Invalid]: a malformed note, key or type never parses, so a retry
// of the same call fails the same way. [ErrUnknownType] classifies as
// [errs.Unsupported]: the type is well formed, and the caller's
// [Resolver] has no entry for it.
var (
	// ErrNote reports a malformed note or note text: invalid UTF-8, a
	// character below U+0020 other than newline, no blank line before the
	// signature lines, or a malformed signature line.
	ErrNote = errs.WithClass(errors.New("note: malformed note"), errs.Invalid)

	// ErrKey reports a malformed verifier key or key name, and a signer
	// whose key does not fit its type.
	ErrKey = errs.WithClass(errors.New("note: malformed key"), errs.Invalid)

	// ErrType reports a malformed signature type.
	ErrType = errs.WithClass(errors.New("note: malformed signature type"), errs.Invalid)

	// ErrUnknownType reports a signature type that a [Resolver] cannot
	// resolve: one without an entry, or one whose entry builds no Verifier
	// or a Verifier of another key.
	ErrUnknownType = errs.WithClass(errors.New("note: unknown signature type"), errs.Unsupported)
)
