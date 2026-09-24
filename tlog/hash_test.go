// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"crypto/sha256"
	"crypto/sha512"
	"testing"

	"go.thesmos.sh/testkit"

	coresha256 "go.thesmos.sh/core/crypto/sha256"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/tlog"
)

func TestLeafHash(t *testing.T) {
	t.Parallel()

	data := []byte("an entry")

	t.Run("is SHA-256 of 0x00 and the data", func(t *testing.T) {
		t.Parallel()

		want := sha256.Sum256(append([]byte{0x00}, data...))
		testkit.Equal(t, tlog.LeafHash(coresha256.New(), data).Bytes(), want[:], "the leaf hash must be RFC 6962's")
	})

	t.Run("is SHA-384 of 0x00 and the data over SHA-384", func(t *testing.T) {
		t.Parallel()

		want := sha512.Sum384(append([]byte{0x00}, data...))
		testkit.Equal(t, tlog.LeafHash(coresha512.New384(), data).Bytes(), want[:],
			"the prefix must not depend on the hash")
	})
}

func TestNodeHash(t *testing.T) {
	t.Parallel()

	h := coresha256.New()
	left, right := h.Hash([]byte("left")), h.Hash([]byte("right"))

	t.Run("is SHA-256 of 0x01 and both children", func(t *testing.T) {
		t.Parallel()

		want := sha256.Sum256(append(append([]byte{0x01}, left.Bytes()...), right.Bytes()...))
		testkit.Equal(t, tlog.NodeHash(h, left, right).Bytes(), want[:], "the node hash must be RFC 6962's")
	})

	t.Run("is SHA-512 of 0x01 and both children over SHA-512", func(t *testing.T) {
		t.Parallel()

		h512 := coresha512.New512()
		l512, r512 := h512.Hash([]byte("left")), h512.Hash([]byte("right"))
		want := sha512.Sum512(append(append([]byte{0x01}, l512.Bytes()...), r512.Bytes()...))
		testkit.Equal(t, tlog.NodeHash(h512, l512, r512).Bytes(), want[:],
			"the node hash must cover both 64-byte children")
	})

	t.Run("depends on the order of the children", func(t *testing.T) {
		t.Parallel()
		testkit.NotEqual(t, tlog.NodeHash(h, right, left), tlog.NodeHash(h, left, right),
			"swapping the children must change the hash")
	})
}

// TestHashZeroAlloc enforces the allocation contracts of LeafHash and
// NodeHash. testing.AllocsPerRun reads a process-wide counter, so this
// test does not call t.Parallel, and it skips in a race build.
//
//nolint:paralleltest // see comment above
func TestHashZeroAlloc(t *testing.T) {
	skipUnderRace(t)

	h := coresha256.New()
	data := make([]byte, 128)
	left, right := h.Hash([]byte("left")), h.Hash([]byte("right"))

	for name, fn := range map[string]func(){
		"LeafHash": func() { _ = tlog.LeafHash(h, data) },
		"NodeHash": func() { _ = tlog.NodeHash(h, left, right) },
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, fn), float64(0), name+" must not allocate")
		})
	}
}

func BenchmarkLeafHash(b *testing.B) {
	h := coresha256.New()
	data := make([]byte, 128)
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.LeafHash(h, data)
	}
}

func BenchmarkNodeHash(b *testing.B) {
	h := coresha256.New()
	left, right := h.Hash([]byte("left")), h.Hash([]byte("right"))
	b.ReportAllocs()

	for b.Loop() {
		_ = tlog.NodeHash(h, left, right)
	}
}
