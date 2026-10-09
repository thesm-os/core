// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/internal/der"
)

// tagNumbers generates the tag numbers that fit an identifier octet.
var tagNumbers = prop.Integer[uint8](0, 30)

func TestTag(t *testing.T) {
	t.Parallel()

	t.Run("has the X.690 octet of each universal tag", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t,
			[]der.Tag{
				der.TagBoolean, der.TagInteger, der.TagBitString, der.TagOctetString, der.TagNull,
				der.TagOID, der.TagUTF8String, der.TagGeneralizedTime, der.TagSequence, der.TagSet,
			},
			[]der.Tag{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x0c, 0x18, 0x30, 0x31},
			"the universal tags must have their X.690 octets")
	})

	t.Run("Context", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0x80 plus the tag number", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, der.Context, func(n uint8) der.Tag { return 0x80 + der.Tag(n) },
				"[n] must be 0x80 plus n", prop.Using(tagNumbers), prop.Example(uint8(0)), prop.Example(uint8(1)))
		})
	})

	t.Run("ContextConstructed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0xa0 plus the tag number", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, der.ContextConstructed, func(n uint8) der.Tag { return 0xa0 + der.Tag(n) },
				"constructed [n] must be 0xa0 plus n", prop.Using(tagNumbers),
				prop.Example(uint8(0)), prop.Example(uint8(1)))
		})
	})
}
