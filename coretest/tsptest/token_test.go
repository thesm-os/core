// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest_test

import (
	"encoding/asn1"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
)

func TestToken(t *testing.T) {
	t.Parallel()

	t.Run("Token", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrConfig for a Spec without a TSTInfo", func(t *testing.T) {
			t.Parallel()
			_, err := authority(t, config()).Token(tsptest.Spec{})
			testkit.ErrorIs(t, err, tsptest.ErrConfig, "Token must refuse the Spec")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("writes the SignerInfos of the Spec after the Authority's", func(t *testing.T) {
			t.Parallel()
			a := authority(t, config())
			tst, err := a.TSTInfo(request(t))
			testkit.NoError(t, err, "TSTInfo must accept the request")
			tok, err := a.Token(tsptest.Spec{TSTInfo: tst, SignerInfos: [][]byte{{0x30, 0x00}}})
			testkit.NoError(t, err, "Token must build the token")

			_, err = verifier(t, a).Verify(tok, tsp.SHA256, imprint)
			testkit.ErrorIs(t, err, tsp.ErrMalformed, "a token of two SignerInfos must not verify")
		})
	})

	t.Run("Attributes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			ess  tsptest.ESS
			want int
		}{
			{name: "returns three attributes for ESSV2", ess: tsptest.ESSV2, want: 3},
			{name: "returns three attributes for ESSV1", ess: tsptest.ESSV1, want: 3},
			{name: "returns four attributes for ESSBoth", ess: tsptest.ESSBoth, want: 4},
			{name: "returns two attributes for ESSNone", ess: tsptest.ESSNone, want: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := config()
				cfg.ESS = tt.ess

				testkit.Len(t, authority(t, cfg).Attributes([]byte{0x30, 0x00}), tt.want,
					"Attributes must write the content type, the message digest and the ESS attributes")
			})
		}
	})

	t.Run("Attribute", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the DER of an Attribute of the values", func(t *testing.T) {
			t.Parallel()
			want, err := asn1.Marshal(struct {
				Type   asn1.ObjectIdentifier
				Values []asn1.RawValue `asn1:"set"`
			}{
				Type:   asn1.ObjectIdentifier{1, 2, 3},
				Values: []asn1.RawValue{{FullBytes: []byte{0x02, 0x01, 0x07}}, {FullBytes: []byte{0x05, 0x00}}},
			})
			testkit.NoError(t, err, "encoding/asn1 must encode the Attribute")

			got := tsptest.Attribute([]byte{0x2a, 0x03}, []byte{0x02, 0x01, 0x07}, []byte{0x05, 0x00})
			testkit.Equal(t, got, want, "Attribute must write the type and the values in a SET")
		})

		t.Run("returns the values in the order given", func(t *testing.T) {
			t.Parallel()
			got := tsptest.Attribute([]byte{0x2a, 0x03}, []byte{0x05, 0x00}, []byte{0x02, 0x01, 0x07})
			testkit.Equal(t, got[len(got)-5:], []byte{0x05, 0x00, 0x02, 0x01, 0x07},
				"Attribute must not sort the values")
		})
	})
}
