// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"fmt"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
)

func TestValidKey(t *testing.T) {
	t.Parallel()

	t.Run("accepts the keys a caller can write anywhere", func(t *testing.T) {
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

	t.Run("rejects what no backend agrees on", func(t *testing.T) {
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

	t.Run("the bounds are the ones backends impose", func(t *testing.T) {
		t.Parallel()

		testkit.Equal(t, blob.MaxKeyLen, 1024,
			"the key bound is the S3-family key length")
		testkit.Equal(t, blob.MaxKeyElemLen, 255,
			"the element bound is the common filesystem component length")
	})
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
