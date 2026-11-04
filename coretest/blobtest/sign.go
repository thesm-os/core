// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blobtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

// The lifetimes of the requests that the checks of the signers sign.
const (
	// signedLifetime is the lifetime of a request that a check sends before it
	// expires. Its half second fails a store that rounds an expiry up.
	signedLifetime = time.Hour + 500*time.Millisecond

	// shortestLifetime is the shortest lifetime that a signer signs.
	shortestLifetime = time.Second
)

// The base URL of a store in memory that an httptest server serves, and the
// timeout of a request to it.
const (
	servedPath     = "/blobs"
	servedRoot     = servedPath + "/"
	requestTimeout = 5 * time.Second
)

// The statuses of a response of a backend, in the classes of RFC 9110. A backend
// serves a request with a status of the class 2xx, and it refuses a request with
// a status of the class 4xx or 5xx.
const (
	firstServed  = 200
	lastServed   = 299
	firstRefused = 400
	lastRefused  = 599
)

// The headers, the bodies and the content type of the checks of the signers.
const (
	headerContentType = "Content-Type"
	headerRange       = "Range"

	// signedBody is the body of an object that a client reads or creates.
	signedBody = "a body that a client reads or creates"

	// rangeHeader is the Range header of a read of rangeBody, and rangeBytes
	// are the bytes of the range.
	rangeHeader = "bytes=3-6"
	rangeBytes  = "3456"

	// octetStream is the content type of a body that a client sends in place
	// of textPlain.
	octetStream = "application/octet-stream"
)

// The keys of the checks of the signers. A planted defect changes the calls of
// the keys of its own check alone. A check of GetSigner and a check of
// CreateSigner share a key, because no planted defect of one suite runs a check
// of the other.
const (
	keyServed    = "served/object"
	keyMoved     = "served/moved"
	keyRanged    = "ranged/object"
	keyExpired   = "expired/object"
	keyInvalid   = "invalid/object"
	keyCancelled = "cancelled/object"
	keyCreated   = "created/object"
	keyBody      = "body/object"

	prefixChanged = "changed/"
	prefixOnce    = "once/"
)

// Backend is the side of a store under test that serves its signed requests: a
// client of the backend of the store, the time of the clock of the store, the
// delay of that clock in a call, and the passage of time on the backend. A
// check of [blob.GetSigner] or [blob.CreateSigner] opens the Backend of its
// subject with [go.dokimi.dev/conform.Open]. The test of a store supplies the
// Backend through the option [go.dokimi.dev/conform.Supplies], and each
// planted defect has a Backend of its own.
//
// # Concurrency
//
// A check calls the Backend of its subject from one goroutine.
type Backend interface {
	// Do sends a signed request to the backend of the store.
	Do(req *http.Request) (*http.Response, error)

	// Time returns the time of the clock of the store.
	Time() time.Time

	// SignDelay returns the longest time from a call of Time to the read of
	// the clock of the store in a call of SignGet or SignCreate that follows
	// it. A check of an expiry signs the shortest lifetime plus SignDelay, so
	// that the store accepts the lifetime. A Backend over a clock that does
	// not move between the two reads returns 0. A Backend over a clock that
	// moves returns the delay that the test of the store measured.
	SignDelay() time.Duration

	// Elapse returns once d has elapsed on the clock of the backend. It
	// returns the error of ctx when ctx ends first.
	Elapse(ctx context.Context, d time.Duration) error
}

// getSignerBackend returns the Backend of the servedGetSigner gs, the door of
// the Backend of each planted defect of a check of GetSigner. It stops the
// check when gs is no servedGetSigner.
func getSignerBackend(h *GetSignerHandle, gs blob.GetSigner) Backend {
	h.Helper()

	served, ok := gs.(servedGetSigner)
	assert.True(h, ok, "the GetSigner is a served store")

	return served.backend
}

// createSignerBackend returns the Backend of the servedCreateSigner cs, the
// door of the Backend of each planted defect of a check of CreateSigner. It
// stops the check when cs is no servedCreateSigner.
func createSignerBackend(h *CreateSignerHandle, cs blob.CreateSigner) Backend {
	h.Helper()

	served, ok := cs.(servedCreateSigner)
	assert.True(h, ok, "the CreateSigner is a served store")

	return served.backend
}

// memoryBackend is the Backend of a store in memory that an httptest server
// serves.
type memoryBackend struct {
	client *http.Client
	clock  *fake.Clock
}

// Do sends req with the client of the httptest server.
func (b memoryBackend) Do(req *http.Request) (*http.Response, error) {
	return b.client.Do(req)
}

// Time returns the time of the fake clock of the store.
func (b memoryBackend) Time() time.Time {
	return b.clock.Time()
}

// SignDelay returns 0, because the fake clock of the store does not move
// between a read of the check and a read of the store.
func (memoryBackend) SignDelay() time.Duration {
	return 0
}

// Elapse advances the fake clock of the store by d. It returns the error of
// ctx when ctx has ended before the call.
func (b memoryBackend) Elapse(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.clock.Advance(d)

	return nil
}

// servedStore is a store in memory that an httptest server serves, with its
// Backend.
type servedStore struct {
	*memory.Server

	backend memoryBackend
}

// serveStore returns a store in memory over a fake clock at origin. An
// httptest server serves it at servedPath through the handler that wrap returns
// for the Server of the store, or through the Server for a nil wrap. The
// httptest server closes when the check of h ends.
func serveStore(h assert.TB, wrap func(srv *memory.Server) http.Handler) servedStore {
	h.Helper()

	ts := httptest.NewUnstartedServer(nil)
	assert.Cleanup(h, ts.Close)

	clk := fake.New(origin)
	base := &url.URL{Scheme: "http", Host: ts.Listener.Addr().String(), Path: servedPath}
	srv, err := memory.NewServer(memory.New(clk), base)
	assert.NoError(h, err, "NewServer takes the URL of an httptest server")

	ts.Config.Handler = srv
	if wrap != nil {
		ts.Config.Handler = wrap(srv)
	}
	ts.Start()

	client := ts.Client()
	client.Timeout = requestTimeout

	return servedStore{Server: srv, backend: memoryBackend{client: client, clock: clk}}
}

// servedGetSigner is a served store whose SignGet is the SignGet of a double.
// Each planted defect of a check of GetSigner is a servedGetSigner.
type servedGetSigner struct {
	blob.Store
	*GetSignerStub

	backend memoryBackend
}

// signGetOver is the SignGet of a double over the served store s.
type signGetOver func(s servedStore, ctx context.Context, key string, expiry time.Time) (blob.SignedRequest, error)

// serveGetSigner returns a servedGetSigner over the store that serveStore
// returns for wrap. Its SignGet is signGet over that store, or the SignGet of
// the Server of the store for a nil signGet.
func serveGetSigner(
	h *GetSignerHandle, wrap func(srv *memory.Server) http.Handler, signGet signGetOver,
) servedGetSigner {
	h.Helper()

	s := serveStore(h, wrap)
	opts := []GetSignerStubOption{GetSignerStubDelegateTo(s.Server)}
	if signGet != nil {
		opts = append(opts, WithGetSignerSignGet(func(ctx context.Context, key string, expiry time.Time) (
			blob.SignedRequest, error,
		) {
			return signGet(s, ctx, key, expiry)
		}))
	}

	return servedGetSigner{Store: s.Server, GetSignerStub: NewGetSignerStub(h, opts...), backend: s.backend}
}

// servedCreateSigner is a served store whose SignCreate is the SignCreate of a
// double. Each planted defect of a check of CreateSigner is a
// servedCreateSigner.
type servedCreateSigner struct {
	blob.Store
	*CreateSignerStub

	backend memoryBackend
}

// signCreateOver is the SignCreate of a double over the served store s.
type signCreateOver func(s servedStore, ctx context.Context, key, contentType string, size int64,
	expiry time.Time) (blob.SignedRequest, error)

// serveCreateSigner returns a servedCreateSigner over the store that serveStore
// returns for wrap. Its SignCreate is signCreate over that store, or the
// SignCreate of the Server of the store for a nil signCreate.
func serveCreateSigner(
	h *CreateSignerHandle, wrap func(srv *memory.Server) http.Handler, signCreate signCreateOver,
) servedCreateSigner {
	h.Helper()

	s := serveStore(h, wrap)
	opts := []CreateSignerStubOption{CreateSignerStubDelegateTo(s.Server)}
	if signCreate != nil {
		opts = append(opts, WithCreateSignerSignCreate(func(ctx context.Context, key, contentType string, size int64,
			expiry time.Time,
		) (blob.SignedRequest, error) {
			return signCreate(s, ctx, key, contentType, size, expiry)
		}))
	}

	return servedCreateSigner{Store: s.Server, CreateSignerStub: NewCreateSignerStub(h, opts...), backend: s.backend}
}

// response is the status, the content type and the body of a response of a
// backend.
type response struct {
	contentType string
	body        string
	status      int
}

// send sends method to target through the backend b with each header of header
// and with body, and returns the response. It stops the check when the backend
// does not respond.
func send(h assert.TB, b Backend, method, target string, header map[string][]string, body string) response {
	h.Helper()

	r, err := http.NewRequestWithContext(assert.Context(h), method, target, strings.NewReader(body))
	assert.NoError(h, err, "the signed request is an HTTP request")
	maps.Copy(r.Header, header)

	resp, err := b.Do(r)
	assert.NoError(h, err, "the backend responds to the request")
	data, err := io.ReadAll(resp.Body)
	assert.NoError(h, errors.Join(err, resp.Body.Close()), "the backend sends the body of its response")

	return response{contentType: resp.Header.Get(headerContentType), body: string(data), status: resp.StatusCode}
}

// GetSignerServesTheObject checks that a GET that SignGet signs returns the body
// and the content type that Put stored. The request has no headers, and its
// Expiry is at or before the expiry of the call.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerSignsAnotherKey values=[getSignerBackend]
func GetSignerServesTheObject(h *GetSignerHandle) {
	s, gs := storeOf(h)
	b := conform.Open[Backend](h, gs)
	putBody(h, s, keyServed, signedBody, blob.PutOptions{ContentType: textPlain})

	expiry := b.Time().Add(signedLifetime)
	req, err := gs.SignGet(h.Context(), keyServed, expiry)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, req.Method, http.MethodGet, h.Statement())
	assert.Empty(h, req.Header, h.Statement())
	assert.False(h, req.Expiry.After(expiry), h.Statement())

	resp := send(h, b, req.Method, req.URL, req.Header, "")
	assert.Equal(h, resp.status, http.StatusOK, h.Statement())
	assert.Equal(h, resp.body, signedBody, h.Statement())
	assert.Equal(h, resp.contentType, textPlain, h.Statement())
}

// GetSignerServesARange checks that a GET that SignGet signs returns the bytes
// of the range of its Range header.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerIgnoresTheRange values=[getSignerBackend]
func GetSignerServesARange(h *GetSignerHandle) {
	s, gs := storeOf(h)
	b := conform.Open[Backend](h, gs)
	putBody(h, s, keyRanged, rangeBody, blob.PutOptions{})

	req := signGetFor(h, gs, b, keyRanged)
	header := http.Header{headerRange: {rangeHeader}}
	maps.Copy(header, req.Header)
	resp := send(h, b, req.Method, req.URL, header, "")
	assert.Equal(h, resp.body, rangeBytes, h.Statement())
}

// GetSignerRefusesAChangedRequest checks that the backend refuses a GET that
// SignGet signed under another key, without its query and as a PUT. The PUT
// leaves its key absent.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerServesWithoutASignature values=[getSignerBackend]
func GetSignerRefusesAChangedRequest(h *GetSignerHandle) {
	s, gs := storeOf(h)
	b := conform.Open[Backend](h, gs)
	key, other, absent := prefixChanged+"object", prefixChanged+"other", prefixChanged+"absent"
	putBody(h, s, key, signedBody, blob.PutOptions{})
	putBody(h, s, other, signedBody, blob.PutOptions{})

	req := signGetFor(h, gs, b, key)
	moved := rewrite(h, req.URL, func(u *url.URL) {
		u.Path = strings.TrimSuffix(u.Path, key) + other
		u.RawPath = ""
	})
	resp := send(h, b, req.Method, moved, req.Header, "")
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	bare := rewrite(h, req.URL, func(u *url.URL) { u.RawQuery = "" })
	resp = send(h, b, req.Method, bare, req.Header, "")
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())

	req = signGetFor(h, gs, b, absent)
	resp = send(h, b, http.MethodPut, req.URL, req.Header, "")
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, absent, h.Statement()), object{}, h.Statement())
}

// GetSignerRefusesAnExpiredRequest checks that the backend refuses a GET that
// SignGet signed for the shortest lifetime once its Expiry has passed. The
// lifetime is one second plus the SignDelay of the Backend, and the Expiry of
// the request is at or before the expiry of the call.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerReportsAnEarlierExpiry values=[getSignerBackend]
func GetSignerRefusesAnExpiredRequest(h *GetSignerHandle) {
	s, gs := storeOf(h)
	b := conform.Open[Backend](h, gs)
	putBody(h, s, keyExpired, signedBody, blob.PutOptions{})

	expiry := b.Time().Add(shortestLifetime + b.SignDelay())
	req, err := gs.SignGet(h.Context(), keyExpired, expiry)
	assert.NoError(h, err, "SignGet signs the shortest lifetime")
	assert.False(h, req.Expiry.After(expiry), h.Statement())
	assert.NoError(h, b.Elapse(h.Context(), max(req.Expiry.Sub(b.Time()), 0)), "the time of the backend elapses")
	resp := send(h, b, req.Method, req.URL, req.Header, "")
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
}

// GetSignerRefusesAnInvalidCall checks that SignGet classifies each key that
// blob.ValidKey refuses as Invalid, and each expiry less than one second or
// more than blob.MaxSignedLifetime after the time of the store.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerClampsTheLifetime values=[getSignerBackend]
func GetSignerRefusesAnInvalidCall(h *GetSignerHandle) {
	gs := h.Subject()
	b := conform.Open[Backend](h, gs)

	for _, key := range invalidKeys() {
		_, err := gs.SignGet(h.Context(), key, b.Time().Add(signedLifetime))
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
	for _, expiry := range invalidExpiries(b.Time()) {
		_, err := gs.SignGet(h.Context(), keyInvalid, expiry)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
}

// GetSignerReturnsTheErrorOfADoneContext checks that SignGet returns the error
// of a context that ended.
//
//dokimi:check blob.GetSigner.SignGet defect=getSignerIgnoresItsContext values=[getSignerBackend]
func GetSignerReturnsTheErrorOfADoneContext(h *GetSignerHandle) {
	gs := h.Subject()
	b := conform.Open[Backend](h, gs)

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := gs.SignGet(ctx, keyCancelled, b.Time().Add(signedLifetime))

		return err
	}, h.Statement())
}

// CreateSignerCreatesTheObject checks that a PUT that SignCreate signs creates
// the object with the body, the size and the content type of the call.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerSignsAnotherContentType values=[createSignerBackend]
func CreateSignerCreatesTheObject(h *CreateSignerHandle) {
	s, cs := storeOf(h)
	b := conform.Open[Backend](h, cs)

	req := signCreateFor(h, cs, b, keyCreated)
	assert.Equal(h, req.Method, http.MethodPut, h.Statement())
	resp := send(h, b, req.Method, req.URL, req.Header, signedBody)
	assert.InRange(h, resp.status, firstServed, lastServed, h.Statement())

	body, info := readBody(h, s, keyCreated, h.Statement())
	assert.Equal(h, body, signedBody, h.Statement())
	assert.Equal(h, info.Size, int64(len(signedBody)), h.Statement())
	assert.Equal(h, info.ContentType, textPlain, h.Statement())
}

// CreateSignerCreatesOnce checks that the backend refuses a PUT that SignCreate
// signed a second time, and on a key that Put wrote. Each refusal leaves the
// object as it was.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerReplacesTheObject values=[createSignerBackend]
func CreateSignerCreatesOnce(h *CreateSignerHandle) {
	s, cs := storeOf(h)
	b := conform.Open[Backend](h, cs)
	created, taken := prefixOnce+"created", prefixOnce+"taken"

	req := signCreateFor(h, cs, b, created)
	resp := send(h, b, req.Method, req.URL, req.Header, signedBody)
	assert.InRange(h, resp.status, firstServed, lastServed, "the backend creates the object")
	assert.Pure(h, func() object { return observe(h, s, created, h.Statement()) }, func() {
		resp = send(h, b, req.Method, req.URL, req.Header, signedBody)
	}, h.Statement())
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())

	prior := putBody(h, s, taken, committedBody, blob.PutOptions{})
	req = signCreateFor(h, cs, b, taken)
	resp = send(h, b, req.Method, req.URL, req.Header, signedBody)
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, taken, h.Statement()),
		object{version: prior.Version, body: committedBody, present: true}, h.Statement())
}

// CreateSignerRefusesAnotherBody checks that the backend refuses a PUT that
// SignCreate signed with a body of another size or another content type. Each
// refusal leaves the key absent.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerTakesAnyContentType values=[createSignerBackend]
func CreateSignerRefusesAnotherBody(h *CreateSignerHandle) {
	s, cs := storeOf(h)
	b := conform.Open[Backend](h, cs)
	req := signCreateFor(h, cs, b, keyBody)

	for _, body := range []string{signedBody + "!", signedBody[1:]} {
		resp := send(h, b, req.Method, req.URL, req.Header, body)
		assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
		assert.Equal(h, observe(h, s, keyBody, h.Statement()), object{}, h.Statement())
	}

	header := http.Header{}
	maps.Copy(header, req.Header)
	maps.DeleteFunc(header, func(name string, _ []string) bool { return strings.EqualFold(name, headerContentType) })
	header.Set(headerContentType, octetStream)
	resp := send(h, b, req.Method, req.URL, header, signedBody)
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, keyBody, h.Statement()), object{}, h.Statement())
}

// CreateSignerRefusesAChangedRequest checks that the backend refuses a PUT that
// SignCreate signed under another key, without its query and as a GET. A
// refused PUT leaves its key absent.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerCreatesWithoutASignature values=[createSignerBackend]
func CreateSignerRefusesAChangedRequest(h *CreateSignerHandle) {
	s, cs := storeOf(h)
	b := conform.Open[Backend](h, cs)
	key, other, taken := prefixChanged+"object", prefixChanged+"other", prefixChanged+"taken"

	req := signCreateFor(h, cs, b, key)
	moved := rewrite(h, req.URL, func(u *url.URL) {
		u.Path = strings.TrimSuffix(u.Path, key) + other
		u.RawPath = ""
	})
	resp := send(h, b, req.Method, moved, req.Header, signedBody)
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, other, h.Statement()), object{}, h.Statement())
	bare := rewrite(h, req.URL, func(u *url.URL) { u.RawQuery = "" })
	resp = send(h, b, req.Method, bare, req.Header, signedBody)
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, key, h.Statement()), object{}, h.Statement())

	putBody(h, s, taken, signedBody, blob.PutOptions{})
	req = signCreateFor(h, cs, b, taken)
	resp = send(h, b, http.MethodGet, req.URL, nil, "")
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
}

// CreateSignerRefusesAnExpiredRequest checks that the backend refuses a PUT that
// SignCreate signed for the shortest lifetime once its Expiry has passed. The
// lifetime is one second plus the SignDelay of the Backend, the Expiry of the
// request is at or before the expiry of the call, and the refusal leaves the
// key absent.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerReportsAnEarlierExpiry values=[createSignerBackend]
func CreateSignerRefusesAnExpiredRequest(h *CreateSignerHandle) {
	s, cs := storeOf(h)
	b := conform.Open[Backend](h, cs)

	expiry := b.Time().Add(shortestLifetime + b.SignDelay())
	req, err := cs.SignCreate(h.Context(), keyExpired, textPlain, int64(len(signedBody)), expiry)
	assert.NoError(h, err, "SignCreate signs the shortest lifetime")
	assert.False(h, req.Expiry.After(expiry), h.Statement())
	assert.NoError(h, b.Elapse(h.Context(), max(req.Expiry.Sub(b.Time()), 0)), "the time of the backend elapses")
	resp := send(h, b, req.Method, req.URL, req.Header, signedBody)
	assert.InRange(h, resp.status, firstRefused, lastRefused, h.Statement())
	assert.Equal(h, observe(h, s, keyExpired, h.Statement()), object{}, h.Statement())
}

// CreateSignerRefusesAnInvalidCall checks that SignCreate classifies as Invalid
// each call that SignGet classifies as Invalid, an empty content type, a
// content type that blob.ValidContentType refuses, and a size below 1 or above
// blob.MaxCreateSize.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerDefaultsTheContentType values=[createSignerBackend]
func CreateSignerRefusesAnInvalidCall(h *CreateSignerHandle) {
	cs := h.Subject()
	b := conform.Open[Backend](h, cs)
	size, expiry := int64(len(signedBody)), b.Time().Add(signedLifetime)

	for _, key := range invalidKeys() {
		_, err := cs.SignCreate(h.Context(), key, textPlain, size, expiry)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
	for _, invalid := range invalidExpiries(b.Time()) {
		_, err := cs.SignCreate(h.Context(), keyInvalid, textPlain, size, invalid)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}

	_, err := cs.SignCreate(h.Context(), keyInvalid, "", size, expiry)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	for _, contentType := range invalidContentTypes() {
		_, err = cs.SignCreate(h.Context(), keyInvalid, contentType, size, expiry)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
	for _, n := range []int64{-1, 0, blob.MaxCreateSize + 1} {
		_, err = cs.SignCreate(h.Context(), keyInvalid, textPlain, n, expiry)
		assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	}
}

// CreateSignerReturnsTheErrorOfADoneContext checks that SignCreate returns the
// error of a context that ended.
//
//dokimi:check blob.CreateSigner.SignCreate defect=createSignerIgnoresItsContext values=[createSignerBackend]
func CreateSignerReturnsTheErrorOfADoneContext(h *CreateSignerHandle) {
	cs := h.Subject()
	b := conform.Open[Backend](h, cs)

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := cs.SignCreate(ctx, keyCancelled, textPlain, int64(len(signedBody)), b.Time().Add(signedLifetime))

		return err
	}, h.Statement())
}

// invalidExpiries returns expiries that a signer refuses at the time now of its
// store, each named by the rule that it breaks.
func invalidExpiries(now time.Time) map[string]time.Time {
	return map[string]time.Time{
		"the zero time":                   {},
		"less than the shortest lifetime": now.Add(shortestLifetime - time.Millisecond),
		"more than MaxSignedLifetime":     now.Add(blob.MaxSignedLifetime + time.Second),
	}
}

// signGetFor returns the GET of the object under key that gs signs for
// signedLifetime after the time of the backend b, and stops the check when
// SignGet fails.
func signGetFor(h assert.TB, gs blob.GetSigner, b Backend, key string) blob.SignedRequest {
	h.Helper()

	req, err := gs.SignGet(assert.Context(h), key, b.Time().Add(signedLifetime))
	assert.NoError(h, err, "SignGet signs the GET")

	return req
}

// signCreateFor returns the PUT that cs signs to create the object under key
// with the size of signedBody and the content type textPlain, for
// signedLifetime after the time of the backend b. It stops the check when
// SignCreate fails.
func signCreateFor(h assert.TB, cs blob.CreateSigner, b Backend, key string) blob.SignedRequest {
	h.Helper()

	req, err := cs.SignCreate(assert.Context(h), key, textPlain, int64(len(signedBody)), b.Time().Add(signedLifetime))
	assert.NoError(h, err, "SignCreate signs the PUT")

	return req
}

// rewrite returns rawURL with the change of change, and stops the check when
// rawURL does not parse.
func rewrite(h assert.TB, rawURL string, change func(u *url.URL)) string {
	h.Helper()

	u, err := url.Parse(rawURL)
	assert.NoError(h, err, "the URL of the signed request parses")
	change(u)

	return u.String()
}

// getSignerSignsAnotherKey returns the planted defect of
// GetSignerServesTheObject: a store whose SignGet of keyServed signs a GET of
// keyMoved.
func getSignerSignsAnotherKey(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, nil, func(s servedStore, ctx context.Context, key string, expiry time.Time) (
		blob.SignedRequest, error,
	) {
		if key == keyServed {
			key = keyMoved
		}

		return s.SignGet(ctx, key, expiry)
	})
}

// getSignerIgnoresTheRange returns the planted defect of GetSignerServesARange:
// a store whose backend serves the whole object under keyRanged for a request
// with a Range header.
func getSignerIgnoresTheRange(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, func(srv *memory.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == servedRoot+keyRanged {
				r = r.Clone(r.Context())
				r.Header.Del(headerRange)
			}
			srv.ServeHTTP(w, r)
		})
	}, nil)
}

// getSignerServesWithoutASignature returns the planted defect of
// GetSignerRefusesAChangedRequest: a store whose backend serves a GET without a
// query of a key under prefixChanged, as a bucket that anyone may read does.
func getSignerServesWithoutASignature(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, func(srv *memory.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimPrefix(r.URL.Path, servedRoot)
			if r.Method != http.MethodGet || r.URL.RawQuery != "" || !strings.HasPrefix(key, prefixChanged) {
				srv.ServeHTTP(w, r)

				return
			}

			data, info, err := blob.GetBytes(r.Context(), srv, key, int64(len(signedBody)))
			if err != nil {
				http.NotFound(w, r)

				return
			}
			http.ServeContent(w, r, "", info.ModTime, bytes.NewReader(data))
		})
	}, nil)
}

// getSignerReportsAnEarlierExpiry returns the planted defect of
// GetSignerRefusesAnExpiredRequest: a store whose SignGet of keyExpired signs
// signedLifetime more than the call, and reports the Expiry of the lifetime of
// the call.
func getSignerReportsAnEarlierExpiry(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, nil, func(s servedStore, ctx context.Context, key string, expiry time.Time) (
		blob.SignedRequest, error,
	) {
		if key != keyExpired {
			return s.SignGet(ctx, key, expiry)
		}

		req, err := s.SignGet(ctx, key, expiry.Add(signedLifetime))
		if err == nil {
			req.Expiry = expiry.Truncate(time.Second)
		}

		return req, err
	})
}

// getSignerClampsTheLifetime returns the planted defect of
// GetSignerRefusesAnInvalidCall: a store whose SignGet of keyInvalid signs
// blob.MaxSignedLifetime for an expiry past it.
func getSignerClampsTheLifetime(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, nil, func(s servedStore, ctx context.Context, key string, expiry time.Time) (
		blob.SignedRequest, error,
	) {
		if longest := s.backend.Time().Add(blob.MaxSignedLifetime); key == keyInvalid && expiry.After(longest) {
			expiry = longest
		}

		return s.SignGet(ctx, key, expiry)
	})
}

// getSignerIgnoresItsContext returns the planted defect of
// GetSignerReturnsTheErrorOfADoneContext: a store whose SignGet of keyCancelled
// drops the cancellation of its context.
func getSignerIgnoresItsContext(h *GetSignerHandle) blob.GetSigner {
	return serveGetSigner(h, nil, func(s servedStore, ctx context.Context, key string, expiry time.Time) (
		blob.SignedRequest, error,
	) {
		if key == keyCancelled {
			ctx = context.WithoutCancel(ctx)
		}

		return s.SignGet(ctx, key, expiry)
	})
}

// createSignerSignsAnotherContentType returns the planted defect of
// CreateSignerCreatesTheObject: a store whose SignCreate of keyCreated signs the
// content type octetStream.
func createSignerSignsAnotherContentType(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, nil, func(s servedStore, ctx context.Context, key, contentType string, size int64,
		expiry time.Time,
	) (blob.SignedRequest, error) {
		if key == keyCreated {
			contentType = octetStream
		}

		return s.SignCreate(ctx, key, contentType, size, expiry)
	})
}

// createSignerReplacesTheObject returns the planted defect of
// CreateSignerCreatesOnce: a store whose backend deletes the object under a key
// under prefixOnce before it serves a PUT of the key, as a PUT without a
// precondition replaces an object.
func createSignerReplacesTheObject(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, func(srv *memory.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimPrefix(r.URL.Path, servedRoot)
			if r.Method == http.MethodPut && strings.HasPrefix(key, prefixOnce) {
				if err := srv.Delete(r.Context(), key, version.Unspecified); err != nil {
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

					return
				}
			}
			srv.ServeHTTP(w, r)
		})
	}, nil)
}

// createSignerTakesAnyContentType returns the planted defect of
// CreateSignerRefusesAnotherBody: a store whose backend serves a PUT of keyBody
// under the signed content type textPlain, whatever content type the PUT sends.
func createSignerTakesAnyContentType(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, func(srv *memory.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && r.URL.Path == servedRoot+keyBody {
				r = r.Clone(r.Context())
				r.Header.Set(headerContentType, textPlain)
			}
			srv.ServeHTTP(w, r)
		})
	}, nil)
}

// createSignerCreatesWithoutASignature returns the planted defect of
// CreateSignerRefusesAChangedRequest: a store whose backend creates the object
// of a PUT without a query of a key under prefixChanged.
func createSignerCreatesWithoutASignature(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, func(srv *memory.Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimPrefix(r.URL.Path, servedRoot)
			if r.Method != http.MethodPut || r.URL.RawQuery != "" || !strings.HasPrefix(key, prefixChanged) {
				srv.ServeHTTP(w, r)

				return
			}

			createOnly := blob.PutOptions{Write: version.WriteOptions{IfNoneMatch: version.Wildcard}}
			if _, err := srv.Put(r.Context(), key, r.Body, createOnly); err != nil {
				http.Error(w, http.StatusText(http.StatusPreconditionFailed), http.StatusPreconditionFailed)

				return
			}
			w.WriteHeader(http.StatusCreated)
		})
	}, nil)
}

// createSignerReportsAnEarlierExpiry returns the planted defect of
// CreateSignerRefusesAnExpiredRequest: a store whose SignCreate of keyExpired
// signs signedLifetime more than the call, and reports the Expiry of the
// lifetime of the call.
func createSignerReportsAnEarlierExpiry(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, nil, func(s servedStore, ctx context.Context, key, contentType string, size int64,
		expiry time.Time,
	) (blob.SignedRequest, error) {
		if key != keyExpired {
			return s.SignCreate(ctx, key, contentType, size, expiry)
		}

		req, err := s.SignCreate(ctx, key, contentType, size, expiry.Add(signedLifetime))
		if err == nil {
			req.Expiry = expiry.Truncate(time.Second)
		}

		return req, err
	})
}

// createSignerDefaultsTheContentType returns the planted defect of
// CreateSignerRefusesAnInvalidCall: a store whose SignCreate of keyInvalid signs
// the content type octetStream for an empty content type.
func createSignerDefaultsTheContentType(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, nil, func(s servedStore, ctx context.Context, key, contentType string, size int64,
		expiry time.Time,
	) (blob.SignedRequest, error) {
		if key == keyInvalid && contentType == "" {
			contentType = octetStream
		}

		return s.SignCreate(ctx, key, contentType, size, expiry)
	})
}

// createSignerIgnoresItsContext returns the planted defect of
// CreateSignerReturnsTheErrorOfADoneContext: a store whose SignCreate of
// keyCancelled drops the cancellation of its context.
func createSignerIgnoresItsContext(h *CreateSignerHandle) blob.CreateSigner {
	return serveCreateSigner(h, nil, func(s servedStore, ctx context.Context, key, contentType string, size int64,
		expiry time.Time,
	) (blob.SignedRequest, error) {
		if key == keyCancelled {
			ctx = context.WithoutCancel(ctx)
		}

		return s.SignCreate(ctx, key, contentType, size, expiry)
	})
}
