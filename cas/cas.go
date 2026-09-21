// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package cas is the content-addressed storage seam: a store whose
// keys ARE the digests of its values, so writes are idempotent,
// deduplication is inherent, and every read is verifiable against
// the address that requested it.
//
// # The kind
//
// cas answers the storage-kind discriminators as its own kind:
// absence is an error — an address is minted from bytes that
// existed; the content mints the key; and nothing mutates after a
// write.
//
// Listing, TTLs, deletion, and metadata are deliberately absent. A
// digest space has no prefix structure to enumerate; expiry
// contradicts address-implies-availability; deletion is garbage
// collection over a reachability set only the consumer knows, and
// erasure of meaning is the encryption layer's job — encrypt, then
// shred the key via [go.thesmos.sh/core/crypto.Destroyer]; content
// identity paired with names or provenance is consumer vocabulary.
//
// # Transfer
//
// The hot path is whole values. This kind holds small immutable
// records at high rates, and a streaming seam charges every one of
// those operations for a reader that no implementation can avoid: a
// streamed read allocates three times where a whole-value read
// allocates once, and [Store.Get] allocates nothing at all when the
// caller supplies a buffer.
//
// Large objects stream through the optional [Streamer] capability.
// [PutStream] and [GetStream] use a store's native streaming path
// when it has one and buffer when it does not, so both are correct
// against any Store and fast against the ones built for size.
//
// # Failure semantics
//
// Every failure returns an error classifying under
// [go.thesmos.sh/core/errs.Classify]: a put whose data does not
// hash to its address is Integrity, an absent address on read is
// NotFound, and the zero [crypto.Digest] — which is a sentinel, not
// an address — is Invalid from every method.
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
// The contract is the classification, not the cause. It is
// unexported so no consumer couples to this package's spelling, and
// it exists so this package's own tests can assert cause identity
// as well as class.
var errReadMismatch = errors.New("cas: bytes read do not hash to the address they were read under")

// Store is content-addressed storage over one hashing algorithm.
//
// The address is a [crypto.Digest] because a CAS address is a
// commitment, not a name. An assigned identifier — a
// [go.thesmos.sh/core/id.ID], a
// string key — is minted by someone and relates to its content by
// convention only; a digest is computed FROM the content, which is
// what makes Put's verification, idempotent writes, and verifiable
// reads expressible at all. A consumer that wants to NAME stored
// content keeps its own (name → digest) record beside this seam;
// naming and committing compose, and conflating them would reduce
// this store to a key-value seam with an unverifiable key.
//
// A Store is bound to exactly one [crypto.Hasher] at construction.
// One address space, one algorithm: a store that admitted two would
// give identical bytes two addresses, and deduplication — half the
// point of content addressing — would silently halve. An address
// produced by any other algorithm fails Put's verification and is
// absent for Get and Has.
//
// The zero [crypto.Digest] is not an address: it is the documented
// "no digest computed" sentinel — see [crypto.Digest.IsZero] — and
// every method rejects it with an error classifying as
// [go.thesmos.sh/core/errs.Invalid] before touching storage.
//
// # Context
//
// ctx cancels the call and bounds its duration. A method returns
// the context's error when it is already done, and abandons work in
// progress when it becomes done mid-call — a streamed transfer
// cancelled part-way commits nothing. It carries no authorisation
// and selects no backend; an implementation that reads values from
// it is adding vocabulary this seam does not define.
//
// # Fencing
//
// A Store implementation MAY be fenced: the fence epoch binds at
// handle construction and Put validates it atomically with the
// write, per the fence laws documented on
// [go.thesmos.sh/core/epoch.Admissible].
//
// # Concurrency
//
// Implementations must be safe for concurrent use. For concurrent
// Puts of the same address, exactly one reports wrote=true — the
// signal is an accounting primitive, and double-counting it
// double-charges whatever is metered against it.
type Store interface {
	// Hasher returns the algorithm this store's addresses are
	// computed under.
	//
	// A caller must mint an address before it can Put, and minting
	// requires the algorithm. Without this method the binding
	// travels beside the Store as a second parameter, and a Store
	// received through injection cannot be written to at all.
	Hasher() crypto.Hasher

	// Put stores data under its digest. It MUST verify that the
	// bound hasher's digest of data equals d, and return an error
	// classifying as [go.thesmos.sh/core/errs.Integrity] — storing
	// nothing — when they disagree. Returns wrote=false, with no
	// error, when the address is already present: re-putting
	// identical bytes is the idempotent no-op that makes CAS
	// retry-safe.
	Put(ctx context.Context, d crypto.Digest, data []byte) (wrote bool, err error)

	// Get appends the bytes stored under d to dst and returns the
	// extended slice. Pass nil for a fresh slice.
	//
	// The append shape exists because this is the hot path. A
	// caller reading at rate passes a pooled buffer sliced to zero
	// length and the read allocates nothing; a caller that passes
	// nil pays one allocation, as a plain byte-slice return always
	// would.
	//
	// The returned bytes are the caller's: implementations must not
	// alias internal storage into dst, and later Puts must not
	// affect what was already appended. An absent address returns an
	// error classifying as [go.thesmos.sh/core/errs.NotFound], and
	// dst is returned unchanged.
	//
	// Get does not verify. The bytes were verified when they were
	// put, and re-hashing every read costs more than the read. A
	// caller that does not trust the backend to return what it
	// stored checks with [Store.Hasher], or wraps a stream with
	// [Verify].
	Get(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error)

	// Has reports presence without transferring the body.
	Has(ctx context.Context, d crypto.Digest) (bool, error)
}

// Streamer is the optional large-object capability: transfer
// without holding the object.
//
// A store over a backend with no streaming primitive leaves this
// unimplemented. [PutStream] and [GetStream] then buffer, which is
// correct at any size and free at none, so absence costs a consumer
// memory rather than an error. A store used for objects larger than
// available memory MUST implement it.
//
// # Concurrency
//
// The same requirements as [Store]. The returned reader is not safe
// for concurrent use; each goroutine reads its own.
type Streamer interface {
	// PutStream reads r to EOF, hashing as it reads, and commits
	// the bytes under d only once they hash to d.
	//
	// A reader failure, a done context, or a digest mismatch MUST
	// leave d absent, even though the implementation consumed the
	// bytes. Staging the transfer and committing on agreement is
	// the implementation's affair; leaving no trace of a failed one
	// is the contract.
	//
	// PutStream MAY return (false, nil) without reading r when d is
	// already present. Not transferring a duplicate is the point of
	// content addressing, so the caller owns r and must not assume
	// it was consumed.
	PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (wrote bool, err error)

	// GetStream opens the bytes stored under d; the caller closes
	// the reader. An absent address returns an error classifying as
	// [go.thesmos.sh/core/errs.NotFound].
	//
	// The reader is not verified, for the reason [Store.Get] is
	// not. Wrap it with [Verify] when the backend is not trusted to
	// return what it stored.
	GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error)
}

// PutStream streams r into s under d.
//
// It uses s's native streaming path when s implements [Streamer].
// Otherwise it reads r into memory and calls [Store.Put], so a
// store used for objects larger than available memory must
// implement Streamer.
//
// # Allocation contract
//
// Nothing beyond what the implementation allocates on the native
// path. One buffer the size of the object on the fallback path.
func PutStream(ctx context.Context, s Store, d crypto.Digest, r io.Reader) (bool, error) {
	if st, ok := s.(Streamer); ok {
		return st.PutStream(ctx, d, r)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return false, err //nolint:wrapcheck // the reader's own failure is the whole story
	}

	return s.Put(ctx, d, data)
}

// GetStream opens the bytes stored under d; the caller closes the
// reader.
//
// It uses s's native streaming path when s implements [Streamer].
// Otherwise it reads the whole object through [Store.Get] and wraps
// it, so the fallback holds the object in memory for the reader's
// lifetime.
//
// # Allocation contract
//
// Nothing beyond what the implementation allocates on the native
// path. One buffer the size of the object plus one reader wrapper
// on the fallback path.
func GetStream(ctx context.Context, s Store, d crypto.Digest) (io.ReadCloser, error) {
	if st, ok := s.(Streamer); ok {
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
// the caller. That is inherent to streaming and is the trade a
// consumer accepts by not reading the object whole: stage a large
// read before acting on it.
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
// Integrity error for the terminal io.EOF when the digest
// disagrees. The failure latches so a caller that ignores the first
// one cannot then observe a clean EOF.
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
