// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

//go:generate testkit sentinel -o errors.gen_test.go

package kernel

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// ErrRefresh is returned by [New] for a refresh interval that is not
// positive. It classifies as [errs.Invalid].
var ErrRefresh = errs.WithClass(errors.New("kernel: refresh must be positive"), errs.Invalid)
