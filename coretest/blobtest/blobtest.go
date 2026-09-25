// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package blobtest provides the conformance suite for
// [go.thesmos.sh/core/blob.Store].
//
// [AssertStore] runs one subtest per law of the seam, and the laws of
// [go.thesmos.sh/core/blob.RangeReader] for a store that implements it.
// [WithReopen] and [WithCrash] add the laws that only a restart can
// test, for a store over durable storage. [WithRangeReader] fails a
// store that does not implement RangeReader.
package blobtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
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

// maxPages bounds a walk of a cursor chain. A token that does not
// advance fails the walk at this page instead of at the test deadline.
const maxPages = 16

// The shape of the case that overwrites an object while ReadRange runs.
// Each writer puts overwritesPerWriter bodies, and each reader reads the
// whole object readsPerReader times.
const (
	overwriteWriters    = 3
	overwritesPerWriter = 20
	overwriteReaders    = 3
	readsPerReader      = 50
)

// errInterrupted is the error a [failingReader] returns after its data.
var errInterrupted = errors.New("blobtest: upload interrupted")

// Option configures a run of [AssertStore].
type Option func(*config)

// config is the result of applying every [Option] of one run.
type config struct {
	reopen      func(t *testing.T, s blob.Store) blob.Store
	crash       func(t *testing.T, s blob.Store, write func()) blob.Store
	rangeReader bool
}

// WithReopen gives the suite a way to open the storage behind s again,
// as after a process restart, and adds a case that requires:
//
//   - every Put that returned to be readable with the same version,
//     body, size and content type;
//   - every refused Put and every deleted key to be absent;
//   - a version issued before the reopen to guard its object after it;
//   - no version issued before the reopen to be issued again for the
//     same key.
//
// reopen returns a new Store over the storage behind s. It fails t
// when it cannot open the storage.
func WithReopen(reopen func(t *testing.T, s blob.Store) blob.Store) Option {
	return func(c *config) { c.reopen = reopen }
}

// WithCrash gives the suite a way to crash the storage behind s while
// write runs, and adds a case that requires each key that write puts to
// be as it was before write or to contain the whole new object. A key
// whose Put returned without error must contain the whole new object,
// with the version that Put returned.
//
// crash calls write once, crashes the storage at a point the adapter
// chooses, and returns after write returns. It returns a new Store over
// the storage as the crash left it, and fails t when it cannot open the
// storage.
func WithCrash(crash func(t *testing.T, s blob.Store, write func()) blob.Store) Option {
	return func(c *config) { c.crash = crash }
}

// WithRangeReader adds a case that requires the store to implement
// [blob.RangeReader], found through [blob.AsRangeReader]. A store without
// the capability fails the run.
//
// [AssertStore] runs the cases of RangeReader for every store that
// implements it, with or without this option. The option catches an
// adapter whose ReadRange no longer matches the interface, which
// detection alone would skip.
func WithRangeReader() Option {
	return func(c *config) { c.rangeReader = true }
}

// invalidKeys returns keys that [blob.ValidKey] rejects, each named by
// the rule it breaks.
func invalidKeys() map[string]string {
	return map[string]string{
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
	}
}

// overwriteBody returns the body that writer w puts on its i-th
// overwrite. Every body is 56 bytes long, and no two are equal.
func overwriteBody(w, i int) string {
	return strings.Repeat(fmt.Sprintf("w%d/%03d;", w, i), 8)
}

// failingReader returns its data and then err, as an upload that fails
// part-way does.
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

// object is the state of one key in a store: its body and version, or
// absent. The fields are exported so that testkit.Equal can compare
// them.
type object struct {
	Version version.Version
	Body    string
	Present bool
}

// assertSameObject requires got and want to describe the same written
// object: the same key, version, size and content type. It does not
// compare ModTime, because Put may return a zero ModTime.
func assertSameObject(t *testing.T, got, want blob.Info, who string) {
	t.Helper()

	testkit.Equal(t, got.Key, want.Key, who+" must name the same key")
	testkit.Equal(t, got.Version, want.Version, who+" must report the same version")
	testkit.Equal(t, got.Size, want.Size, who+" must report the same size")
	testkit.Equal(t, got.ContentType, want.ContentType,
		who+" must report the same content type")
}

// AssertStore runs the laws of [blob.Store] against the stores that
// newStore returns. newStore returns an empty store that reads wall
// time from c. Each case builds its own store, so no case observes the
// objects of another.
//
// The cases are:
//
//   - Put, Get and Stat agree on the body, key, version, size and
//     content type. Put may return a zero ModTime, because the S3
//     family reports no modification time on write. Get and Stat must
//     report one.
//   - Each write issues a new version. A key deleted and written again
//     never reuses a version, so a writer with a version from before
//     the delete cannot overwrite the new object.
//   - IfMatch, IfNoneMatch and Delete follow the conditional table of
//     [version.WriteOptions].
//   - A Put that fails for a reader error, a precondition or a done
//     context leaves the previous object in place.
//   - An open reader serves the version its Info named, through an
//     overwrite.
//   - A walk of the cursor chain at page sizes 1, 2 and 10 yields every
//     key under the prefix exactly once. The walk stops at 16 pages.
//   - Every method rejects a key that [blob.ValidKey] rejects, as
//     Invalid. The rules are those of [io/fs.ValidPath], so a caller can
//     write a key without knowing the backend.
//   - A key and a longer key it prefixes, such as "a/b" and "a/b/c",
//     exist together. A backend whose namespace cannot store both
//     encodes keys so that it can.
//   - Absence classifies as NotFound, and every method returns the
//     error of a done context.
//
// For a store that implements [blob.RangeReader], found through
// [blob.AsRangeReader], the cases also cover ReadRange:
//
//   - The count, the error and the bytes are those of
//     [bytes.Reader.ReadAt] over the body, and every result returns the
//     Info that Stat returns.
//   - A negative offset and an invalid key classify as Invalid, and an
//     absent object classifies as NotFound whatever ifMatch names.
//   - A non-zero ifMatch reads only the version it names.
//   - A read during overwrites returns the bytes of the version its Info
//     names.
//   - ReadRange returns the error of a done context.
//
// Each [Option] adds the cases its docblock lists.
func AssertStore(t *testing.T, newStore func(c clock.Clock) blob.Store, options ...Option) {
	t.Helper()

	var cfg config
	for _, o := range options {
		o(&cfg)
	}

	fresh := func() blob.Store {
		return newStore(fake.New(time.Unix(0, 0).UTC()))
	}
	put := func(t *testing.T, s blob.Store, key, body string, opts blob.PutOptions) blob.Info {
		t.Helper()

		info, err := s.Put(t.Context(), key, strings.NewReader(body), opts)
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
	observe := func(t *testing.T, s blob.Store, key string) object {
		t.Helper()

		if _, err := s.Stat(t.Context(), key); errs.Classify(err) == errs.NotFound {
			return object{}
		}
		body, info := read(t, s, key)

		return object{Version: info.Version, Body: body, Present: true}
	}

	t.Run("Put, Get and Stat agree on the body and metadata", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		info := put(t, s, "k", "hello blob", blob.PutOptions{ContentType: "text/plain"})
		testkit.Equal(t, info.Key, "k", "Info must name the key")
		testkit.Equal(t, info.Size, int64(10), "Put's Info must report the consumed size")
		testkit.Equal(t, info.ContentType, "text/plain", "ContentType must round-trip")
		testkit.False(t, info.Version.IsZero(), "a written object must have a version")

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
			"each write must have a distinct version")
	})

	t.Run("IfMatch guards the version it names", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		first := put(t, s, "k", "one", blob.PutOptions{})

		_, err := s.Put(t.Context(), "k", strings.NewReader("two"),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.NoError(t, err, "IfMatch naming the current version must succeed")

		_, err = s.Put(t.Context(), "k", strings.NewReader("three"),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"IfMatch naming a superseded version must fail")

		_, err = s.Put(t.Context(), "absent", bytes.NewReader(nil),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"IfMatch against an absent key must fail")
	})

	t.Run("IfNoneMatch wildcard creates exactly once", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		opts := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}

		_, err := s.Put(t.Context(), "k", strings.NewReader("one"), opts)
		testkit.NoError(t, err, "create-only must succeed on an absent key")

		_, err = s.Put(t.Context(), "k", strings.NewReader("two"), opts)
		testkit.ErrorIs(t, err, version.ErrExists,
			"create-only must fail once the key exists")
	})

	t.Run("IfNoneMatch naming a version guards that version", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		first := put(t, s, "k", "one", blob.PutOptions{})

		_, err := s.Put(t.Context(), "k", strings.NewReader("two"),
			blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrExists,
			"IfNoneMatch naming the current version must fail")

		_, err = s.Put(t.Context(), "k", strings.NewReader("two"),
			blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: "unrelated"}})
		testkit.NoError(t, err,
			"IfNoneMatch naming a version that is not current must succeed")
	})

	t.Run("Delete follows the conditional table", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		testkit.NoError(t, s.Delete(t.Context(), "absent", ""),
			"an unconditional Delete of an absent key must succeed")

		testkit.ErrorIs(t,
			s.Delete(t.Context(), "absent", "v"),
			version.ErrMismatch,
			"a Delete naming a version of an absent key must fail")

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

	t.Run("a failed Put leaves the previous object in place", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		prior := put(t, s, "k", "committed", blob.PutOptions{})

		assertIntact := func(t *testing.T) {
			t.Helper()

			body, info := read(t, s, "k")
			testkit.Equal(t, body, "committed", "the prior body must be untouched")
			testkit.Equal(t, info.Version, prior.Version, "the prior version must be untouched")
		}

		t.Run("a reader that fails part-way", func(t *testing.T) {
			_, err := s.Put(t.Context(), "k",
				&failingReader{data: []byte("partial"), err: errInterrupted}, blob.PutOptions{})
			testkit.ErrorIs(t, err, errInterrupted, "Put must return the reader's error")
			assertIntact(t)
		})

		t.Run("a failed precondition", func(t *testing.T) {
			_, err := s.Put(t.Context(), "k", strings.NewReader("nope"),
				blob.PutOptions{Write: version.WriteOptions{IfMatch: "stale"}})
			testkit.ErrorIs(t, err, version.ErrMismatch, "the precondition must fail")
			assertIntact(t)
		})

		t.Run("a done context", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := s.Put(ctx, "k", strings.NewReader("nope"), blob.PutOptions{})
			testkit.ErrorIs(t, err, context.Canceled, "Put must return the context's error")
			assertIntact(t)
		})
	})

	t.Run("an open reader serves the version its Info named", func(t *testing.T) {
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

	t.Run("an empty body is an object", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		info := put(t, s, "k", "", blob.PutOptions{})
		testkit.Equal(t, info.Size, int64(0), "the empty body must have size zero")

		body, _ := read(t, s, "k")
		testkit.Len(t, body, 0, "the empty body must round-trip empty")
	})

	t.Run("a cursor chain yields every key exactly once", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		for i := range 5 {
			put(t, s, "a/"+strconv.Itoa(i), "x", blob.PutOptions{})
		}
		put(t, s, "b/0", "x", blob.PutOptions{})
		put(t, s, "b/1", "x", blob.PutOptions{})

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

			t.Fatalf("the cursor chain ran past %d pages, so its token does not advance", maxPages)

			return nil
		}

		for _, limit := range []int{1, 2, 10} {
			t.Run("page size "+strconv.Itoa(limit), func(t *testing.T) {
				all := walk(t, "", limit)
				testkit.Len(t, all, 7, "the empty prefix must enumerate every key")
				for key, n := range all {
					testkit.Equal(t, n, 1, "key "+key+" must appear exactly once")
				}

				scoped := walk(t, "a/", limit)
				testkit.Len(t, scoped, 5, "the prefix must select exactly its keys")
				for key := range scoped {
					testkit.True(t, strings.HasPrefix(key, "a/"),
						"key "+key+" must not appear under another prefix")
				}
			})
		}
	})

	t.Run("every method rejects an invalid key as Invalid", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		for name, key := range invalidKeys() {
			testkit.False(t, blob.ValidKey(key), name+" must not satisfy ValidKey")

			_, perr := s.Put(t.Context(), key, bytes.NewReader(nil), blob.PutOptions{})
			testkit.Equal(t, errs.Classify(perr), errs.Invalid, "Put must reject "+name)

			_, _, gerr := s.Get(t.Context(), key)
			testkit.Equal(t, errs.Classify(gerr), errs.Invalid, "Get must reject "+name)

			_, serr := s.Stat(t.Context(), key)
			testkit.Equal(t, errs.Classify(serr), errs.Invalid, "Stat must reject "+name)

			testkit.Equal(t, errs.Classify(s.Delete(t.Context(), key, "")),
				errs.Invalid, "Delete must reject "+name)
		}
	})

	t.Run("a key and a longer key it prefixes exist together", func(t *testing.T) {
		t.Parallel()

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

	t.Run("every method returns the error of a done context", func(t *testing.T) {
		t.Parallel()

		s := fresh()
		put(t, s, "k", "body", blob.PutOptions{})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := s.Put(ctx, "k", bytes.NewReader(nil), blob.PutOptions{})
		testkit.ErrorIs(t, err, context.Canceled, "Put must return the context's error")

		_, _, err = s.Get(ctx, "k")
		testkit.ErrorIs(t, err, context.Canceled, "Get must return the context's error")

		_, err = s.Stat(ctx, "k")
		testkit.ErrorIs(t, err, context.Canceled, "Stat must return the context's error")

		testkit.ErrorIs(t, s.Delete(ctx, "k", ""), context.Canceled,
			"Delete must return the context's error")

		_, err = s.List(ctx, "", page.Page{})
		testkit.ErrorIs(t, err, context.Canceled, "List must return the context's error")
	})

	t.Run("a recreated key never reuses a version", func(t *testing.T) {
		t.Parallel()

		s := fresh()

		first := put(t, s, "k", "one", blob.PutOptions{})
		testkit.NoError(t, s.Delete(t.Context(), "k", ""),
			"Delete must succeed")

		second := put(t, s, "k", "two", blob.PutOptions{})
		testkit.NotEqual(t, second.Version, first.Version,
			"a recreated key must not reuse a version")

		_, err := s.Put(t.Context(), "k", strings.NewReader("three"),
			blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
		testkit.ErrorIs(t, err, version.ErrMismatch,
			"a version from before the delete must not match the recreated key")
	})

	if cfg.rangeReader {
		t.Run("a store run with WithRangeReader implements RangeReader", func(t *testing.T) {
			t.Parallel()

			_, ok := blob.AsRangeReader(fresh())
			testkit.True(t, ok, "WithRangeReader requires the store to implement blob.RangeReader")
		})
	}

	if _, ok := blob.AsRangeReader(fresh()); ok {
		assertRangeReader(t, fresh)
	}

	if cfg.reopen != nil {
		t.Run("a reopened store keeps every write that returned", func(t *testing.T) {
			t.Parallel()

			s := fresh()
			deleted := put(t, s, "deleted", "deleted before the reopen", blob.PutOptions{})
			testkit.NoError(t, s.Delete(t.Context(), "deleted", ""), "Delete must succeed")
			kept := put(t, s, "kept", "written before the reopen",
				blob.PutOptions{ContentType: "text/plain"})

			_, err := s.Put(t.Context(), "refused", strings.NewReader("refused"),
				blob.PutOptions{Write: version.WriteOptions{IfMatch: "stale"}})
			testkit.ErrorIs(t, err, version.ErrMismatch, "the precondition must fail")

			_, err = s.Put(t.Context(), "interrupted",
				&failingReader{data: []byte("partial"), err: errInterrupted}, blob.PutOptions{})
			testkit.ErrorIs(t, err, errInterrupted, "Put must return the reader's error")

			r := cfg.reopen(t, s)

			body, info := read(t, r, "kept")
			testkit.Equal(t, body, "written before the reopen", "the body must survive the reopen")
			assertSameObject(t, info, kept, "Get's Info after the reopen")

			for _, key := range []string{"deleted", "refused", "interrupted"} {
				testkit.Equal(t, observe(t, r, key), object{},
					"key "+key+" must be absent after the reopen")
			}

			recreated := put(t, r, "deleted", "recreated after the reopen", blob.PutOptions{})
			testkit.NotEqual(t, recreated.Version, deleted.Version,
				"a key recreated after the reopen must not reuse a version from before it")

			overwritten, err := r.Put(t.Context(), "kept", strings.NewReader("overwritten"),
				blob.PutOptions{Write: version.WriteOptions{IfMatch: kept.Version}})
			testkit.NoError(t, err, "a version from before the reopen must still match its object")
			testkit.NotEqual(t, overwritten.Version, kept.Version,
				"an overwrite after the reopen must issue a new version")
		})
	}

	if cfg.crash != nil {
		t.Run("a crash leaves each key as it was or whole", func(t *testing.T) {
			t.Parallel()

			s := fresh()
			prior := put(t, s, "overwritten", "written before the crash", blob.PutOptions{})

			var (
				created, overwritten       blob.Info
				createdErr, overwrittenErr error
			)
			r := cfg.crash(t, s, func() {
				created, createdErr = s.Put(t.Context(), "created",
					strings.NewReader("created during the crash"), blob.PutOptions{})
				overwritten, overwrittenErr = s.Put(t.Context(), "overwritten",
					strings.NewReader("overwritten during the crash"), blob.PutOptions{})
			})

			assertAfterCrash := func(key string, before object, body string, info blob.Info, err error) {
				t.Helper()

				got := observe(t, r, key)
				if err == nil {
					testkit.Equal(t, got, object{Version: info.Version, Body: body, Present: true},
						"key "+key+" must contain the object its returned Put wrote")

					return
				}
				testkit.True(t, got == before || (got.Present && got.Body == body),
					"key "+key+" must be as it was before the crash or contain the whole new body")
			}

			assertAfterCrash("created", object{}, "created during the crash", created, createdErr)
			assertAfterCrash("overwritten",
				object{Version: prior.Version, Body: "written before the crash", Present: true},
				"overwritten during the crash", overwritten, overwrittenErr)
		})
	}
}

// readResult is one ReadRange that ran during overwrites: the version
// its Info named, the bytes it returned and its error.
type readResult struct {
	err     error
	version version.Version
	body    string
}

// assertRangeReader runs the laws of [blob.RangeReader] against the
// stores that fresh returns. Each case builds its own store.
func assertRangeReader(t *testing.T, fresh func() blob.Store) {
	t.Helper()

	// key names the object that each case writes and reads.
	const key = "k"

	open := func(t *testing.T) (blob.Store, blob.RangeReader) {
		t.Helper()

		s := fresh()
		rr, ok := blob.AsRangeReader(s)
		testkit.True(t, ok, "the store must implement blob.RangeReader")

		return s, rr
	}
	put := func(t *testing.T, s blob.Store, body string) blob.Info {
		t.Helper()

		info, err := s.Put(t.Context(), key, strings.NewReader(body), blob.PutOptions{})
		testkit.NoError(t, err, "Put must succeed")

		return info
	}

	t.Run("ReadRange returns what ReadAt returns over the body", func(t *testing.T) {
		t.Parallel()

		const body = "0123456789"

		s, rr := open(t)
		put(t, s, body)
		stat, err := s.Stat(t.Context(), key)
		testkit.NoError(t, err, "Stat must succeed")

		ref := bytes.NewReader([]byte(body))
		end := int64(len(body))

		for name, c := range map[string]struct {
			off int64
			n   int
		}{
			"an empty range inside the object":   {off: 3},
			"an empty range at the end":          {off: end},
			"one byte":                           {off: 3, n: 1},
			"the whole object":                   {n: len(body)},
			"a range that ends at the last byte": {off: 6, n: 4},
			"a range that ends past the end":     {off: 6, n: 8},
			"an offset at the end":               {off: end, n: 4},
			"an offset past the end":             {off: end + 5, n: 4},
		} {
			dst, want := make([]byte, c.n), make([]byte, c.n)
			n, info, readErr := rr.ReadRange(t.Context(), key, c.off, dst, version.Unspecified)
			wantN, wantErr := ref.ReadAt(want, c.off)

			testkit.Equal(t, n, wantN, name+" must return the count of ReadAt")
			if wantErr == nil {
				testkit.NoError(t, readErr, name+" must return a nil error")
			} else {
				testkit.ErrorIs(t, readErr, io.EOF, name+" must return io.EOF")
			}
			testkit.Equal(t, string(dst[:n]), string(want[:wantN]), name+" must return the bytes of the body")
			testkit.Equal(t, info, stat, name+" must return the Info that Stat returns")
		}
	})

	t.Run("ReadRange rejects a bad offset or key as Invalid and absence as NotFound", func(t *testing.T) {
		t.Parallel()

		s, rr := open(t)
		put(t, s, "body")
		dst := make([]byte, 2)

		_, info, err := rr.ReadRange(t.Context(), key, -1, dst, version.Unspecified)
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "a negative offset must classify as Invalid")
		testkit.Equal(t, info, blob.Info{}, "a refused read must return the zero Info")

		for name, invalid := range invalidKeys() {
			_, _, err = rr.ReadRange(t.Context(), invalid, 0, dst, version.Unspecified)
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ReadRange must reject "+name)
		}

		_, _, err = rr.ReadRange(t.Context(), "absent", 0, dst, version.Unspecified)
		testkit.Equal(t, errs.Classify(err), errs.NotFound, "an absent object must classify as NotFound")

		_, _, err = rr.ReadRange(t.Context(), "absent", 0, dst, "v")
		testkit.Equal(t, errs.Classify(err), errs.NotFound,
			"an absent object must classify as NotFound whatever ifMatch names")
	})

	t.Run("ReadRange with ifMatch reads only the version it names", func(t *testing.T) {
		t.Parallel()

		s, rr := open(t)
		first := put(t, s, "one")
		dst := make([]byte, len("one"))

		n, _, err := rr.ReadRange(t.Context(), key, 0, dst, first.Version)
		testkit.NoError(t, err, "a read naming the stored version must succeed")
		testkit.Equal(t, string(dst[:n]), "one", "the read must return the stored bytes")

		second := put(t, s, "two")

		n, info, err := rr.ReadRange(t.Context(), key, 0, dst, first.Version)
		testkit.ErrorIs(t, err, version.ErrMismatch, "a read naming a superseded version must fail")
		testkit.Equal(t, n, 0, "a refused read must return no bytes")
		testkit.Equal(t, info, blob.Info{}, "a refused read must return the zero Info")

		n, _, err = rr.ReadRange(t.Context(), key, 0, dst, second.Version)
		testkit.NoError(t, err, "a read naming the new version must succeed")
		testkit.Equal(t, string(dst[:n]), "two", "the read must return the new bytes")
	})

	t.Run("ReadRange returns bytes of the version its Info names", func(t *testing.T) {
		t.Parallel()

		s, rr := open(t)
		put(t, s, overwriteBody(overwriteWriters, 0))
		latest := put(t, s, overwriteBody(overwriteWriters, 1))
		size := len(overwriteBody(0, 0))

		dst := make([]byte, size)
		n, info, err := rr.ReadRange(t.Context(), key, 0, dst, version.Unspecified)
		testkit.NoError(t, err, "ReadRange must succeed")
		testkit.Equal(t, info.Version, latest.Version, "ReadRange must read the stored version")
		testkit.Equal(t, string(dst[:n]), overwriteBody(overwriteWriters, 1),
			"ReadRange must return the bytes of the version its Info names")

		var (
			mu      sync.Mutex
			bodies  = map[version.Version]string{latest.Version: overwriteBody(overwriteWriters, 1)}
			reads   []readResult
			putErrs []error
			wg      sync.WaitGroup
		)
		for w := range overwriteWriters {
			wg.Go(func() {
				for i := range overwritesPerWriter {
					body := overwriteBody(w, i)
					written, putErr := s.Put(t.Context(), key, strings.NewReader(body), blob.PutOptions{})
					mu.Lock()
					bodies[written.Version] = body
					putErrs = append(putErrs, putErr)
					mu.Unlock()
				}
			})
		}
		for range overwriteReaders {
			wg.Go(func() {
				buf := make([]byte, size)
				for range readsPerReader {
					got, gotInfo, readErr := rr.ReadRange(t.Context(), key, 0, buf, version.Unspecified)
					// A count outside buf must fail the case, not panic
					// the run, so the slice is clamped to buf.
					got = min(max(got, 0), len(buf))
					mu.Lock()
					reads = append(reads, readResult{err: readErr, version: gotInfo.Version, body: string(buf[:got])})
					mu.Unlock()
				}
			})
		}
		wg.Wait()

		for _, err := range putErrs {
			testkit.NoError(t, err, "a Put during the reads must succeed")
		}
		for _, r := range reads {
			testkit.NoError(t, r.err, "a ReadRange during the overwrites must succeed")
			want, ok := bodies[r.version]
			testkit.True(t, ok, "a read must name a version that a Put returned")
			testkit.Equal(t, r.body, want, "a read must return the bytes of the version its Info names")
		}
	})

	t.Run("ReadRange returns the error of a done context", func(t *testing.T) {
		t.Parallel()

		s, rr := open(t)
		put(t, s, "body")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _, err := rr.ReadRange(ctx, key, 0, make([]byte, 2), version.Unspecified)
		testkit.ErrorIs(t, err, context.Canceled, "ReadRange must return the context's error")
	})
}
