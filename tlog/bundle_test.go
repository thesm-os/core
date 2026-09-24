// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/tlog"
)

func TestAppendBundleEntry(t *testing.T) {
	t.Parallel()

	t.Run("prefixes the entry with its big-endian length", func(t *testing.T) {
		t.Parallel()

		b, err := tlog.AppendBundleEntry([]byte{0xAA}, []byte("abc"))
		testkit.NoError(t, err, "AppendBundleEntry must succeed")
		testkit.Equal(t, b, []byte{0xAA, 0x00, 0x03, 'a', 'b', 'c'}, "the entry must follow its length")
	})

	t.Run("accepts an entry of 65,535 bytes and refuses one more", func(t *testing.T) {
		t.Parallel()

		_, err := tlog.AppendBundleEntry(nil, make([]byte, 65535))
		testkit.NoError(t, err, "the largest entry must fit")

		b, err := tlog.AppendBundleEntry([]byte{0xAA}, make([]byte, 65536))
		testkit.ErrorIs(t, err, tlog.ErrEntrySize, "a longer entry must be ErrEntrySize")
		testkit.Equal(t, b, []byte{0xAA}, "dst must be returned unchanged")
	})
}

func TestBundleEntries(t *testing.T) {
	t.Parallel()

	var bundle []byte
	for _, e := range [][]byte{[]byte("one"), {}, bytes.Repeat([]byte{7}, 300)} {
		var err error
		bundle, err = tlog.AppendBundleEntry(bundle, e)
		testkit.NoError(t, err, "AppendBundleEntry must succeed")
	}

	t.Run("yields every entry in order, without spare capacity", func(t *testing.T) {
		t.Parallel()

		var got [][]byte
		for e, err := range tlog.BundleEntries(bundle) {
			testkit.NoError(t, err, "a well-formed bundle must iterate")
			testkit.Equal(t, cap(e), len(e), "an entry must not expose the next one")
			got = append(got, e)
		}
		testkit.Equal(t, got, [][]byte{[]byte("one"), {}, bytes.Repeat([]byte{7}, 300)}, "the entries must round-trip")
	})

	t.Run("yields ErrBundle for a length that runs past the data", func(t *testing.T) {
		t.Parallel()

		for name, data := range map[string][]byte{
			"a cut length": bundle[:len(bundle)-301],
			"a cut entry":  bundle[:len(bundle)-1],
		} {
			var last error
			for _, err := range tlog.BundleEntries(data) {
				last = err
			}
			testkit.ErrorIs(t, last, tlog.ErrBundle, name+" must end with ErrBundle")
		}
	})

	t.Run("stops when the caller stops", func(t *testing.T) {
		t.Parallel()

		n := 0
		for range tlog.BundleEntries(bundle) {
			n++
			break
		}
		testkit.Equal(t, n, 1, "the iteration must end at the caller's break")
	})
}

func TestBundlePath(t *testing.T) {
	t.Parallel()

	t.Run("writes the C2SP path of a full and a partial bundle", func(t *testing.T) {
		t.Parallel()

		testkit.Equal(t, tlog.BundlePath(1234067, 256), "tile/entries/x001/x234/067", "the path of a full bundle")
		testkit.Equal(t, tlog.BundlePath(0, 5), "tile/entries/000.p/5", "the path of a partial bundle")
	})
}
