// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"go.thesmos.sh/core/rand/crypto"
)

// failingReader returns a fixed error on every Read. Used to
// exercise the failure paths in [crypto.Rand] via the public
// [crypto.NewWithReader] constructor.
type failingReader struct{ err error }

func (f failingReader) Read(_ []byte) (int, error) { return 0, f.err }

// failingReader implements io.Reader; the compile-time check
// keeps the test fixture in sync if the interface changes.
var _ io.Reader = failingReader{}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("default source fills the entire slice", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 64)
		n, err := crypto.New().Read(buf)
		if err != nil {
			t.Fatalf("Read: unexpected error %v", err)
		}
		if n != 64 {
			t.Fatalf("Read: filled %d, want 64", n)
		}
	})

	t.Run("default source produces non-trivial output", func(t *testing.T) {
		t.Parallel()
		// On a healthy /dev/urandom the probability of 32
		// consecutive zero bytes is 2^-256 — effectively zero.
		buf := make([]byte, 32)
		_, _ = crypto.New().Read(buf)
		zero := make([]byte, 32)
		if bytes.Equal(buf, zero) {
			t.Fatal("Read returned all zeros — entropy source broken")
		}
	})

	t.Run("two consecutive reads diverge", func(t *testing.T) {
		t.Parallel()
		r := crypto.New()
		a, b := make([]byte, 32), make([]byte, 32)
		_, _ = r.Read(a)
		_, _ = r.Read(b)
		if bytes.Equal(a, b) {
			t.Fatal("two consecutive Reads produced identical output")
		}
	})

	t.Run("source error is wrapped with package context", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("entropy depleted")
		r := crypto.NewWithReader(failingReader{err: sentinel})
		_, err := r.Read(make([]byte, 8))
		if err == nil {
			t.Fatal("expected error from failing reader, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected wrapped sentinel, got %v", err)
		}
	})
}

func TestUint64(t *testing.T) {
	t.Parallel()

	t.Run("default source produces varied output", func(t *testing.T) {
		t.Parallel()
		// Five draws from a healthy CSPRNG must yield at least
		// two distinct values; matching all five is unobservable.
		r := crypto.New()
		seen := make(map[uint64]struct{})
		for range 5 {
			seen[r.Uint64()] = struct{}{}
		}
		if len(seen) < 2 {
			t.Fatal("Uint64 returned identical values 5 times running")
		}
	})

	t.Run("panics with wrapped error on source failure", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("entropy depleted")
		r := crypto.NewWithReader(failingReader{err: sentinel})

		defer func() {
			rec := recover()
			if rec == nil {
				t.Fatal("expected panic, got nil")
			}
			err, ok := rec.(error)
			if !ok {
				t.Fatalf("expected panic with error, got %T(%v)", rec, rec)
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("panic error did not wrap sentinel: %v", err)
			}
		}()
		_ = r.Uint64()
	})
}

func TestZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	// Documented contract: zero-value Rand uses crypto/rand.Reader.
	var r crypto.Rand
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("zero-value Read: unexpected error %v", err)
	}
	if n != 8 {
		t.Fatalf("zero-value Read: filled %d, want 8", n)
	}
}

func TestNewWithReaderUsesProvidedSource(t *testing.T) {
	t.Parallel()

	// A non-failing custom reader must produce its bytes via Read.
	const want = "deterministic-bytes-for-test"
	r := crypto.NewWithReader(bytes.NewReader([]byte(want)))
	got := make([]byte, len(want))
	n, err := r.Read(got)
	if err != nil {
		t.Fatalf("Read: unexpected error %v", err)
	}
	if n != len(want) {
		t.Fatalf("Read: filled %d, want %d", n, len(want))
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestZeroAlloc enforces the documented "Zero alloc" contract on
// crypto.Rand.Read. Uint64 is intentionally excluded — supporting
// NewWithReader forces an 8-byte heap allocation per Uint64 call
// (see the package's "Allocation contract" docstring).
// testing.AllocsPerRun uses a process-global malloc counter, so
// this test does not call t.Parallel.
func TestZeroAlloc(t *testing.T) {
	r := crypto.New()
	buf := make([]byte, 64)

	if got := testing.AllocsPerRun(100, func() { _, _ = r.Read(buf) }); got != 0 {
		t.Fatalf("Read: %v allocs/op, want 0", got)
	}
}
