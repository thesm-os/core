// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/btree"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/tlog/checkpoint"
)

const (
	// hashText is the length of the lowercase hexadecimal of an origin
	// hash.
	hashText = 64

	// retiredText is the length of the key of a retired object after its
	// prefix: the hashText digits of the origin hash, a hyphen, and the
	// retired size in seqText decimal digits.
	retiredText = 85

	// entryOverhead bounds the bytes of the encoding of an entry without a
	// prefix beyond its origin: the tags and the lengths of its fields, its
	// root and its size.
	entryOverhead = 128
)

// committed is the latest committed checkpoint of an origin, which a
// commit checks a call against.
type committed struct {
	// size and root are the size and the root of the checkpoint, and 0
	// and the zero Digest for a new origin.
	size uint64
	root crypto.Digest

	// fresh reports whether the origin is new to the witness: neither in
	// the state nor retired.
	fresh bool
}

// retiredKey returns the key of the retired object of the origin of hash
// h at size: the prefix of the retired objects, the lowercase hexadecimal
// of h, a hyphen, and size in seqText decimal digits. It builds the key in
// an array on its stack, and allocates the string.
func retiredKey(h originHash, size uint64) string {
	var buf [len(retiredPrefix) + retiredText]byte

	b := append(buf[:0], retiredPrefix...)
	b = hex.AppendEncode(b, h[:])
	b = append(b, '-')

	var digits [seqText]byte

	d := strconv.AppendUint(digits[:0], size, 10)
	for range seqText - len(d) {
		b = append(b, '0')
	}

	return string(append(b, d...))
}

// parseRetiredKey returns the origin hash and the size of the key of a
// retired object, as retiredKey writes it, and reports whether key is such
// a key.
func parseRetiredKey(key string) (originHash, uint64, bool) {
	name, ok := strings.CutPrefix(key, retiredPrefix)
	if !ok || len(name) != retiredText || name[hashText] != '-' {
		return originHash{}, 0, false
	}

	// Text of another character than a lowercase hexadecimal digit does not
	// encode back to itself, whatever Decode decoded of it.
	var h originHash

	_, _ = hex.Decode(h[:], []byte(name[:hashText]))
	if hex.EncodeToString(h[:]) != name[:hashText] {
		return originHash{}, 0, false
	}

	size, err := strconv.ParseUint(name[hashText+1:], 10, 64)

	return h, size, err == nil
}

// listRetired returns the set of the retired origins in the store: the
// first 8 bytes of the origin hash of every retired object, read as a
// big-endian uint64, from one walk of [blob.Store.List] over the prefix of
// the retired objects. The keys contain the hashes, so the walk reads no
// object.
//
// Returns an error that wraps [ErrJournal] for a key under the prefix that
// is not the key of a retired object, and the errors of the store.
//
// # Allocation contract
//
// Allocates the pages of the walk, the bytes of each hash that it decodes,
// and the nodes of the set.
func (s *Server) listRetired(ctx context.Context) (*btree.Set[uint64], error) {
	set := new(btree.Set[uint64])

	err := s.list(ctx, retiredPrefix, func(key string) error {
		h, _, ok := parseRetiredKey(key)
		if !ok {
			return fmt.Errorf("%w: the key %q is not the key of a retired origin", ErrJournal, key)
		}

		set.Add(binary.BigEndian.Uint64(h[:]))

		return nil
	})
	if err != nil {
		return nil, err
	}

	return set, nil
}

// lookupRetired returns the checkpoint at which a snapshot retired origin
// o of hash h last: the root and the size of its retired object of the
// largest size. It returns a fresh committed for an origin without one,
// which shares the first 8 bytes of its hash with a retired origin.
//
// Returns an error that wraps [ErrJournal] for a key under the prefix of
// the origin that is not the key of a retired object, for a retired object
// that does not decode, and for one whose origin or size is not the origin
// and the size of its key. Returns the errors of the store.
//
// # Allocation contract
//
// Allocates the prefix of the walk, its pages, and the bytes of the
// object.
func (s *Server) lookupRetired(ctx context.Context, h originHash, o checkpoint.Origin) (committed, error) {
	key, size, found := "", uint64(0), false

	err := s.list(ctx, retiredPrefix+hex.EncodeToString(h[:])+"-", func(k string) error {
		_, n, ok := parseRetiredKey(k)
		if !ok {
			return fmt.Errorf("%w: the key %q is not the key of a retired origin", ErrJournal, k)
		}

		// n equals size only for a first key of size 0, because the keys of
		// one origin differ in their sizes.
		if n >= size {
			key, size = k, n
		}

		found = true

		return nil
	})
	if err != nil {
		return committed{}, err
	}

	if !found {
		return committed{fresh: true}, nil
	}

	data, err := read(ctx, s.store, key, entryOverhead+int64(len(o)))
	if err != nil {
		return committed{}, err
	}

	var e entry
	if err := e.DecodeKanon(data, kanon.Options{}); err != nil || e.Origin != o || e.Size != size {
		return committed{}, fmt.Errorf("%w: the retired object %s does not contain the origin and the size of its "+
			"key", ErrJournal, key)
	}

	return committed{root: e.Root, size: e.Size}, nil
}

// list calls fn with the key of every object of the store under prefix, in
// the order of the pages of [blob.Store.List], and stops at the first
// error of fn.
//
// Returns the error of fn, and the errors of the store.
func (s *Server) list(ctx context.Context, prefix string, fn func(key string) error) error {
	for token := ""; ; {
		cur, err := s.store.List(ctx, prefix, page.Page{Token: token})
		if err != nil {
			return fmt.Errorf("witness: list %s: %w", prefix, err)
		}

		for info, err := range cur.Seq(ctx) {
			if err == nil {
				err = fn(info.Key)
			}

			if err != nil {
				_ = cur.Close()

				return fmt.Errorf("witness: list %s: %w", prefix, err)
			}
		}

		token = cur.NextPage()
		_ = cur.Close()

		if token == "" {
			return nil
		}
	}
}
