// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// histogram is the empty-struct no-op [telemetry.Histogram]. The
// zero value is the only useful value; stateless and safe for
// concurrent use.
type histogram struct{}

// Compile-time interface check.
var _ telemetry.Histogram = histogram{}

// Record discards value.
func (histogram) Record(context.Context, float64) {}

// With returns the receiver — attribute pre-binding has no effect
// on a no-op histogram.
func (h histogram) With([]telemetry.Attr) telemetry.Histogram { return h }
