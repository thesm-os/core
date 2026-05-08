// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto/sign"
)

func TestKeyIDString(t *testing.T) {
	t.Parallel()

	t.Run("zero KeyID hex-encodes to all zeros", func(t *testing.T) {
		t.Parallel()
		var k sign.KeyID
		testkit.Equal(t, k.String(), "00000000000000000000000000000000",
			"zero KeyID must hex-encode to 32 zeros")
	})

	t.Run("specific bytes hex-encode in order", func(t *testing.T) {
		t.Parallel()
		var k sign.KeyID
		k[0], k[1], k[14], k[15] = 0xab, 0xcd, 0x12, 0x34
		testkit.Equal(t, k.String(), "abcd0000000000000000000000001234",
			"specific bytes must hex-encode in order")
	})
}

func TestKeyIDSize(t *testing.T) {
	t.Parallel()

	testkit.Equal(t, sign.KeyIDSize, 16, "KeyIDSize must equal 16")
	var k sign.KeyID
	testkit.Equal(t, len(k), sign.KeyIDSize, "len(KeyID) must equal KeyIDSize")
}
