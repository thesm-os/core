// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"testing"

	"go.dokimi.dev/assert"
)

func TestProverInternal(t *testing.T) {
	t.Parallel()

	t.Run("proofState", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("drops the node tile of each tile of the proof", func(t *testing.T) {
				t.Parallel()
				var s proofState
				s.needs = append(s.needs, need{nt: &nodeTile{}})
				s.Reset()
				assert.Nil(t, s.needs[:1][0].nt, "Reset must drop the node tile")
			})

			t.Run("drops the buffer of each tile of the last read", func(t *testing.T) {
				t.Parallel()
				var s proofState
				s.dst = append(s.dst, make([]byte, nodeRoom))
				s.Reset()
				assert.Nil(t, s.dst[:1][0], "Reset must drop the buffer of the tile")
			})
		})
	})
}
