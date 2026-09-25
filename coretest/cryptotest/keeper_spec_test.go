// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/localkey"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// scheduled is a custodian that destroys in two steps, as hosted key
// services do. The key stops unwrapping at once, and the first Destroy
// returns the zero time because the custodian does not yet know when
// the destruction becomes irreversible. Later calls return a time 30
// days ahead.
type scheduled struct {
	*localkey.Keeper

	calls atomic.Int32
}

func (s *scheduled) Destroy(ctx context.Context, keyID string) (time.Time, error) {
	if _, err := s.Keeper.Destroy(ctx, keyID); err != nil {
		return time.Time{}, err //nolint:wrapcheck // the test double passes the error through
	}

	if s.calls.Add(1) == 1 {
		return time.Time{}, nil
	}

	return origin.Add(30 * 24 * time.Hour), nil
}

func newLocal(t *testing.T) *localkey.Keeper {
	t.Helper()

	k, err := localkey.New("cryptotest/root", make([]byte, localkey.RootKeySize), randcrypto.New(), fake.New(origin))
	testkit.NoError(t, err, "New must accept a 32-byte root key")

	return k
}

func TestAssertDestroyerContract(t *testing.T) {
	t.Parallel()

	t.Run("accepts a custodian that destroys at once", func(t *testing.T) {
		t.Parallel()
		cryptotest.AssertDestroyerContract(t, newLocal(t))
	})

	t.Run("accepts a custodian that learns the time after the first call", func(t *testing.T) {
		t.Parallel()
		cryptotest.AssertDestroyerContract(t, &scheduled{Keeper: newLocal(t)})
	})
}

func TestAssertAADKeeperContract(t *testing.T) {
	t.Parallel()

	t.Run("accepts the local keeper", func(t *testing.T) {
		t.Parallel()
		cryptotest.AssertAADKeeperContract(t, newLocal(t))
	})
}
