// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"testing"

	"go.thesmos.sh/core/crypto"
)

func TestIDSize(t *testing.T) {
	t.Parallel()

	if got := crypto.IDSize; got != 16 {
		t.Fatalf("IDSize: got %d, want 16", got)
	}
	if got := len(crypto.ID{}); got != crypto.IDSize {
		t.Fatalf("len(ID): got %d, want %d", got, crypto.IDSize)
	}
}

func TestIDString(t *testing.T) {
	t.Parallel()

	t.Run("zero ID hex-encodes to 32 zeros", func(t *testing.T) {
		t.Parallel()
		got := crypto.ID{}.String()
		want := "00000000000000000000000000000000"
		if got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})

	t.Run("specific bytes hex-encode in order", func(t *testing.T) {
		t.Parallel()
		id := crypto.ID{0xde, 0xad, 0xbe, 0xef}
		got := id.String()
		want := "deadbeef000000000000000000000000"
		if got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})
}
