// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
)

// newClock returns a Clock of node 0 on the system clock. The conformance test
// of the package runs the suite of clock.Clock on it.
func newClock() *hlc.Clock {
	return hlc.New(0)
}

func TestClock(t *testing.T) {
	t.Parallel()

	t.Run("Now", func(t *testing.T) {
		t.Parallel()

		t.Run("returns instants of the node of New", func(t *testing.T) {
			t.Parallel()
			const (
				node  = clock.NodeID(42)
				calls = 100
			)
			c := hlc.New(node)
			nodes := make([]clock.NodeID, 0, calls)
			for range calls {
				nodes = append(nodes, c.Now().Node)
			}
			assert.Equal(t, nodes, slices.Repeat([]clock.NodeID{node}, calls),
				"every Instant of Now has the node of New")
		})

		t.Run("returns Logical 1 after the source advances", func(t *testing.T) {
			t.Parallel()
			now := int64(100)
			c := hlc.NewWithSource(clock.NodeID(1), func() int64 { return now })
			for range 5 {
				c.Now()
			}
			now = 200
			got := c.Now()
			expect.Equal(t, got.Wall, int64(200), "Now takes the new wall of the source")
			expect.Equal(t, got.Logical, uint32(1), "Now starts Logical at 1 on a new wall")
		})

		t.Run("returns the next Logical while the source repeats its wall", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), func() int64 { return 100 })
			first := c.Now()
			second := c.Now()
			expect.Equal(t, second.Wall, first.Wall, "Now keeps the wall that the source repeats")
			expect.Equal(t, second.Logical, first.Logical+1, "Now adds 1 to Logical on a repeated wall")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name                string
			giveStartWall       int64
			giveStartNows       int
			givePhysicalWall    int64
			giveObservedWall    int64
			giveObservedLogical uint32
			wantWall            int64
			wantLogical         uint32
		}{
			{
				name:                "returns the Logical of a peer ahead plus 1",
				giveStartWall:       100,
				giveStartNows:       5,
				givePhysicalWall:    100,
				giveObservedWall:    200,
				giveObservedLogical: 2,
				wantWall:            200,
				wantLogical:         3,
			},
			{
				name:                "returns the local Logical plus 1 for a peer behind the clock",
				giveStartWall:       200,
				giveStartNows:       2,
				givePhysicalWall:    200,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            200,
				wantLogical:         3,
			},
			{
				name:                "returns the larger Logical plus 1 for a peer at the wall of the clock",
				giveStartWall:       100,
				giveStartNows:       2,
				givePhysicalWall:    100,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            100,
				wantLogical:         6,
			},
			{
				name:                "returns Logical 1 for a source ahead of both walls",
				giveStartWall:       50,
				giveStartNows:       3,
				givePhysicalWall:    300,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            300,
				wantLogical:         1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				now := tt.giveStartWall
				c := hlc.NewWithSource(clock.NodeID(1), func() int64 { return now })
				for range tt.giveStartNows {
					c.Now()
				}
				now = tt.givePhysicalWall
				got := c.Update(clock.Instant{Wall: tt.giveObservedWall, Logical: tt.giveObservedLogical, Node: 99})
				expect.Equal(t, got.Wall, tt.wantWall, "Update returns the wall of the merge")
				expect.Equal(t, got.Logical, tt.wantLogical, "Update returns the Logical of the merge")
				expect.Equal(t, got.Node, clock.NodeID(1), "Update returns the node of the clock")
			})
		}
	})
}
