// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"errors"
	"math"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// samples is the number of calls of Now whose instants
// NowReturnsAscendingInstants compares, and the number of readings of Elapsed
// that ElapsedDoesNotDecrease compares.
const samples = 100

// yearAhead is the time by which the peer of UpdateRefusesAPeerAYearAhead and
// UpdateKeepsTheWallForARefusedPeer is ahead of the clock. No max offset of a
// deployment comes near it.
const yearAhead = 365 * 24 * time.Hour

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

// ElapsedDoesNotDecrease checks that each of 100 readings of Elapsed is at or
// above the reading before it.
//
//dokimi:check Clock.Elapsed defect=countsElapsedDown
func ElapsedDoesNotDecrease(h *ClockHandle) {
	c := h.Subject()
	readings := make([]time.Duration, 0, samples)
	for range samples {
		readings = append(readings, c.Elapsed())
	}
	assert.Pairwise(h, readings, func(earlier, later time.Duration) bool { return earlier <= later },
		h.Statement())
}

// ElapsedIsNotNegative checks that Elapsed does not return a negative duration.
//
//dokimi:check Clock.Elapsed defect=startsElapsedBelowZero
func ElapsedIsNotNegative(h *ClockHandle) {
	assert.InRange(h, h.Subject().Elapsed(), 0, math.MaxInt64, h.Statement())
}

// UpdateReturnsALaterInstant checks that Update returns an Instant after the
// Instant of a peer at the Wall of Now with Logical 99.
//
//dokimi:check Clock.Update defect=dropsTheLogicalOfAPeer
func UpdateReturnsALaterInstant(h *ClockHandle) {
	c := h.Subject()
	observed := NewInstantPeer().WithWall(c.Now().Wall).WithLogical(99).Build()
	merged, err := c.Update(observed)
	assert.NoError(h, err, "Update must merge a peer at the wall of Now")
	assert.True(h, observed.HappensBefore(merged), h.Statement())
}

// UpdateKeepsTheNode checks that Update returns an Instant of the Node of Now
// and not of the Node of a peer at the Wall of Now.
//
//dokimi:check Clock.Update defect=takesTheNodeOfAPeer
func UpdateKeepsTheNode(h *ClockHandle) {
	c := h.Subject()
	now := c.Now()
	merged, err := c.Update(NewInstantPeer().WithWall(now.Wall).WithLogical(1).Build())
	assert.NoError(h, err, "Update must merge a peer at the wall of Now")
	assert.Equal(h, merged.Node, now.Node, h.Statement())
}

// UpdateFollowsAPeerAhead checks that Update returns an Instant after the
// Instant of a peer whose Wall is a millisecond after the Wall of Now.
//
//dokimi:check Clock.Update defect=ignoresAPeerAhead
func UpdateFollowsAPeerAhead(h *ClockHandle) {
	c := h.Subject()
	ahead := NewInstantPeer().WithWall(c.Now().Wall + int64(time.Millisecond)).Build()
	merged, err := c.Update(ahead)
	assert.NoError(h, err, "Update must merge a peer a millisecond ahead")
	assert.True(h, ahead.HappensBefore(merged), h.Statement())
}

// UpdatePassesAPeerAtTheLastLogical checks that Update returns an Instant after
// the Instant of a peer at the Wall of Now with Logical 2^32 - 1.
//
//dokimi:check Clock.Update defect=wrapsTheLogical
func UpdatePassesAPeerAtTheLastLogical(h *ClockHandle) {
	c := h.Subject()
	peer := NewInstantPeer().WithWall(c.Now().Wall).WithLogical(math.MaxUint32).Build()
	merged, err := c.Update(peer)
	assert.NoError(h, err, "Update must merge a peer at the wall of Now")
	assert.True(h, peer.HappensBefore(merged), h.Statement())
}

// UpdateRefusesAPeerAYearAhead checks that Update returns an error that wraps
// ErrOffset for a peer whose Wall is a year after the Wall of Now.
//
//dokimi:check Clock.Update defect=followsAPeerAYearAhead
func UpdateRefusesAPeerAYearAhead(h *ClockHandle) {
	c := h.Subject()
	_, err := c.Update(NewInstantPeer().WithWall(c.Now().Wall + int64(yearAhead)).Build())
	assert.ErrorIs(h, err, clock.ErrOffset, h.Statement())
}

// UpdateKeepsTheWallForARefusedPeer checks that the Instant of Now after an
// Update of a peer a year ahead happens before the Instant of the peer.
//
//dokimi:check Clock.Update defect=adoptsARefusedPeer
func UpdateKeepsTheWallForARefusedPeer(h *ClockHandle) {
	c := h.Subject()
	ahead := NewInstantPeer().WithWall(c.Now().Wall + int64(yearAhead)).Build()
	// UpdateRefusesAPeerAYearAhead checks the error of the Update.
	_, _ = c.Update(ahead)
	assert.True(h, c.Now().HappensBefore(ahead), h.Statement())
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

// countsElapsedDown returns the planted defect of ElapsedDoesNotDecrease: a
// Clock whose Elapsed returns a shorter positive duration on each call.
func countsElapsedDown(h *ClockHandle) clock.Clock {
	var calls time.Duration

	return NewClockStub(h, ClockStubDelegateTo(fake.New(reference)), WithClockElapsed(func() time.Duration {
		calls++

		return time.Hour - calls
	}))
}

// startsElapsedBelowZero returns the planted defect of ElapsedIsNotNegative: a
// Clock whose Elapsed returns -1s on every call.
func startsElapsedBelowZero(h *ClockHandle) clock.Clock {
	return NewClockStub(h, ClockStubDelegateTo(fake.New(reference)),
		WithClockElapsed(func() time.Duration { return -time.Second }))
}

// dropsTheLogicalOfAPeer returns the planted defect of
// UpdateReturnsALaterInstant: a Clock whose Update of a peer at the Wall of
// the Clock returns the next Instant of Now, for each Logical of the peer below
// 2^32 - 1.
func dropsTheLogicalOfAPeer(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		if now := f.Now(); observed.Wall == now.Wall && observed.Logical < math.MaxUint32 {
			return now, nil
		}

		return f.Update(observed)
	}))
}

// takesTheNodeOfAPeer returns the planted defect of UpdateKeepsTheNode: a Clock
// whose Update of a peer that it does not refuse returns an Instant of the Node
// of the peer.
func takesTheNodeOfAPeer(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		merged, err := f.Update(observed)
		if err != nil {
			return merged, err
		}
		merged.Node = observed.Node

		return merged, nil
	}))
}

// ignoresAPeerAhead returns the planted defect of UpdateFollowsAPeerAhead: a
// Clock whose Update of a peer ahead of the Clock, within the default max
// offset, returns the next Instant of Now.
func ignoresAPeerAhead(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		now := f.Now()
		if observed.Wall > now.Wall && observed.Wall-now.Wall <= int64(clock.DefaultMaxOffset) {
			return now, nil
		}

		return f.Update(observed)
	}))
}

// wrapsTheLogical returns the planted defect of
// UpdatePassesAPeerAtTheLastLogical: a Clock whose Update of a peer at the Wall
// of the Clock with Logical 2^32 - 1 returns an Instant of Logical 0 at that
// Wall.
func wrapsTheLogical(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		if now := f.Now(); observed.Wall == now.Wall && observed.Logical == math.MaxUint32 {
			return clock.Instant{Wall: now.Wall, Node: now.Node}, nil
		}

		return f.Update(observed)
	}))
}

// followsAPeerAYearAhead returns the planted defect of
// UpdateRefusesAPeerAYearAhead: a Clock whose Update of a peer beyond the
// default max offset returns an Instant after the peer and no error.
func followsAPeerAYearAhead(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		if now := f.Now(); observed.Wall-now.Wall > int64(clock.DefaultMaxOffset) {
			return clock.Instant{Wall: observed.Wall, Logical: observed.Logical + 1, Node: now.Node}, nil
		}

		return f.Update(observed)
	}))
}

// adoptsARefusedPeer returns the planted defect of
// UpdateKeepsTheWallForARefusedPeer: a Clock whose Update of a peer beyond the
// default max offset returns ErrOffset and moves the Clock to the Wall of the
// peer.
func adoptsARefusedPeer(h *ClockHandle) clock.Clock {
	f := fake.New(reference)

	return NewClockStub(h, ClockStubDelegateTo(f), WithClockUpdate(func(observed clock.Instant) (clock.Instant, error) {
		merged, err := f.Update(observed)
		if errors.Is(err, clock.ErrOffset) {
			f.Set(time.Unix(0, observed.Wall).UTC())
		}

		return merged, err
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
