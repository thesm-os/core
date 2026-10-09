// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// exampleTimestamp is the timestamp of the example of tlog-cosignature.
const exampleTimestamp = 1679315147

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
				give: append(binary.BigEndian.AppendUint64(nil, exampleTimestamp), 1, 2, 3),
				want: time.Unix(exampleTimestamp, 0).UTC(),
			},
			{
				name: "returns the time of a value of 8 bytes",
				give: binary.BigEndian.AppendUint64(nil, 1),
				want: time.Unix(1, 0).UTC(),
			},
			{
				name: "returns the time of the timestamp 2^63 − 1",
				give: binary.BigEndian.AppendUint64(nil, math.MaxInt64),
				want: time.Unix(math.MaxInt64, 0).UTC(),
			},
			{
				name: "returns the zero Time for the timestamp 0",
				give: append(binary.BigEndian.AppendUint64(nil, 0), 1),
				want: time.Time{},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.Timestamp(tt.give)
				assert.NoError(t, err, "Timestamp must accept the value")
				assert.Equal(t, got, tt.want, "Timestamp must return the time of the value in UTC")
			})
		}

		t.Run("returns the time in UTC of every timestamp from 1 to 2^63 − 1", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Timestamp must return the time in UTC of the timestamp", func(c *prop.Case) {
				stamp := c.Draw(prop.Integer[uint64](1, math.MaxInt64), "timestamp")
				got, err := checkpoint.Timestamp(binary.BigEndian.AppendUint64(nil, stamp))
				assert.NoError(c, err, "Timestamp must accept the value")
				assert.Equal(c, got, time.Unix(int64(stamp), 0).UTC(),
					"Timestamp must return the time of the timestamp")
			})
		})

		t.Run("returns ErrTimestamp for every timestamp above 2^63 − 1", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(stamp uint64) error {
				_, err := checkpoint.Timestamp(binary.BigEndian.AppendUint64(nil, stamp))

				return err
			}, checkpoint.ErrTimestamp, "Timestamp must refuse a timestamp above 2^63 − 1",
				prop.Using(prop.Integer[uint64](math.MaxInt64+1, math.MaxUint64)))
		})

		tests = []struct {
			want time.Time
			name string
			give []byte
		}{
			{name: "returns ErrTimestamp for an empty value", give: nil},
			{name: "returns ErrTimestamp for a value of 7 bytes", give: binary.BigEndian.AppendUint64(nil, 1)[:7]},
			{
				name: "returns ErrTimestamp for the timestamp 2^63",
				give: binary.BigEndian.AppendUint64(nil, math.MaxInt64+1),
			},
			{
				name: "returns ErrTimestamp for the largest uint64",
				give: binary.BigEndian.AppendUint64(nil, math.MaxUint64),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := checkpoint.Timestamp(tt.give)
				expect.ErrorIs(t, err, checkpoint.ErrTimestamp, "Timestamp must refuse the value")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, got, time.Time{}, "Timestamp must return the zero Time with an error")
			})
		}
	})
}

// TestTimestampAllocs checks the allocation contract of Timestamp.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestTimestampAllocs(t *testing.T) {
	t.Run("Timestamp", func(t *testing.T) {
		v := append(binary.BigEndian.AppendUint64(nil, exampleTimestamp), 1, 2, 3)

		var err error
		expect.MaxAllocs(t, func() { _, err = checkpoint.Timestamp(v) }, 0, "Timestamp must not allocate")
		assert.NoError(t, err, "the test must measure a value that Timestamp accepts")
	})
}

// BenchmarkTimestamp reports the cost of Timestamp, and fails above the
// allocations that its contract states.
func BenchmarkTimestamp(b *testing.B) {
	b.Run("Timestamp", func(b *testing.B) {
		v := append(binary.BigEndian.AppendUint64(nil, exampleTimestamp), 1, 2, 3)

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = checkpoint.Timestamp(v)
		}

		assert.NoError(b, err, "the benchmark must measure a value that Timestamp accepts")
	})
}
