// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package kernel

import (
	"errors"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

func TestReadKernel(t *testing.T) {
	t.Parallel()

	t.Run("returns an error classified Unsupported", func(t *testing.T) {
		t.Parallel()
		s, err := New(time.Second)
		testkit.NoError(t, err, "New must accept a positive refresh")

		_, err = s.ReadUTC()
		testkit.ErrorIs(t, err, errors.ErrUnsupported, "reading the kernel must be unsupported off Linux")
		testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
	})
}
