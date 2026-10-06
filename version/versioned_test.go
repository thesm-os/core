// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/version"
)

func TestVersioned(t *testing.T) {
	t.Parallel()

	t.Run("zero value carries zero Version and zero T", func(t *testing.T) {
		t.Parallel()
		var v version.Versioned[int]
		assert.Equal(t, v.Value, 0, "zero Versioned[int].Value must be 0")
		assert.True(t, v.Version.IsZero(), "zero Versioned.Version must be IsZero")
	})

	t.Run("carries value and version round-trip", func(t *testing.T) {
		t.Parallel()
		v := version.Versioned[string]{
			Value:   "payload",
			Version: "v123",
		}
		assert.Equal(t, v.Value, "payload", "Value must round-trip")
		assert.Equal(t, v.Version, version.Version("v123"), "Version must round-trip")
	})
}
