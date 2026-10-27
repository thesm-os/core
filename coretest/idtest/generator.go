// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idtest

import (
	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/id"
)

// draws is the number of calls of Generate whose IDs ReturnsDistinctIDs compares.
const draws = 16

// ReturnsNonZero checks that Generate does not return the Zero ID.
//
//dokimi:check Generator.Generate defect=returnsZero
func ReturnsNonZero(h *GeneratorHandle) {
	assert.False(h, h.Subject().Generate().IsZero(), h.Statement())
}

// KeepsItsSize checks that the calls of Generate return IDs of one size.
//
//dokimi:check Generator.Generate defect=changesSize
func KeepsItsSize(h *GeneratorHandle) {
	assert.Deterministic(h, func(g id.Generator) (int, error) { return g.Generate().Size(), nil }, h.Subject(),
		h.Statement())
}

// ReturnsDistinctIDs checks that 16 calls of Generate return 16 distinct IDs.
//
//dokimi:check Generator.Generate defect=returnsZero
func ReturnsDistinctIDs(h *GeneratorHandle) {
	g := h.Subject()
	assert.NoDuplicates(h, func() ([]id.ID, error) {
		ids := make([]id.ID, 0, draws)
		for range draws {
			ids = append(ids, g.Generate())
		}

		return ids, nil
	}, h.Statement())
}

// returnsZero returns the planted defect of ReturnsNonZero and ReturnsDistinctIDs: a
// Generator whose Generate returns the Zero ID on every call.
func returnsZero(h *GeneratorHandle) id.Generator {
	return NewGeneratorStub(h, WithGeneratorGenerate(func() id.ID { return id.Zero }))
}

// changesSize returns the planted defect of KeepsItsSize: a Generator whose Generate
// returns distinct IDs of 16 bytes and of 32 bytes in turn.
func changesSize(h *GeneratorHandle) id.Generator {
	var calls byte

	return NewGeneratorStub(h, WithGeneratorGenerate(func() id.ID {
		calls++
		if calls%2 == 0 {
			return id.New256([id.Size256]byte{calls})
		}

		return id.New128([id.Size128]byte{calls})
	}))
}
