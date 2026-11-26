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

// ErrLoad is returned by [Cache.GetOrLoad] for a nil load on a miss, and for
// a load that succeeds with an expiry time in a cache without a
// [Config.Clock]. Classifies as Invalid.
var ErrLoad = errs.WithClass(errors.New("cache: invalid load"), errs.Invalid)
