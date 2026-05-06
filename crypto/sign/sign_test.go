// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"testing"

	"go.thesmos.sh/core/crypto/sign"
)

func TestKeyIDString(t *testing.T) {
	t.Parallel()

	t.Run("zero KeyID hex-encodes to all zeros", func(t *testing.T) {
		t.Parallel()
		var k sign.KeyID
		const want = "00000000000000000000000000000000"
		if got := k.String(); got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})

	t.Run("specific bytes hex-encode in order", func(t *testing.T) {
		t.Parallel()
		var k sign.KeyID
		k[0], k[1], k[14], k[15] = 0xab, 0xcd, 0x12, 0x34
		const want = "abcd0000000000000000000000001234"
		if got := k.String(); got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})
}

func TestKeyIDSize(t *testing.T) {
	t.Parallel()

	if sign.KeyIDSize != 16 {
		t.Fatalf("KeyIDSize: got %d, want 16", sign.KeyIDSize)
	}
	var k sign.KeyID
	if got := len(k); got != sign.KeyIDSize {
		t.Fatalf("len(KeyID): got %d, want %d", got, sign.KeyIDSize)
	}
}
