// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

var framerDomain = crypto.Domain{Name: "entry", Version: 1}

func TestFramerDistinctSequencesDiffer(t *testing.T) {
	t.Parallel()

	t.Run("a shifted boundary between variable fields changes the bytes", func(t *testing.T) {
		t.Parallel()
		// The defect this type exists to prevent: without a length
		// prefix, "ab" || "c" and "a" || "bc" are the same bytes.
		a := crypto.NewFramer(nil, framerDomain)
		a.Bytes([]byte("ab"))
		a.Bytes([]byte("c"))

		b := crypto.NewFramer(nil, framerDomain)
		b.Bytes([]byte("a"))
		b.Bytes([]byte("bc"))

		testkit.NotEqual(t, a.Frame(), b.Frame(),
			"a shifted field boundary must change the framed bytes")
	})

	t.Run("an empty field is distinguishable from an absent one", func(t *testing.T) {
		t.Parallel()
		withEmpty := crypto.NewFramer(nil, framerDomain)
		withEmpty.Bytes(nil)

		without := crypto.NewFramer(nil, framerDomain)

		testkit.NotEqual(t, withEmpty.Frame(), without.Frame(),
			"an empty length-prefixed field must not vanish")
	})

	t.Run("a different domain name changes the bytes", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewFramer(nil, crypto.Domain{Name: "entry", Version: 1})
		b := crypto.NewFramer(nil, crypto.Domain{Name: "receipt", Version: 1})
		testkit.NotEqual(t, a.Frame(), b.Frame(), "a different domain must not collide")
	})

	t.Run("a longer name sharing a prefix does not collide", func(t *testing.T) {
		t.Parallel()
		// A fixed-width, truncating tag would map these together.
		a := crypto.NewFramer(nil, crypto.Domain{Name: "audit-entry", Version: 1})
		b := crypto.NewFramer(nil, crypto.Domain{Name: "audit-entry-v2-extended", Version: 1})
		testkit.NotEqual(t, a.Frame(), b.Frame(),
			"names sharing a prefix must not collide")
	})

	t.Run("a different version changes the bytes", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewFramer(nil, crypto.Domain{Name: "entry", Version: 1})
		b := crypto.NewFramer(nil, crypto.Domain{Name: "entry", Version: 2})
		testkit.NotEqual(t, a.Frame(), b.Frame(), "a version bump must change the bytes")
	})
}

func TestFramerLayout(t *testing.T) {
	t.Parallel()

	t.Run("the domain tag is length-prefixed name then version", func(t *testing.T) {
		t.Parallel()
		f := crypto.NewFramer(nil, crypto.Domain{Name: "ab", Version: 0x0102})
		testkit.Equal(t, f.Frame(), []byte{
			0, 0, 0, 0, 0, 0, 0, 2, // uint64 len("ab")
			'a', 'b',
			0x01, 0x02, // uint16 version
		}, "domain tag must match the documented layout")
	})

	t.Run("Bytes writes a uint64 length prefix", func(t *testing.T) {
		t.Parallel()
		f := crypto.NewFramer(nil, crypto.Domain{})
		before := len(f.Frame())
		f.Bytes([]byte("xyz"))
		testkit.Equal(t, f.Frame()[before:], []byte{
			0, 0, 0, 0, 0, 0, 0, 3,
			'x', 'y', 'z',
		}, "Bytes must length-prefix its input")
	})

	t.Run("String matches Bytes for the same content", func(t *testing.T) {
		t.Parallel()
		a := crypto.NewFramer(nil, framerDomain)
		a.String("hello")
		b := crypto.NewFramer(nil, framerDomain)
		b.Bytes([]byte("hello"))
		testkit.Equal(t, a.Frame(), b.Frame(), "String and Bytes must agree")
	})

	t.Run("Fixed writes no length prefix", func(t *testing.T) {
		t.Parallel()
		f := crypto.NewFramer(nil, crypto.Domain{})
		before := len(f.Frame())
		f.Fixed([]byte("xyz"))
		testkit.Equal(t, f.Frame()[before:], []byte("xyz"), "Fixed must append verbatim")
	})

	t.Run("Uint64 and Uint32 write fixed-width big-endian", func(t *testing.T) {
		t.Parallel()
		f := crypto.NewFramer(nil, crypto.Domain{})
		before := len(f.Frame())
		f.Uint64(1)
		f.Uint32(2)
		testkit.Equal(t, f.Frame()[before:], []byte{
			0, 0, 0, 0, 0, 0, 0, 1,
			0, 0, 0, 2,
		}, "fixed-width fields must carry no length prefix")
	})

	t.Run("appends to a non-empty destination", func(t *testing.T) {
		t.Parallel()
		f := crypto.NewFramer([]byte{0xAA}, crypto.Domain{})
		testkit.Equal(t, f.Frame()[0], byte(0xAA), "NewFramer must append to dst, not overwrite it")
	})
}

func BenchmarkFramerReusedBuffer(b *testing.B) {
	buf := make([]byte, 0, 256)
	payload := []byte("some variable width payload")
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		f := crypto.NewFramer(buf[:0], framerDomain)
		f.Uint64(42)
		f.Bytes(payload)
		sink = f.Frame()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkFramerSizedAtConstruction(b *testing.B) {
	payload := []byte("some variable width payload")
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		f := crypto.NewFramer(make([]byte, 0, 128), framerDomain)
		f.Uint64(42)
		f.Bytes(payload)
		sink = f.Frame()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkFramerFreshBuffer(b *testing.B) {
	payload := []byte("some variable width payload")
	b.ReportAllocs()
	var sink []byte
	for b.Loop() {
		f := crypto.NewFramer(nil, framerDomain)
		f.Uint64(42)
		f.Bytes(payload)
		sink = f.Frame()
	}
	runtime.KeepAlive(sink)
}
