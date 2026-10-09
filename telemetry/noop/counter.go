// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package noop

import (
	"context"

	"go.thesmos.sh/core/telemetry"
)

// counter is the no-op [telemetry.Counter]. It has no state, so its zero
// value is its only value, and it is safe for concurrent use.
type counter struct{}

// Compile-time interface check.
var _ telemetry.Counter = counter{}

// Add discards value.
func (counter) Add(context.Context, int64) {}

// With returns the receiver, because binding attributes has no effect on
// a no-op counter.
func (c counter) With([]telemetry.Attr) telemetry.Counter { return c }

// Release does nothing, because a no-op counter keeps no state per
// attribute set.
func (counter) Release() {}
