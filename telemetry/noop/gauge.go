// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// gauge is the no-op [telemetry.Gauge]. It has no state, so its zero
// value is its only value, and it is safe for concurrent use.
type gauge struct{}

// Compile-time interface check.
var _ telemetry.Gauge = gauge{}

// Set discards value.
func (gauge) Set(context.Context, float64) {}

// Add discards delta.
func (gauge) Add(context.Context, float64) {}

// With returns the receiver, because binding attributes has no effect on
// a no-op gauge.
func (g gauge) With([]telemetry.Attr) telemetry.Gauge { return g }

// Release does nothing, because a no-op gauge keeps no state per
// attribute set.
func (gauge) Release() {}
