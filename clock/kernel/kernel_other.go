// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package kernel

import (
	"errors"
	"fmt"
)

// readKernel fails, because only Linux exposes its time discipline
// through adjtimex(2).
func readKernel() (status, error) {
	return status{}, fmt.Errorf("kernel: adjtimex: %w", errors.ErrUnsupported)
}
