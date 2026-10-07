// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"

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
// metadata, as [AppendBytes] does for a nil dst. limit bounds the read as
// AppendBytes states, and every error returns no bytes.
//
// # Allocation contract
//
// One buffer, with room for the object and [bytes.MinRead] more bytes,
// plus what the store allocates. For a size of -1, the buffer grows in
// steps, as AppendBytes states.
func GetBytes(ctx context.Context, s Store, key string, limit int64) ([]byte, Info, error) {
	return AppendBytes(ctx, s, key, nil, limit)
}

// AppendBytes reads the whole object under key, appends it to dst, and
// returns the extended slice with the metadata of the object.
//
// limit bounds the read and must be positive, so a caller cannot be made
// to allocate without bound by an object that it did not write.
// AppendBytes reads at most limit bytes of the body, and then reads once
// more into the capacity of the buffer past its length. A byte there makes
// the object larger than limit. When the store reports the size of the
// object, AppendBytes refuses a size above limit before it reads the body,
// so limit also bounds the buffer that AppendBytes allocates for the size.
//
// The body is closed before AppendBytes returns, also after a read that
// fails. When the object is replaced or deleted during the read,
// AppendBytes can return an error that wraps
// [go.thesmos.sh/core/version.ErrMismatch], as [Store.Get] permits. The
// caller then reads the object again.
//
// Error modes, each with dst unchanged and the zero Info:
//
//   - A limit at or below zero classifies as
//     [go.thesmos.sh/core/errs.Invalid]. It admits nothing, so it is a
//     mistake of the caller and not a policy.
//   - An object larger than limit classifies as errs.Invalid.
//   - The errors of [Store.Get], of a read of the body and of its Close
//     return unwrapped.
//
// AppendBytes reads into the capacity of dst past its length, so those
// bytes are unspecified after an error.
//
// # Allocation contract
//
// Allocates nothing beyond what the store allocates when dst has room for
// the object and [bytes.MinRead] more bytes. Otherwise it grows dst once,
// to room for the size that the store reports and bytes.MinRead more
// bytes. For a size of -1, it grows dst in steps of at least bytes.MinRead
// bytes.
func AppendBytes(ctx context.Context, s Store, key string, dst []byte, limit int64) ([]byte, Info, error) {
	if limit <= 0 {
		return dst, Info{}, errs.WithClass(errBadLimit, errs.Invalid)
	}

	rc, info, err := s.Get(ctx, key)
	if err != nil {
		return dst, Info{}, err
	}

	if info.Size > limit {
		// The size refuses the object, so the error of Close does not
		// change the result.
		_ = rc.Close()

		return dst, Info{}, errs.WithClass(errTooLarge, errs.Invalid)
	}

	// A reported size grows the buffer once, with bytes.MinRead bytes of
	// room for the read that sees the end of the body. limit bounds the
	// size, so it fits an int on a 64-bit platform. A size of -1 grows the
	// buffer as the first step of the loop does.
	b := slices.Grow(dst, max(int(info.Size), 0)+bytes.MinRead)

	// The reader is a local value whose Read the loop calls directly, so it
	// does not escape to the heap.
	lr := io.LimitedReader{R: rc, N: limit}
	for err == nil {
		b = slices.Grow(b, bytes.MinRead)

		var n int
		n, err = lr.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
	}

	// The read past the limit finds an object larger than limit, where a
	// reader of limit+1 bytes would overflow at a limit of math.MaxInt64.
	// The loop grows the buffer before each read. A last read that the
	// limit ends returns no bytes, so the read past the limit has room. A
	// last read that the body ends can fill the buffer, and the read past
	// the limit then returns no bytes, as at the end of any body.
	var over int
	if errors.Is(err, io.EOF) {
		err = nil
		over, _ = rc.Read(b[len(b):cap(b)])
	}

	closeErr := rc.Close()

	if err != nil {
		return dst, Info{}, err //nolint:wrapcheck // AppendBytes returns the error of the body unwrapped
	}

	if closeErr != nil {
		return dst, Info{}, closeErr //nolint:wrapcheck // AppendBytes returns the error of the body unwrapped
	}

	if over > 0 {
		return dst, Info{}, errs.WithClass(errTooLarge, errs.Invalid)
	}

	return b, info, nil
}
