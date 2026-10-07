// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
)

// uuidv4Bytes are the bytes of a UUIDv4. Its first 48 bits are
// 0x123456789abc.
var uuidv4Bytes = [id.Size128]byte{
	0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef,
	0x80, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
}

func TestLayout(t *testing.T) {
	t.Parallel()

	t.Run("TimestampMillis", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give id.ID
			want uint64
		}{
			{name: "returns the milliseconds of a UUIDv7", give: exampleID, want: exampleMillis},
			{
				name: "returns the first 48 bits of a UUID of another version",
				give: id.New128(uuidv4Bytes),
				want: 0x123456789abc,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv7.TimestampMillis(tt.give), tt.want,
					"TimestampMillis must return the first 48 bits")
			})
		}

		for _, tt := range notUUIDs {
			t.Run("returns 0 for "+tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv7.TimestampMillis(tt.id), uint64(0), "TimestampMillis must return 0")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		otherVariant := exampleBytes
		otherVariant[8] = 0xc0

		tests := []struct {
			name string
			give id.ID
			want bool
		}{
			{name: "reports true for a UUIDv7", give: exampleID, want: true},
			{name: "reports false for a UUIDv4", give: id.New128(uuidv4Bytes), want: false},
			{name: "reports false for version 7 with another variant", give: id.New128(otherVariant), want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv7.Valid(tt.give), tt.want, "Valid must check the version and the variant")
			})
		}

		for _, tt := range notUUIDs {
			t.Run("reports false for "+tt.name, func(t *testing.T) {
				t.Parallel()
				assert.False(t, uuidv7.Valid(tt.id), "Valid must report false for an ID that is not 128 bits")
			})
		}
	})
}

// TestLayoutAllocs checks the allocation contracts of TimestampMillis and
// Valid. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestLayoutAllocs(t *testing.T) {
	t.Run("TimestampMillis", func(t *testing.T) {
		var ms uint64
		expect.MaxAllocs(t, func() { ms = uuidv7.TimestampMillis(exampleID) }, 0, "TimestampMillis must not allocate")
		assert.Equal(t, ms, uint64(exampleMillis), "the test must measure the milliseconds of the UUIDv7")
	})

	t.Run("Valid", func(t *testing.T) {
		var valid bool
		expect.MaxAllocs(t, func() { valid = uuidv7.Valid(exampleID) }, 0, "Valid must not allocate")
		assert.True(t, valid, "the test must measure a UUIDv7")
	})
}

// BenchmarkLayout reports the cost of TimestampMillis and Valid, and fails
// when one allocates.
func BenchmarkLayout(b *testing.B) {
	b.Run("TimestampMillis", func(b *testing.B) {
		var ms uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ms = uuidv7.TimestampMillis(exampleID)
		}

		assert.Equal(b, ms, uint64(exampleMillis), "the benchmark must measure the milliseconds of the UUIDv7")
	})

	b.Run("Valid", func(b *testing.B) {
		var valid bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			valid = uuidv7.Valid(exampleID)
		}

		assert.True(b, valid, "the benchmark must measure a UUIDv7")
	})
}
