// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// ErrFull is returned by [Concurrent.Store] for a new key when the map has
// its capacity of keys. Classifies as Transient: a Store of the key
// succeeds once a Delete has made room.
var ErrFull = errs.WithClass(errors.New("idmap: map is full"), errs.Transient)
