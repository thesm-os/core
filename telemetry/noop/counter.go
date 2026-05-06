// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// counter is the empty-struct no-op [telemetry.Counter]. The zero
// value is the only useful value; stateless and safe for
// concurrent use.
type counter struct{}

// Compile-time interface check.
var _ telemetry.Counter = counter{}

// Add discards value.
func (counter) Add(context.Context, int64) {}

// With returns the receiver — attribute pre-binding has no effect
// on a no-op counter.
func (c counter) With([]telemetry.Attr) telemetry.Counter { return c }
