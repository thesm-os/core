// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/localkey"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// origin is the start time of the fake clock of every local keeper of the
// tests.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// scheduled is a custodian that destroys in two steps, as hosted key services
// do. The key stops unwrapping at once, and the first Destroy returns the zero
// time because the custodian does not yet know when the destruction becomes
// irreversible. Later calls return a time 30 days ahead.
type scheduled struct {
	*localkey.Keeper

	calls atomic.Int32
}

// Destroy destroys the key of the embedded Keeper, and returns the zero time
// on the first call and a time 30 days after origin on every later call.
func (s *scheduled) Destroy(ctx context.Context, keyID string) (time.Time, error) {
	if _, err := s.Keeper.Destroy(ctx, keyID); err != nil {
		return time.Time{}, err
	}

	if s.calls.Add(1) == 1 {
		return time.Time{}, nil
	}

	return origin.Add(30 * 24 * time.Hour), nil
}

// ignoringAAD is a local keeper that binds no aad: WrapAAD and UnwrapAAD pass
// empty aad whatever aad the caller passes.
type ignoringAAD struct {
	*localkey.Keeper
}

// WrapAAD wraps dek with empty aad.
func (k ignoringAAD) WrapAAD(ctx context.Context, dek, _ []byte) ([]byte, error) {
	return k.Keeper.WrapAAD(ctx, dek, nil)
}

// UnwrapAAD unwraps wrapped with empty aad.
func (k ignoringAAD) UnwrapAAD(ctx context.Context, wrapped, _ []byte) ([]byte, error) {
	return k.Keeper.UnwrapAAD(ctx, wrapped, nil)
}

// TestKeeper checks that a local keeper passes the checks of the suite of
// crypto.Keeper and the contract of each capability, and that the contract of
// each capability fails for a custodian that breaks it. The second instance of
// a check is another local keeper of the same root key.
func TestKeeper(t *testing.T) {
	t.Parallel()

	newKeeper := func(h *cryptotest.KeeperHandle) crypto.Keeper { return newLocal(h) }
	cryptotest.RunKeeperSuiteFromHandle(t, newKeeper, conform.Second(newKeeper))

	t.Run("AssertDestroyerContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for a custodian that destroys at once", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertDestroyerContract(t, newLocal(t))
		})

		t.Run("passes for a custodian that learns the time after the first call", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertDestroyerContract(t, &scheduled{Keeper: newLocal(t)})
		})

		t.Run("fails for a custodian whose Destroy keeps the key", func(t *testing.T) {
			t.Parallel()
			keeps := func(context.Context, string) (time.Time, error) { return origin, nil }
			d := cryptotest.NewDestroyerStub(t, cryptotest.DestroyerStubDelegateTo(newLocal(t)),
				cryptotest.WithDestroyerDestroy(keeps))
			got := assert.Rejects(t, "a Destroy that keeps the key fails the contract", func(tb assert.TB) {
				cryptotest.AssertDestroyerContract(tb, d)
			})
			assert.Length(t, got, 1, "the contract fails once")
			assert.Equal(t, got[0].Contract, "material does not unwrap after Destroy of its wrapping key",
				"the contract fails at the Unwrap after Destroy")
		})
	})

	t.Run("AssertKeyGeneratorContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for a local keeper", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertKeyGeneratorContract(t, newLocal(t))
		})

		t.Run("fails for a custodian that generates one data key twice", func(t *testing.T) {
			t.Parallel()
			local := newLocal(t)
			plaintext, wrapped, err := local.GenerateKey(t.Context(), 32)
			assert.NoError(t, err, "GenerateKey generates a data key")
			g := cryptotest.NewKeyGeneratorStub(t, cryptotest.KeyGeneratorStubDelegateTo(local),
				cryptotest.WithKeyGeneratorGenerateKey(func(context.Context, int) ([]byte, []byte, error) {
					return plaintext, wrapped, nil
				}))
			got := assert.Rejects(t, "a custodian that repeats its data key fails the contract", func(tb assert.TB) {
				cryptotest.AssertKeyGeneratorContract(tb, g)
			})
			assert.Length(t, got, 1, "the contract fails once")
			assert.Equal(t, got[0].Contract, "two calls of GenerateKey return different data keys",
				"the contract fails at the second data key")
		})
	})

	t.Run("AssertAADKeeperContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for a local keeper", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertAADKeeperContract(t, newLocal(t))
		})

		t.Run("fails for a custodian that binds no aad", func(t *testing.T) {
			t.Parallel()
			k := ignoringAAD{Keeper: newLocal(t)}
			got := assert.Rejects(t, "a custodian that binds no aad fails the contract", func(tb assert.TB) {
				cryptotest.AssertAADKeeperContract(tb, k)
			})
			assert.Length(t, got, 1, "the contract fails once")
			assert.Equal(t, got[0].Contract, "material that WrapAAD bound to one aad does not unwrap with another",
				"the contract fails at the UnwrapAAD with another aad")
		})
	})

	t.Run("AssertKeyCreatorContract", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for a local keeper", func(t *testing.T) {
			t.Parallel()
			cryptotest.AssertKeyCreatorContract(t, newLocal(t))
		})
	})
}

// newLocal returns a local keeper over a root key of zero bytes, with a fake
// clock at origin. It fails tb when New refuses the root key.
func newLocal(tb assert.TB) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New("cryptotest/root", make([]byte, localkey.RootKeySize), randcrypto.New(), fake.New(origin))
	assert.NoError(tb, err, "New accepts a root key of 32 bytes")

	return k
}
