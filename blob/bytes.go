// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob

import (
	"bytes"
	"context"
	"errors"
	"io"

	"go.thesmos.sh/core/errs"
)

// Sentinel causes for the byte helpers' failures.
//
// The contract is the classification, not the cause. They are
// unexported so no consumer couples to this package's spelling, and
// they exist so this package's own tests can assert cause identity
// as well as class.
var (
	errBadLimit = errors.New("blob: a read limit must be positive")
	errTooLarge = errors.New("blob: object is larger than the read limit")
)

// PutBytes stores b under key. It is [Store.Put] over a reader the
// caller would otherwise construct.
//
// This seam streams because the kind does, and a small value still
// travels as a reader. PutBytes is where that cost is paid once
// rather than at every call site holding a configuration blob or a
// short document.
//
// # Allocation contract
//
// One reader wrapper, plus whatever the implementation allocates.
func PutBytes(ctx context.Context, s Store, key string, b []byte, opts PutOptions) (Info, error) {
	return s.Put(ctx, key, bytes.NewReader(b), opts)
}

// GetBytes reads the whole object under key and returns it with its
// metadata.
//
// limit bounds the read and must be positive. An object larger than
// limit returns an error classifying as
// [go.thesmos.sh/core/errs.Invalid] and no bytes, so a caller
// cannot be made to allocate without bound by an object it did not
// write. A limit at or below zero is the same class of error: it
// admits nothing, so it is a caller mistake rather than a policy.
//
// The reader is closed before GetBytes returns, whether or not the
// read succeeded.
//
// # Allocation contract
//
// One buffer the size of the object, plus whatever the
// implementation allocates.
func GetBytes(ctx context.Context, s Store, key string, limit int64) ([]byte, Info, error) {
	if limit <= 0 {
		return nil, Info{}, errs.WithClass(errBadLimit, errs.Invalid)
	}

	rc, info, err := s.Get(ctx, key)
	if err != nil {
		return nil, Info{}, err
	}

	b, readErr := io.ReadAll(io.LimitReader(rc, limit))

	// One byte past the limit decides whether the object was
	// truncated. Reading limit+1 up front would overflow on a limit
	// of math.MaxInt64 and silently return nothing.
	var over int
	if readErr == nil {
		var probe [1]byte
		over, _ = rc.Read(probe[:])
	}

	closeErr := rc.Close()

	if readErr != nil {
		return nil, Info{}, readErr //nolint:wrapcheck // the reader's own failure is the whole story
	}

	if closeErr != nil {
		return nil, Info{}, closeErr //nolint:wrapcheck // the reader's own failure is the whole story
	}

	if over > 0 {
		return nil, Info{}, errs.WithClass(errTooLarge, errs.Invalid)
	}

	return b, info, nil
}
