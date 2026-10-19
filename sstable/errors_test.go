// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/sstable"
)

// errorPrefix is the start of the text of every error of the package.
const errorPrefix = "sstable: "

func TestErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		give      error
		wantClass errs.Class
	}{
		{name: "ErrOptions", give: sstable.ErrOptions, wantClass: errs.Invalid},
		{name: "ErrOrder", give: sstable.ErrOrder, wantClass: errs.Invalid},
		{name: "ErrClosed", give: sstable.ErrClosed, wantClass: errs.Invalid},
		{name: "ErrTooLarge", give: sstable.ErrTooLarge, wantClass: errs.Invalid},
		{name: "ErrRange", give: sstable.ErrRange, wantClass: errs.Invalid},
		{name: "ErrPrefix", give: sstable.ErrPrefix, wantClass: errs.Invalid},
		{name: "ErrCorrupt", give: sstable.ErrCorrupt, wantClass: errs.Integrity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("classifies as "+tt.wantClass.String(), func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, errs.Classify(tt.give), tt.wantClass, tt.name+" classifies as "+tt.wantClass.String())
			})

			t.Run("starts its text with the name of the package", func(t *testing.T) {
				t.Parallel()
				assert.HasPrefix(t, tt.give.Error(), errorPrefix, tt.name+" must start with "+errorPrefix)
			})

			t.Run("has a text that no other sentinel has", func(t *testing.T) {
				t.Parallel()
				for _, other := range tests {
					if other.name != tt.name {
						assert.NotEqual(t, tt.give.Error(), other.give.Error(),
							tt.name+" and "+other.name+" must have distinct texts")
						assert.ErrorIsNot(t, tt.give, other.give, tt.name+" must not match "+other.name)
					}
				}
			})
		})
	}
}
