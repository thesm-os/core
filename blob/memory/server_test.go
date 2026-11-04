// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
	"go.thesmos.sh/core/errs"
)

// The base URL of the Servers of the tests, and the prefix of the URLs that
// they sign.
const (
	serverHost   = "blobs.example"
	serverPath   = "/store"
	serverPrefix = "https://" + serverHost + serverPath + "/"
)

// The objects and the requests of the tests.
const (
	serverKey   = "photos/2026/a.jpg"
	createdKey  = "photos/2026/new.jpg"
	serverBody  = "the body of the object"
	textPlain   = "text/plain"
	imagePNG    = "image/png"
	octetStream = "application/octet-stream"
	lifetime    = time.Hour

	// clientTimeout is the timeout of a request of a conformance test to the
	// httptest server of a served Server.
	clientTimeout = 5 * time.Second
)

// Allocation counts of the signing methods of a Server.
const (
	// signGetAllocs is the number of allocations of a SignGet of a key
	// without characters to escape: the URL. A pool keeps the buffers of the
	// frames.
	signGetAllocs = 1

	// signCreateAllocs is the number of allocations of a SignCreate: the URL,
	// and the map of the Header, its group of entries and the list of its
	// values.
	signCreateAllocs = 4

	// refusedAllocs is the number of allocations of a ServeHTTP of a request
	// whose signature the Server refuses: four for the parsed query, one for
	// the decoded signature, and eight for the headers and the body that
	// http.Error writes into a recorder. A pool keeps the buffers of the
	// frames.
	refusedAllocs = 13
)

var (
	// errCut is the error of a body that ends before its Content-Length, as
	// net/http returns it for a connection that closes early.
	errCut = io.ErrUnexpectedEOF

	// serverBase is the base URL of the Servers of the tests. Its path has no
	// slash at its end. A test passes a copy to NewServer.
	serverBase = url.URL{Scheme: "https", Host: serverHost, Path: serverPath}

	// expiry is the expiry of the requests that the tests sign: lifetime
	// after the epoch, the time of the fake clock of each Server.
	expiry = time.Unix(0, 0).Add(lifetime)

	// serverKeys generates keys with characters that a URL escapes: one to
	// five elements, none of them "." or "..".
	serverKeys = prop.StringMatching(
		`[a-zA-Zäé0-9%?#&=+;@!$()*,:~ _.-]{0,8}[a-zA-Zäé0-9%?#&=+;@!$()*,:~_-]` +
			`(/[a-zA-Zäé0-9%?#&=+;@!$()*,:~_-][a-zA-Zäé0-9%?#&=+;@!$()*,:~ _.-]{0,8}){0,4}`)
)

// cutReader returns its data and then errCut, as the body of a request
// whose connection closes before its Content-Length.
type cutReader struct {
	data []byte
}

// Read copies the data that remains into p, and returns errCut once no data
// remains.
func (r *cutReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errCut
	}
	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// served is a Server that an httptest server serves, with the Backend of the
// conformance tests of its signers.
type served struct {
	*memory.Server

	backend blobtest.Backend
}

// newServed returns a Server over an empty store with a fake clock at the Unix
// epoch. An httptest server serves it at serverPath until the test of tb ends.
func newServed(tb assert.TB) *served {
	tb.Helper()

	ts := httptest.NewUnstartedServer(nil)
	assert.Cleanup(tb, ts.Close)

	clk := fake.New(time.Unix(0, 0).UTC())
	base := url.URL{Scheme: "http", Host: ts.Listener.Addr().String(), Path: serverPath}
	srv, err := memory.NewServer(memory.New(clk), &base)
	assert.NoError(tb, err, "NewServer must take the URL of an httptest server")

	ts.Config.Handler = srv
	ts.Start()

	client := ts.Client()
	client.Timeout = clientTimeout

	return &served{Server: srv, backend: clockBackend{client: client, clock: clk}}
}

// newGetSigner returns a served Server, the subject of the conformance test of
// blob.GetSigner.
func newGetSigner(h *blobtest.GetSignerHandle) *served {
	return newServed(h)
}

// newCreateSigner returns a served Server, the subject of the conformance test
// of blob.CreateSigner.
func newCreateSigner(h *blobtest.CreateSignerHandle) *served {
	return newServed(h)
}

// clockBackend is the Backend of a served Server.
type clockBackend struct {
	client *http.Client
	clock  *fake.Clock
}

// Do sends req with the client of the httptest server.
func (b clockBackend) Do(req *http.Request) (*http.Response, error) {
	return b.client.Do(req)
}

// Time returns the time of the fake clock of the store.
func (b clockBackend) Time() time.Time {
	return b.clock.Time()
}

// SignDelay returns 0, because the fake clock of the store does not move
// between a read of the check and a read of the store.
func (clockBackend) SignDelay() time.Duration {
	return 0
}

// Elapse advances the fake clock of the store by d. It returns the error of
// ctx when ctx has ended before the call.
func (b clockBackend) Elapse(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.clock.Advance(d)

	return nil
}

func TestServer(t *testing.T) {
	t.Parallel()

	t.Run("NewServer", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveStore *memory.Store
			giveBase  *url.URL
		}{
			{name: "classifies a nil store as Invalid", giveBase: &url.URL{Scheme: "https", Host: serverHost}},
			{name: "classifies a nil base URL as Invalid", giveStore: newStore()},
			{
				name:      "classifies a base URL without a scheme as Invalid",
				giveStore: newStore(),
				giveBase:  &url.URL{Host: serverHost, Path: serverPath},
			},
			{
				name:      "classifies a base URL without a host as Invalid",
				giveStore: newStore(),
				giveBase:  &url.URL{Scheme: "https", Path: serverPath},
			},
			{
				name:      "classifies a base URL with a query as Invalid",
				giveStore: newStore(),
				giveBase:  &url.URL{Scheme: "https", Host: serverHost, Path: serverPath, RawQuery: "a=b"},
			},
			{
				name:      "classifies a base URL with an empty query as Invalid",
				giveStore: newStore(),
				giveBase:  &url.URL{Scheme: "https", Host: serverHost, Path: serverPath, ForceQuery: true},
			},
			{
				name:      "classifies a base URL with a fragment as Invalid",
				giveStore: newStore(),
				giveBase:  &url.URL{Scheme: "https", Host: serverHost, Path: serverPath, Fragment: "f"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv, err := memory.NewServer(tt.giveStore, tt.giveBase)
				assert.Nil(t, srv, "a refused base URL must return no Server")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
			})
		}

		t.Run("signs URLs under a base URL whose path ends with a slash", func(t *testing.T) {
			t.Parallel()
			base := serverBase
			base.Path += "/"
			srv, err := memory.NewServer(newStore(), &base)
			assert.NoError(t, err, "a base URL with a slash at the end of its path must be valid")
			req, err := srv.SignGet(t.Context(), serverKey, expiry)
			assert.NoError(t, err, "SignGet must succeed")
			assert.HasPrefix(t, req.URL, serverPrefix+serverKey+"?", "the path must have one slash before the key")
		})

		t.Run("leaves its base URL as it was", func(t *testing.T) {
			t.Parallel()
			base := serverBase
			_, err := memory.NewServer(newStore(), &base)
			assert.NoError(t, err, "the base URL must be valid")
			assert.Equal(t, base, serverBase, "NewServer must not change the base URL")
		})
	})

	t.Run("SignGet", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a GET of the escaped key under the base URL", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req, err := srv.SignGet(t.Context(), "a b/c%d", expiry)
			assert.NoError(t, err, "SignGet must succeed")
			expect.Equal(t, req.Method, http.MethodGet, "the request must be a GET")
			expect.HasPrefix(t, req.URL, serverPrefix+"a%20b/c%25d?", "the URL must have the escaped key")
			expect.Empty(t, req.Header, "a GET must have no headers")
			expect.Equal(t, req.Expiry, expiry.UTC(), "the request must expire at the expiry")
		})

		t.Run("rounds the expiry down to the second", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req, err := srv.SignGet(t.Context(), serverKey, time.Unix(90, int64(999*time.Millisecond)))
			assert.NoError(t, err, "SignGet must succeed")
			assert.Equal(t, req.Expiry, time.Unix(90, 0).UTC(), "the expiry must lose its fraction of a second")
		})

		t.Run("signs a lifetime of one second", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req, err := srv.SignGet(t.Context(), serverKey, time.Unix(1, 0))
			assert.NoError(t, err, "the shortest lifetime must be signed")
			assert.Equal(t, req.Expiry, time.Unix(1, 0).UTC(), "the request must expire after one second")
		})

		t.Run("signs a lifetime of MaxSignedLifetime", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			longest := time.Unix(0, 0).Add(blob.MaxSignedLifetime)
			req, err := srv.SignGet(t.Context(), serverKey, longest)
			assert.NoError(t, err, "the longest lifetime must be signed")
			assert.Equal(t, req.Expiry, longest.UTC(), "the request must expire after the longest lifetime")
		})

		tests := []struct {
			name       string
			giveKey    string
			giveExpiry time.Time
		}{
			{name: "classifies an invalid key as Invalid", giveKey: "a//b", giveExpiry: expiry},
			{
				name:       "classifies a lifetime below one second as Invalid",
				giveKey:    serverKey,
				giveExpiry: time.Unix(0, 999_999_999),
			},
			{
				name:       "classifies an expiry at the time of the store as Invalid",
				giveKey:    serverKey,
				giveExpiry: time.Unix(0, 0),
			},
			{name: "classifies the zero expiry as Invalid", giveKey: serverKey},
			{
				name:       "classifies a lifetime above MaxSignedLifetime as Invalid",
				giveKey:    serverKey,
				giveExpiry: time.Unix(0, 1).Add(blob.MaxSignedLifetime),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv, _ := newServer(t)
				req, err := srv.SignGet(t.Context(), tt.giveKey, tt.giveExpiry)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
				assert.Equal(t, req, blob.SignedRequest{}, "a refused call must return no request")
			})
		}

		t.Run("returns the error of a done context", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := srv.SignGet(ctx, serverKey, expiry)

				return err
			}, "SignGet must return the error of a done context")
		})
	})

	t.Run("SignCreate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a PUT with the content type in its Header", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req, err := srv.SignCreate(t.Context(), serverKey, imagePNG, 10, expiry)
			assert.NoError(t, err, "SignCreate must succeed")
			expect.Equal(t, req.Method, http.MethodPut, "the request must be a PUT")
			expect.HasPrefix(t, req.URL, serverPrefix+serverKey+"?", "the URL must have the key")
			expect.Equal(t, req.Header, map[string][]string{"Content-Type": {imagePNG}},
				"the Header must have the content type alone")
			expect.Equal(t, req.Expiry, expiry.UTC(), "the request must expire at the expiry")
		})

		t.Run("signs a size of 1", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			_, err := srv.SignCreate(t.Context(), serverKey, imagePNG, 1, expiry)
			assert.NoError(t, err, "the smallest size must be signed")
		})

		t.Run("signs a size of MaxCreateSize", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			_, err := srv.SignCreate(t.Context(), serverKey, imagePNG, blob.MaxCreateSize, expiry)
			assert.NoError(t, err, "the largest size must be signed")
		})

		tests := []struct {
			name            string
			giveKey         string
			giveContentType string
			giveSize        int64
			giveExpiry      time.Time
		}{
			{
				name:            "classifies an invalid key as Invalid",
				giveKey:         "/k",
				giveContentType: imagePNG,
				giveSize:        10,
				giveExpiry:      expiry,
			},
			{
				name:       "classifies an empty content type as Invalid",
				giveKey:    serverKey,
				giveSize:   10,
				giveExpiry: expiry,
			},
			{
				name:            "classifies a content type that ValidContentType rejects as Invalid",
				giveKey:         serverKey,
				giveContentType: "text/plain\n",
				giveSize:        10,
				giveExpiry:      expiry,
			},
			{
				name:            "classifies a size of 0 as Invalid",
				giveKey:         serverKey,
				giveContentType: imagePNG,
				giveExpiry:      expiry,
			},
			{
				name:            "classifies a size above MaxCreateSize as Invalid",
				giveKey:         serverKey,
				giveContentType: imagePNG,
				giveSize:        blob.MaxCreateSize + 1,
				giveExpiry:      expiry,
			},
			{
				name:            "classifies a lifetime above MaxSignedLifetime as Invalid",
				giveKey:         serverKey,
				giveContentType: imagePNG,
				giveSize:        10,
				giveExpiry:      time.Unix(0, 1).Add(blob.MaxSignedLifetime),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv, _ := newServer(t)
				req, err := srv.SignCreate(t.Context(), tt.giveKey, tt.giveContentType, tt.giveSize, tt.giveExpiry)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
				assert.Equal(t, req, blob.SignedRequest{}, "a refused call must return no request")
			})
		}

		t.Run("returns the error of a done context", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := srv.SignCreate(ctx, serverKey, imagePNG, 10, expiry)

				return err
			}, "SignCreate must return the error of a done context")
		})
	})

	t.Run("ServeHTTP", func(t *testing.T) {
		t.Parallel()

		t.Run("serves the body and the content type of the object", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			info := put(t, srv, serverKey, serverBody, textPlain)
			rec := serve(srv, newRequest(t, signGet(t, srv, serverKey), nil))
			assert.Equal(t, rec.Code, http.StatusOK, "a signed GET must succeed")
			expect.Equal(t, rec.Body.String(), serverBody, "the response must have the body of the object")
			h := rec.Header()
			expect.Equal(t, h.Get("Content-Type"), textPlain, "the response must have the content type")
			expect.Equal(t, h.Get("ETag"), strconv.Quote(string(info.Version)), "the ETag must be the version")
			expect.Equal(t, h.Get("X-Content-Type-Options"), "nosniff", "a client must not sniff the body")
		})

		t.Run("serves a range of the object", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			put(t, srv, serverKey, serverBody, textPlain)
			r := newRequest(t, signGet(t, srv, serverKey), nil)
			r.Header.Set("Range", "bytes=4-7")
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusPartialContent, "a ranged GET must return part of the object")
			assert.Equal(t, rec.Body.String(), serverBody[4:8], "the response must have the bytes of the range")
		})

		t.Run("serves an object without a content type as application/octet-stream", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			put(t, srv, serverKey, "<html></html>", "")
			rec := serve(srv, newRequest(t, signGet(t, srv, serverKey), nil))
			assert.Equal(t, rec.Code, http.StatusOK, "a signed GET must succeed")
			assert.Equal(t, rec.Header().Get("Content-Type"), octetStream, "the Server must not infer a content type")
		})

		t.Run("serves the object under every valid key", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			prop.ForAll(t, "a signed GET must serve the object under its key", func(c *prop.Case) {
				key := c.Draw(serverKeys, "key")
				c.Assume(blob.ValidKey(key))
				put(c, srv, key, key, textPlain)
				rec := serve(srv, newRequest(c, signGet(c, srv, key), nil))
				assert.Equal(c, rec.Code, http.StatusOK, "a signed GET must succeed")
				assert.Equal(c, rec.Body.String(), key, "the response must have the body under the key")
			})
		})

		t.Run("responds 404 to a key without an object", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			rec := serve(srv, newRequest(t, signGet(t, srv, serverKey), nil))
			assert.Equal(t, rec.Code, http.StatusNotFound, "a GET of an absent key must fail")
		})

		t.Run("creates the object of a signed create", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req := signCreate(t, srv, serverKey, imagePNG, len(serverBody))
			rec := serve(srv, newRequest(t, req, strings.NewReader(serverBody)))
			assert.Equal(t, rec.Code, http.StatusCreated, "a signed create must succeed")
			info, err := srv.Stat(t.Context(), serverKey)
			assert.NoError(t, err, "the object must exist")
			expect.Equal(t, info.Size, int64(len(serverBody)), "the object must have the signed size")
			expect.Equal(t, info.ContentType, imagePNG, "the object must have the signed content type")
		})

		t.Run("responds 412 to a create on a key with an object", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			info := put(t, srv, serverKey, serverBody, textPlain)
			rec := serve(srv, newRequest(t, signCreate(t, srv, serverKey, imagePNG, 3), strings.NewReader("new")))
			assert.Equal(t, rec.Code, http.StatusPreconditionFailed, "a create on an object must fail")
			assertObject(t, srv, info, serverBody)
		})

		t.Run("responds 412 to the same create a second time", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req := signCreate(t, srv, serverKey, imagePNG, len(serverBody))
			first := serve(srv, newRequest(t, req, strings.NewReader(serverBody)))
			assert.Equal(t, first.Code, http.StatusCreated, "the first create must succeed")
			again := strings.NewReader(strings.Repeat("x", len(serverBody)))
			second := serve(srv, newRequest(t, req, again))
			assert.Equal(t, second.Code, http.StatusPreconditionFailed, "the second create must fail")
			assert.Equal(t, get(t, srv, serverKey), serverBody, "the object must have the body of the first create")
		})

		t.Run("responds 400 to a body that ends before its Content-Length", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req := signCreate(t, srv, serverKey, imagePNG, len(serverBody))
			r := newRequest(t, req, strings.NewReader(serverBody))
			r.Body = io.NopCloser(&cutReader{data: []byte(serverBody[:4])})
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusBadRequest, "a create of a cut body must fail")
			assertAbsent(t, srv, serverKey)
		})

		t.Run("responds 400 to a body that is longer than its Content-Length", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			req := signCreate(t, srv, serverKey, imagePNG, len(serverBody))
			r := newRequest(t, req, strings.NewReader(serverBody))
			r.Body = io.NopCloser(strings.NewReader(serverBody + "and more"))
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusBadRequest, "a create of a longer body must fail")
			assertAbsent(t, srv, serverKey)
		})

		tests := []struct {
			name string
			give func(tb assert.TB, srv *memory.Server) *http.Request
		}{
			{
				name: "responds 403 to a GET with the signature of a PUT",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					r := newRequest(tb, signCreate(tb, srv, createdKey, imagePNG, len(serverBody)), nil)
					r.Method = http.MethodGet

					return r
				},
			},
			{
				name: "responds 403 to an empty PUT with the signature of a GET",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signGet(tb, srv, createdKey)
					req.Method = http.MethodPut

					return newRequest(tb, req, nil)
				},
			},
			{
				name: "responds 403 to a request under another key",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signGet(tb, srv, serverKey)
					req.URL = strings.Replace(req.URL, "a.jpg", "b.jpg", 1)

					return newRequest(tb, req, nil)
				},
			},
			{
				name: "responds 403 to a request outside the path of the base URL",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signGet(tb, srv, serverKey)
					req.URL = strings.Replace(req.URL, serverPath+"/", "/other/", 1)

					return newRequest(tb, req, nil)
				},
			},
			{
				name: "responds 403 to a request without its query",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signGet(tb, srv, serverKey)
					req.URL, _, _ = strings.Cut(req.URL, "?")

					return newRequest(tb, req, nil)
				},
			},
			{
				name: "responds 403 to a request with another expiry",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					r := newRequest(tb, signGet(tb, srv, serverKey), nil)
					setQuery(r, "expires", strconv.FormatInt(int64(lifetime/time.Second)+1, 10))

					return r
				},
			},
			{
				name: "responds 403 to a request with another signature",
				give: refusedRequest,
			},
			{
				name: "responds 403 to a request with a signature that is no base64",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					r := newRequest(tb, signGet(tb, srv, serverKey), nil)
					setQuery(r, "signature", "!!!!")

					return r
				},
			},
			{
				name: "responds 403 to a create with another content type",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signCreate(tb, srv, createdKey, imagePNG, len(serverBody))
					r := newRequest(tb, req, strings.NewReader(serverBody))
					r.Header.Set("Content-Type", textPlain)

					return r
				},
			},
			{
				name: "responds 403 to a create with a body of another size",
				give: func(tb assert.TB, srv *memory.Server) *http.Request {
					req := signCreate(tb, srv, createdKey, imagePNG, len(serverBody))

					return newRequest(tb, req, strings.NewReader(serverBody+"!"))
				},
			},
			{
				name: "responds 403 to a request of another Server",
				give: func(tb assert.TB, _ *memory.Server) *http.Request {
					other, _ := newServer(tb)

					return newRequest(tb, signGet(tb, other, serverKey), nil)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv, _ := newServer(t)
				info := put(t, srv, serverKey, serverBody, textPlain)
				rec := serve(srv, tt.give(t, srv))
				assert.Equal(t, rec.Code, http.StatusForbidden, "an altered request must fail")
				assertObject(t, srv, info, serverBody)
				assertAbsent(t, srv, createdKey)
			})
		}

		t.Run("serves a GET that has a Content-Type and a body", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			put(t, srv, serverKey, serverBody, textPlain)
			r := newRequest(t, signGet(t, srv, serverKey), strings.NewReader("a body of a GET"))
			r.Header.Set("Content-Type", textPlain)
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusOK, "the signature of a GET must not cover a body or a content type")
		})

		t.Run("responds 403 to an expiry that is no number", func(t *testing.T) {
			t.Parallel()
			// The request expires at the Unix epoch. ParseInt returns its value,
			// 0, with its error for a text that is no number.
			base := serverBase
			srv, err := memory.NewServer(memory.New(fake.New(time.Unix(-10, 0).UTC())), &base)
			assert.NoError(t, err, "the base URL of the tests must be valid")
			put(t, srv, serverKey, serverBody, textPlain)
			req, err := srv.SignGet(t.Context(), serverKey, time.Unix(0, 0))
			assert.NoError(t, err, "SignGet must succeed")
			r := newRequest(t, req, nil)
			setQuery(r, "expires", "soon")
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusForbidden, "an expiry that is no number must fail")
		})

		t.Run("serves a request until the second before its expiry", func(t *testing.T) {
			t.Parallel()
			srv, clk := newServer(t)
			put(t, srv, serverKey, serverBody, textPlain)
			req := signGet(t, srv, serverKey)
			clk.Advance(lifetime - time.Second)
			rec := serve(srv, newRequest(t, req, nil))
			assert.Equal(t, rec.Code, http.StatusOK, "a request before its expiry must succeed")
		})

		t.Run("responds 403 to a request at its expiry", func(t *testing.T) {
			t.Parallel()
			srv, clk := newServer(t)
			put(t, srv, serverKey, serverBody, textPlain)
			req := signGet(t, srv, serverKey)
			clk.Advance(lifetime)
			rec := serve(srv, newRequest(t, req, nil))
			assert.Equal(t, rec.Code, http.StatusForbidden, "a request at its expiry must fail")
		})

		t.Run("responds 405 to a method other than GET and PUT", func(t *testing.T) {
			t.Parallel()
			srv, _ := newServer(t)
			info := put(t, srv, serverKey, serverBody, textPlain)
			r := newRequest(t, signGet(t, srv, serverKey), nil)
			r.Method = http.MethodDelete
			rec := serve(srv, r)
			assert.Equal(t, rec.Code, http.StatusMethodNotAllowed, "a DELETE must fail")
			expect.Equal(t, rec.Header().Get("Allow"), "GET, PUT", "the response must state the methods of the Server")
			assertObject(t, srv, info, serverBody)
		})
	})
}

// TestServerAllocs checks the allocation contracts of SignGet and
// SignCreate. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel. Each case empties the pool of the frames
// first, so a call that does not return its buffer allocates in every count.
func TestServerAllocs(t *testing.T) {
	srv, _ := newServer(t)
	ctx := t.Context()

	t.Run("SignGet", func(t *testing.T) {
		runtime.GC()
		runtime.GC()

		var err error
		expect.MaxAllocs(t, func() { _, err = srv.SignGet(ctx, serverKey, expiry) }, signGetAllocs,
			"SignGet must allocate its URL alone")
		assert.NoError(t, err, "the test must measure a signed GET")
	})

	t.Run("SignCreate", func(t *testing.T) {
		runtime.GC()
		runtime.GC()

		var err error
		expect.MaxAllocs(t, func() { _, err = srv.SignCreate(ctx, serverKey, imagePNG, 10, expiry) }, signCreateAllocs,
			"SignCreate must allocate what SignGet allocates and the Header alone")
		assert.NoError(t, err, "the test must measure a signed create")
	})

	t.Run("ServeHTTP", func(t *testing.T) {
		r := refusedRequest(t, srv)
		runtime.GC()
		runtime.GC()

		var code int
		expect.MaxAllocsWithSetup(t, httptest.NewRecorder, func(rec *httptest.ResponseRecorder) {
			srv.ServeHTTP(rec, r)
			code = rec.Code
		}, refusedAllocs, "the refusal of a signature must allocate the query, the signature and the response alone")
		assert.Equal(t, code, http.StatusForbidden, "the test must measure a refused request")
	})
}

// BenchmarkServer reports the cost of SignGet and SignCreate, and fails when
// one of them allocates more than its contract states.
func BenchmarkServer(b *testing.B) {
	srv, _ := newServer(b)

	b.Run("SignGet", func(b *testing.B) {
		ctx := b.Context()

		var err error

		c := bench.Start(b).MaxAllocs(signGetAllocs)
		defer c.End()

		for c.Loop() {
			_, err = srv.SignGet(ctx, serverKey, expiry)
		}

		assert.NoError(b, err, "the benchmark must measure a signed GET")
	})

	b.Run("SignCreate", func(b *testing.B) {
		ctx := b.Context()

		var err error

		c := bench.Start(b).MaxAllocs(signCreateAllocs)
		defer c.End()

		for c.Loop() {
			_, err = srv.SignCreate(ctx, serverKey, imagePNG, 10, expiry)
		}

		assert.NoError(b, err, "the benchmark must measure a signed create")
	})

	b.Run("ServeHTTP", func(b *testing.B) {
		r := refusedRequest(b, srv)

		var rec *httptest.ResponseRecorder

		c := bench.Start(b).MaxAllocs(refusedAllocs)
		defer c.End()

		for c.Loop() {
			c.Excluding(func() { rec = httptest.NewRecorder() })
			srv.ServeHTTP(rec, r)
		}

		assert.Equal(b, rec.Code, http.StatusForbidden, "the benchmark must measure a refused request")
	})
}

// newServer returns a Server over an empty store at serverBase, and the fake
// clock of the store at the Unix epoch.
func newServer(tb assert.TB) (*memory.Server, *fake.Clock) {
	tb.Helper()

	clk := fake.New(time.Unix(0, 0).UTC())
	base := serverBase
	srv, err := memory.NewServer(memory.New(clk), &base)
	assert.NoError(tb, err, "the base URL of the tests must be valid")

	return srv, clk
}

// put stores body under key with contentType, and returns the Info of the
// object.
func put(tb assert.TB, srv *memory.Server, key, body, contentType string) blob.Info {
	info, err := srv.Put(assert.Context(tb), key, strings.NewReader(body), blob.PutOptions{ContentType: contentType})
	assert.NoError(tb, err, "Put must succeed")

	return info
}

// get returns the body of the object under key.
func get(tb assert.TB, srv *memory.Server, key string) string {
	data, _, err := blob.GetBytes(assert.Context(tb), srv, key, 1<<20)
	assert.NoError(tb, err, "Get must succeed")

	return string(data)
}

// assertObject asserts that the object under the key of info is the object
// of info with body.
func assertObject(tb assert.TB, srv *memory.Server, info blob.Info, body string) {
	data, got, err := blob.GetBytes(assert.Context(tb), srv, info.Key, 1<<20)
	assert.NoError(tb, err, "the object must exist")
	expect.Equal(tb, string(data), body, "the object must have its body")
	expect.Equal(tb, got.Version, info.Version, "the object must have its version")
}

// assertAbsent asserts that no object is under key.
func assertAbsent(tb assert.TB, srv *memory.Server, key string) {
	_, err := srv.Stat(assert.Context(tb), key)
	assert.Equal(tb, errs.Classify(err), errs.NotFound, "no object may be under the key")
}

// signGet signs a GET of key with a lifetime of lifetime.
func signGet(tb assert.TB, srv *memory.Server, key string) blob.SignedRequest {
	req, err := srv.SignGet(assert.Context(tb), key, expiry)
	assert.NoError(tb, err, "SignGet must succeed")

	return req
}

// signCreate signs a create of key with contentType and size, with a
// lifetime of lifetime.
func signCreate(tb assert.TB, srv *memory.Server, key, contentType string, size int) blob.SignedRequest {
	req, err := srv.SignCreate(assert.Context(tb), key, contentType, int64(size), expiry)
	assert.NoError(tb, err, "SignCreate must succeed")

	return req
}

// newRequest returns the HTTP request of req with body, with each header of
// req.Header.
func newRequest(tb assert.TB, req blob.SignedRequest, body io.Reader) *http.Request {
	r := httptest.NewRequestWithContext(assert.Context(tb), req.Method, req.URL, body)
	maps.Copy(r.Header, req.Header)

	return r
}

// serve returns the recorded response of srv to r.
func serve(srv *memory.Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)

	return rec
}

// refusedRequest returns a GET of serverKey whose signature srv refuses: a
// signature of the right length in which every byte is 0.
func refusedRequest(tb assert.TB, srv *memory.Server) *http.Request {
	r := newRequest(tb, signGet(tb, srv, serverKey), nil)
	sig := r.URL.Query().Get("signature")
	setQuery(r, "signature", strings.Repeat("A", len(sig)))

	return r
}

// setQuery sets the query parameter name of r to value.
func setQuery(r *http.Request, name, value string) {
	q := r.URL.Query()
	q.Set(name, value)
	r.URL.RawQuery = q.Encode()
}

// getSignerOptions returns the door of the Backend of a served Server, for the
// conformance test of blob.GetSigner.
func getSignerOptions() []blobtest.GetSignerOption {
	return []blobtest.GetSignerOption{conform.Supplies(conform.Supply(backendOf[blob.GetSigner]))}
}

// createSignerOptions returns the door of the Backend of a served Server, for
// the conformance test of blob.CreateSigner.
func createSignerOptions() []blobtest.CreateSignerOption {
	return []blobtest.CreateSignerOption{conform.Supplies(conform.Supply(backendOf[blob.CreateSigner]))}
}

// backendOf returns the Backend of the served Server s, and stops the check of
// h when s is no served Server.
func backendOf[S any](h *conform.Handle[S], s S) blobtest.Backend {
	h.Helper()

	srv, ok := any(s).(*served)
	assert.True(h, ok, "the subject must be a served Server")

	return srv.backend
}
