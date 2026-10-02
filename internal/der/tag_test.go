// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("Context", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0x80 plus the tag number", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, der.Context(0), der.Tag(0x80), "[0] must be 0x80")
			testkit.Equal(t, der.Context(1), der.Tag(0x81), "[1] must be 0x81")
		})
	})

	t.Run("ContextConstructed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0xa0 plus the tag number", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, der.ContextConstructed(0), der.Tag(0xa0), "constructed [0] must be 0xa0")
			testkit.Equal(t, der.ContextConstructed(1), der.Tag(0xa1), "constructed [1] must be 0xa1")
		})
	})

	t.Run("constants", func(t *testing.T) {
		t.Parallel()

		t.Run("have the octets of X.690", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t,
				[]der.Tag{
					der.TagBoolean, der.TagInteger, der.TagBitString, der.TagOctetString, der.TagNull,
					der.TagOID, der.TagUTF8String, der.TagGeneralizedTime, der.TagSequence, der.TagSet,
				},
				[]der.Tag{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x0c, 0x18, 0x30, 0x31},
				"the universal tags must have their X.690 octets")
		})
	})
}
