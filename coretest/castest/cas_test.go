// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package castest_test

import (
	"testing"

	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/coretest/castest"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/crypto/sha3"
)

// TestCas checks that the store in memory passes the checks of the suite of
// cas.Store under SHA-256 and under SHA3-512.
func TestCas(t *testing.T) {
	t.Parallel()

	t.Run("Store", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the store in memory under SHA-256", func(t *testing.T) {
			t.Parallel()
			castest.RunStoreSuite(t, func() *memory.Store { return memory.New(sha256.New()) }, storeOptions()...)
		})

		t.Run("passes the store in memory under SHA3-512", func(t *testing.T) {
			t.Parallel()
			castest.RunStoreSuite(t, func() *memory.Store { return memory.New(sha3.New512()) }, storeOptions()...)
		})
	})
}

// storeOptions returns the doors of a store in memory: a restart and a crash
// return the store itself, which keeps its values across both. The crash runs
// call before it returns.
func storeOptions() []castest.StoreOption {
	return []castest.StoreOption{
		conform.Restartable(func(_ *castest.StoreHandle, s cas.Store) cas.Store { return s }),
		conform.Crashable(func(_ *castest.StoreHandle, s cas.Store, call func()) cas.Store {
			call()

			return s
		}),
	}
}
