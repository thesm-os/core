// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto/tsp"
	"go.thesmos.sh/core/errs"
)

func TestStatusError(t *testing.T) {
	t.Parallel()

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsp.StatusError
			want string
		}{
			{
				name: "returns every field for an error with every field set",
				give: tsp.StatusError{
					Status:   tsp.StatusRejection,
					FailInfo: 1 << tsp.FailTimeNotAvailable,
					Text:     "time source unavailable",
				},
				want: "tsp: authority returned status 2, failure bits 0x4000: time source unavailable",
			},
			{
				name: "returns the status alone for an error that has only a status",
				give: tsp.StatusError{Status: tsp.StatusWaiting},
				want: "tsp: authority returned status 3",
			},
			{
				name: "returns the failure bits for an error without text",
				give: tsp.StatusError{Status: tsp.StatusRejection, FailInfo: 1 << tsp.FailBadAlg},
				want: "tsp: authority returned status 2, failure bits 0x1",
			},
			{
				name: "returns the text for an error without failure bits",
				give: tsp.StatusError{Status: tsp.StatusRejection, Text: "no"},
				want: "tsp: authority returned status 2: no",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Error(), tt.want, "Error must describe the status")
			})
		}
	})

	t.Run("Class", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give tsp.StatusError
			want errs.Class
		}{
			{
				name: "returns Transient for waiting",
				give: tsp.StatusError{Status: tsp.StatusWaiting},
				want: errs.Transient,
			},
			{
				name: "returns Transient for a rejection whose time source is not available",
				give: tsp.StatusError{Status: tsp.StatusRejection, FailInfo: 1 << tsp.FailTimeNotAvailable},
				want: errs.Transient,
			},
			{
				name: "returns Transient for a rejection of a system failure",
				give: tsp.StatusError{
					Status:   tsp.StatusRejection,
					FailInfo: 1<<tsp.FailSystemFailure | 1<<tsp.FailBadAlg,
				},
				want: errs.Transient,
			},
			{
				name: "returns Invalid for a rejection of the request",
				give: tsp.StatusError{Status: tsp.StatusRejection, FailInfo: 1 << tsp.FailBadDataFormat},
				want: errs.Invalid,
			},
			{
				name: "returns Invalid for a rejection without failure bits",
				give: tsp.StatusError{Status: tsp.StatusRejection},
				want: errs.Invalid,
			},
			{
				name: "returns Denied for a revocation warning",
				give: tsp.StatusError{Status: tsp.StatusRevocationWarning},
				want: errs.Denied,
			},
			{
				name: "returns Denied for a revocation notification",
				give: tsp.StatusError{Status: tsp.StatusRevocationNotification},
				want: errs.Denied,
			},
			{
				name: "returns Unspecified for a status that RFC 3161 does not define",
				give: tsp.StatusError{Status: 6},
				want: errs.Unspecified,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				expect.Equal(t, tt.give.Class(), tt.want, "Class must classify the status")
				expect.Equal(t, errs.Classify(&tt.give), tt.want, "errs.Classify must use Class")
			})
		}
	})
}
