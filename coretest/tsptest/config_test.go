// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
)

func TestESS(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsptest.ESS
			want bool
		}{
			{name: "reports true for ESSV2, the zero ESS", give: tsptest.ESSV2, want: true},
			{name: "reports true for ESSNone", give: tsptest.ESSNone, want: true},
			{name: "reports false for an ESS after ESSNone", give: tsptest.ESSNone + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the ESS is a constant")
			})
		}
	})
}

func TestUsage(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsptest.Usage
			want bool
		}{
			{name: "reports true for UsageCritical, the zero Usage", give: tsptest.UsageCritical, want: true},
			{name: "reports true for UsageNone", give: tsptest.UsageNone, want: true},
			{name: "reports false for a Usage after UsageNone", give: tsptest.UsageNone + 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the Usage is a constant")
			})
		}
	})
}
