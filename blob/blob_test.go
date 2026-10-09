// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/version"
)

// Character classes of the elements of the generated keys. An element
// starts and ends with a character of keyEdge, so it is never "." or "..".
const (
	// keyEdge contains ASCII and non-ASCII letters, digits, underscores and
	// hyphens.
	keyEdge = "[a-zA-Z0-9äéïñöü" + "пример" + "_-]"

	// keyInner adds spaces and dots to keyEdge.
	keyInner = "[a-zA-Z0-9äéïñöü" + "пример" + " _.-]"
)

// origin is the time at which the fake clock of every memory store of the
// tests starts.
var origin = time.Unix(0, 0).UTC()

// Generators of the properties over keys and content types.
var (
	// keys generates keys that every backend stores: one to five elements
	// of ASCII and non-ASCII letters, digits, spaces, dots, underscores and
	// hyphens, none of them "." or "..".
	keys = prop.StringMatching(keyInner + `{0,20}` + keyEdge + `(/` + keyEdge + keyInner + `{0,20}){0,4}`)

	// printable generates content types of printable ASCII characters
	// within MaxContentTypeLen bytes.
	printable = prop.String(prop.Alphabet(printableASCII()), prop.MaxSize(blob.MaxContentTypeLen))
)

// decorated wraps a store and returns it from Unwrap, as a tracing
// decorator does.
type decorated struct{ blob.Store }

// Unwrap returns the wrapped store.
func (d decorated) Unwrap() blob.Store { return d.Store }

// storeOnly embeds a store as the Store interface and has no Unwrap, so
// [blob.AsRangeReader] cannot find a RangeReader behind it.
type storeOnly struct{ blob.Store }

// selfRanging is a decorator that implements [blob.RangeReader] itself,
// by delegating to the RangeReader it wraps.
type selfRanging struct {
	decorated

	inner blob.RangeReader
}

// ReadRange delegates to the wrapped RangeReader.
func (s selfRanging) ReadRange(
	ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version,
) (int, blob.Info, error) {
	return s.inner.ReadRange(ctx, key, off, dst, ifMatch)
}

func TestAsRangeReader(t *testing.T) {
	t.Parallel()

	t.Run("returns the store itself when it has ranged reads", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		rr, ok := blob.AsRangeReader(s)
		assert.True(t, ok, "a store with ranged reads must be found")
		assert.Equal(t, rr, blob.RangeReader(s), "the store itself must be returned", assert.ByIdentity())
	})

	t.Run("returns a RangeReader behind two decorators", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		rr, ok := blob.AsRangeReader(decorated{decorated{s}})
		assert.True(t, ok, "a RangeReader behind decorators must be found")
		assert.Equal(t, rr, blob.RangeReader(s), "the wrapped store must be returned", assert.ByIdentity())
	})

	t.Run("returns a decorator that has ranged reads before the store it wraps", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(origin))
		outer := selfRanging{decorated: decorated{s}, inner: s}
		rr, ok := blob.AsRangeReader(outer)
		assert.True(t, ok, "the decorator must be found")
		assert.Equal(t, rr, blob.RangeReader(outer), "the outermost RangeReader must be returned", assert.ByIdentity())
	})

	tests := []struct {
		name string
		give blob.Store
	}{
		{name: "reports false for a decorator without Unwrap", give: storeOnly{memory.New(fake.New(origin))}},
		{
			name: "reports false for a chain that ends without a RangeReader",
			give: decorated{storeOnly{memory.New(fake.New(origin))}},
		},
		{name: "reports false for a nil store", give: nil},
		{name: "reports false for a decorator of nil", give: decorated{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rr, ok := blob.AsRangeReader(tt.give)
			assert.False(t, ok, "a chain without a RangeReader must not report one")
			assert.Nil(t, rr, "no RangeReader may be returned")
		})
	}
}

func TestValidKey(t *testing.T) {
	t.Parallel()

	t.Run("reports true for a key that every backend stores", func(t *testing.T) {
		t.Parallel()
		prop.True(t, blob.ValidKey, "every key of slash-separated elements within the bounds must be valid",
			prop.Using(keys), prop.Example("k"), prop.Example("a/b/c"), prop.Example("photos/2026/holiday.jpg"),
			prop.Example("a name with spaces"), prop.Example("ünïcode/пример"))
	})

	t.Run("reports true for a key of MaxKeyLen bytes", func(t *testing.T) {
		t.Parallel()
		assert.True(t, blob.ValidKey(maxLengthKey()), "a key at the bound must be valid")
	})

	t.Run("reports true for an element of MaxKeyElemLen bytes", func(t *testing.T) {
		t.Parallel()
		assert.True(t, blob.ValidKey(strings.Repeat("a", blob.MaxKeyElemLen)), "an element at the bound must be valid")
	})

	t.Run("reports false for a key that io/fs.ValidPath refuses", func(t *testing.T) {
		t.Parallel()
		// Each of these means something different on a filesystem than in
		// an object store, which is why the rule is the standard library's
		// rather than each backend's.
		prop.False(t, blob.ValidKey, "a key outside io/fs.ValidPath must be invalid",
			prop.Using(prop.SampledFrom("", ".", "/k", "k/", "//k", "a//b", "a/./b", "a/../b", "../k", "..")))
	})

	t.Run("reports false for a key over MaxKeyLen bytes", func(t *testing.T) {
		t.Parallel()
		prop.False(t, blob.ValidKey, "a key over MaxKeyLen bytes must be invalid",
			prop.Using(prop.StringMatching(`([a-z]{200}/){5}[a-z]{20,100}`)))
		assert.False(t, blob.ValidKey(maxLengthKey()+"a"), "a key one byte over the bound must be invalid")
	})

	t.Run("reports false for an element over MaxKeyElemLen bytes", func(t *testing.T) {
		t.Parallel()
		prop.False(t, blob.ValidKey, "an element over MaxKeyElemLen bytes must make the key invalid",
			prop.Using(prop.StringMatching(`([a-z]{1,10}/){0,3}[a-z]{256,300}(/[a-z]{1,10}){0,3}`)),
			prop.Example(strings.Repeat("a", blob.MaxKeyElemLen+1)+"/b"),
			prop.Example("a/"+strings.Repeat("b", blob.MaxKeyElemLen+1)))
	})

	t.Run("uses the S3 key length and the filesystem component length as bounds", func(t *testing.T) {
		t.Parallel()
		expect.Equal(t, blob.MaxKeyLen, 1024, "the key bound is the S3-family key length")
		expect.Equal(t, blob.MaxKeyElemLen, 255, "the element bound is the common filesystem component length")
	})
}

func TestValidContentType(t *testing.T) {
	t.Parallel()

	t.Run("reports true for printable ASCII within MaxContentTypeLen bytes", func(t *testing.T) {
		t.Parallel()
		prop.True(t, blob.ValidContentType, "printable ASCII within the bound must be a valid content type",
			prop.Using(printable), prop.Example(""), prop.Example("text/plain; charset=utf-8"), prop.Example(" ~"),
			prop.Example(strings.Repeat("a", blob.MaxContentTypeLen)))
	})

	t.Run("reports false for a content type over MaxContentTypeLen bytes", func(t *testing.T) {
		t.Parallel()
		prop.False(t, blob.ValidContentType, "a content type over the bound must be invalid",
			prop.Using(prop.String(prop.Alphabet(printableASCII()), prop.MinSize(blob.MaxContentTypeLen+1),
				prop.MaxSize(2*blob.MaxContentTypeLen))),
			prop.Example(strings.Repeat("a", blob.MaxContentTypeLen+1)))
	})

	t.Run("reports false for a byte outside printable ASCII", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "a byte below a space or above a tilde must make the content type invalid",
			func(c *prop.Case) {
				head := c.Draw(prop.String(prop.Alphabet(printableASCII()), prop.MaxSize(20)), "head")
				bad := c.Draw(prop.OneOf(prop.Integer[byte](0, ' '-1), prop.Integer[byte]('~'+1, 255)), "byte")
				tail := c.Draw(prop.String(prop.Alphabet(printableASCII()), prop.MaxSize(20)), "tail")
				assert.False(c, blob.ValidContentType(head+string([]byte{bad})+tail),
					"a byte outside printable ASCII must be refused")
			})
	})

	t.Run("uses 255 bytes as the bound", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, blob.MaxContentTypeLen, 255, "the bound fits every type and subtype pair of RFC 6838")
	})
}

// TestBlobAllocs checks the allocation contracts of ValidKey,
// ValidContentType and AsRangeReader. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
func TestBlobAllocs(t *testing.T) {
	t.Run("ValidKey", func(t *testing.T) {
		key := maxLengthKey()

		var got bool
		expect.MaxAllocs(t, func() { got = blob.ValidKey(key) }, 0, "ValidKey must not allocate")
		assert.True(t, got, "the test must measure a valid key")
	})

	t.Run("ValidContentType", func(t *testing.T) {
		contentType := strings.Repeat("a", blob.MaxContentTypeLen)

		var got bool
		expect.MaxAllocs(t, func() { got = blob.ValidContentType(contentType) }, 0,
			"ValidContentType must not allocate")
		assert.True(t, got, "the test must measure a valid content type")
	})

	t.Run("AsRangeReader", func(t *testing.T) {
		var s blob.Store = decorated{decorated{memory.New(fake.New(origin))}}

		var got bool
		expect.MaxAllocs(t, func() { _, got = blob.AsRangeReader(s) }, 0, "AsRangeReader must not allocate")
		assert.True(t, got, "the test must measure a RangeReader behind decorators")
	})
}

// BenchmarkBlob reports the cost of ValidKey, ValidContentType and
// AsRangeReader, and fails when one of them allocates.
func BenchmarkBlob(b *testing.B) {
	b.Run("ValidKey", func(b *testing.B) {
		key := maxLengthKey()

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = blob.ValidKey(key)
		}

		assert.True(b, got, "the benchmark must measure a valid key")
	})

	b.Run("ValidContentType", func(b *testing.B) {
		contentType := strings.Repeat("a", blob.MaxContentTypeLen)

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = blob.ValidContentType(contentType)
		}

		assert.True(b, got, "the benchmark must measure a valid content type")
	})

	b.Run("AsRangeReader", func(b *testing.B) {
		var s blob.Store = decorated{decorated{memory.New(fake.New(origin))}}

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, got = blob.AsRangeReader(s)
		}

		assert.True(b, got, "the benchmark must measure a RangeReader behind decorators")
	})
}

// maxLengthKey builds a key of exactly [blob.MaxKeyLen] bytes whose every
// element is within [blob.MaxKeyElemLen], so the two bounds can be probed
// one at a time.
func maxLengthKey() string {
	const elems = 5

	elem := strings.Repeat("a", (blob.MaxKeyLen-(elems-1))/elems)
	key := strings.Join([]string{elem, elem, elem, elem, elem}, "/")

	return key + strings.Repeat("a", blob.MaxKeyLen-len(key))
}

// printableASCII returns every character from a space to a tilde, the
// characters of a valid content type.
func printableASCII() string {
	var b strings.Builder
	for c := byte(' '); c <= '~'; c++ {
		b.WriteByte(c)
	}

	return b.String()
}
