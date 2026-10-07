// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.thesmos.sh/kanon/wire"
)

func TestAdvanceInternal(t *testing.T) {
	t.Parallel()

	t.Run("prepare", func(t *testing.T) {
		t.Parallel()

		l := newInternalLog(t, "example.com/a")
		msg := l.note(t, 5)
		root := l.update(t, 0, 5, nil).Body.Root

		// callSize returns the length of the encoding in a record of the call
		// of msg with the update of l to the size 5 and a prefix of n bytes.
		callSize := func(n int) int {
			c := call{Note: msg, Updates: []entry{{Origin: l.origin, Prefix: make([]byte, n), Root: root, Size: 5}}}

			return wire.SizeBytes(c.SizeKanon())
		}

		// Near 16 MiB the lengths in the encoding have a fixed width, so each
		// byte of the prefix adds one byte to the call.
		near := maxCommitBytes - 1024
		prefix := near + maxCommitBytes - callSize(near)
		assert.Equal(t, callSize(prefix), maxCommitBytes, "the call of the cases must take 16 MiB in a record")

		t.Run("accepts a call of 16 MiB in a record", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t, l).server(t)
			p := &pending{}
			u := l.update(t, 0, 5, make([]byte, prefix))
			assert.NoError(t, p.fill(msg, []Update{u}), "fill must copy the call")
			assert.NoError(t, s.prepare(p), "prepare must accept a call of 16 MiB")
		})

		t.Run("returns ErrRequest for a call of 16 MiB and one byte in a record", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t, l).server(t)
			p := &pending{}
			u := l.update(t, 0, 5, make([]byte, prefix+1))
			assert.NoError(t, p.fill(msg, []Update{u}), "fill must copy the call")
			assert.ErrorIs(t, s.prepare(p), ErrRequest, "prepare must refuse a call above 16 MiB")
		})
	})
}
