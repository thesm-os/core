// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

func TestRepairInternal(t *testing.T) {
	t.Parallel()

	t.Run("repairRecord", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the store for a record that it does not read", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			missing := recordPrefix + string(appendName(nil, 1, []byte("a missing record")))

			err := s.repairRecord(t.Context(), missing)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "repairRecord must return the error of the store")
		})

		t.Run("returns ErrJournal for a record whose note is not a signed note", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			name := putRecord(t, f.store, &record{Seq: 1, Time: 1, Calls: []call{{Note: []byte("not a note")}}})

			err := f.server(t).repairRecord(t.Context(), recordPrefix+name)
			testkit.ErrorIs(t, err, ErrJournal, "repairRecord must refuse the record")
		})
	})
}
