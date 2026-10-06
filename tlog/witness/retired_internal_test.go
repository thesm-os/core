// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/tlog/checkpoint"
)

func TestRetiredInternal(t *testing.T) {
	t.Parallel()

	origin := checkpoint.Origin("example.com/retired")
	h := hashOrigin(origin)
	root := crypto.NewDigest256([32]byte{7})

	// retire stores the retired object of origin at size in st.
	retire := func(tb testing.TB, st blob.Store, size uint64) {
		tb.Helper()

		data, _ := (&entry{Origin: origin, Root: root, Size: size}).MarshalBinary()
		putObject(tb, st, retiredKey(h, size), data)
	}

	t.Run("retiredKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the prefix, the hash, a hyphen and the size in 20 digits", func(t *testing.T) {
			t.Parallel()
			key := retiredKey(h, 42)
			testkit.True(t, strings.HasPrefix(key, retiredPrefix), "the key must start with the prefix")
			testkit.True(t, strings.HasSuffix(key, "-00000000000000000042"), "the key must end with the size")
			testkit.Equal(t, len(key), len(retiredPrefix)+retiredText, "the key must have the length of a key")
		})
	})

	t.Run("parseRetiredKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the hash and the size of a key", func(t *testing.T) {
			t.Parallel()
			got, size, ok := parseRetiredKey(retiredKey(h, 42))
			testkit.True(t, ok, "parseRetiredKey must accept the key")
			testkit.Equal(t, got, h, "parseRetiredKey must read the hash")
			testkit.Equal(t, size, uint64(42), "parseRetiredKey must read the size")
		})

		valid := retiredKey(h, 42)
		hash := valid[len(retiredPrefix) : len(retiredPrefix)+hashText]
		size := valid[len(retiredPrefix)+hashText+1:]
		tests := []struct {
			name string
			give string
		}{
			{name: "reports false for a key without the prefix", give: "x" + valid[1:]},
			{name: "reports false for a key of another length", give: valid + "0"},
			{name: "reports false for a key without the hyphen", give: retiredPrefix + hash + "0" + size},
			{
				name: "reports false for a hash that is not hexadecimal",
				give: retiredPrefix + "g" + hash[1:] + "-" + size,
			},
			{name: "reports false for a hash of upper case", give: retiredPrefix + strings.ToUpper(hash) + "-" + size},
			{name: "reports false for a size that is not decimal", give: valid[:len(valid)-1] + "x"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, ok := parseRetiredKey(tt.give)
				testkit.False(t, ok, "parseRetiredKey must refuse "+tt.give)
			})
		}
	})

	t.Run("listRetired", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the prefix of every retired origin over every page", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)

			for i := range 120 {
				var other originHash
				binary.BigEndian.PutUint64(other[:], uint64(i))
				putObject(t, f.store, retiredKey(other, 1), []byte("an entry"))
			}

			set, err := f.server(t).listRetired(bounded(t))
			testkit.NoError(t, err, "listRetired must walk the store")
			testkit.Equal(t, set.Len(), 120, "listRetired must return every prefix")
		})

		t.Run("returns ErrJournal for a key that is not the key of a retired origin", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			putObject(t, f.store, retiredPrefix+"other", []byte("an object"))

			_, err := f.server(t).listRetired(bounded(t))
			testkit.ErrorIs(t, err, ErrJournal, "listRetired must refuse the key")
		})

		failures := []struct {
			edit func(st *faulty)
			name string
		}{
			{
				name: "returns the error of the store for a walk that fails",
				edit: func(st *faulty) { st.list = func(string) error { return errors.New("the walk failed") } },
			},
			{
				name: "returns the error of a page that fails",
				edit: func(st *faulty) { st.page = func(string) error { return errors.New("the page failed") } },
			},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newInternalFixture(t)
				st := &faulty{Store: f.store}
				cfg := f.config()
				cfg.State = st
				s := newInternalServer(t, cfg)
				tt.edit(st)

				_, err := s.listRetired(bounded(t))
				testkit.Error(t, err, "listRetired must fail")
			})
		}
	})

	t.Run("lookupRetired", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the checkpoint of the retired object of the largest size", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			retire(t, f.store, 3)
			retire(t, f.store, 9)
			retire(t, f.store, 5)

			c, err := f.server(t).lookupRetired(bounded(t), h, origin)
			testkit.NoError(t, err, "lookupRetired must read the object")
			testkit.True(t, c == committed{root: root, size: 9}, "lookupRetired must return the largest size")
		})

		t.Run("returns a fresh checkpoint for an origin without a retired object", func(t *testing.T) {
			t.Parallel()
			c, err := newInternalFixture(t).server(t).lookupRetired(bounded(t), h, origin)
			testkit.NoError(t, err, "lookupRetired must walk the store")
			testkit.True(t, c.fresh, "the origin must be new")
		})

		t.Run("returns the checkpoint of a retired object of size 0", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			retire(t, f.store, 0)

			c, err := f.server(t).lookupRetired(bounded(t), h, origin)
			testkit.NoError(t, err, "lookupRetired must read the object")
			testkit.True(t, c == committed{root: root}, "lookupRetired must return the size 0")
		})

		t.Run("returns the checkpoint of the retired object of a long origin", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			long := checkpoint.Origin("example.com/" + strings.Repeat("a", 200))
			lh := hashOrigin(long)
			data, _ := (&entry{Origin: long, Root: root, Size: 3}).MarshalBinary()
			putObject(t, f.store, retiredKey(lh, 3), data)

			c, err := f.server(t).lookupRetired(bounded(t), lh, long)
			testkit.NoError(t, err, "lookupRetired must read the object of the long origin")
			testkit.True(t, c == committed{root: root, size: 3}, "lookupRetired must return the checkpoint")
		})

		other, _ := (&entry{Origin: "example.com/other", Root: root, Size: 3}).MarshalBinary()
		larger, _ := (&entry{Origin: origin, Root: root, Size: 4}).MarshalBinary()
		tests := []struct {
			name string
			key  string
			give []byte
		}{
			{
				name: "returns ErrJournal for a key that is not the key of a retired origin",
				key:  retiredKey(h, 0)[:len(retiredPrefix)+hashText+1] + "x",
			},
			{
				name: "returns ErrJournal for an object above its read limit", key: retiredKey(h, 3),
				give: make([]byte, entryOverhead+len(origin)+1),
			},
			{name: "returns ErrJournal for an object that does not decode", key: retiredKey(h, 3), give: []byte{0xff}},
			{name: "returns ErrJournal for an object of another origin", key: retiredKey(h, 3), give: other},
			{name: "returns ErrJournal for an object of another size", key: retiredKey(h, 3), give: larger},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newInternalFixture(t)
				putObject(t, f.store, tt.key, tt.give)

				_, err := f.server(t).lookupRetired(bounded(t), h, origin)
				testkit.ErrorIs(t, err, ErrJournal, "lookupRetired must refuse the object")
			})
		}

		t.Run("returns the error of the store for a read that fails", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			retire(t, f.store, 3)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)
			st.get = func(key string) error {
				if strings.HasPrefix(key, retiredPrefix) {
					return errors.New("the read failed")
				}

				return nil
			}

			_, err := s.lookupRetired(bounded(t), h, origin)
			testkit.Error(t, err, "lookupRetired must fail")
			testkit.ErrorIsNot(t, err, ErrJournal, "the error must be the error of the store")
		})

		t.Run("fails every call of a commit when the walk of a retired origin fails", func(t *testing.T) {
			t.Parallel()
			l := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, l)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			lh := hashOrigin(l.origin)
			s.st.retired.Add(binary.BigEndian.Uint64(lh[:]))
			st.list = func(string) error { return errors.New("the walk failed") }

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			testkit.Error(t, err, "Advance must fail")
		})
	})
}
