// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

import (
	"testing"

	"go.dokimi.dev/assert"
)

func TestReaderInternal(t *testing.T) {
	t.Parallel()

	t.Run("prover", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("empties the memory of the tiles", func(t *testing.T) {
				t.Parallel()
				var p prover
				p.mem.Alloc(64)
				p.Reset()
				assert.Equal(t, p.mem.Len(), 0, "Reset must empty the memory of the tiles")
			})

			t.Run("drops the buffer of each tile", func(t *testing.T) {
				t.Parallel()
				var p prover
				p.dst = append(p.dst, p.mem.Alloc(64))
				p.Reset()
				assert.Nil(t, p.dst[:1][0], "Reset must drop the buffer of the tile")
			})
		})
	})
}
