// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry_test

import (
	"slices"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/telemetry"
)

func TestMapCarrier(t *testing.T) {
	t.Parallel()

	t.Run("Get returns empty for an absent key", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, telemetry.MapCarrier{}.Get("absent"), "",
			"an absent key must read as the empty string, not panic")
	})

	t.Run("Get returns a stored value", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, telemetry.MapCarrier{"k": "v"}.Get("k"), "v",
			"Get must return the stored value")
	})

	t.Run("Set replaces an existing value", func(t *testing.T) {
		t.Parallel()
		c := telemetry.MapCarrier{"k": "old"}
		c.Set("k", "new")
		testkit.Equal(t, c.Get("k"), "new", "Set must replace rather than append")
	})

	t.Run("Set adds a new key", func(t *testing.T) {
		t.Parallel()
		c := telemetry.MapCarrier{}
		c.Set("k", "v")
		testkit.Equal(t, c.Get("k"), "v", "Set must be visible to Get")
	})

	t.Run("Keys lists every entry", func(t *testing.T) {
		t.Parallel()
		keys := telemetry.MapCarrier{"a": "1", "b": "2"}.Keys()
		slices.Sort(keys)
		testkit.Equal(t, keys, []string{"a", "b"}, "Keys must list every entry")
	})

	t.Run("Keys of an empty carrier is empty", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, len(telemetry.MapCarrier{}.Keys()), 0,
			"an empty carrier must report no keys")
	})
}
