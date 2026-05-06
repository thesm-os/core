// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

	"go.thesmos.sh/core/version"
)

func TestVersioned(t *testing.T) {
	t.Parallel()

	t.Run("zero value carries zero Version and zero T", func(t *testing.T) {
		t.Parallel()
		var v version.Versioned[int]
		if v.Value != 0 {
			t.Fatalf("zero Value: got %d, want 0", v.Value)
		}
		if !v.Version.IsZero() {
			t.Fatalf("zero Version: got %q, want IsZero", v.Version)
		}
	})

	t.Run("carries value and version round-trip", func(t *testing.T) {
		t.Parallel()
		v := version.Versioned[string]{
			Value:   "payload",
			Version: "v123",
		}
		if v.Value != "payload" {
			t.Fatalf("Value: got %q, want payload", v.Value)
		}
		if v.Version != "v123" {
			t.Fatalf("Version: got %q, want v123", v.Version)
		}
	})
}
