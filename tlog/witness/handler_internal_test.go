// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

func TestHandlerInternal(t *testing.T) {
	t.Parallel()

	t.Run("pending", func(t *testing.T) {
		t.Parallel()

		t.Run("parseRequest", func(t *testing.T) {
			t.Parallel()

			// The pool of a Server returns a call to the next request with the
			// parses of the request before, which reset keeps.
			t.Run("returns ErrRequest for a body without a newline after a body that it parsed", func(t *testing.T) {
				t.Parallel()
				p := &pending{}
				assert.NoError(t, p.parseRequest(strings.NewReader(exampleRequest)),
					"parseRequest must accept the example")
				p.reset()

				err := p.parseRequest(strings.NewReader("old 5"))
				assert.ErrorIs(t, err, ErrRequest, "parseRequest must refuse the body")
			})

			_, lines, _ := strings.Cut(exampleNote, "\n\n")
			tests := []struct {
				want error
				name string
				give string
			}{
				{
					name: "returns an error that wraps the error of the note for a note that does not parse",
					give: "old 0\n\nnot a note", want: note.ErrNote,
				},
				{
					name: "returns an error that wraps the error of the body for a text that is not a checkpoint body",
					give: "old 0\n\na text\n\n" + lines, want: checkpoint.ErrBody,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					err := (&pending{}).parseRequest(strings.NewReader(tt.give))
					expect.That(t, err).
						ErrorIs(ErrRequest, "parseRequest must refuse the body").
						ErrorIs(tt.want, "the error must wrap the error of the parse")
				})
			}
		})
	})
}
