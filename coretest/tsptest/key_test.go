// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
)

func TestDigest(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsptest.Digest
			want bool
		}{
			{name: "reports false for the zero Digest", give: 0},
			{name: "reports true for DigestSHA256", give: tsptest.DigestSHA256, want: true},
			{name: "reports true for DigestSHA384", give: tsptest.DigestSHA384, want: true},
			{name: "reports true for DigestSHA512", give: tsptest.DigestSHA512, want: true},
			{name: "reports false for a Digest after DigestSHA512", give: tsptest.DigestSHA512 + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the Digest is a constant")
			})
		}
	})
}

func TestKey(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsptest.Key
			want bool
		}{
			{name: "reports false for the zero Key", give: 0},
			{name: "reports true for KeyEd25519", give: tsptest.KeyEd25519, want: true},
			{name: "reports true for KeyMLDSA87", give: tsptest.KeyMLDSA87, want: true},
			{name: "reports false for a Key after KeyMLDSA87", give: tsptest.KeyMLDSA87 + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the Key is a constant")
			})
		}
	})
}
