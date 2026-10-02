// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest_test

import (
	"crypto/x509"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto/tsp"
)

func TestStatusResponse(t *testing.T) {
	t.Parallel()

	t.Run("returns a response of the status, the failure bits and the texts", func(t *testing.T) {
		t.Parallel()
		resp := tsptest.StatusResponse(tsp.StatusRejection,
			[]int{tsp.FailUnacceptedPolicy, tsp.FailSystemFailure}, "first", "second")

		_, err := tsp.ParseResponse(resp, tsp.SHA256, imprint, nonce, x509.OID{})
		se := testkit.ErrorAs[*tsp.StatusError](t, err, "the response must grant no token")
		testkit.Equal(t, se.Status, tsp.StatusRejection, "the status must be the given one")
		testkit.Equal(t, se.FailInfo, uint32(1<<tsp.FailUnacceptedPolicy|1<<tsp.FailSystemFailure),
			"the failInfo must set the given bits")
		testkit.Equal(t, se.Text, "first; second", "the statusString must hold the given texts")
	})

	t.Run("returns a response of the status alone", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(
			t,
			tsptest.StatusResponse(tsp.StatusWaiting, nil),
			[]byte{0x30, 0x05, 0x30, 0x03, 0x02, 0x01, 0x03},
			"a response without texts or bits must hold only the PKIStatus",
		)
	})
}
