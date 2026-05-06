// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// gauge is the empty-struct no-op [telemetry.Gauge]. The zero
// value is the only useful value; stateless and safe for
// concurrent use.
type gauge struct{}

// Compile-time interface check.
var _ telemetry.Gauge = gauge{}

// Set discards value.
func (gauge) Set(context.Context, float64) {}

// Add discards delta.
func (gauge) Add(context.Context, float64) {}

// With returns the receiver — attribute pre-binding has no effect
// on a no-op gauge.
func (g gauge) With([]telemetry.Attr) telemetry.Gauge { return g }
