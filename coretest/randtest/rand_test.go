// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package randtest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// TestRand checks that a seeded Rand passes the checks of the suite of rand.Rand.
func TestRand(t *testing.T) {
	t.Parallel()

	randtest.RunRandSuite(t, func() *seeded.Rand { return seeded.New(rand.Seed(1)) })
}
