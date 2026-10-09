// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import "context"

// AppendSigner is the optional capability of a [Signer] that writes its
// signature into a buffer that the caller supplies. A caller that reuses
// the buffer signs without allocating the signature.
//
// AppendSign reads message and writes dst only until it returns, so the
// caller reuses both as soon as it returns. A signer behind a process
// boundary that returns when ctx ends, while its operation continues,
// copies message first.
//
// The signers of this module implement it. The Ed25519 signer appends
// without an allocation, because [crypto/ed25519.Sign] returns a signature
// that the compiler keeps on the stack of its caller. The ML-DSA and ECDSA
// P-384 signers allocate what [crypto/mldsa.PrivateKey.Sign] and
// [crypto/ecdsa.SignASN1] allocate, since both return a new slice.
type AppendSigner interface {
	Signer

	// AppendSign appends a signature over message to dst and returns the
	// extended slice. It returns dst unchanged with an error, which wraps
	// context.Cause(ctx) when ctx ends before the signature is available.
	//
	//testkit:nondeterministic
	AppendSign(ctx context.Context, dst, message []byte) ([]byte, error)
}

// AppendSign appends the signature of s over message to dst and returns
// the extended slice.
//
// When [AsAppendSigner] finds an [AppendSigner] in s, AppendSign calls it.
// Otherwise it signs through [SignContext], so ctx bounds the wait of a
// [ContextSigner], and appends a copy of the signature.
//
// Returns dst unchanged and the error of the signer, or the cause of a
// context that ended.
//
// # Allocation contract
//
// What the AppendSigner allocates, which for the Ed25519 signer of this
// module and a dst with room for the signature is nothing. Without the
// capability, what SignContext allocates and the growth of dst.
func AppendSign(ctx context.Context, s Signer, dst, message []byte) ([]byte, error) {
	if as, ok := AsAppendSigner(s); ok {
		return as.AppendSign(ctx, dst, message)
	}

	sig, err := SignContext(ctx, s, message)
	if err != nil {
		return dst, err
	}

	return append(dst, sig...), nil
}
