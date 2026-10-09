// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// histogram is the no-op [telemetry.Histogram]. It has no state, so its
// zero value is its only value, and it is safe for concurrent use.
type histogram struct{}

// Compile-time interface check.
var _ telemetry.Histogram = histogram{}

// Record discards value.
func (histogram) Record(context.Context, float64) {}

// With returns the receiver, because binding attributes has no effect on
// a no-op histogram.
func (h histogram) With([]telemetry.Attr) telemetry.Histogram { return h }

// Release does nothing, because a no-op histogram keeps no state per
// attribute set.
func (histogram) Release() {}
