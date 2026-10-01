// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"encoding/hex"
	"runtime"
	"testing"

	"go.thesmos.sh/kanon"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/tag"
)

// The recorded encodings of recordedTag.
const (
	// tagHex pins the kanon encoding of recordedTag, which a consumer
	// persists, so it never changes: field 1 is the key env, and field 2
	// the value prod.
	tagHex = "0a03" + "656e76" +
		"1204" + "70726f64"

	// swappedHex is tagHex with its two fields in the other order, which
	// the canonical decode rejects.
	swappedHex = "1204" + "70726f64" +
		"0a03" + "656e76"
)

// recordedTag is the tag whose encoding tagHex pins.
var recordedTag = tag.Tag{Key: "env", Value: "prod"}

// decodeHex returns the bytes of the hexadecimal text s.
func decodeHex(t *testing.T, s string) []byte {
	t.Helper()

	data, err := hex.DecodeString(s)
	testkit.NoError(t, err, "the recorded encoding must be hexadecimal")

	return data
}

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tag.Tag
			want bool
		}{
			{name: "reports true for the zero Tag", give: tag.Tag{}, want: true},
			{name: "reports false for a Tag with a key", give: tag.Tag{Key: "k"}, want: false},
			{name: "reports false for a Tag with a value", give: tag.Tag{Value: "v"}, want: false},
			{name: "reports false for a Tag with both fields set", give: tag.Tag{Key: "k", Value: "v"}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.IsZero(), tt.want, "IsZero must report whether both fields are empty")
			})
		}
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the recorded encoding of a tag", func(t *testing.T) {
			t.Parallel()
			tg := recordedTag
			got, err := tg.AppendBinary(nil)
			testkit.NoError(t, err, "AppendBinary must encode the tag")
			testkit.Equal(t, hex.EncodeToString(got), tagHex, "AppendBinary must write field 1 and 2 as recorded")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the tag of the recorded encoding", func(t *testing.T) {
			t.Parallel()
			var got tag.Tag
			testkit.NoError(t, got.UnmarshalBinary(decodeHex(t, tagHex)),
				"UnmarshalBinary must decode the recorded encoding")
			testkit.Equal(t, got, recordedTag, "UnmarshalBinary must return the recorded tag")
		})

		t.Run("returns ErrNotCanonical for the recorded fields in the other order", func(t *testing.T) {
			t.Parallel()
			var got tag.Tag
			testkit.ErrorIs(t, got.UnmarshalBinary(decodeHex(t, swappedHex)), kanon.ErrNotCanonical,
				"UnmarshalBinary must reject an encoding that the encode does not write")
		})
	})
}

// TestTagZeroAlloc does not call t.Parallel, because testing.AllocsPerRun
// panics while a parallel test runs.
//
//nolint:paralleltest // see comment above
func TestTagZeroAlloc(t *testing.T) {
	tt := tag.Tag{Key: "k", Value: "v"}
	testkit.Equal(t, testing.AllocsPerRun(100, func() { _ = tt.IsZero() }),
		float64(0), "IsZero must be zero-alloc")
}

func BenchmarkIsZero(b *testing.B) {
	tt := tag.Tag{Key: "k", Value: "v"}
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = tt.IsZero()
	}
	runtime.KeepAlive(sink)
}
