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

// FuzzHashReader asserts the property that streaming bytes
// through [crypto.HashReader] produces the same digest as
// calling [crypto.Hasher.Hash] on the same bytes directly,
// for arbitrary input. Catches any divergence between the
// streaming and one-shot paths.
func FuzzHashReader(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("hello world"))
	f.Add(bytes.Repeat([]byte{0xAA}, 1024))

	h := sha256.New()
	f.Fuzz(func(t *testing.T, data []byte) {
		want := h.Hash(data)
		got, err := crypto.HashReader(h, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("HashReader: %v", err)
		}
		if !got.Equal(want) {
			t.Fatalf("HashReader != Hash for %d bytes", len(data))
		}
	})
}

// FuzzHashDomain asserts the property that
// [crypto.HashDomain] is equivalent to manually streaming the
// domain followed by a single payload through a [crypto.Stream].
// The variadic-parts case is tested elsewhere; this fuzz keeps
// the surface small.
func FuzzHashDomain(f *testing.F) {
	f.Add([]byte("domain:"), []byte("payload"))
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("audit"), bytes.Repeat([]byte{0x55}, 256))

	h := sha256.New()
	f.Fuzz(func(t *testing.T, domain, part []byte) {
		s := h.NewStream()
		_, _ = s.Write(domain)
		_, _ = s.Write(part)
		want := s.Sum()

		got := crypto.HashDomain(h, domain, part)
		if !got.Equal(want) {
			t.Fatalf("HashDomain != Stream(domain).Write(part).Sum() for "+
				"domain=%d bytes part=%d bytes", len(domain), len(part))
		}
	})
}

// FuzzStreamReset asserts that [crypto.Stream.Reset] restores
// the initial state: writing the same bytes before and after
// Reset produces equal digests.
func FuzzStreamReset(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("hello"))
	f.Add(bytes.Repeat([]byte{0x33}, 4096))

	h := sha256.New()
	s := h.NewStream()
	f.Fuzz(func(t *testing.T, data []byte) {
		s.Reset()
		_, _ = s.Write(data)
		first := s.Sum()

		s.Reset()
		_, _ = s.Write(data)
		second := s.Sum()

		if !first.Equal(second) {
			t.Fatalf("Reset did not restore initial state for %d bytes",
				len(data))
		}
	})
}

func BenchmarkHashDomain(b *testing.B) {
	h := sha256.New()
	domain := []byte("thesmos.audit.v1")
	parts := [][]byte{[]byte("entry"), []byte("payload")}
	b.ReportAllocs()
	for b.Loop() {
		_ = crypto.HashDomain(h, domain, parts...)
	}
}

func BenchmarkHashReader(b *testing.B) {
	h := sha256.New()
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"4K", 4 * 1024},
		{"64K", 64 * 1024},
		{"1M", 1024 * 1024},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_, _ = crypto.HashReader(h, bytes.NewReader(data))
			}
		})
	}
}
