// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package blobtest holds the conformance suite for
// [go.thesmos.sh/core/blob.Store]: atomic visibility, reader
// snapshots, cursor-chain completeness, the conditional-write
// table, and uniform failure classification. Hand-rolled; nothing
// here is generated.
package blobtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// failingReader yields its data and then fails, standing in for an
// upload interrupted mid-stream.
type failingReader struct {
	err  error
	data []byte
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// assertSameObject compares the fields that identify one written
// object.
//
// [blob.Info.ModTime] is excluded on purpose. A backend may not
// report a timestamp on write — the S3 family returns an entity tag
// and no last-modified — so requiring Put's Info to equal Get's in
// full would cost such an adapter a second request per write to
// fill in a field nothing here compares. The seam says Put MAY
// return a zero ModTime and Get and Stat MUST report one, and the
// cases assert exactly that.
func assertSameObject(t *testing.T, got, want blob.Info, who string) {
	t.Helper()

	testkit.Equal(t, got.Key, want.Key, who+" must carry the same key")
	testkit.Equal(t, got.Version, want.Version, who+" must carry the same version")
	testkit.Equal(t, got.Size, want.Size, who+" must report the same size")
	testkit.Equal(t, got.ContentType, want.ContentType,
		who+" must carry the same content type")
}

// AssertStore drives a [blob.Store] implementation through the blob
// laws. newStore must return an empty store reading wall time from
// the given clock; the suite constructs a fresh one per case, so
// cases cannot observe each other's contents.
func AssertStore(t *testing.T, newStore func(c clock.Clock) blob.Store) {
	t.Helper()

	fresh := func() blob.Store {
		return newStore(fake.New(time.Unix(0, 0).UTC()))
	}
	put := func(t *testing.T, s blob.Store, key, body string, opts blob.PutOptions) blob.Info {
		t.Helper()

		info, err := s.Put(t.Context(), key, bytes.NewReader([]byte(body)), opts)
		testkit.NoError(t, err, "Put must succeed")

		return info
	}
	read := func(t *testing.T, s blob.Store, key string) (string, blob.Info) {
		t.Helper()

		rc, info, err := s.Get(t.Context(), key)
		testkit.NoError(t, err, "Get must succeed")

		body, err := io.ReadAll(rc)
		testkit.NoError(t, err, "the body must drain")
		testkit.NoError(t, rc.Close(), "the body must close")

		return string(body), info
	}

	t.Run("round-trips body and metadata", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		info := put(t, s, "k", "hello blob", blob.PutOptions{ContentType: "text/plain"})
		testkit.Equal(t, info.Key, "k", "Info must carry the key")
		testkit.Equal(t, info.Size, int64(10), "Put's Info must report the consumed size")
		testkit.Equal(t, info.ContentType, "text/plain", "ContentType must round-trip")
		testkit.False(t, info.Version.IsZero(), "a written object must carry a version")

		body, got := read(t, s, "k")
		testkit.Equal(t, body, "hello blob", "the body must round-trip")
		assertSameObject(t, got, info, "Get's Info")
		testkit.False(t, got.ModTime.IsZero(), "Get must report a modification time")

		stat, err := s.Stat(t.Context(), "k")
		testkit.NoError(t, err, "Stat must succeed")
		assertSameObject(t, stat, info, "Stat's Info")
		testkit.False(t, stat.ModTime.IsZero(), "Stat must report a modification time")
	})

	t.Run("an overwrite issues a new version", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		first := put(t, s, "k", "one", blob.PutOptions{})
		second := put(t, s, "k", "two", blob.PutOptions{})

		testkit.NotEqual(t, second.Version, first.Version,
			"each write must carry a distinct version")
	})

	t.Run("IfMatch guards the version it names", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		first := put(t, s, "k", "one", blob.PutOptions{})

		_, err := s.Put(t.Context(), "k", bytes.NewReader([]byte("two")),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.NoError(t, err, "IfMatch naming the current version must succeed")

		_, err = s.Put(t.Context(), "k", bytes.NewReader([]byte("three")),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"IfMatch naming a superseded version must fail")

		_, err = s.Put(t.Context(), "absent", bytes.NewReader(nil),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"IfMatch against an absent key must fail — no version is present")
	})

	t.Run("IfNoneMatch wildcard creates exactly once", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		opts := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}

		_, err := s.Put(t.Context(), "k", bytes.NewReader([]byte("one")), opts)
		testkit.NoError(t, err, "create-only must succeed on an absent key")

		_, err = s.Put(t.Context(), "k", bytes.NewReader([]byte("two")), opts)
		testkit.ErrorIs(t, err, version.ErrExists,
			"create-only must fail once the key exists")
	})

	t.Run("IfNoneMatch naming a version guards that version", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		first := put(t, s, "k", "one", blob.PutOptions{})

		_, err := s.Put(t.Context(), "k", bytes.NewReader([]byte("two")),
			blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrExists,
			"IfNoneMatch naming the current version must fail — it is present")

		_, err = s.Put(t.Context(), "k", bytes.NewReader([]byte("two")),
			blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: "unrelated"}})
		testkit.NoError(t, err,
			"IfNoneMatch naming an absent version must pass — nothing matches it")
	})

	t.Run("Delete follows the conditional table", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		testkit.NoError(t, s.Delete(t.Context(), "absent", ""),
			"unconditional delete of an absent key succeeds — the intent holds")

		testkit.ErrorIs(t,
			s.Delete(t.Context(), "absent", "v"),
			version.ErrMismatch,
			"a version named against an absent key is not there")

		info := put(t, s, "k", "body", blob.PutOptions{})

		testkit.ErrorIs(t,
			s.Delete(t.Context(), "k", "stale"),
			version.ErrMismatch,
			"a superseded version must not delete")

		testkit.NoError(t,
			s.Delete(t.Context(), "k", info.Version),
			"the current version must delete")

		_, err := s.Stat(t.Context(), "k")
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"the object must be gone")
	})

	t.Run("visibility is atomic under every failure", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		prior := put(t, s, "k", "committed", blob.PutOptions{})

		assertIntact := func(t *testing.T) {
			t.Helper()

			body, info := read(t, s, "k")
			testkit.Equal(t, body, "committed", "the prior body must be untouched")
			testkit.Equal(t, info.Version, prior.Version, "the prior version must be untouched")
		}

		t.Run("a reader failing mid-stream", func(t *testing.T) {
			boom := errors.New("blobtest: upload interrupted")

			_, err := s.Put(t.Context(), "k",
				&failingReader{data: []byte("partial"), err: boom}, blob.PutOptions{})
			testkit.ErrorIs(t, err, boom, "the reader's own error must surface")
			assertIntact(t)
		})

		t.Run("a failed precondition", func(t *testing.T) {
			_, err := s.Put(t.Context(), "k", bytes.NewReader([]byte("nope")),
				blob.PutOptions{Write: version.WriteOptions{IfMatch: "stale"}})
			testkit.ErrorIs(t, err, version.ErrMismatch, "the precondition must fail")
			assertIntact(t)
		})

		t.Run("a done context", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := s.Put(ctx, "k", bytes.NewReader([]byte("nope")), blob.PutOptions{})
			testkit.ErrorIs(t, err, context.Canceled, "the context must surface")
			assertIntact(t)
		})
	})

	t.Run("an open reader is one consistent object", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		first := put(t, s, "k", "the original body", blob.PutOptions{})

		rc, info, err := s.Get(t.Context(), "k")
		testkit.NoError(t, err, "Get must succeed")
		testkit.Equal(t, info.Version, first.Version, "the open must name the current version")

		put(t, s, "k", "an overwrite while the reader is open", blob.PutOptions{})

		body, err := io.ReadAll(rc)
		testkit.NoError(t, err, "the body must drain after the overwrite")
		testkit.NoError(t, rc.Close(), "the body must close")
		testkit.Equal(t, string(body), "the original body",
			"the reader must serve the version its Info named")
	})

	t.Run("an empty body is a real object", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		info := put(t, s, "k", "", blob.PutOptions{})
		testkit.Equal(t, info.Size, int64(0), "the empty body has size zero")

		body, _ := read(t, s, "k")
		testkit.Len(t, body, 0, "the empty body must round-trip empty")
	})

	t.Run("a cursor chain is complete, exactly once", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		for i := range 5 {
			put(t, s, "a/"+strconv.Itoa(i), "x", blob.PutOptions{})
		}
		put(t, s, "b/0", "x", blob.PutOptions{})
		put(t, s, "b/1", "x", blob.PutOptions{})

		// The walk is bounded: a continuation token that fails to
		// advance would otherwise loop forever, and a suite must
		// convert that defect into a named failure rather than
		// observe it by outliving the deadline.
		const maxPages = 16

		walk := func(t *testing.T, prefix string, limit int) map[string]int {
			t.Helper()

			seen := map[string]int{}
			p := page.Page{Limit: limit}
			for range maxPages {
				cur, err := s.List(t.Context(), prefix, p)
				testkit.NoError(t, err, "List must succeed")
				for info, ierr := range cur.Seq(t.Context()) {
					testkit.NoError(t, ierr, "iteration must not fail")
					seen[info.Key]++
				}
				tok := cur.NextPage()
				testkit.NoError(t, cur.Close(), "the cursor must close")
				if tok == "" {
					return seen
				}
				p.Token = tok
			}

			t.Fatalf("cursor chain exceeded %d pages — the continuation token failed to advance", maxPages)

			return nil
		}

		for _, limit := range []int{1, 2, 10} {
			t.Run("page size "+strconv.Itoa(limit), func(t *testing.T) {
				all := walk(t, "", limit)
				testkit.Len(t, all, 7, "the empty prefix must enumerate everything")
				for key, n := range all {
					testkit.Equal(t, n, 1, "key "+key+" must appear exactly once")
				}

				scoped := walk(t, "a/", limit)
				testkit.Len(t, scoped, 5, "the prefix must narrow exactly")
				for key := range scoped {
					testkit.True(t, key[:2] == "a/", "no key outside the prefix may leak")
				}
			})
		}
	})

	t.Run("a key the seam rejects is Invalid on every method", func(t *testing.T) {
		t.Parallel()

		// The rules are the standard library's so a caller can write
		// a key without knowing the backend. A store that admitted
		// any of these would take a key that escapes the root on one
		// backend and works on another.
		s0 := fresh()

		for name, key := range map[string]string{
			"empty":             "",
			"the root":          ".",
			"rooted":            "/k",
			"trailing slash":    "k/",
			"empty element":     "a//b",
			"dot element":       "a/./b",
			"dot-dot element":   "a/../b",
			"escaping the root": "../k",
			"over the length":   strings.Repeat("a", blob.MaxKeyLen+1),
			"over the element":  strings.Repeat("a", blob.MaxKeyElemLen+1) + "/b",
		} {
			testkit.False(t, blob.ValidKey(key), name+" must not satisfy ValidKey")

			_, perr := s0.Put(t.Context(), key, bytes.NewReader(nil), blob.PutOptions{})
			testkit.Equal(t, errs.Classify(perr), errs.Invalid, "Put must reject "+name)

			_, _, gerr := s0.Get(t.Context(), key)
			testkit.Equal(t, errs.Classify(gerr), errs.Invalid, "Get must reject "+name)

			_, serr := s0.Stat(t.Context(), key)
			testkit.Equal(t, errs.Classify(serr), errs.Invalid, "Stat must reject "+name)

			testkit.Equal(t, errs.Classify(s0.Delete(t.Context(), key, "")),
				errs.Invalid, "Delete must reject "+name)
		}
	})

	t.Run("a nested key coexists with its own prefix", func(t *testing.T) {
		t.Parallel()

		// Object stores hold both, so the seam does. A backend whose
		// namespace cannot must encode around it rather than refuse
		// the caller's key.
		s := fresh()

		put(t, s, "a/b", "the shorter one", blob.PutOptions{})
		put(t, s, "a/b/c", "the longer one", blob.PutOptions{})

		shorter, _ := read(t, s, "a/b")
		longer, _ := read(t, s, "a/b/c")

		testkit.Equal(t, shorter, "the shorter one", "the shorter key must keep its body")
		testkit.Equal(t, longer, "the longer one", "the longer key must keep its body")
	})

	t.Run("absence classifies as NotFound", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		_, _, gerr := s.Get(t.Context(), "absent")
		testkit.Equal(t, errs.Classify(gerr), errs.NotFound, "Get of an absent key")

		_, serr := s.Stat(t.Context(), "absent")
		testkit.Equal(t, errs.Classify(serr), errs.NotFound, "Stat of an absent key")
	})

	t.Run("a done context surfaces on every method", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		put(t, s, "k", "body", blob.PutOptions{})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := s.Put(ctx, "k", bytes.NewReader(nil), blob.PutOptions{})
		testkit.ErrorIs(t, err, context.Canceled, "Put must surface the context")

		_, _, err = s.Get(ctx, "k")
		testkit.ErrorIs(t, err, context.Canceled, "Get must surface the context")

		_, err = s.Stat(ctx, "k")
		testkit.ErrorIs(t, err, context.Canceled, "Stat must surface the context")

		testkit.ErrorIs(t, s.Delete(ctx, "k", ""), context.Canceled,
			"Delete must surface the context")

		_, err = s.List(ctx, "", page.Page{})
		testkit.ErrorIs(t, err, context.Canceled, "List must surface the context")
	})

	t.Run("versions never recur across delete and recreate", func(t *testing.T) {
		t.Parallel()

		// The ABA requirement inherited from the version package: a
		// stalled writer holding the pre-delete token must fail its
		// conditional write against the recreated key.
		s := fresh()

		first := put(t, s, "k", "one", blob.PutOptions{})
		testkit.NoError(t, s.Delete(t.Context(), "k", ""),
			"Delete must succeed")

		second := put(t, s, "k", "two", blob.PutOptions{})
		testkit.NotEqual(t, second.Version, first.Version,
			"a recreated key must not reuse a version — the ABA guard")

		_, err := s.Put(t.Context(), "k", bytes.NewReader([]byte("three")),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"the stalled writer's token must fail against the recreated key")
	})
}
