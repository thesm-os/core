// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package cas is the content-addressed storage seam: a store whose
// keys are the digests of its values, so writes are idempotent,
// deduplication is inherent, and every read is verifiable against the
// address that requested it.
//
// # The kind
//
// Absence is an error, because an address is computed from bytes that
// existed. The content determines the key, and nothing changes after a
// write.
//
// The seam has no listing, expiry, deletion or metadata:
//
//   - A digest space has no prefix structure to enumerate.
//   - Expiry contradicts the rule that an address implies the value is
//     available.
//   - Deletion is garbage collection over a reachability set that only
//     the consumer knows. Erasing meaning is the encryption layer's
//     job: encrypt, then destroy the key through
//     [go.thesmos.sh/core/crypto.Destroyer].
//   - Content paired with a name or with provenance is consumer
//     vocabulary.
//
// # Transfer
//
// The hot path is whole values. This kind stores small immutable
// records at high rates, and a streaming seam would charge every
// operation for a reader. A streamed read allocates three times where
// a whole-value read allocates once, and [Store.Get] allocates nothing
// when the caller supplies a buffer.
//
// Large objects stream through the optional [Streamer] capability.
// [PutStream] and [GetStream] use a store's native streaming path when
// it has one and buffer when it does not, so both are correct against
// any Store and fast against a store built for size.
//
// # Decorators
//
// A decorator that wraps a Store, for example to add tracing, implements
// Unwrap() Store and returns the store it wraps. [AsStreamer] follows
// Unwrap to find a Streamer behind any number of decorators, so a
// decorator does not hide the capability of the store it wraps.
//
// # Failure semantics
//
// Every failure returns an error that classifies under
// [go.thesmos.sh/core/errs.Classify]. A put whose data does not hash
// to its address is Integrity, and an absent address on read is
// NotFound. The zero [crypto.Digest] is the uninitialised value and not
// an address, and every method rejects it as Invalid.
package cas

import (
	"bytes"
	"context"
	"errors"
	"io"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// errReadMismatch is the cause behind a verified reader's failure.
//
// The contract is the classification, not the cause. The error is
// unexported so no consumer couples to this package's spelling, and it
// exists so this package's own tests can assert cause identity as well
// as class.
var errReadMismatch = errors.New("cas: bytes read do not hash to the address they were read under")

// Store is content-addressed storage over one hashing algorithm.
//
// The address is a [crypto.Digest], computed from the content. An
// assigned identifier, such as a [go.thesmos.sh/core/id.ID] or a string
// key, relates to its content only by convention. A digest is what
// makes Put's verification, idempotent writes and verifiable reads
// expressible. A consumer that names stored content keeps its own
// record from name to digest beside this seam.
//
// A Store is bound to exactly one [crypto.Hasher] at construction. A
// store that admitted two algorithms would give identical bytes two
// addresses and lose deduplication. An address produced by any other
// algorithm fails Put's verification and is absent for Get and Has.
//
// The zero [crypto.Digest] is not an address, as [crypto.Digest.IsZero]
// documents. Every method rejects it with an error that classifies as
// [go.thesmos.sh/core/errs.Invalid], before it touches storage.
//
// # Context
//
// ctx cancels the call and bounds its duration. A method returns the
// context's error when it is already done, and abandons work in
// progress when it becomes done mid-call. A streamed transfer cancelled
// part-way commits nothing. The context authorises nothing and selects
// no backend. An implementation that reads values from it adds
// vocabulary this seam does not define.
//
// # Fencing
//
// A Store implementation MAY be fenced: the fence epoch binds at handle
// construction, and Put validates it atomically with the write, per the
// fence laws documented on [go.thesmos.sh/core/epoch.Admissible].
//
// # Concurrency
//
// Implementations must be safe for concurrent use. Of concurrent Puts
// of one address, exactly one reports wrote=true. The signal is an
// accounting primitive, and counting a write twice charges twice for
// whatever is metered against it.
type Store interface {
	// Hasher returns the algorithm this store's addresses are
	// computed under.
	//
	// A caller must compute an address before it can Put, and that
	// needs the algorithm. Without this method a caller would need
	// the algorithm as a second parameter beside the Store, and a
	// Store received through injection could not be written to.
	Hasher() crypto.Hasher

	// Put stores data under its digest. It MUST verify that the
	// bound hasher's digest of data equals d. When they disagree it
	// stores nothing and returns an error that classifies as
	// [go.thesmos.sh/core/errs.Integrity]. It returns wrote=false and
	// no error when the address is already present: putting identical
	// bytes again is the idempotent no-op that makes CAS safe to
	// retry.
	Put(ctx context.Context, d crypto.Digest, data []byte) (wrote bool, err error)

	// Get appends the bytes stored under d to dst and returns the
	// extended slice. Pass nil for a fresh slice.
	//
	// The append shape exists because this is the hot path. A
	// caller reading at rate passes a pooled buffer sliced to zero
	// length and the read allocates nothing. A caller that passes
	// nil pays one allocation, as a plain byte-slice return always
	// would.
	//
	// The returned bytes are the caller's: implementations must not
	// alias internal storage into dst, and later Puts must not
	// affect what was already appended. An absent address returns an
	// error that classifies as [go.thesmos.sh/core/errs.NotFound],
	// and dst is returned unchanged.
	//
	// Get does not verify. The bytes were verified when they were
	// put, and hashing every read costs more than the read. A caller
	// that does not trust the backend to return what it stored checks
	// with [Store.Hasher], or wraps a stream with [Verify].
	Get(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error)

	// Has reports presence without transferring the body.
	Has(ctx context.Context, d crypto.Digest) (bool, error)
}

// Streamer is the optional large-object capability: transfer without
// holding the object in memory.
//
// A store over a backend with no streaming primitive does not
// implement it. [PutStream] and [GetStream] then buffer the object, so
// a store without Streamer costs a consumer memory and never an error.
// A store used for objects larger than available memory MUST implement
// it.
//
// # Concurrency
//
// The same requirements as [Store]. The returned reader is not safe
// for concurrent use, and each goroutine reads its own.
type Streamer interface {
	// PutStream reads r to EOF, hashing as it reads, and commits
	// the bytes under d only once they hash to d.
	//
	// A reader failure, a done context or a digest mismatch MUST
	// leave d absent, even though the implementation consumed the
	// bytes. How the implementation stages the transfer is its own
	// affair. Leaving no trace of a failed transfer is the contract.
	//
	// PutStream MAY return (false, nil) without reading r when d is
	// already present, because not transferring a duplicate is the
	// point of content addressing. The caller owns r and must not
	// assume it was consumed.
	PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (wrote bool, err error)

	// GetStream opens the bytes stored under d, and the caller
	// closes the reader. An absent address returns an error that
	// classifies as [go.thesmos.sh/core/errs.NotFound].
	//
	// The reader is not verified, for the reason [Store.Get] is
	// not. Wrap it with [Verify] when the backend is not trusted to
	// return what it stored.
	GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error)
}

// AsStreamer returns the first [Streamer] in the chain that starts at s
// and follows each decorator's Unwrap() Store, and reports whether it
// found one.
//
// A decorator that implements Streamer itself is found before the
// store it wraps. A decorator's Unwrap must not return a value earlier
// in its own chain, or AsStreamer does not return, as errors.As does
// not for a cyclic error chain.
//
// # Allocation contract
//
// Zero alloc.
func AsStreamer(s Store) (Streamer, bool) {
	for s != nil {
		if st, ok := s.(Streamer); ok {
			return st, true
		}

		u, ok := s.(interface{ Unwrap() Store })
		if !ok {
			break
		}

		s = u.Unwrap()
	}

	return nil, false
}

// PutStream streams r into s under d.
//
// It uses the native streaming path when [AsStreamer] finds a
// [Streamer] in s. Otherwise it reads r into memory and calls
// [Store.Put], so a store used for objects larger than available
// memory must implement Streamer.
//
// # Allocation contract
//
// Nothing beyond what the implementation allocates on the native
// path. One buffer the size of the object on the fallback path.
func PutStream(ctx context.Context, s Store, d crypto.Digest, r io.Reader) (bool, error) {
	if st, ok := AsStreamer(s); ok {
		return st.PutStream(ctx, d, r)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return false, err //nolint:wrapcheck // returned as the reader produced it
	}

	return s.Put(ctx, d, data)
}

// GetStream opens the bytes stored under d, and the caller closes the
// reader.
//
// It uses the native streaming path when [AsStreamer] finds a
// [Streamer] in s. Otherwise it reads the whole object through
// [Store.Get] and wraps it, so the fallback holds the object in memory
// for the reader's lifetime.
//
// # Allocation contract
//
// Nothing beyond what the implementation allocates on the native
// path. One buffer the size of the object plus one reader wrapper
// on the fallback path.
func GetStream(ctx context.Context, s Store, d crypto.Digest) (io.ReadCloser, error) {
	if st, ok := AsStreamer(s); ok {
		return st.GetStream(ctx, d)
	}

	data, err := s.Get(ctx, d, nil)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

// Verify wraps rc so that reading it proves the bytes hash to d
// under h.
//
// The Read that would have returned [io.EOF] returns an error
// classifying as [go.thesmos.sh/core/errs.Integrity] instead when
// the digests disagree, and every Read after that returns the same
// error. A reader closed before EOF verifies nothing, because a
// digest over a prefix proves nothing about the whole.
//
// Verification therefore completes after the last byte has reached
// the caller. That is inherent to streaming, and a consumer that does
// not read the object whole accepts it. Stage a large read before
// acting on it.
//
// # Allocation contract
//
// One wrapper, plus one [crypto.Stream] borrowed from h's pool and
// returned on Close.
func Verify(h crypto.Hasher, d crypto.Digest, rc io.ReadCloser) io.ReadCloser {
	return &verifyReader{rc: rc, stream: h.NewStream(), want: d}
}

// verifyReader hashes what it passes through and compares at EOF.
type verifyReader struct {
	rc     io.ReadCloser
	stream crypto.Stream
	failed error
	want   crypto.Digest
}

// Read passes bytes through, hashing them, and substitutes an
// Integrity error for the terminal io.EOF when the digest disagrees.
// The failure latches, so a caller that ignores the first one cannot
// then observe a clean EOF.
func (v *verifyReader) Read(p []byte) (int, error) {
	if v.failed != nil {
		return 0, v.failed
	}

	n, err := v.rc.Read(p)

	// No length guard: hashing zero bytes changes nothing, and a
	// branch that cannot alter the result is a mutant no test can
	// kill.
	_, _ = v.stream.Write(p[:n])

	if errors.Is(err, io.EOF) && !v.stream.Sum().Equal(v.want) {
		v.failed = errs.WithClass(errReadMismatch, errs.Integrity)

		return n, v.failed
	}

	return n, err //nolint:wrapcheck // the wrapped reader's own failure passes through
}

// Close releases the hashing stream and closes the wrapped reader.
func (v *verifyReader) Close() error {
	v.stream.Close()

	return v.rc.Close() //nolint:wrapcheck // the wrapped reader's own failure passes through
}
