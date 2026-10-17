// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"math"

	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// exampleText is the text form of exampleID.
const exampleText = "12345678-9abc-4def-8012-3456789abcde"

// exampleID is a UUIDv4 whose bytes each differ.
var exampleID = id.New128([id.Size128]byte{
	0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x80, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
})

// uuids generates IDs of 128 bits.
var uuids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
})

// seeds generates the seeds of the sources of the generators.
var seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)
