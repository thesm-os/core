// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/witness"
)

// errorPrefix is the start of the text of every error of the package.
const errorPrefix = "witness: "

func TestErrors(t *testing.T) {
	t.Parallel()

	sentinels := []struct {
		err   error
		name  string
		class errs.Class
	}{
		{name: "ErrConfig", err: witness.ErrConfig, class: errs.Invalid},
		{name: "ErrRequest", err: witness.ErrRequest, class: errs.Invalid},
		{name: "ErrUnknownOrigin", err: witness.ErrUnknownOrigin, class: errs.NotFound},
		{name: "ErrSignature", err: witness.ErrSignature, class: errs.Denied},
		{name: "ErrInconsistent", err: witness.ErrInconsistent, class: errs.Integrity},
		{name: "ErrCosignature", err: witness.ErrCosignature, class: errs.Integrity},
		{name: "ErrJournal", err: witness.ErrJournal, class: errs.Integrity},
		{name: "ErrContention", err: witness.ErrContention, class: errs.Transient},
	}

	for _, tt := range sentinels {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("classifies as "+tt.class.String(), func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, errs.Classify(tt.err), tt.class, tt.name+" must classify as "+tt.class.String())
			})

			t.Run("starts its text with the name of the package", func(t *testing.T) {
				t.Parallel()
				assert.HasPrefix(t, tt.err.Error(), errorPrefix, tt.name+" must start with "+errorPrefix)
			})

			t.Run("has a text that no other sentinel has", func(t *testing.T) {
				t.Parallel()
				for _, other := range sentinels {
					if other.name != tt.name {
						assert.NotEqual(t, tt.err.Error(), other.err.Error(),
							tt.name+" and "+other.name+" must have distinct texts")
						assert.ErrorIsNot(t, tt.err, other.err, tt.name+" must not match "+other.name)
					}
				}
			})
		})
	}
}
