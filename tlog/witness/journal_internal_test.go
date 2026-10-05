// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

// sha256OfAbc is the lowercase hexadecimal SHA-256 of "abc", which pins the
// hash of a name.
const sha256OfAbc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestJournalInternal(t *testing.T) {
	t.Parallel()

	t.Run("appendName", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the sequence number in 20 digits, a hyphen and the hash of the encoding", func(t *testing.T) {
			t.Parallel()
			got := string(appendName([]byte("records/"), 42, []byte("abc")))
			testkit.Equal(t, got, "records/00000000000000000042-"+sha256OfAbc, "appendName must write the name")
		})

		t.Run("appends the largest sequence number in 20 digits", func(t *testing.T) {
			t.Parallel()
			got := string(appendName(nil, 18446744073709551615, []byte("abc")))
			testkit.Equal(t, got, "18446744073709551615-"+sha256OfAbc, "appendName must write the name")
		})
	})

	t.Run("parseName", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sequence number of a name", func(t *testing.T) {
			t.Parallel()
			seq, ok := parseName("00000000000000000042-" + sha256OfAbc)
			testkit.True(t, ok, "parseName must accept the name")
			testkit.Equal(t, seq, uint64(42), "parseName must read the sequence number")
		})

		valid := "00000000000000000042-" + sha256OfAbc
		tests := []struct {
			name string
			give string
		}{
			{name: "reports false for an empty name", give: ""},
			{name: "reports false for a name of another length", give: valid + "0"},
			{name: "reports false for a name without the hyphen", give: strings.Replace(valid, "-", "0", 1)},
			{name: "reports false for a sequence number that is not decimal", give: "a" + valid[1:]},
			{name: "reports false for a hash of upper case", give: valid[:21] + strings.ToUpper(valid[21:])},
			{name: "reports false for a hash that is not hexadecimal", give: valid[:len(valid)-1] + "g"},
			{
				name: "reports false for a sequence number above the largest uint64",
				give: "99999999999999999999-" + sha256OfAbc,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseName(tt.give)
				testkit.False(t, ok, "parseName must refuse "+tt.give)
			})
		}
	})

	t.Run("checkName", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for the encoding of the name", func(t *testing.T) {
			t.Parallel()
			testkit.NoError(t, checkName("00000000000000000042-"+sha256OfAbc, []byte("abc")),
				"checkName must accept the encoding")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrJournal for another encoding", give: "00000000000000000042-" + sha256OfAbc},
			{name: "returns ErrJournal for a name of another length", give: "42-" + sha256OfAbc},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				err := checkName(tt.give, []byte("abd"))
				testkit.ErrorIs(t, err, ErrJournal, "checkName must refuse the encoding")
				testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			})
		}
	})
}
