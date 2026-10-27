// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

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
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// maxPages bounds a walk of a cursor chain. A token that does not advance
// fails the walk at this page instead of at the test deadline.
const maxPages = 16

// The shape of the check that overwrites an object while ReadRange runs. Each
// writer puts overwritesPerWriter bodies, and each reader reads the whole
// object readsPerReader times.
const (
	overwriteWriters    = 3
	overwritesPerWriter = 20
	overwriteReaders    = 3
	readsPerReader      = 50
)

// The bodies and the content type of the checks.
const (
	describedBody = "hello blob"
	textPlain     = "text/plain"
	committedBody = "committed"

	// openedBody and replacementBody are the bodies of an object and of its
	// overwrite while a reader of it is open. They share no byte, so a reader
	// that returns a byte of the overwrite returns no prefix of the opened body.
	openedBody      = "the body that the reader opened"
	replacementBody = "A-BODY-WRITTEN-WHILE-THE-READER-IS-OPEN"

	// rangeBody is the body of the check of ReadRange against ReadAt.
	rangeBody = "0123456789"
)

// The keys of the checks. A planted defect changes the calls of the keys of its
// own check alone, so that it passes every other check.
const (
	keyDescribed = "described"
	keyGot       = "got"
	keyStatted   = "statted"
	keyVersioned = "versioned"
	keyIntact    = "intact"
	keyEmpty     = "empty"
	keyTyped     = "typed"
	keyTypedOK   = "typed-valid"
	keyShorter   = "a/b"
	keyLonger    = "a/b/c"
	keyAbsent    = "absent"
	keyRecreated = "recreated"
	keyRange     = "k"
	keyRaced     = "raced"

	prefixIfMatch   = "if-match/"
	prefixNoneMatch = "none-match/"
	prefixDelete    = "delete/"
	prefixOpened    = "opened/"
	prefixMismatch  = "mismatch/"
)

// emptyElementKey is the invalid key whose Stat the planted defect of
// StoreRefusesAnInvalidKey reports as absent.
const emptyElementKey = "a//b"

// The errors of the readers and of the planted defects of the checks.
var (
	// errInterrupted is the error that a failingReader returns after its data.
	errInterrupted = errors.New("blobtest: upload interrupted")

	// errReadFailed is the error of the reader of the planted defect of
	// StoreGetReaderFailsWithErrMismatch.
	errReadFailed = errors.New("blobtest: the read of the planted defect failed")

	// errStatAbsent is the error of the planted defect of
	// StoreRefusesAnInvalidKey, which classifies as NotFound.
	errStatAbsent = errs.WithClass(errors.New("blobtest: no object under the key"), errs.NotFound)

	// errAbsentInvalid is the error of the planted defect of
	// StoreReportsAbsenceAsNotFound, which classifies as Invalid.
	errAbsentInvalid = errs.WithClass(errors.New("blobtest: the key is refused"), errs.Invalid)
)

// origin is the start time of the fake clock of the sound store of the planted
// defects.
var origin = time.Unix(0, 0).UTC()

// failingReader returns its data and then err, as an upload that fails part-way
// does.
type failingReader struct {
	err  error
	data []byte
}

// Read copies the data that remains into p, and returns r.err once no data
// remains.
func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// object is the state of one key in a store: its body and version, or absent.
type object struct {
	version version.Version
	body    string
	present bool
}

// readResult is one ReadRange that ran during overwrites: the version that its
// Info named, the bytes that it returned and its error.
type readResult struct {
	err     error
	version version.Version
	body    string
}

// rangeStore is a store whose ReadRange is the ReadRange of a double, the
// subject of a planted defect of the suite of RangeReader.
type rangeStore struct {
	blob.Store
	*RangeReaderStub
}

// StorePutDescribesTheObject checks that Put returns the key, the size and the
// content type of the object, and a version that is not zero.
//
//dokimi:check blob.Store.Put defect=storeMisreportsTheSize
func StorePutDescribesTheObject(h *StoreHandle) {
	info := putBody(h, h.Subject(), keyDescribed, describedBody, blob.PutOptions{ContentType: textPlain})
	assert.Equal(h, info.Key, keyDescribed, h.Statement())
	assert.Equal(h, info.Size, int64(len(describedBody)), h.Statement())
	assert.Equal(h, info.ContentType, textPlain, h.Statement())
	assert.False(h, info.Version.IsZero(), h.Statement())
}

// StoreGetReturnsTheObject checks that Get returns the body that Put stored,
// with the key, the version, the size and the content type of the Info of Put
// and a modification time.
//
//dokimi:check blob.Store.Get defect=storeGetChangesTheBody
func StoreGetReturnsTheObject(h *StoreHandle) {
	s := h.Subject()
	info := putBody(h, s, keyGot, describedBody, blob.PutOptions{ContentType: textPlain})
	body, got := readBody(h, s, keyGot, h.Statement())
	assert.Equal(h, body, describedBody, h.Statement())
	assertSameObject(h, got, info, h.Statement())
	assert.False(h, got.ModTime.IsZero(), h.Statement())
}

// StoreStatDescribesTheObject checks that Stat returns the key, the version, the
// size and the content type of the Info of Put, and a modification time.
//
//dokimi:check blob.Store.Stat defect=storeStatHasNoModTime
func StoreStatDescribesTheObject(h *StoreHandle) {
	s := h.Subject()
	info := putBody(h, s, keyStatted, describedBody, blob.PutOptions{ContentType: textPlain})
	stat, err := s.Stat(h.Context(), keyStatted)
	assert.NoError(h, err, h.Statement())
	assertSameObject(h, stat, info, h.Statement())
	assert.False(h, stat.ModTime.IsZero(), h.Statement())
}

// StorePutIssuesANewVersion checks that a Put over an object returns another
// version than the Put of the object.
//
//dokimi:check blob.Store.Put defect=storeKeepsTheVersionOfAnOverwrite
func StorePutIssuesANewVersion(h *StoreHandle) {
	s := h.Subject()
	first := putBody(h, s, keyVersioned, "one", blob.PutOptions{})
	second := putBody(h, s, keyVersioned, "two", blob.PutOptions{})
	assert.NotEqual(h, second.Version, first.Version, h.Statement())
}

// StorePutIfMatchGuardsItsVersion checks that a Put with IfMatch writes over the
// version that IfMatch names, and returns ErrMismatch for a superseded version
// and for an absent key.
//
//dokimi:check blob.Store.Put defect=storeIgnoresIfMatch
func StorePutIfMatchGuardsItsVersion(h *StoreHandle) {
	s := h.Subject()
	key := prefixIfMatch + "k"
	first := putBody(h, s, key, "one", blob.PutOptions{})
	guarded := blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}}

	_, err := s.Put(h.Context(), key, strings.NewReader("two"), guarded)
	assert.NoError(h, err, h.Statement())
	_, err = s.Put(h.Context(), key, strings.NewReader("three"), guarded)
	assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
	_, err = s.Put(h.Context(), prefixIfMatch+keyAbsent, bytes.NewReader(nil), guarded)
	assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
}

// StorePutIfNoneMatchCreatesOnce checks that a Put with the IfNoneMatch wildcard
// creates an absent key, and returns ErrExists once the key exists.
//
//dokimi:check blob.Store.Put defect=storeIgnoresIfNoneMatch
func StorePutIfNoneMatchCreatesOnce(h *StoreHandle) {
	s := h.Subject()
	key := prefixNoneMatch + "created"
	createOnly := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}

	_, err := s.Put(h.Context(), key, strings.NewReader("one"), createOnly)
	assert.NoError(h, err, h.Statement())
	_, err = s.Put(h.Context(), key, strings.NewReader("two"), createOnly)
	assert.ErrorIs(h, err, version.ErrExists, h.Statement())
}

// StorePutIfNoneMatchGuardsItsVersion checks that a Put with IfNoneMatch returns
// ErrExists for the stored version, and writes over another version.
//
//dokimi:check blob.Store.Put defect=storeIgnoresIfNoneMatch
func StorePutIfNoneMatchGuardsItsVersion(h *StoreHandle) {
	s := h.Subject()
	key := prefixNoneMatch + "guarded"
	first := putBody(h, s, key, "one", blob.PutOptions{})

	_, err := s.Put(h.Context(), key, strings.NewReader("two"),
		blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: first.Version}})
	assert.ErrorIs(h, err, version.ErrExists, h.Statement())
	_, err = s.Put(h.Context(), key, strings.NewReader("two"),
		blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: "unrelated"}})
	assert.NoError(h, err, h.Statement())
}

// StoreDeleteFollowsTheConditionalTable checks that Delete removes an absent key
// without a version and the stored version of a key, and returns ErrMismatch
// for a version of an absent key and for a superseded version.
//
//dokimi:check blob.Store.Delete defect=storeDeleteIgnoresItsVersion
func StoreDeleteFollowsTheConditionalTable(h *StoreHandle) {
	s := h.Subject()
	absent, key := prefixDelete+keyAbsent, prefixDelete+"k"

	assert.NoError(h, s.Delete(h.Context(), absent, version.Unspecified), h.Statement())
	assert.ErrorIs(h, s.Delete(h.Context(), absent, "v"), version.ErrMismatch, h.Statement())

	info := putBody(h, s, key, "body", blob.PutOptions{})
	assert.ErrorIs(h, s.Delete(h.Context(), key, "stale"), version.ErrMismatch, h.Statement())
	assert.NoError(h, s.Delete(h.Context(), key, info.Version), h.Statement())

	_, err := s.Stat(h.Context(), key)
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
}

// StorePutLeavesTheObjectOnFailure checks that a Put that fails for an error of
// its reader, a failed precondition or a done context leaves the previous
// object in place.
//
//dokimi:check blob.Store.Put defect=storeKeepsAPartialUpload
func StorePutLeavesTheObjectOnFailure(h *StoreHandle) {
	s := h.Subject()
	prior := putBody(h, s, keyIntact, committedBody, blob.PutOptions{})
	want := object{version: prior.Version, body: committedBody, present: true}

	_, err := s.Put(h.Context(), keyIntact, &failingReader{data: []byte("partial"), err: errInterrupted},
		blob.PutOptions{})
	assert.ErrorIs(h, err, errInterrupted, h.Statement())
	assert.Equal(h, observe(h, s, keyIntact, h.Statement()), want, h.Statement())

	_, err = s.Put(h.Context(), keyIntact, strings.NewReader("nope"),
		blob.PutOptions{Write: version.WriteOptions{IfMatch: "stale"}})
	assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
	assert.Equal(h, observe(h, s, keyIntact, h.Statement()), want, h.Statement())

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Put(ctx, keyIntact, strings.NewReader("nope"), blob.PutOptions{})

		return err
	}, h.Statement())
	assert.Equal(h, observe(h, s, keyIntact, h.Statement()), want, h.Statement())
}

// StoreGetReaderReturnsTheOpenedVersion checks that a reader that Get opened
// returns only bytes of the version that its Info names, through an overwrite
// and through a delete of the object.
//
//dokimi:check blob.Store.Get defect=storeReaderOpensTheCurrentObject
func StoreGetReaderReturnsTheOpenedVersion(h *StoreHandle) {
	s := h.Subject()
	for _, overwrite := range []bool{true, false} {
		body, err := drainRemoved(h, s, prefixOpened+strconv.FormatBool(overwrite), overwrite)
		if err == nil {
			assert.Equal(h, body, openedBody, h.Statement())

			continue
		}
		assert.HasPrefix(h, openedBody, body, h.Statement())
	}
}

// StoreGetReaderFailsWithErrMismatch checks that a reader of a version that an
// overwrite or a delete removed fails only with an error that wraps
// version.ErrMismatch.
//
//dokimi:check blob.Store.Get defect=storeReaderFailsWithAnotherError
func StoreGetReaderFailsWithErrMismatch(h *StoreHandle) {
	s := h.Subject()
	for _, overwrite := range []bool{true, false} {
		if _, err := drainRemoved(h, s, prefixMismatch+strconv.FormatBool(overwrite), overwrite); err != nil {
			assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
		}
	}
}

// StorePutStoresAnEmptyBody checks that Put stores an empty body as an object of
// size zero, and Get returns the empty body.
//
//dokimi:check blob.Store.Put defect=storeStoresAByteForAnEmptyBody
func StorePutStoresAnEmptyBody(h *StoreHandle) {
	s := h.Subject()
	info := putBody(h, s, keyEmpty, "", blob.PutOptions{})
	assert.Equal(h, info.Size, int64(0), h.Statement())
	body, _ := readBody(h, s, keyEmpty, h.Statement())
	assert.Empty(h, body, h.Statement())
}

// StoreListYieldsEveryKeyOnce checks that a walk of the cursor chain of List at
// page sizes 1, 2 and 10 yields every key under the prefix exactly once.
//
//dokimi:check blob.Store.List defect=storeListRepeatsAKey
func StoreListYieldsEveryKeyOnce(h *StoreHandle) {
	s := h.Subject()
	want := make([]string, 0, 7)
	for i := range 5 {
		want = append(want, "a/"+strconv.Itoa(i))
	}
	want = append(want, "b/0", "b/1")
	for _, key := range want {
		putBody(h, s, key, "x", blob.PutOptions{})
	}

	for _, limit := range []int{1, 2, 10} {
		assert.Permutation(h, walk(h, s, "", limit), want, h.Statement())
		assert.Permutation(h, walk(h, s, "a/", limit), want[:5], h.Statement())
	}
}

// StoreRefusesAnInvalidKey checks that Put, Get, Stat and Delete classify each
// key that blob.ValidKey refuses as Invalid.
//
//dokimi:check blob.Store.Put defect=storeStatReportsAnInvalidKeyAsAbsent
func StoreRefusesAnInvalidKey(h *StoreHandle) {
	s := h.Subject()
	for name, key := range invalidKeys() {
		assert.False(h, blob.ValidKey(key), "ValidKey refuses "+name)

		_, err := s.Put(h.Context(), key, bytes.NewReader(nil), blob.PutOptions{})
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
		_, _, err = s.Get(h.Context(), key)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
		_, err = s.Stat(h.Context(), key)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
		assert.Equal(h, errs.Classify(s.Delete(h.Context(), key, version.Unspecified)), errs.Invalid, h.Statement())
	}
}

// StorePutRefusesAnInvalidContentType checks that Put classifies each content
// type that blob.ValidContentType refuses as Invalid, and leaves the previous
// object in place.
//
//dokimi:check blob.Store.Put defect=storeAcceptsALineBreak
func StorePutRefusesAnInvalidContentType(h *StoreHandle) {
	s := h.Subject()
	prior := putBody(h, s, keyTyped, committedBody, blob.PutOptions{})
	want := object{version: prior.Version, body: committedBody, present: true}

	for name, contentType := range invalidContentTypes() {
		assert.False(h, blob.ValidContentType(contentType), "ValidContentType refuses "+name)

		_, err := s.Put(h.Context(), keyTyped, strings.NewReader("refused"), blob.PutOptions{ContentType: contentType})
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
		assert.Equal(h, observe(h, s, keyTyped, h.Statement()), want, h.Statement())
	}
}

// StorePutStoresAValidContentType checks that Put stores each content type that
// blob.ValidContentType accepts as given, and Get and Stat return it.
//
//dokimi:check blob.Store.Put defect=storeTrimsTheContentType
func StorePutStoresAValidContentType(h *StoreHandle) {
	s := h.Subject()
	for name, contentType := range validContentTypes() {
		assert.True(h, blob.ValidContentType(contentType), "ValidContentType accepts "+name)

		info := putBody(h, s, keyTypedOK, "body", blob.PutOptions{ContentType: contentType})
		assert.Equal(h, info.ContentType, contentType, h.Statement())
		_, got := readBody(h, s, keyTypedOK, h.Statement())
		assert.Equal(h, got.ContentType, contentType, h.Statement())
		stat, err := s.Stat(h.Context(), keyTypedOK)
		assert.NoError(h, err, h.Statement())
		assert.Equal(h, stat.ContentType, contentType, h.Statement())
	}
}

// StoreKeepsAKeyAndALongerKey checks that a key and a longer key that it
// prefixes, such as "a/b" and "a/b/c", keep their bodies together.
//
//dokimi:check blob.Store.Put defect=storeDeletesTheShorterKey
func StoreKeepsAKeyAndALongerKey(h *StoreHandle) {
	s := h.Subject()
	putBody(h, s, keyShorter, "the shorter one", blob.PutOptions{})
	putBody(h, s, keyLonger, "the longer one", blob.PutOptions{})

	shorter, _ := readBody(h, s, keyShorter, h.Statement())
	assert.Equal(h, shorter, "the shorter one", h.Statement())
	longer, _ := readBody(h, s, keyLonger, h.Statement())
	assert.Equal(h, longer, "the longer one", h.Statement())
}

// StoreReportsAbsenceAsNotFound checks that Get and Stat of an absent key return
// an error of class NotFound.
//
//dokimi:check blob.Store.Get defect=storeReportsAbsenceAsInvalid
func StoreReportsAbsenceAsNotFound(h *StoreHandle) {
	s := h.Subject()
	_, _, err := s.Get(h.Context(), keyAbsent)
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
	_, err = s.Stat(h.Context(), keyAbsent)
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
}

// StoreReturnsTheErrorOfADoneContext checks that Put, Get, Stat, Delete and List
// return the error of a context that ended.
//
//dokimi:check blob.Store.List defect=storeListIgnoresItsContext
func StoreReturnsTheErrorOfADoneContext(h *StoreHandle) {
	s := h.Subject()
	putBody(h, s, keyRange, "body", blob.PutOptions{})

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Put(ctx, keyRange, bytes.NewReader(nil), blob.PutOptions{})

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, _, err := s.Get(ctx, keyRange)

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Stat(ctx, keyRange)

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		return s.Delete(ctx, keyRange, version.Unspecified)
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.List(ctx, "", page.Page{})

		return err
	}, h.Statement())
}

// StorePutNeverReusesAVersion checks that a key that a Delete removed and a Put
// wrote again has another version than before the Delete, and that a Put with
// IfMatch of the old version returns ErrMismatch.
//
//dokimi:check blob.Store.Put defect=storeReusesTheVersionOfADeletedKey
func StorePutNeverReusesAVersion(h *StoreHandle) {
	s := h.Subject()
	first := putBody(h, s, keyRecreated, "one", blob.PutOptions{})
	assert.NoError(h, s.Delete(h.Context(), keyRecreated, version.Unspecified), "Delete removes the object")
	second := putBody(h, s, keyRecreated, "two", blob.PutOptions{})
	assert.NotEqual(h, second.Version, first.Version, h.Statement())

	_, err := s.Put(h.Context(), keyRecreated, strings.NewReader("three"),
		blob.PutOptions{Write: version.WriteOptions{IfMatch: first.Version}})
	assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
}

// StoreKeepsEveryWriteAcrossARestart checks that a store that Handle.Restart
// opened again returns each object that a Put wrote before the restart, keeps
// each refused, interrupted and deleted key absent, matches a version of
// before the restart, and issues no version of before the restart again.
//
//dokimi:check blob.Store.Put defect=storeOverStorage restart=restartForgets
func StoreKeepsEveryWriteAcrossARestart(h *StoreHandle) {
	s := h.Subject()
	deleted := putBody(h, s, "deleted", "deleted before the restart", blob.PutOptions{})
	assert.NoError(h, s.Delete(h.Context(), "deleted", version.Unspecified), "Delete removes the object")
	kept := putBody(h, s, "kept", "written before the restart", blob.PutOptions{ContentType: textPlain})

	_, err := s.Put(h.Context(), "refused", strings.NewReader("refused"),
		blob.PutOptions{Write: version.WriteOptions{IfMatch: "stale"}})
	assert.ErrorIs(h, err, version.ErrMismatch, "Put refuses a stale version")
	_, err = s.Put(h.Context(), "interrupted", &failingReader{data: []byte("partial"), err: errInterrupted},
		blob.PutOptions{})
	assert.ErrorIs(h, err, errInterrupted, "Put returns the error of its reader")

	r := h.Restart(s)

	body, info := readBody(h, r, "kept", h.Statement())
	assert.Equal(h, body, "written before the restart", h.Statement())
	assertSameObject(h, info, kept, h.Statement())
	for _, key := range []string{"deleted", "refused", "interrupted"} {
		assert.Equal(h, observe(h, r, key, h.Statement()), object{}, h.Statement())
	}

	recreated := putBody(h, r, "deleted", "recreated after the restart", blob.PutOptions{})
	assert.NotEqual(h, recreated.Version, deleted.Version, h.Statement())
	overwritten, err := r.Put(h.Context(), "kept", strings.NewReader("overwritten"),
		blob.PutOptions{Write: version.WriteOptions{IfMatch: kept.Version}})
	assert.NoError(h, err, h.Statement())
	assert.NotEqual(h, overwritten.Version, kept.Version, h.Statement())
}

// StoreLeavesEachKeyAsItWasOrWhole checks that each key that Puts wrote during
// Handle.Crash is as it was before the crash or contains the whole new body,
// and that a key whose Put returned without an error contains the object of
// that Put.
//
//dokimi:check blob.Store.Put defect=storeOverStorage crash=crashTearsTheOverwrite
func StoreLeavesEachKeyAsItWasOrWhole(h *StoreHandle) {
	s := h.Subject()
	prior := putBody(h, s, "overwritten", "written before the crash", blob.PutOptions{})

	var (
		created, overwritten       blob.Info
		createdErr, overwrittenErr error
	)
	r := h.Crash(s, func() {
		created, createdErr = s.Put(h.Context(), "created", strings.NewReader("created during the crash"),
			blob.PutOptions{})
		overwritten, overwrittenErr = s.Put(h.Context(), "overwritten",
			strings.NewReader("overwritten during the crash"), blob.PutOptions{})
	})

	keys := []struct {
		err    error
		key    string
		body   string
		info   blob.Info
		before object
	}{
		{key: "created", body: "created during the crash", info: created, err: createdErr},
		{
			key:    "overwritten",
			body:   "overwritten during the crash",
			info:   overwritten,
			err:    overwrittenErr,
			before: object{version: prior.Version, body: "written before the crash", present: true},
		},
	}
	for _, k := range keys {
		got := observe(h, r, k.key, h.Statement())
		if k.err == nil {
			assert.Equal(h, got, object{version: k.info.Version, body: k.body, present: true}, h.Statement())

			continue
		}
		assert.True(h, got == k.before || got.present && got.body == k.body, h.Statement())
	}
}

// RangeReaderReadsLikeReadAt checks that ReadRange returns the count, the error
// and the bytes of bytes.Reader.ReadAt over the body, with the Info that Stat
// returns.
//
//dokimi:check RangeReader.ReadRange defect=rangeReaderEndsWithoutEOF
func RangeReaderReadsLikeReadAt(h *RangeReaderHandle) {
	s, rr := storeOf(h)
	putBody(h, s, keyRange, rangeBody, blob.PutOptions{})
	stat, err := s.Stat(h.Context(), keyRange)
	assert.NoError(h, err, "Stat describes the object")

	ref := bytes.NewReader([]byte(rangeBody))
	end := int64(len(rangeBody))
	for _, c := range []struct {
		off int64
		n   int
	}{
		{off: 3},
		{off: end},
		{off: 3, n: 1},
		{n: len(rangeBody)},
		{off: 6, n: 4},
		{off: 6, n: 8},
		{off: end, n: 4},
		{off: end + 5, n: 4},
	} {
		dst, want := make([]byte, c.n), make([]byte, c.n)
		n, info, readErr := rr.ReadRange(h.Context(), keyRange, c.off, dst, version.Unspecified)
		wantN, wantErr := ref.ReadAt(want, c.off)

		assert.Equal(h, n, wantN, h.Statement())
		if wantErr == nil {
			assert.NoError(h, readErr, h.Statement())
		} else {
			assert.ErrorIs(h, readErr, io.EOF, h.Statement())
		}
		assert.Equal(h, string(dst[:n]), string(want[:wantN]), h.Statement())
		assert.Equal(h, info, stat, h.Statement())
	}
}

// RangeReaderRefusesABadRead checks that ReadRange classifies a negative offset
// and an invalid key as Invalid, with the zero Info, and an absent object as
// NotFound whatever ifMatch names.
//
//dokimi:check RangeReader.ReadRange defect=rangeReaderReadsANegativeOffset
func RangeReaderRefusesABadRead(h *RangeReaderHandle) {
	s, rr := storeOf(h)
	putBody(h, s, keyRange, "body", blob.PutOptions{})
	dst := make([]byte, 2)

	_, info, err := rr.ReadRange(h.Context(), keyRange, -1, dst, version.Unspecified)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	assert.Equal(h, info, blob.Info{}, h.Statement())
	for _, key := range invalidKeys() {
		_, _, err = rr.ReadRange(h.Context(), key, 0, dst, version.Unspecified)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
	for _, ifMatch := range []version.Version{version.Unspecified, "v"} {
		_, _, err = rr.ReadRange(h.Context(), keyAbsent, 0, dst, ifMatch)
		assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
	}
}

// RangeReaderReadsTheVersionOfIfMatch checks that ReadRange with ifMatch reads
// the version that ifMatch names, and returns no bytes, the zero Info and
// ErrMismatch once a Put superseded that version.
//
//dokimi:check RangeReader.ReadRange defect=rangeReaderIgnoresIfMatch
func RangeReaderReadsTheVersionOfIfMatch(h *RangeReaderHandle) {
	s, rr := storeOf(h)
	first := putBody(h, s, keyRange, "one", blob.PutOptions{})
	dst := make([]byte, len("one"))

	n, _, err := rr.ReadRange(h.Context(), keyRange, 0, dst, first.Version)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, string(dst[:n]), "one", h.Statement())

	second := putBody(h, s, keyRange, "two", blob.PutOptions{})
	n, info, err := rr.ReadRange(h.Context(), keyRange, 0, dst, first.Version)
	assert.ErrorIs(h, err, version.ErrMismatch, h.Statement())
	assert.Equal(h, n, 0, h.Statement())
	assert.Equal(h, info, blob.Info{}, h.Statement())

	n, _, err = rr.ReadRange(h.Context(), keyRange, 0, dst, second.Version)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, string(dst[:n]), "two", h.Statement())
}

// RangeReaderReadsOneVersion checks that ReadRange returns the bytes of the
// version that its Info names, while writers overwrite the object.
//
//dokimi:check RangeReader.ReadRange defect=rangeReaderNamesAnotherVersion
func RangeReaderReadsOneVersion(h *RangeReaderHandle) {
	s, rr := storeOf(h)
	putBody(h, s, keyRaced, overwriteBody(overwriteWriters, 0), blob.PutOptions{})
	latest := putBody(h, s, keyRaced, overwriteBody(overwriteWriters, 1), blob.PutOptions{})
	size := len(overwriteBody(0, 0))

	dst := make([]byte, size)
	n, info, err := rr.ReadRange(h.Context(), keyRaced, 0, dst, version.Unspecified)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, info.Version, latest.Version, h.Statement())
	assert.Equal(h, string(dst[:n]), overwriteBody(overwriteWriters, 1), h.Statement())

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
				written, putErr := s.Put(h.Context(), keyRaced, strings.NewReader(body), blob.PutOptions{})
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
				got, gotInfo, readErr := rr.ReadRange(h.Context(), keyRaced, 0, buf, version.Unspecified)
				got = min(max(got, 0), len(buf))
				mu.Lock()
				reads = append(reads, readResult{err: readErr, version: gotInfo.Version, body: string(buf[:got])})
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	for _, err := range putErrs {
		assert.NoError(h, err, "a Put during the reads stores its object")
	}
	for _, r := range reads {
		assert.NoError(h, r.err, h.Statement())
		assert.Equal(h, r.body, bodies[r.version], h.Statement())
	}
}

// RangeReaderReturnsTheErrorOfADoneContext checks that ReadRange returns the
// error of a context that ended.
//
//dokimi:check RangeReader.ReadRange defect=rangeReaderIgnoresItsContext
func RangeReaderReturnsTheErrorOfADoneContext(h *RangeReaderHandle) {
	s, rr := storeOf(h)
	putBody(h, s, keyRange, "body", blob.PutOptions{})

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, _, err := rr.ReadRange(ctx, keyRange, 0, make([]byte, 2), version.Unspecified)

		return err
	}, h.Statement())
}

// invalidKeys returns keys that blob.ValidKey refuses, each named by the rule
// that it breaks.
func invalidKeys() map[string]string {
	return map[string]string{
		"empty":             "",
		"the root":          ".",
		"rooted":            "/k",
		"trailing slash":    "k/",
		"empty element":     emptyElementKey,
		"dot element":       "a/./b",
		"dot-dot element":   "a/../b",
		"escaping the root": "../k",
		"over the length":   strings.Repeat("a", blob.MaxKeyLen+1),
		"over the element":  strings.Repeat("a", blob.MaxKeyElemLen+1) + "/b",
	}
}

// invalidContentTypes returns content types that blob.ValidContentType refuses,
// each named by the rule that it breaks.
func invalidContentTypes() map[string]string {
	return map[string]string{
		"a content type over MaxContentTypeLen bytes": strings.Repeat("a", blob.MaxContentTypeLen+1),
		"a line break":         "text/plain\r\n",
		"a horizontal tab":     "text/plain;\tcharset=utf-8",
		"the delete byte":      "text/plain\x7f",
		"a byte outside ASCII": "text/pl\xc3\xa4in",
	}
}

// validContentTypes returns content types that blob.ValidContentType accepts,
// each named by the edge of the rule that it tests.
func validContentTypes() map[string]string {
	printable := make([]byte, 0, '~'-' '+1)
	for c := byte(' '); c <= '~'; c++ {
		printable = append(printable, c)
	}

	return map[string]string{
		"the empty content type":                    "",
		"a media type with a parameter":             "text/plain; charset=utf-8",
		"every printable ASCII character":           string(printable),
		"a content type of MaxContentTypeLen bytes": strings.Repeat("a", blob.MaxContentTypeLen),
	}
}

// overwriteBody returns the body that writer w puts on its i-th overwrite.
// Every body is 56 bytes long, and no two are equal.
func overwriteBody(w, i int) string {
	return strings.Repeat(fmt.Sprintf("w%d/%03d;", w, i), 8)
}

// putBody puts body under key in s, and stops the check when Put fails.
func putBody(h assert.TB, s blob.Store, key, body string, opts blob.PutOptions) blob.Info {
	h.Helper()

	info, err := s.Put(assert.Context(h), key, strings.NewReader(body), opts)
	assert.NoError(h, err, "Put stores the object")

	return info
}

// readBody returns the body and the Info of the object under key in s. It stops
// the check with the message msg when Get fails, or when the body does not
// drain or close.
func readBody(h assert.TB, s blob.Store, key, msg string) (string, blob.Info) {
	h.Helper()

	rc, info, err := s.Get(assert.Context(h), key)
	assert.NoError(h, err, msg)
	body, err := io.ReadAll(rc)
	assert.NoError(h, err, msg)
	assert.NoError(h, rc.Close(), msg)

	return string(body), info
}

// observe returns the object under key in s, or the zero object when Stat
// classifies the key as NotFound. It stops the check with the message msg when
// a read fails.
func observe(h assert.TB, s blob.Store, key, msg string) object {
	h.Helper()

	if _, err := s.Stat(assert.Context(h), key); errs.Classify(err) == errs.NotFound {
		return object{}
	}
	body, info := readBody(h, s, key, msg)

	return object{version: info.Version, body: body, present: true}
}

// assertSameObject asserts with the message msg that got and want describe one
// written object: the same key, version, size and content type. It does not
// compare ModTime, because Put may return a zero ModTime.
func assertSameObject(h assert.TB, got, want blob.Info, msg string) {
	h.Helper()

	assert.Equal(h, got.Key, want.Key, msg)
	assert.Equal(h, got.Version, want.Version, msg)
	assert.Equal(h, got.Size, want.Size, msg)
	assert.Equal(h, got.ContentType, want.ContentType, msg)
}

// drainRemoved puts openedBody under key in s, opens a reader of it, removes
// its version while the reader is open, and drains the reader. An overwrite
// removes the version with a Put of replacementBody, and otherwise a Delete
// removes it. It returns the bytes that the reader returned and its error.
func drainRemoved(h *StoreHandle, s blob.Store, key string, overwrite bool) (string, error) {
	h.Helper()

	first := putBody(h, s, key, openedBody, blob.PutOptions{})
	rc, info, err := s.Get(h.Context(), key)
	assert.NoError(h, err, "Get opens the object")
	assert.Equal(h, info.Version, first.Version, "Get opens the stored version")

	if overwrite {
		putBody(h, s, key, replacementBody, blob.PutOptions{})
	} else {
		assert.NoError(h, s.Delete(h.Context(), key, version.Unspecified), "Delete removes the object")
	}

	body, err := io.ReadAll(rc)
	assert.NoError(h, rc.Close(), "the reader closes")

	return string(body), err
}

// walk returns the keys that a walk of the cursor chain of List under prefix at
// page size limit yields, in the order of the walk. It stops the check when the
// chain runs past maxPages pages.
func walk(h *StoreHandle, s blob.Store, prefix string, limit int) []string {
	h.Helper()

	var keys []string
	p := page.Page{Limit: limit}
	for range maxPages {
		cur, err := s.List(h.Context(), prefix, p)
		assert.NoError(h, err, "List lists the objects")
		for info, ierr := range cur.Seq(h.Context()) {
			assert.NoError(h, ierr, "the cursor yields the objects")
			keys = append(keys, info.Key)
		}
		token := cur.NextPage()
		assert.NoError(h, cur.Close(), "the cursor closes")
		if token == "" {
			return keys
		}
		p.Token = token
	}
	h.Fatalf("the cursor chain ran past %d pages, so its token does not advance", maxPages)

	return nil
}

// storeOf returns the subject of a check of RangeReader as a Store and as a
// RangeReader, and stops the check when the subject is no Store.
func storeOf(h *RangeReaderHandle) (blob.Store, blob.RangeReader) {
	h.Helper()

	rr := h.Subject()
	s, ok := rr.(blob.Store)
	assert.True(h, ok, "the RangeReader of the application is a Store")

	return s, rr
}

// storeOverStorage returns the planted defect of
// StoreKeepsEveryWriteAcrossARestart and of StoreLeavesEachKeyAsItWasOrWhole:
// the sound store in memory, whose doors restartForgets and
// crashTearsTheOverwrite break the laws of the checks.
func storeOverStorage(*StoreHandle) blob.Store {
	return memory.New(fake.New(origin))
}

// restartForgets is the restart of the planted defect of
// StoreKeepsEveryWriteAcrossARestart: a store over empty storage.
func restartForgets(*StoreHandle, blob.Store) blob.Store {
	return memory.New(fake.New(origin))
}

// crashTearsTheOverwrite is the crash of the planted defect of
// StoreLeavesEachKeyAsItWasOrWhole: it runs call, and then writes a part of the
// body of the overwrite under its key.
func crashTearsTheOverwrite(h *StoreHandle, s blob.Store, call func()) blob.Store {
	call()
	putBody(h, s, "overwritten", "overwritten dur", blob.PutOptions{})

	return s
}

// storeMisreportsTheSize returns the planted defect of
// StorePutDescribesTheObject: a store whose Put of keyDescribed returns the
// size 0.
func storeMisreportsTheSize(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			info, err := sound.Put(ctx, key, r, opts)
			if key == keyDescribed {
				info.Size = 0
			}

			return info, err
		}))
}

// storeGetChangesTheBody returns the planted defect of StoreGetReturnsTheObject:
// a store whose Get of keyGot returns the body with its first byte changed.
func storeGetChangesTheBody(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
			rc, info, err := sound.Get(ctx, key)
			if err != nil || key != keyGot {
				return rc, info, err
			}
			body, err := io.ReadAll(rc)
			if closeErr := rc.Close(); err == nil {
				err = closeErr
			}
			if len(body) > 0 {
				body[0] ^= 0x01
			}

			return io.NopCloser(bytes.NewReader(body)), info, err
		}))
}

// storeStatHasNoModTime returns the planted defect of
// StoreStatDescribesTheObject: a store whose Stat of keyStatted returns the zero
// modification time.
func storeStatHasNoModTime(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreStat(func(ctx context.Context, key string) (blob.Info, error) {
			info, err := sound.Stat(ctx, key)
			if key == keyStatted {
				info.ModTime = time.Time{}
			}

			return info, err
		}))
}

// storeKeepsTheVersionOfAnOverwrite returns the planted defect of
// StorePutIssuesANewVersion: a store whose Put over the object under
// keyVersioned returns the version of the object that it replaced.
func storeKeepsTheVersionOfAnOverwrite(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			before, statErr := sound.Stat(ctx, key)
			info, err := sound.Put(ctx, key, r, opts)
			if key == keyVersioned && statErr == nil {
				info.Version = before.Version
			}

			return info, err
		}))
}

// storeIgnoresIfMatch returns the planted defect of
// StorePutIfMatchGuardsItsVersion: a store whose Put of a key under
// prefixIfMatch drops IfMatch.
func storeIgnoresIfMatch(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if strings.HasPrefix(key, prefixIfMatch) {
				opts.Write.IfMatch = version.Unspecified
			}

			return sound.Put(ctx, key, r, opts)
		}))
}

// storeIgnoresIfNoneMatch returns the planted defect of
// StorePutIfNoneMatchCreatesOnce and of StorePutIfNoneMatchGuardsItsVersion: a
// store whose Put of a key under prefixNoneMatch drops IfNoneMatch.
func storeIgnoresIfNoneMatch(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if strings.HasPrefix(key, prefixNoneMatch) {
				opts.Write.IfNoneMatch = version.Unspecified
			}

			return sound.Put(ctx, key, r, opts)
		}))
}

// storeDeleteIgnoresItsVersion returns the planted defect of
// StoreDeleteFollowsTheConditionalTable: a store whose Delete of a key under
// prefixDelete deletes whatever version ifMatch names.
func storeDeleteIgnoresItsVersion(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreDelete(func(ctx context.Context, key string, ifMatch version.Version) error {
			if strings.HasPrefix(key, prefixDelete) {
				ifMatch = version.Unspecified
			}

			return sound.Delete(ctx, key, ifMatch)
		}))
}

// storeKeepsAPartialUpload returns the planted defect of
// StorePutLeavesTheObjectOnFailure: a store whose Put of keyIntact stores the
// bytes that it read when its reader fails.
func storeKeepsAPartialUpload(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if key != keyIntact {
				return sound.Put(ctx, key, r, opts)
			}
			body, readErr := io.ReadAll(r)
			info, err := sound.Put(ctx, key, bytes.NewReader(body), opts)
			if readErr != nil {
				return blob.Info{}, readErr
			}

			return info, err
		}))
}

// storeReaderOpensTheCurrentObject returns the planted defect of
// StoreGetReaderReturnsTheOpenedVersion: a store whose reader of a key under
// prefixOpened opens the object of the key at its first Read.
func storeReaderOpensTheCurrentObject(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
			rc, info, err := sound.Get(ctx, key)
			if err != nil || !strings.HasPrefix(key, prefixOpened) {
				return rc, info, err
			}

			return &currentReader{ctx: ctx, s: sound, key: key, opened: rc}, info, nil
		}))
}

// currentReader is the reader of the planted defect of
// StoreGetReaderReturnsTheOpenedVersion. It closes the reader that Get opened,
// and reads the object that the key names at its first Read.
type currentReader struct {
	ctx    context.Context //nolint:containedctx // the reader opens the object at its first Read
	s      blob.Store
	opened io.ReadCloser
	r      io.ReadCloser
	key    string
}

// Read reads the object that the key names when the reader opened it.
func (c *currentReader) Read(p []byte) (int, error) {
	if c.r == nil {
		rc, _, err := c.s.Get(c.ctx, c.key)
		if err != nil {
			return 0, err
		}
		c.r = rc
	}

	return c.r.Read(p)
}

// Close closes the reader that Get opened and the reader of the current object.
func (c *currentReader) Close() error {
	err := c.opened.Close()
	if c.r != nil {
		err = errors.Join(err, c.r.Close())
	}

	return err
}

// storeReaderFailsWithAnotherError returns the planted defect of
// StoreGetReaderFailsWithErrMismatch: a store whose reader of a key under
// prefixMismatch fails with errReadFailed.
func storeReaderFailsWithAnotherError(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
			rc, info, err := sound.Get(ctx, key)
			if err != nil || !strings.HasPrefix(key, prefixMismatch) {
				return rc, info, err
			}
			if err := rc.Close(); err != nil {
				return nil, blob.Info{}, err
			}

			return io.NopCloser(&failingReader{err: errReadFailed}), info, nil
		}))
}

// storeStoresAByteForAnEmptyBody returns the planted defect of
// StorePutStoresAnEmptyBody: a store whose Put of an empty body under keyEmpty
// stores one zero byte.
func storeStoresAByteForAnEmptyBody(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			body, err := io.ReadAll(r)
			if err != nil {
				return blob.Info{}, err
			}
			if key == keyEmpty && len(body) == 0 {
				body = []byte{0}
			}

			return sound.Put(ctx, key, bytes.NewReader(body), opts)
		}))
}

// storeListRepeatsAKey returns the planted defect of StoreListYieldsEveryKeyOnce:
// a store whose page of List yields its first object twice.
func storeListRepeatsAKey(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreList(func(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
			cur, err := sound.List(ctx, prefix, p)
			if err != nil {
				return nil, err
			}
			var infos []blob.Info
			for info, ierr := range cur.Seq(ctx) {
				if ierr != nil {
					return nil, errors.Join(ierr, cur.Close())
				}
				infos = append(infos, info)
			}
			if len(infos) > 0 {
				infos = append(infos, infos[0])
			}
			next := cur.NextPage()
			if err := cur.Close(); err != nil {
				return nil, err
			}

			return page.NewSliceCursor(infos, next), nil
		}))
}

// storeStatReportsAnInvalidKeyAsAbsent returns the planted defect of
// StoreRefusesAnInvalidKey: a store whose Stat of emptyElementKey reports the
// key as absent.
func storeStatReportsAnInvalidKeyAsAbsent(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreStat(func(ctx context.Context, key string) (blob.Info, error) {
			if key == emptyElementKey {
				return blob.Info{}, errStatAbsent
			}

			return sound.Stat(ctx, key)
		}))
}

// storeAcceptsALineBreak returns the planted defect of
// StorePutRefusesAnInvalidContentType: a store whose Put stores an object
// without its content type when the content type has a line break.
func storeAcceptsALineBreak(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if strings.ContainsAny(opts.ContentType, "\r\n") {
				opts.ContentType = ""
			}

			return sound.Put(ctx, key, r, opts)
		}))
}

// storeTrimsTheContentType returns the planted defect of
// StorePutStoresAValidContentType: a store whose Put of keyTypedOK stores the
// content type without its leading and trailing spaces.
func storeTrimsTheContentType(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if key == keyTypedOK {
				opts.ContentType = strings.TrimSpace(opts.ContentType)
			}

			return sound.Put(ctx, key, r, opts)
		}))
}

// storeDeletesTheShorterKey returns the planted defect of
// StoreKeepsAKeyAndALongerKey: a store whose Put of keyLonger deletes the object
// under keyShorter, as a filesystem without an encoding of its keys does.
func storeDeletesTheShorterKey(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			if key == keyLonger {
				if err := sound.Delete(ctx, keyShorter, version.Unspecified); err != nil {
					return blob.Info{}, err
				}
			}

			return sound.Put(ctx, key, r, opts)
		}))
}

// storeReportsAbsenceAsInvalid returns the planted defect of
// StoreReportsAbsenceAsNotFound: a store whose Get of an absent key returns an
// error of class Invalid.
func storeReportsAbsenceAsInvalid(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
			rc, info, err := sound.Get(ctx, key)
			if errs.Classify(err) == errs.NotFound {
				return nil, blob.Info{}, errAbsentInvalid
			}

			return rc, info, err
		}))
}

// storeListIgnoresItsContext returns the planted defect of
// StoreReturnsTheErrorOfADoneContext: a store whose List lists the objects under
// a context that ended.
func storeListIgnoresItsContext(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreList(func(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
			return sound.List(context.WithoutCancel(ctx), prefix, p)
		}))
}

// storeReusesTheVersionOfADeletedKey returns the planted defect of
// StorePutNeverReusesAVersion: a store whose Put of a key that a Delete removed
// returns the version of the deleted object.
func storeReusesTheVersionOfADeletedKey(h *StoreHandle) blob.Store {
	sound := memory.New(fake.New(origin))
	deleted := make(map[string]version.Version)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreDelete(func(ctx context.Context, key string, ifMatch version.Version) error {
			before, statErr := sound.Stat(ctx, key)
			err := sound.Delete(ctx, key, ifMatch)
			if err == nil && statErr == nil {
				deleted[key] = before.Version
			}

			return err
		}),
		WithStorePut(func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
			info, err := sound.Put(ctx, key, r, opts)
			if v, ok := deleted[key]; ok && err == nil {
				info.Version = v
			}

			return info, err
		}))
}

// rangeReaderDefect returns a store over the sound store whose ReadRange is
// readRange.
func rangeReaderDefect(
	h *RangeReaderHandle,
	readRange func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		ifMatch version.Version) (int, blob.Info, error),
) blob.RangeReader {
	sound := memory.New(fake.New(origin))

	return rangeStore{Store: sound, RangeReaderStub: NewRangeReaderStub(h, RangeReaderStubDelegateTo(sound),
		WithRangeReaderReadRange(func(ctx context.Context, key string, off int64, dst []byte,
			ifMatch version.Version,
		) (int, blob.Info, error) {
			return readRange(sound, ctx, key, off, dst, ifMatch)
		}))}
}

// rangeReaderEndsWithoutEOF returns the planted defect of
// RangeReaderReadsLikeReadAt: a store whose ReadRange of a range past the end of
// the object returns a nil error.
func rangeReaderEndsWithoutEOF(h *RangeReaderHandle) blob.RangeReader {
	return rangeReaderDefect(h, func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		ifMatch version.Version,
	) (int, blob.Info, error) {
		n, info, err := sound.ReadRange(ctx, key, off, dst, ifMatch)
		if errors.Is(err, io.EOF) {
			return n, info, nil
		}

		return n, info, err
	})
}

// rangeReaderReadsANegativeOffset returns the planted defect of
// RangeReaderRefusesABadRead: a store whose ReadRange reads a negative offset
// from the first byte.
func rangeReaderReadsANegativeOffset(h *RangeReaderHandle) blob.RangeReader {
	return rangeReaderDefect(h, func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		ifMatch version.Version,
	) (int, blob.Info, error) {
		return sound.ReadRange(ctx, key, max(off, 0), dst, ifMatch)
	})
}

// rangeReaderIgnoresIfMatch returns the planted defect of
// RangeReaderReadsTheVersionOfIfMatch: a store whose ReadRange reads the stored
// version whatever ifMatch names.
func rangeReaderIgnoresIfMatch(h *RangeReaderHandle) blob.RangeReader {
	return rangeReaderDefect(h, func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		_ version.Version,
	) (int, blob.Info, error) {
		return sound.ReadRange(ctx, key, off, dst, version.Unspecified)
	})
}

// rangeReaderNamesAnotherVersion returns the planted defect of
// RangeReaderReadsOneVersion: a store whose ReadRange of keyRaced returns an
// Info with a version that no Put returned.
func rangeReaderNamesAnotherVersion(h *RangeReaderHandle) blob.RangeReader {
	return rangeReaderDefect(h, func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		ifMatch version.Version,
	) (int, blob.Info, error) {
		n, info, err := sound.ReadRange(ctx, key, off, dst, ifMatch)
		if key == keyRaced {
			info.Version += "-another"
		}

		return n, info, err
	})
}

// rangeReaderIgnoresItsContext returns the planted defect of
// RangeReaderReturnsTheErrorOfADoneContext: a store whose ReadRange reads under
// a context that ended.
func rangeReaderIgnoresItsContext(h *RangeReaderHandle) blob.RangeReader {
	return rangeReaderDefect(h, func(sound *memory.Store, ctx context.Context, key string, off int64, dst []byte,
		ifMatch version.Version,
	) (int, blob.Info, error) {
		return sound.ReadRange(context.WithoutCancel(ctx), key, off, dst, ifMatch)
	})
}
