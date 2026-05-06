// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/core/id"
)

func TestSizeConstants(t *testing.T) {
	t.Parallel()

	if id.Size128 != 16 {
		t.Fatalf("Size128: got %d, want 16", id.Size128)
	}
	if id.Size160 != 20 {
		t.Fatalf("Size160: got %d, want 20", id.Size160)
	}
	if id.Size256 != 32 {
		t.Fatalf("Size256: got %d, want 32", id.Size256)
	}
	if id.MaxSize != id.Size256 {
		t.Fatalf("MaxSize: got %d, want %d", id.MaxSize, id.Size256)
	}
}

func TestNewConstructors(t *testing.T) {
	t.Parallel()

	t.Run("New128 produces a 16-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size128]byte
		for i := range b {
			b[i] = byte(i)
		}
		got := id.New128(b)
		if got.Size() != id.Size128 {
			t.Fatalf("Size: got %d, want %d", got.Size(), id.Size128)
		}
		if !bytes.Equal(got.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", got.Bytes(), b[:])
		}
	})

	t.Run("New160 produces a 20-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size160]byte
		for i := range b {
			b[i] = byte(i + 1)
		}
		got := id.New160(b)
		if got.Size() != id.Size160 {
			t.Fatalf("Size: got %d, want %d", got.Size(), id.Size160)
		}
		if !bytes.Equal(got.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", got.Bytes(), b[:])
		}
	})

	t.Run("New256 produces a 32-byte ID", func(t *testing.T) {
		t.Parallel()
		var b [id.Size256]byte
		for i := range b {
			b[i] = byte(i + 2)
		}
		got := id.New256(b)
		if got.Size() != id.Size256 {
			t.Fatalf("Size: got %d, want %d", got.Size(), id.Size256)
		}
		if !bytes.Equal(got.Bytes(), b[:]) {
			t.Fatalf("Bytes: got %x, want %x", got.Bytes(), b[:])
		}
	})
}

func TestIDIsZero(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   id.ID
		want bool
	}{
		"zero value":          {id.ID{}, true},
		"Zero sentinel":       {id.Zero, true},
		"size-128 with bytes": {id.New128([id.Size128]byte{1}), false},
		"size-160 with bytes": {id.New160([id.Size160]byte{2}), false},
		"size-256 with bytes": {id.New256([id.Size256]byte{3}), false},
		"all-zero size-128":   {id.New128([id.Size128]byte{}), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsZero(); got != tc.want {
				t.Fatalf("IsZero: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIDEqual(t *testing.T) {
	t.Parallel()

	a := id.New128(fill128(0x42))
	b := id.New128(fill128(0x42))
	c := id.New128(fill128(0x43))
	d160 := id.New160(fill160(0x42))

	if !a.Equal(b) {
		t.Fatal("identical IDs should be Equal")
	}
	if a.Equal(c) {
		t.Fatal("differing IDs must not be Equal")
	}
	if a.Equal(d160) {
		t.Fatal("IDs of different sizes must not be Equal")
	}
}

func TestIDCompare(t *testing.T) {
	t.Parallel()

	t.Run("identical IDs compare equal", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x42))
		b := id.New128(fill128(0x42))
		if got := a.Compare(b); got != 0 {
			t.Fatalf("Compare on equal: got %d, want 0", got)
		}
	})

	t.Run("smaller bytes compare less", func(t *testing.T) {
		t.Parallel()
		a := id.New128(fill128(0x01))
		b := id.New128(fill128(0x02))
		if got := a.Compare(b); got != -1 {
			t.Fatalf("Compare a<b: got %d, want -1", got)
		}
		if got := b.Compare(a); got != 1 {
			t.Fatalf("Compare b>a: got %d, want 1", got)
		}
	})

	t.Run("smaller size compares less when prefixes match", func(t *testing.T) {
		t.Parallel()
		short := id.New128([id.Size128]byte{})
		long := id.New160([id.Size160]byte{})
		if got := short.Compare(long); got != -1 {
			t.Fatalf("Compare short<long: got %d, want -1", got)
		}
		if got := long.Compare(short); got != 1 {
			t.Fatalf("Compare long>short: got %d, want 1", got)
		}
	})
}

func TestIDString(t *testing.T) {
	t.Parallel()

	t.Run("Zero encodes to bare 'id:' prefix", func(t *testing.T) {
		t.Parallel()
		if got := id.Zero.String(); got != "id:" {
			t.Fatalf("String: got %q, want %q", got, "id:")
		}
	})

	t.Run("128-bit ID encodes as id:<32-hex-chars>", func(t *testing.T) {
		t.Parallel()
		var b [id.Size128]byte
		b[0], b[1], b[2] = 0x01, 0x23, 0x45
		b[13], b[14], b[15] = 0xab, 0xcd, 0xef
		got := id.New128(b).String()
		const middleZeros = "00000000000000000000000000000000"
		want := "id:012345" + middleZeros[:20] + "abcdef"
		if got != want {
			t.Fatalf("String: got %q, want %q", got, want)
		}
	})

	t.Run("prefix is visually distinct from algorithm encodings", func(t *testing.T) {
		t.Parallel()
		// Canonical algorithm encodings start with alphanumeric
		// characters: ULID Crockford base32 ("0..9A-Z"), UUIDv4
		// hyphenated hex, KSUID base62. A diagnostic id.String()
		// starts with "id:", which doesn't match any of those.
		s := id.New128([id.Size128]byte{0x42}).String()
		if len(s) < 3 || s[:3] != "id:" {
			t.Fatalf("String: %q does not start with 'id:'", s)
		}
	})
}

// TestIDZeroAlloc cannot run in parallel — testing.AllocsPerRun
// panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestIDZeroAlloc(t *testing.T) {
	a := id.New128(fill128(0x42))
	other := id.New128(fill128(0x43))
	var raw128 [id.Size128]byte
	var raw160 [id.Size160]byte
	var raw256 [id.Size256]byte

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _ = id.New128(raw128) }, "New128"},
		{func() { _ = id.New160(raw160) }, "New160"},
		{func() { _ = id.New256(raw256) }, "New256"},
		{func() { _ = a.IsZero() }, "IsZero"},
		{func() { _ = a.Equal(other) }, "Equal"},
		{func() { _ = a.Compare(other) }, "Compare"},
		{func() { _ = a.Size() }, "Size"},
		{func() { _ = a.Bytes() }, "Bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkEqual(b *testing.B) {
	a := id.New128(fill128(0x42))
	other := id.New128(fill128(0x43))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Equal(other)
	}
}

func BenchmarkCompare(b *testing.B) {
	a := id.New128(fill128(0x42))
	other := id.New128(fill128(0x43))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Compare(other)
	}
}

func BenchmarkBytes(b *testing.B) {
	a := id.New128(fill128(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Bytes()
	}
}

func BenchmarkString(b *testing.B) {
	a := id.New128(fill128(0x42))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.String()
	}
}

func fill128(b byte) [id.Size128]byte {
	var out [id.Size128]byte
	for i := range out {
		out[i] = b
	}
	return out
}

func fill160(b byte) [id.Size160]byte {
	var out [id.Size160]byte
	for i := range out {
		out[i] = b
	}
	return out
}
