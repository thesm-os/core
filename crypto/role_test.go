// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
)

// roleBoundary is the first binary role. A protocol writes its role
// bytes into persisted digests, so the boundary is part of the contract.
const roleBoundary = 0x80

func TestRole(t *testing.T) {
	t.Parallel()

	t.Run("IsUnary", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true exactly for a role below 0x80", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, crypto.Role.IsUnary, func(r crypto.Role) bool { return r < roleBoundary },
				"IsUnary must report the roles below 0x80",
				prop.Using(prop.Integer[crypto.Role](0, 0xFF)),
				prop.Example(crypto.Role(0x7F)), prop.Example(crypto.Role(roleBoundary)))
		})
	})

	t.Run("IsBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true exactly for a role from 0x80", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, crypto.Role.IsBinary, func(r crypto.Role) bool { return r >= roleBoundary },
				"IsBinary must report the roles from 0x80",
				prop.Using(prop.Integer[crypto.Role](0, 0xFF)),
				prop.Example(crypto.Role(0x7F)), prop.Example(crypto.Role(roleBoundary)))
		})

		t.Run("reports the opposite of IsUnary for every role", func(t *testing.T) {
			t.Parallel()
			roles := make([]crypto.Role, 0, 256)
			for i := range 256 {
				roles = append(roles, crypto.Role(i))
			}
			assert.Total(t, func(r crypto.Role) error {
				if r.IsUnary() == r.IsBinary() {
					return fmt.Errorf("role %#02x: IsUnary and IsBinary both report %t", byte(r), r.IsUnary())
				}

				return nil
			}, roles, "every role must have exactly one arity")
		})
	})
}
