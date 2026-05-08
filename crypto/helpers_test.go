// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

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

		testkit.True(t, got.Equal(want),
			"HashDomain must equal manual stream over domain || parts")
	})

	t.Run("different domains produce different digests", func(t *testing.T) {
		t.Parallel()
		data := []byte("identical-payload")
		a := crypto.HashDomain(h, []byte("domain-a"), data)
		b := crypto.HashDomain(h, []byte("domain-b"), data)
		testkit.False(t, a.Equal(b),
			"different domains must produce different digests for identical data")
	})

	t.Run("zero parts produces hash of domain alone", func(t *testing.T) {
		t.Parallel()
		domain := []byte("just:the:domain")
		testkit.True(t, crypto.HashDomain(h, domain).Equal(h.Hash(domain)),
			"HashDomain with no parts must equal Hash(domain)")
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
		testkit.NoError(t, err, "HashReader")
		testkit.True(t, got.Equal(want),
			"HashReader must equal Hash over the same bytes")
	})

	t.Run("empty reader produces hash of empty input", func(t *testing.T) {
		t.Parallel()
		got, err := crypto.HashReader(h, strings.NewReader(""))
		testkit.NoError(t, err, "HashReader")
		testkit.True(t, got.Equal(h.Hash(nil)),
			"HashReader on empty must equal Hash(nil)")
	})

	t.Run("reader error is wrapped with package context", func(t *testing.T) {
		t.Parallel()
		sentinel := testkit.TestError("network read failed")
		_, err := crypto.HashReader(h, &errReader{err: sentinel})
		testkit.ErrorIs(t, err, sentinel,
			"HashReader must wrap the source error preserving the cause via errors.Is")
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
		testkit.NoError(t, err, "HashReader")
		testkit.True(t, got.Equal(want),
			"HashReader must equal Hash over the same bytes for any input")
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

		testkit.True(t, crypto.HashDomain(h, domain, part).Equal(want),
			"HashDomain must equal manual Stream(domain, part).Sum")
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

		testkit.True(t, first.Equal(second),
			"Stream.Reset must restore initial state — same bytes must produce equal digests")
	})
}

// TestHashDomainZeroAlloc locks in the warm-path zero-allocation
// contract documented on [HashDomain]: the Stream is borrowed
// from the [Hasher]'s pool and returned via [Stream.Close]
// before the function returns.
//
// testing.AllocsPerRun uses a process-global malloc counter, so
// this test does not call t.Parallel.
//
//nolint:paralleltest // see comment above
func TestHashDomainZeroAlloc(t *testing.T) {
	h := sha256.New()
	domain := []byte("thesmos.audit.v1")
	parts := [][]byte{[]byte("entry"), []byte("payload")}

	// Warm the pool so the first measured iteration doesn't
	// count the stream-construction alloc.
	_ = crypto.HashDomain(h, domain, parts...)

	testkit.Equal(t, testing.AllocsPerRun(100, func() {
		_ = crypto.HashDomain(h, domain, parts...)
	}), float64(0), "HashDomain warm path must be zero-alloc")
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
			r := bytes.NewReader(data)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				r.Reset(data)
				_, _ = crypto.HashReader(h, r)
			}
		})
	}
}
