// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

func TestIDSize(t *testing.T) {
	t.Parallel()

	testkit.Equal(t, crypto.IDSize, 16, "IDSize must equal 16")
	testkit.Equal(t, len(crypto.ID{}), crypto.IDSize, "len(ID{}) must equal IDSize")
}

func TestIDString(t *testing.T) {
	t.Parallel()

	t.Run("zero ID hex-encodes to 32 zeros", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, crypto.ID{}.String(),
			"00000000000000000000000000000000",
			"zero ID must hex-encode to 32 zeros")
	})

	t.Run("specific bytes hex-encode in order", func(t *testing.T) {
		t.Parallel()
		id := crypto.ID{0xde, 0xad, 0xbe, 0xef}
		testkit.Equal(t, id.String(),
			"deadbeef000000000000000000000000",
			"specific bytes must hex-encode in order, padded with zeros")
	})
}
