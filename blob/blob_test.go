// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/version"
)

// decorated wraps a store and returns it from Unwrap, as a tracing
// decorator does.
type decorated struct{ blob.Store }

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

//nolint:wrapcheck // the test double passes the error through
func (s selfRanging) ReadRange(
	ctx context.Context, key string, off int64, dst []byte, ifMatch version.Version,
) (int, blob.Info, error) {
	return s.inner.ReadRange(ctx, key, off, dst, ifMatch)
}

// newMemory returns an empty memory store over a fake clock.
func newMemory() *memory.Store {
	return memory.New(fake.New(time.Unix(0, 0).UTC()))
}

func TestAsRangeReader(t *testing.T) {
	t.Parallel()

	t.Run("finds a RangeReader on the store itself", func(t *testing.T) {
		t.Parallel()
		s := newMemory()
		rr, ok := blob.AsRangeReader(s)
		testkit.True(t, ok, "a store with ranged reads must be found")
		testkit.True(t, rr == blob.RangeReader(s), "the store itself must be returned")
	})

	t.Run("finds a RangeReader through two decorators", func(t *testing.T) {
		t.Parallel()
		s := newMemory()
		rr, ok := blob.AsRangeReader(decorated{decorated{s}})
		testkit.True(t, ok, "a RangeReader behind decorators must be found")
		testkit.True(t, rr == blob.RangeReader(s), "the wrapped store must be returned")
	})

	t.Run("finds a decorator that implements RangeReader before the store it wraps", func(t *testing.T) {
		t.Parallel()
		s := newMemory()
		outer := selfRanging{decorated: decorated{s}, inner: s}
		rr, ok := blob.AsRangeReader(outer)
		testkit.True(t, ok, "the decorator must be found")
		testkit.True(t, rr == blob.RangeReader(outer), "the outermost RangeReader must be returned")
	})

	t.Run("reports false for a decorator without Unwrap", func(t *testing.T) {
		t.Parallel()
		_, ok := blob.AsRangeReader(storeOnly{newMemory()})
		testkit.False(t, ok, "a decorator without Unwrap must hide the capability")
	})

	t.Run("reports false when the chain ends without a RangeReader", func(t *testing.T) {
		t.Parallel()
		_, ok := blob.AsRangeReader(decorated{storeOnly{newMemory()}})
		testkit.False(t, ok, "a chain without a RangeReader must not report one")
	})

	t.Run("reports false for a nil store and a decorator of nil", func(t *testing.T) {
		t.Parallel()
		_, ok := blob.AsRangeReader(nil)
		testkit.False(t, ok, "a nil store must not report a RangeReader")

		_, ok = blob.AsRangeReader(decorated{})
		testkit.False(t, ok, "a decorator that wraps nothing must not report a RangeReader")
	})
}

// BenchmarkAsRangeReader reports the cost and the allocations of
// AsRangeReader through two decorators.
func BenchmarkAsRangeReader(b *testing.B) {
	var s blob.Store = decorated{decorated{newMemory()}}
	b.ReportAllocs()

	for b.Loop() {
		_, _ = blob.AsRangeReader(s)
	}
}

func TestValidKey(t *testing.T) {
	t.Parallel()

	t.Run("reports true for a key that every backend stores", func(t *testing.T) {
		t.Parallel()

		for _, key := range []string{
			"k",
			"a/b",
			"a/b/c",
			"photos/2026/holiday.jpg",
			"a name with spaces",
			"ünïcode/пример",
			strings.Repeat("a", blob.MaxKeyElemLen),
			maxLengthKey(),
		} {
			testkit.True(t, blob.ValidKey(key),
				fmt.Sprintf("%q must be a valid key", key))
		}
	})

	t.Run("reports false for a key outside io/fs.ValidPath or the bounds", func(t *testing.T) {
		t.Parallel()

		// Each of these means something different on a filesystem
		// than it does in an object store, which is why the rule is
		// the standard library's rather than each backend's.
		cases := map[string]string{
			"empty":                        "",
			"the root":                     ".",
			"rooted":                       "/k",
			"trailing slash":               "k/",
			"leading slash element":        "//k",
			"empty element":                "a//b",
			"dot element":                  "a/./b",
			"dot-dot element":              "a/../b",
			"escaping the root":            "../k",
			"a bare dot-dot":               "..",
			"one byte over the key length": maxLengthKey() + "a",
			"one byte over the element length": strings.Repeat("a", blob.MaxKeyElemLen+1) +
				"/b",
			"a long element late in the key": "a/" +
				strings.Repeat("b", blob.MaxKeyElemLen+1),
		}
		for name, key := range cases {
			testkit.False(t, blob.ValidKey(key), name+" must not be a valid key")
		}
	})

	t.Run("uses the S3 key length and the filesystem component length as bounds", func(t *testing.T) {
		t.Parallel()

		testkit.Equal(t, blob.MaxKeyLen, 1024,
			"the key bound is the S3-family key length")
		testkit.Equal(t, blob.MaxKeyElemLen, 255,
			"the element bound is the common filesystem component length")
	})
}

func TestValidContentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give string
		want bool
	}{
		{name: "reports true for the empty content type", give: "", want: true},
		{name: "reports true for a media type with a parameter", give: "text/plain; charset=utf-8", want: true},
		{name: "reports true for a space and a tilde", give: " ~", want: true},
		{
			name: "reports true for MaxContentTypeLen bytes",
			give: strings.Repeat("a", blob.MaxContentTypeLen),
			want: true,
		},
		{
			name: "reports false for one byte over MaxContentTypeLen",
			give: strings.Repeat("a", blob.MaxContentTypeLen+1),
			want: false,
		},
		{name: "reports false for a byte below a space", give: "text/plain\x1f", want: false},
		{name: "reports false for a horizontal tab", give: "text/plain;\tcharset=utf-8", want: false},
		{name: "reports false for a line break", give: "text/plain\r\n", want: false},
		{name: "reports false for the delete byte", give: "text/plain\x7f", want: false},
		{name: "reports false for a byte outside ASCII", give: "text/pl\xc3\xa4in", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, blob.ValidContentType(tt.give), tt.want,
				fmt.Sprintf("ValidContentType(%q) must report %v", tt.give, tt.want))
		})
	}

	t.Run("uses 255 bytes as the bound", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, blob.MaxContentTypeLen, 255,
			"the bound fits every type and subtype pair of RFC 6838")
	})
}

// BenchmarkValidContentType reports the cost and the allocations of
// ValidContentType for a content type of MaxContentTypeLen bytes.
func BenchmarkValidContentType(b *testing.B) {
	contentType := strings.Repeat("a", blob.MaxContentTypeLen)
	b.ReportAllocs()

	for b.Loop() {
		_ = blob.ValidContentType(contentType)
	}
}

// maxLengthKey builds a key of exactly [blob.MaxKeyLen] bytes whose
// every element is within [blob.MaxKeyElemLen], so the two bounds
// can be probed one at a time.
func maxLengthKey() string {
	const elems = 5

	elem := strings.Repeat("a", (blob.MaxKeyLen-(elems-1))/elems)
	key := strings.Join([]string{elem, elem, elem, elem, elem}, "/")

	return key + strings.Repeat("a", blob.MaxKeyLen-len(key))
}
