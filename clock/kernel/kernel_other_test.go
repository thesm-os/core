// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package kernel_test

import (
	"errors"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock/kernel"
	"go.thesmos.sh/core/errs"
)

func TestReadKernel(t *testing.T) {
	t.Parallel()

	t.Run("returns an error classified Unsupported", func(t *testing.T) {
		t.Parallel()
		s, err := kernel.New(time.Second)
		assert.NoError(t, err, "New accepts a positive refresh")

		_, err = s.ReadUTC()
		assert.ErrorIs(t, err, errors.ErrUnsupported, "the read of the kernel is unsupported off Linux")
		assert.Equal(t, errs.Classify(err), errs.Unsupported, "the error classifies as Unsupported")
	})
}
