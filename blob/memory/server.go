// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/hmac/sha256"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/version"
)

// The query parameters of a signed URL.
const (
	queryExpires   = "expires"
	querySignature = "signature"
)

// The headers that a Server reads and writes, and the values that it writes.
const (
	headerAllow       = "Allow"
	headerContentType = "Content-Type"
	headerETag        = "ETag"
	headerNoSniff     = "X-Content-Type-Options"

	allowedMethods = "GET, PUT"
	noSniff        = "nosniff"

	// octetStream is the content type of the response for an object without
	// a content type. A Server does not infer the type of a body.
	octetStream = "application/octet-stream"
)

// The sizes of the parts of a signed request.
const (
	// maxDigits is the length of the decimal text of the smallest int64,
	// with its sign.
	maxDigits = 20

	// signatureLen is the length of the text of a signature: an
	// HMAC-SHA-256 in the URL alphabet of base64, without padding.
	signatureLen = (crypto.DigestSize256*8 + 5) / 6

	// frameSize is the room of a frame beside its key and its content type:
	// the domain, the method, the two numbers and the length prefix of each
	// field.
	frameSize = 160
)

var (
	// signedDomain is the domain of the frame of the signed fields of a
	// request.
	signedDomain = crypto.Domain{Name: "go.thesmos.sh/core/blob/memory signed request", Version: 1}

	// frames is the pool of the buffers of frames. A buffer has room for the
	// frame of the longest key and the longest content type, so the frame of
	// a signed request never grows it.
	frames = pool.NewPool(func() *[]byte {
		b := make([]byte, 0, frameSize+blob.MaxKeyLen+blob.MaxContentTypeLen)

		return &b
	})
)

// Server is a Store that serves its objects over HTTP at a base URL and signs
// requests for them. A developer mounts it in the HTTP server of an
// application, and the conformance tests of signed requests run against it.
//
// crypto.Framer frames the method, the key, the expiry, the Content-Length
// and the Content-Type of a request. The signature of the request is the
// HMAC-SHA-256 of the frame. [NewServer] reads the key of the HMAC from
// crypto/rand, so a request is valid only while its Server exists.
//
// The URL of a request is the base URL with the escaped key at the end of
// its path. Its query contains the expiry in Unix seconds and the signature.
// The Server measures the lifetime of a request with the clock of its Store.
//
// # Concurrency
//
// Safe for concurrent use, as a Store is. A Server does not change after
// NewServer returns it.
//
// # Allocation contract
//
// SignGet allocates its URL. A key with a character to escape also
// allocates each escaped element of the key. SignCreate also allocates its
// Header: the map, its group of entries and the list of its values. A pool
// keeps the buffers of the frames.
//
//dokimi:subject blob.GetSigner new=newGetSigner options=getSignerOptions
//dokimi:subject blob.CreateSigner new=newCreateSigner options=createSignerOptions
type Server struct {
	*Store

	mac *sha256.MAC

	// prefix is the URL of the root of the keys: the base URL, with a slash
	// at the end of its path.
	prefix string

	// root is the unescaped path of the root of the keys, with a slash at
	// its end.
	root string
}

// Compile-time interface checks.
var (
	_ blob.Store        = (*Server)(nil)
	_ blob.RangeReader  = (*Server)(nil)
	_ blob.GetSigner    = (*Server)(nil)
	_ blob.CreateSigner = (*Server)(nil)
	_ http.Handler      = (*Server)(nil)
)

// NewServer returns a Server over s whose requests address base. The Server
// adds a slash to the end of the path of base, and does not change base.
//
// Error modes: a nil s classifies as [errs.Invalid]. So does a base without
// a scheme or a host, and a base with a query or a fragment.
func NewServer(s *Store, base *url.URL) (*Server, error) {
	if s == nil {
		return nil, errs.WithClass(errNoStore, errs.Invalid)
	}

	if base == nil || base.Scheme == "" || base.Host == "" || base.RawQuery != "" || base.ForceQuery ||
		base.Fragment != "" {

		return nil, errs.WithClass(errBadBase, errs.Invalid)
	}

	root := url.URL{Scheme: base.Scheme, Host: base.Host, Path: strings.TrimSuffix(base.Path, "/") + "/"}

	// The key of the HMAC joins two texts of crypto/rand, so it has at least
	// 256 bits of randomness.
	key := []byte(rand.Text() + rand.Text())

	return &Server{Store: s, mac: sha256.New(key), prefix: root.String(), root: root.Path}, nil
}

// SignGet returns a GET of the object under key until expiry. The Server
// rounds expiry down to the second.
//
// Error modes, each with the zero request:
//
//   - A done ctx returns the context's error, unwrapped.
//   - A key that [blob.ValidKey] rejects classifies as [errs.Invalid].
//   - An expiry less than one second after the time of the Store classifies
//     as errs.Invalid. So does an expiry more than [blob.MaxSignedLifetime]
//     after it.
//
// # Allocation contract
//
// One allocation, the URL, for a key without a character to escape.
func (srv *Server) SignGet(ctx context.Context, key string, expiry time.Time) (blob.SignedRequest, error) {
	return srv.sign(ctx, http.MethodGet, key, "", 0, expiry)
}

// SignCreate returns a PUT that creates the object under key until expiry.
// The body of the object has size bytes and the content type contentType.
// The Header of the request contains the content type.
//
// Error modes, each with the zero request:
//
//   - The error modes of SignGet.
//   - An empty contentType classifies as [errs.Invalid]. So does a
//     contentType that [blob.ValidContentType] rejects.
//   - A size below 1 or above [blob.MaxCreateSize] classifies as
//     errs.Invalid.
//
// # Allocation contract
//
// The allocation of SignGet, and three for the Header: the map, its group of
// entries and the list of its values.
func (srv *Server) SignCreate(
	ctx context.Context, key, contentType string, size int64, expiry time.Time,
) (blob.SignedRequest, error) {
	return srv.sign(ctx, http.MethodPut, key, contentType, size, expiry)
}

// ServeHTTP serves the signed requests under the path of the base URL:
//
//   - A GET of a key with an object responds 200, or 206 for a Range
//     header, through [http.ServeContent]. The response has the content type
//     of the object, application/octet-stream for an object without one, the
//     version as an ETag and the header X-Content-Type-Options: nosniff.
//   - A GET of a key without an object responds 404.
//   - A PUT of a key without an object creates the object with the
//     Content-Type of the request and responds 201. A PUT of a key with an
//     object responds 412, and a body that fails responds 400. Each refused
//     PUT leaves the key as it was.
//   - A request whose signature fails, or that arrives at or after its
//     expiry, responds 403. The signature covers the method, the key, the
//     expiry, the Content-Length and the Content-Type.
//   - Another method responds 405.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set(headerAllow, allowedMethods)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	// A path outside the root keeps its prefix, so its key has no signature.
	key := strings.TrimPrefix(r.URL.Path, srv.root)

	var (
		size        int64
		contentType string
	)
	if r.Method == http.MethodPut {
		size, contentType = r.ContentLength, r.Header.Get(headerContentType)
	}

	if !srv.verify(r.URL.Query(), r.Method, key, contentType, size) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)

		return
	}

	if r.Method == http.MethodGet {
		srv.serveGet(w, r, key)

		return
	}

	srv.serveCreate(w, r, key, contentType, size)
}

// sign returns the request of method for the object under key until expiry.
// A PUT has a body of size bytes and contentType, and a GET has the empty
// content type and the size 0.
func (srv *Server) sign(
	ctx context.Context, method, key, contentType string, size int64, expiry time.Time,
) (blob.SignedRequest, error) {
	if err := ctx.Err(); err != nil {
		return blob.SignedRequest{}, err
	}

	if !blob.ValidKey(key) {
		return blob.SignedRequest{}, errs.WithClass(errBadKey, errs.Invalid)
	}

	var header map[string][]string
	if method == http.MethodPut {
		if contentType == "" {
			return blob.SignedRequest{}, errs.WithClass(errNoContentType, errs.Invalid)
		}

		if !blob.ValidContentType(contentType) {
			return blob.SignedRequest{}, errs.WithClass(errBadContentType, errs.Invalid)
		}

		if size < 1 || size > blob.MaxCreateSize {
			return blob.SignedRequest{}, errs.WithClass(errBadSize, errs.Invalid)
		}

		header = map[string][]string{headerContentType: {contentType}}
	}

	if d := expiry.Sub(srv.c.Time()); d < time.Second || d > blob.MaxSignedLifetime {
		return blob.SignedRequest{}, errs.WithClass(errBadExpiry, errs.Invalid)
	}

	expires := expiry.Unix()
	buf := frames.Get()
	sig := srv.mac.Sign(frame((*buf)[:0], method, key, contentType, size, expires))
	frames.Put(buf)

	var b strings.Builder
	b.Grow(len(srv.prefix) + 3*len(key) + len(queryExpires) + len(querySignature) + maxDigits + signatureLen + 3)
	b.WriteString(srv.prefix)

	for rest := key; ; {
		elem, after, more := strings.Cut(rest, "/")
		b.WriteString(url.PathEscape(elem))

		if !more {
			break
		}

		b.WriteByte('/')
		rest = after
	}

	var (
		digits [maxDigits]byte
		text   [signatureLen]byte
	)
	b.WriteByte('?')
	b.WriteString(queryExpires)
	b.WriteByte('=')
	b.Write(strconv.AppendInt(digits[:0], expires, 10))
	b.WriteByte('&')
	b.WriteString(querySignature)
	b.WriteByte('=')
	b.Write(base64.RawURLEncoding.AppendEncode(text[:0], sig.Bytes()))

	return blob.SignedRequest{Expiry: time.Unix(expires, 0).UTC(), Header: header, Method: method, URL: b.String()}, nil
}

// verify reports whether the query q contains a signature of the fields of a
// request and an expiry after the time of the Store.
func (srv *Server) verify(q url.Values, method, key, contentType string, size int64) bool {
	expires, err := strconv.ParseInt(q.Get(queryExpires), 10, 64)
	if err != nil {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(q.Get(querySignature))
	//dokimi:mutate-skip ror-false,sbr-delete: DecodeString returns a multiple of 3 bytes with its error, and Verify refuses all but 32
	if err != nil {
		return false
	}

	// A key or a content type of a request can be longer than any that the
	// Server signs. The frame then grows past the buffer, and the pool keeps
	// the buffer as it was.
	buf := frames.Get()
	signed := srv.mac.Verify(frame((*buf)[:0], method, key, contentType, size, expires), sig)
	frames.Put(buf)

	return signed && srv.c.Time().Before(time.Unix(expires, 0))
}

// serveGet serves the object under key through [http.ServeContent], with
// its content type, its version as an ETag and its modification time. It
// responds 404 for a key without an object.
func (srv *Server) serveGet(w http.ResponseWriter, r *http.Request, key string) {
	srv.mu.Lock()
	obj, ok := srv.m.Get(key)
	srv.mu.Unlock()

	if !ok {
		http.NotFound(w, r)

		return
	}

	contentType := obj.info.ContentType
	if contentType == "" {
		contentType = octetStream
	}

	h := w.Header()
	h.Set(headerContentType, contentType)
	h.Set(headerNoSniff, noSniff)
	h.Set(headerETag, strconv.Quote(string(obj.info.Version)))
	http.ServeContent(w, r, "", obj.info.ModTime, bytes.NewReader(obj.data))
}

// serveCreate creates the object under key from the body of r, of size bytes
// and contentType, and responds 201. It responds 412 for a key with an
// object, and 400 for a body that fails. [http.MaxBytesReader] refuses the
// bytes of the body past size.
func (srv *Server) serveCreate(w http.ResponseWriter, r *http.Request, key, contentType string, size int64) {
	_, err := srv.Put(r.Context(), key, http.MaxBytesReader(w, r.Body, size), blob.PutOptions{
		ContentType: contentType,
		Write:       version.WriteOptions{IfNoneMatch: version.Wildcard},
	})

	switch {
	case errors.Is(err, version.ErrExists):
		http.Error(w, http.StatusText(http.StatusPreconditionFailed), http.StatusPreconditionFailed)
	case err != nil:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusCreated)
	}
}

// frame appends to dst the frame of the signed fields of a request under
// signedDomain, and returns the extended slice. Each number is a field of
// its decimal digits.
func frame(dst []byte, method, key, contentType string, size, expires int64) []byte {
	var digits [maxDigits]byte

	f := crypto.NewFramer(dst, signedDomain)
	f.String(method)
	f.String(key)
	f.Bytes(strconv.AppendInt(digits[:0], expires, 10))
	f.Bytes(strconv.AppendInt(digits[:0], size, 10))
	f.String(contentType)

	return f.Frame()
}
