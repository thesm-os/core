// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"context"
	"time"

	"go.thesmos.sh/core/rand"
)

// Keeper wraps and unwraps data encryption keys.
//
// The data key crosses this boundary, and the key protecting it does
// not. That lets a hardware module or a hosted service whose root key
// cannot be exported implement Keeper. An interface that returned root
// key material would exclude both.
//
// Keeper is the custodian half of envelope encryption. A per-object
// data key encrypts the payload through an [AEAD], and the Keeper
// protects the data key. No payload passes through a Keeper.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
//
// # Allocation contract
//
// Unspecified. Every method of a hosted or hardware Keeper crosses a
// process boundary, and that round trip costs more than any
// allocation. Only an in-memory implementation could meet an
// allocation bound, so the interface does not set one.
type Keeper interface {
	// KeyID names the wrapping key in the custodian's own form: a
	// resource identifier, a slot number or a label. Persist it with
	// each wrapped key, so material wrapped before a rotation can
	// still be unwrapped after one.
	//
	// core cannot derive the name and does not reshape it, so it is a
	// string and not an [ID] or a [crypto/sign.KeyID].
	KeyID() string

	// Wrap encrypts a data key.
	//
	// Implementations must not wrap deterministically. Two calls with
	// the same data key must produce different bytes. Otherwise a
	// holder of two wrapped keys could tell whether the underlying
	// data keys are equal without unwrapping either.
	Wrap(ctx context.Context, dek []byte) ([]byte, error)

	// Unwrap decrypts a data key. Material wrapped under a different
	// key, or corrupted in any position, returns an error and never
	// a wrong key.
	Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}

// Destroyer is the optional capability for custodians that can destroy
// a wrapping key. It is the primitive underneath erasure of
// encrypted-at-rest data. The payload remains in place and becomes
// unreadable, so erasure also covers data that is replicated, backed
// up or on tape.
//
// Hosted custodians destroy in two steps. AWS KMS schedules deletion
// 7 to 30 days ahead, and Cloud KMS keeps a destroyed key version for
// 30 days by default. Both refuse cryptographic operations under the
// key from the moment destruction is scheduled, and both let an
// administrator cancel it until the wait ends. Destroy reports when
// the destruction becomes irreversible, and an attestation of erasure
// records that time.
//
// A caller that must provide erasure asserts for Destroyer when it is
// wired, and fails at once when the assertion fails. A custodian that
// cannot destroy cannot provide erasure. The failure belongs in
// configuration, before the caller accepts a deletion request it
// cannot honour.
type Destroyer interface {
	Keeper

	// Destroy schedules the named wrapping key for destruction and
	// returns the time at which the destruction becomes irreversible.
	//
	// From the moment Destroy returns, Unwrap under keyID fails with
	// [ErrKeyDestroyed], on this and every other instance. Before the
	// returned time an administrator of the custodian may still be
	// able to cancel the destruction. A custodian that destroys at
	// once returns the time of the call.
	//
	// The zero time means the destruction is scheduled and the
	// custodian cannot yet say when it becomes irreversible. AWS KMS
	// returns no deletion date for a multi-Region primary key while
	// its replicas remain. The caller calls Destroy again later.
	//
	// Destroy is idempotent. For a key already scheduled or destroyed,
	// it returns the time already set, or the zero time while that is
	// still unknown, and a nil error. A caller can retry after a
	// failed call without losing the time.
	//
	// Destroying a key the custodian does not have returns
	// [ErrKeyID]. Success would leave a caller believing data was
	// erased when it was not.
	Destroy(ctx context.Context, keyID string) (time.Time, error)
}

// KeyGenerator is the optional capability for custodians that can
// generate a data key internally and return it wrapped.
//
// Hosted custodians offer this path, and it is the stronger one. The
// plaintext data key comes from the custodian's entropy source and not
// the caller's. A caller that only encrypts can discard the plaintext
// at once and keeps no key material it does not need.
type KeyGenerator interface {
	Keeper

	// GenerateKey returns a fresh data key of size bytes, both in the
	// clear and wrapped under this Keeper's key. Callers that only
	// encrypt should zero plaintext as soon as the payload is sealed.
	//
	// A non-positive size is an error.
	GenerateKey(ctx context.Context, size int) (plaintext, wrapped []byte, err error)
}

// GenerateKey returns a fresh data key of size bytes, in the clear and
// wrapped under k.
//
// When k implements [KeyGenerator], the custodian generates the key
// and r is not read. Otherwise GenerateKey reads size bytes from r and
// wraps them with [Keeper.Wrap]. A caller gets the stronger path
// whenever the custodian offers it, without asserting for the
// capability itself. Callers that only encrypt should zero plaintext
// as soon as the payload is sealed.
//
// Returns [ErrKeySize] for a non-positive size on the r path, and the
// custodian's own error on the KeyGenerator path. An error from r or
// from Wrap is returned as it is. No key material accompanies an
// error, because GenerateKey zeroes the bytes it read before it
// returns.
//
// # Allocation contract
//
// On the r path, one allocation for plaintext plus whatever Wrap
// allocates. On the KeyGenerator path, whatever the custodian
// allocates.
func GenerateKey(ctx context.Context, k Keeper, r rand.Rand, size int) (plaintext, wrapped []byte, err error) {
	if g, ok := k.(KeyGenerator); ok {
		return g.GenerateKey(ctx, size) //nolint:wrapcheck // returned as the custodian produced it
	}

	if size <= 0 {
		return nil, nil, ErrKeySize
	}

	plaintext = make([]byte, size)
	if _, err = r.Read(plaintext); err != nil {
		clear(plaintext)

		return nil, nil, err //nolint:wrapcheck // returned as the source produced it
	}

	wrapped, err = k.Wrap(ctx, plaintext)
	if err != nil {
		clear(plaintext)

		return nil, nil, err //nolint:wrapcheck // returned as the custodian produced it
	}

	return plaintext, wrapped, nil
}
