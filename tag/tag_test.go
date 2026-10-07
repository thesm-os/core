// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.thesmos.sh/kanon"

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

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("IsZero", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the Tag is the zero Tag", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, tag.Tag.IsZero, func(tg tag.Tag) bool { return tg == tag.Tag{} },
				"IsZero must report whether both fields are empty", prop.Using(prop.Of[tag.Tag]()),
				prop.Example(tag.Tag{}), prop.Example(tag.Tag{Key: "k"}), prop.Example(tag.Tag{Value: "v"}),
				prop.Example(tag.Tag{Key: "k", Value: "v"}))
		})
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the recorded encoding of a Tag", func(t *testing.T) {
			t.Parallel()
			tg := recordedTag
			got, err := tg.AppendBinary(nil)
			assert.NoError(t, err, "AppendBinary must encode the Tag")
			assert.Equal(t, hex.EncodeToString(got), tagHex, "AppendBinary must write fields 1 and 2 as recorded")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Tag of the recorded encoding", func(t *testing.T) {
			t.Parallel()
			var got tag.Tag
			assert.NoError(t, got.UnmarshalBinary(decodeHex(t, tagHex)),
				"UnmarshalBinary must decode the recorded encoding")
			assert.Equal(t, got, recordedTag, "UnmarshalBinary must return the recorded Tag")
		})

		t.Run("returns ErrNotCanonical for the recorded fields in the other order", func(t *testing.T) {
			t.Parallel()
			var got tag.Tag
			assert.ErrorIs(t, got.UnmarshalBinary(decodeHex(t, swappedHex)), kanon.ErrNotCanonical,
				"UnmarshalBinary must reject an encoding that the encode does not write")
		})
	})
}

// TestTagAllocs checks the allocation contract of IsZero. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestTagAllocs(t *testing.T) {
	tg := tag.Tag{Key: "k", Value: "v"}

	t.Run("IsZero", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = tg.IsZero() }, 0, "IsZero must not allocate")
		assert.False(t, got, "the test must measure a Tag with both fields set")
	})
}

// BenchmarkTag reports the cost of IsZero, and fails when it allocates.
func BenchmarkTag(b *testing.B) {
	tg := tag.Tag{Key: "k", Value: "v"}

	b.Run("IsZero", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = tg.IsZero()
		}

		assert.False(b, got, "the benchmark must measure a Tag with both fields set")
	})
}

// decodeHex returns the bytes of the hexadecimal text s, and fails t when
// s is not hexadecimal.
func decodeHex(t *testing.T, s string) []byte {
	t.Helper()

	data, err := hex.DecodeString(s)
	assert.NoError(t, err, "the recorded encoding must be hexadecimal")

	return data
}
