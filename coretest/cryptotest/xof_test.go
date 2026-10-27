// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"testing"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/shake"
)

// TestXOF checks that the SHAKE128 XOF of crypto/shake passes the checks of the
// suite of crypto.XOF.
func TestXOF(t *testing.T) {
	t.Parallel()

	cryptotest.RunXOFSuite(t, shake.New128)
}
