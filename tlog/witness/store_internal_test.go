// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

func TestStoreInternal(t *testing.T) {
	t.Parallel()

	name := string(appendName(nil, 1, []byte("abc")))

	t.Run("readHead", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrJournal for a head that names a snapshot that is not a name", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			putHead(t, st, head{Record: name, Snapshot: "a snapshot"})

			_, _, err := readHead(t.Context(), st)
			assert.ErrorIs(t, err, ErrJournal, "readHead must refuse the name of the snapshot")
			assert.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})

		t.Run("returns ErrJournal for a head that names a record that is not a name", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			putHead(t, st, head{Record: "a record"})

			_, _, err := readHead(t.Context(), st)
			assert.ErrorIs(t, err, ErrJournal, "readHead must refuse the name of the record")
		})

		t.Run("returns ErrJournal for a head with a byte after its encoding", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			data, err := (&head{Record: name}).MarshalBinary()
			assert.NoError(t, err, "the head must encode")
			putObject(t, st, headKey, append(data, 0))

			_, _, err = readHead(t.Context(), st)
			assert.ErrorIs(t, err, ErrJournal, "readHead must refuse the head")
		})
	})

	t.Run("put", func(t *testing.T) {
		t.Parallel()

		// The pool returns the reader that put returned to it to the next Get
		// of the goroutine.
		t.Run("returns its reader to the pool without the data", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			_, err := put(t.Context(), st, recordPrefix+name, []byte("abc"), version.WriteOptions{})
			assert.NoError(t, err, "put must write the object")

			r := readers.Get()
			defer readers.Put(r)

			assert.Equal(t, r.Size(), int64(0), "the pooled reader must contain no data")
		})
	})

	t.Run("read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrJournal for an object above the limit", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			putObject(t, st, recordPrefix+name, []byte("abcdef"))

			data, err := read(t.Context(), st, recordPrefix+name, 5)
			expect.ErrorIs(t, err, ErrJournal, "read must refuse the object")
			expect.Nil(t, data, "read must return no bytes with the error")
		})
	})

	t.Run("readNamed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrJournal for bytes whose hash is not the hash of the name", func(t *testing.T) {
			t.Parallel()
			st := memory.New(newInternalFixture(t).clock)
			putObject(t, st, recordPrefix+name, []byte("abd"))

			_, err := readNamed(t.Context(), st, recordPrefix, name, maxObjectBytes)
			assert.ErrorIs(t, err, ErrJournal, "readNamed must refuse the bytes")
			assert.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})
	})
}
