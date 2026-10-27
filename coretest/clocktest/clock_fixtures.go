// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import "time"

// reference is the time of [NewInstantSample], 2026-01-01T00:00:00Z. The
// planted defects of the suites start their fake Clocks at it.
var reference = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// NewInstantOrigin returns a builder of the zero Instant, whose Wall, Logical
// and Node are 0.
func NewInstantOrigin() *InstantBuilder {
	return NewInstantBuilder()
}

// NewInstantSample returns a builder of the Instant at 2026-01-01T00:00:00Z
// with Logical 0 and Node 1.
func NewInstantSample() *InstantBuilder {
	return NewInstantBuilder().
		WithWall(reference.UnixNano()).
		WithLogical(0).
		WithNode(1)
}

// NewInstantPeer returns a builder of the Instant of [NewInstantSample] with
// Node 2. A check of Update passes it as the Instant of a peer.
func NewInstantPeer() *InstantBuilder {
	return NewInstantSample().WithNode(2)
}

// NewInstantRangeSample returns a builder of the InstantRange from the Instant
// of [NewInstantOrigin] to the Instant of [NewInstantSample].
func NewInstantRangeSample() *InstantRangeBuilder {
	return NewInstantRangeBuilder().
		WithSince(NewInstantOrigin().Build()).
		WithUntil(NewInstantSample().Build())
}
