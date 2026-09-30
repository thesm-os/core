// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
)

// uuidv4Bytes are the bytes of a UUIDv4. Its first 48 bits are
// 0x123456789abc.
var uuidv4Bytes = [id.Size128]byte{
	0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef,
	0x80, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
}

// The benchmarks write each result to a sink, so that the compiler keeps
// every call that they measure.
var (
	sinkMillis uint64
	sinkValid  bool
)

func TestTimestampMillis(t *testing.T) {
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
			testkit.Equal(t, uuidv7.TimestampMillis(tt.give), tt.want,
				"TimestampMillis must return the first 48 bits")
		})
	}

	t.Run("returns 0 for an ID that is not 128 bits", func(t *testing.T) {
		t.Parallel()
		for name, u := range notUUIDs() {
			testkit.Equal(t, uuidv7.TimestampMillis(u), uint64(0), "TimestampMillis must return 0 for "+name)
		}
	})
}

func TestValid(t *testing.T) {
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
			testkit.Equal(t, uuidv7.Valid(tt.give), tt.want, "Valid must check the version and the variant")
		})
	}

	t.Run("reports false for an ID that is not 128 bits", func(t *testing.T) {
		t.Parallel()
		for name, u := range notUUIDs() {
			testkit.False(t, uuidv7.Valid(u), "Valid must report false for "+name)
		}
	})
}

// BenchmarkTimestampMillis reports the cost of TimestampMillis, and fails
// when it allocates.
func BenchmarkTimestampMillis(b *testing.B) {
	benchZeroAlloc(b, func() { sinkMillis = uuidv7.TimestampMillis(exampleID) })
}

// BenchmarkValid reports the cost of Valid, and fails when it allocates.
func BenchmarkValid(b *testing.B) {
	benchZeroAlloc(b, func() { sinkValid = uuidv7.Valid(exampleID) })
}
