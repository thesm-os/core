// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idtest_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// origin is the wall time of the ULIDs of the test.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestGenerator checks that a ULID Generator passes the checks of the suite of
// id.Generator.
func TestGenerator(t *testing.T) {
	t.Parallel()

	idtest.RunGeneratorSuite(t, func() *ulid.Generator { return ulid.New(fake.New(origin), seeded.New(rand.Seed(1))) })
}
