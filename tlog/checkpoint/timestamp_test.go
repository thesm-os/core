// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// exampleTimestamp is the timestamp of the example of tlog-cosignature.
const exampleTimestamp = 1679315147

// sinkTime receives the results of the benchmarks of Timestamp.
var sinkTime time.Time

// value returns the value of a timestamped signature with timestamp t and
// the signature sig.
func value(t uint64, sig ...byte) []byte {
	return append(binary.BigEndian.AppendUint64(nil, t), sig...)
}

func TestTimestamp(t *testing.T) {
	t.Parallel()

	t.Run("Timestamp", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			want time.Time
			name string
			give []byte
		}{
			{
				name: "returns the time of the example of tlog-cosignature",
				give: value(exampleTimestamp, 1, 2, 3), want: time.Unix(exampleTimestamp, 0),
			},
			{name: "returns the time of a value of 8 bytes", give: value(1), want: time.Unix(1, 0)},
			{
				name: "returns the time of the timestamp 2^63 − 1",
				give: value(math.MaxInt64),
				want: time.Unix(math.MaxInt64, 0),
			},
			{name: "returns the zero Time for the timestamp 0", give: value(0, 1), want: time.Time{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.Timestamp(tt.give)
				testkit.NoError(t, err, "Timestamp must accept the value")
				testkit.True(t, got.Equal(tt.want), "Timestamp must return "+tt.want.String()+", not "+got.String())
			})
		}

		t.Run("returns a time in UTC", func(t *testing.T) {
			t.Parallel()
			got, err := checkpoint.Timestamp(value(exampleTimestamp))
			testkit.NoError(t, err, "Timestamp must accept the value")
			testkit.True(t, got.Location() == time.UTC, "Timestamp must return the time in UTC")
		})

		errorTests := []struct {
			name string
			give []byte
		}{
			{name: "returns ErrTimestamp for an empty value", give: nil},
			{name: "returns ErrTimestamp for a value of 7 bytes", give: value(1)[:7]},
			{name: "returns ErrTimestamp for the timestamp 2^63", give: value(math.MaxInt64 + 1)},
			{name: "returns ErrTimestamp for the largest uint64", give: value(math.MaxUint64)},
		}
		for _, tt := range errorTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.Timestamp(tt.give)
				testkit.ErrorIs(t, err, checkpoint.ErrTimestamp, "Timestamp must refuse the value")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, got.IsZero(), "Timestamp must return the zero Time with an error")
			})
		}
	})
}

func BenchmarkTimestamp(b *testing.B) {
	v := value(exampleTimestamp, 1, 2, 3)

	b.Run("Timestamp", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkTime, errSink = checkpoint.Timestamp(v) })
	})
}
