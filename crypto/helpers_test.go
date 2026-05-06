// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
)

func TestHashDomain(t *testing.T) {
	t.Parallel()

	h := sha256.New()

	t.Run("equals manual stream over domain || parts", func(t *testing.T) {
		t.Parallel()
		domain := []byte("test:domain:")
		parts := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}

		got := crypto.HashDomain(h, domain, parts...)

		// Build the same digest manually via Stream.
		s := h.NewStream()
		_, _ = s.Write(domain)
		for _, p := range parts {
			_, _ = s.Write(p)
		}
		want := s.Sum()

		if !got.Equal(want) {
			t.Fatalf("HashDomain != manual stream:\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("different domains produce different digests", func(t *testing.T) {
		t.Parallel()
		data := []byte("identical-payload")
		a := crypto.HashDomain(h, []byte("domain-a"), data)
		b := crypto.HashDomain(h, []byte("domain-b"), data)
		if a.Equal(b) {
			t.Fatal("different domains must produce different digests for identical data")
		}
	})

	t.Run("zero parts produces hash of domain alone", func(t *testing.T) {
		t.Parallel()
		domain := []byte("just:the:domain")
		got := crypto.HashDomain(h, domain)
		want := h.Hash(domain)
		if !got.Equal(want) {
			t.Fatalf("HashDomain(_, domain) != Hash(domain):\n got=%s\nwant=%s", got, want)
		}
	})
}

func TestHashReader(t *testing.T) {
	t.Parallel()

	h := sha256.New()

	t.Run("equals Hash over the same bytes", func(t *testing.T) {
		t.Parallel()
		payload := bytes.Repeat([]byte("agentic context bytes "), 1000) // ~22 KB
		want := h.Hash(payload)

		got, err := crypto.HashReader(h, bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("HashReader: unexpected error %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("HashReader != Hash(buf):\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("empty reader produces hash of empty input", func(t *testing.T) {
		t.Parallel()
		want := h.Hash(nil)
		got, err := crypto.HashReader(h, strings.NewReader(""))
		if err != nil {
			t.Fatalf("HashReader: unexpected error %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("HashReader(empty) != Hash(nil):\n got=%s\nwant=%s", got, want)
		}
	})

	t.Run("reader error is wrapped with package context", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("network read failed")
		_, err := crypto.HashReader(h, &errReader{err: sentinel})
		if err == nil {
			t.Fatal("expected error from failing reader")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("error did not wrap sentinel: %v", err)
		}
	})
}

// errReader returns a fixed error on every Read.
type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// Compile-time interface check.
var _ io.Reader = (*errReader)(nil)
