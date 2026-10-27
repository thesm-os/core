// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// ErrConfig is returned by [New] for a [Config] with a Capacity that is not
// positive. Classifies as Invalid.
var ErrConfig = errs.WithClass(errors.New("cache: invalid configuration"), errs.Invalid)
