// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"testing"
	"unsafe"

	"go.thesmos.sh/testkit"
)

func TestCell(t *testing.T) {
	t.Parallel()

	t.Run("takes cellSize bytes", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, unsafe.Sizeof(cell{}), uintptr(cellSize), "a cell must fill its pair of cache lines")
	})
}
