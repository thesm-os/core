// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// samples is the number of calls of Now whose instants
// NowReturnsAscendingInstants compares.
const samples = 100

// eastOfUTC is the zone of the planted defect of TimeReturnsUTC, an hour east
// of UTC.
var eastOfUTC = time.FixedZone("UTC+1", int(time.Hour/time.Second))

// NowReturnsAscendingInstants checks that the Instant of each of 100 calls of
// Now happens before the Instant of the next call.
//
//dokimi:check Clock.Now defect=repeatsNow
func NowReturnsAscendingInstants(h *ClockHandle) {
	c := h.Subject()
	instants := make([]clock.Instant, 0, samples)
	for range samples {
		instants = append(instants, c.Now())
	}
	assert.Pairwise(h, instants, clock.Instant.HappensBefore, h.Statement())
}

// NowReturnsANonZeroWall checks that Now returns an Instant whose Wall is not 0.
//
//dokimi:check Clock.Now defect=startsAtTheEpoch
func NowReturnsANonZeroWall(h *ClockHandle) {
	assert.NotEqual(h, h.Subject().Now().Wall, int64(0), h.Statement())
}

// NowFallsBetweenTwoTimes checks that the Wall of Now is at or after the time
// of a call of Time before it, and at or before the time of a call of Time
// after it.
//
//dokimi:check Clock.Now defect=returnsZeroTime
func NowFallsBetweenTwoTimes(h *ClockHandle) {
	c := h.Subject()
	before := c.Time().UnixNano()
	wall := c.Now().Wall
	after := c.Time().UnixNano()
	assert.Pairwise(h, []int64{before, wall, after}, func(earlier, later int64) bool { return earlier <= later },
		h.Statement())
}

// TimeReturnsUTC checks that Time returns a time in the location UTC.
//
//dokimi:check Clock.Time defect=returnsLocalTime
func TimeReturnsUTC(h *ClockHandle) {
	assert.Equal(h, h.Subject().Time().Location(), time.UTC, h.Statement(), assert.ByIdentity())
}

// TimeReturnsNonZero checks that Time does not return the zero time.Time.
//
//dokimi:check Clock.Time defect=returnsZeroTime
func TimeReturnsNonZero(h *ClockHandle) {
	assert.False(h, h.Subject().Time().IsZero(), h.Statement())
}

// UpdateReturnsALaterInstant checks that Update returns an Instant after the
// Instant of a peer at 2026-01-01T00:00:00Z with Logical 99.
//
//dokimi:check Clock.Update defect=dropsTheLogicalOfAPeer
func UpdateReturnsALaterInstant(h *ClockHandle) {
	observed := NewInstantPeer().WithLogical(99).Build()
	assert.True(h, observed.HappensBefore(h.Subject().Update(observed)), h.Statement())
}

// UpdateKeepsTheNode checks that Update returns an Instant of the Node of Now
// and not of the Node of the peer.
//
//dokimi:check Clock.Update defect=takesTheNodeOfAPeer
func UpdateKeepsTheNode(h *ClockHandle) {
	c := h.Subject()
	node := c.Now().Node
	assert.Equal(h, c.Update(NewInstantPeer().WithLogical(1).Build()).Node, node, h.Statement())
}

// UpdateFollowsAPeerAhead checks that Update returns an Instant after the
// Instant of a peer whose Wall is an hour after the Wall of Now.
//
//dokimi:check Clock.Update defect=ignoresAPeerAhead
func UpdateFollowsAPeerAhead(h *ClockHandle) {
	c := h.Subject()
	ahead := NewInstantPeer().WithWall(c.Now().Wall + int64(time.Hour)).Build()
	assert.True(h, ahead.HappensBefore(c.Update(ahead)), h.Statement())
}

// NewTimerOfZeroSendsATick checks that the Timer of NewTimer(0) sends its tick
// within a second.
//
//dokimi:check Clock.NewTimer defect=delaysATimerOfZero
func NewTimerOfZeroSendsATick(h *ClockHandle) {
	assert.CompletesWithin(h, tickWithin, receiveTick(h.Subject().NewTimer(0).C()), h.Statement())
}

// NewTimerOfANegativeDurationSendsATick checks that the Timer of a NewTimer of
// -1s sends its tick within a second.
//
//dokimi:check Clock.NewTimer defect=delaysATimerOfANegativeDuration
func NewTimerOfANegativeDurationSendsATick(h *ClockHandle) {
	assert.CompletesWithin(h, tickWithin, receiveTick(h.Subject().NewTimer(-time.Second).C()), h.Statement())
}

// repeatsNow returns the planted defect of NowReturnsAscendingInstants: a
// Clock whose Now returns one Instant on every call.
func repeatsNow(h *ClockHandle) clock.Clock {
	f := fake.New(reference)
	first := f.Now()

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockNow(func() clock.Instant { return first }))
}

// startsAtTheEpoch returns the planted defect of NowReturnsANonZeroWall: a fake
// Clock at the Unix epoch, whose Now returns instants at Wall 0.
func startsAtTheEpoch(*ClockHandle) clock.Clock {
	return fake.New(time.Unix(0, 0).UTC())
}

// returnsZeroTime returns the planted defect of NowFallsBetweenTwoTimes and
// TimeReturnsNonZero: a Clock whose Time returns the zero time.Time.
func returnsZeroTime(h *ClockHandle) clock.Clock {
	return NewClockStub(h, ClockStubDelegateTo(fake.New(reference)),
		WithClockTime(func() time.Time { return time.Time{} }))
}

// returnsLocalTime returns the planted defect of TimeReturnsUTC: a Clock whose
// Time returns its time in a zone an hour east of UTC.
func returnsLocalTime(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f),
		WithClockTime(func() time.Time { return f.Time().In(eastOfUTC) }))
}

// dropsTheLogicalOfAPeer returns the planted defect of
// UpdateReturnsALaterInstant: a Clock whose Update of a peer at the Wall of
// the Clock returns the next Instant of Now, whatever the Logical of the peer.
func dropsTheLogicalOfAPeer(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) clock.Instant {
		if now := f.Now(); observed.Wall == now.Wall {
			return now
		}

		return f.Update(observed)
	}))
}

// takesTheNodeOfAPeer returns the planted defect of UpdateKeepsTheNode: a Clock
// whose Update returns an Instant of the Node of the peer.
func takesTheNodeOfAPeer(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) clock.Instant {
		merged := f.Update(observed)
		merged.Node = observed.Node

		return merged
	}))
}

// ignoresAPeerAhead returns the planted defect of UpdateFollowsAPeerAhead: a
// Clock whose Update of a peer ahead of the Clock returns the next Instant of
// Now.
func ignoresAPeerAhead(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) clock.Instant {
		if now := f.Now(); observed.Wall > now.Wall {
			return now
		}

		return f.Update(observed)
	}))
}

// delaysATimerOfZero returns the planted defect of NewTimerOfZeroSendsATick: a
// Clock whose NewTimer(0) returns a Timer of an hour.
func delaysATimerOfZero(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockNewTimer(func(d time.Duration) clock.Timer {
		if d == 0 {
			return f.NewTimer(time.Hour)
		}

		return f.NewTimer(d)
	}))
}

// delaysATimerOfANegativeDuration returns the planted defect of
// NewTimerOfANegativeDurationSendsATick: a Clock whose NewTimer of a negative
// duration returns a Timer of an hour.
func delaysATimerOfANegativeDuration(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockNewTimer(func(d time.Duration) clock.Timer {
		if d < 0 {
			return f.NewTimer(time.Hour)
		}

		return f.NewTimer(d)
	}))
}
