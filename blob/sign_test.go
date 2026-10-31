// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blob_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
)

// The URLs of the requests of the text forms. The query and the fragment of
// signedURL contain marks that no text form may contain.
const (
	signedURL    = "https://bucket.example/photos/2026/a.jpg?X-Signature=" + queryMark + "#" + fragmentMark
	unsignedURL  = "https://bucket.example/photos/2026/a.jpg"
	queryMark    = "query-mark"
	fragmentMark = "fragment-mark"
)

// The parts of the request of the text forms.
const (
	methodPut  = "PUT"
	imageJPEG  = "image/jpeg"
	expiryText = "2026-10-31T18:00:00Z"
)

var (
	// requestExpiry is the expiry of the request of the text forms.
	requestExpiry = time.Date(2026, time.October, 31, 18, 0, 0, 0, time.UTC)

	// textRequest is the PUT of the text forms, whose URL has a query and a
	// fragment. No test changes its Header.
	textRequest = blob.SignedRequest{
		Expiry: requestExpiry,
		Header: map[string][]string{"Content-Type": {imageJPEG}},
		Method: methodPut,
		URL:    signedURL,
	}

	// unsignedChains are the chains of decorators in which no As function of
	// the signers finds a signer. No test writes to their stores.
	unsignedChains = []struct {
		name string
		give blob.Store
	}{
		{name: "reports false for a store that signs nothing", give: memory.New(fake.New(origin))},
		{name: "reports false for a decorator without Unwrap", give: storeOnly{memory.New(fake.New(origin))}},
		{
			name: "reports false for a chain that ends without a signer",
			give: decorated{storeOnly{memory.New(fake.New(origin))}},
		},
		{name: "reports false for a nil store", give: nil},
		{name: "reports false for a decorator of nil", give: decorated{}},
	}
)

// selfSigning is a decorator that signs requests itself, by delegating to
// the signers that it wraps.
type selfSigning struct {
	decorated

	gets    blob.GetSigner
	creates blob.CreateSigner
}

// SignGet delegates to the wrapped GetSigner.
func (s selfSigning) SignGet(ctx context.Context, key string, expiry time.Time) (blob.SignedRequest, error) {
	return s.gets.SignGet(ctx, key, expiry)
}

// SignCreate delegates to the wrapped CreateSigner.
func (s selfSigning) SignCreate(
	ctx context.Context, key, contentType string, size int64, expiry time.Time,
) (blob.SignedRequest, error) {
	return s.creates.SignCreate(ctx, key, contentType, size, expiry)
}

func TestSign(t *testing.T) {
	t.Parallel()

	t.Run("AsGetSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the store itself when it signs gets", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			gs, ok := blob.AsGetSigner(s)
			assert.True(t, ok, "a store that signs gets must be found")
			assert.Equal(t, gs, blob.GetSigner(s), "the store itself must be returned", assert.ByIdentity())
		})

		t.Run("returns a GetSigner behind two decorators", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			gs, ok := blob.AsGetSigner(decorated{decorated{s}})
			assert.True(t, ok, "a GetSigner behind decorators must be found")
			assert.Equal(t, gs, blob.GetSigner(s), "the wrapped store must be returned", assert.ByIdentity())
		})

		t.Run("returns a decorator that signs gets before the store it wraps", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			outer := selfSigning{decorated: decorated{s}, gets: s, creates: s}
			gs, ok := blob.AsGetSigner(outer)
			assert.True(t, ok, "the decorator must be found")
			assert.Equal(t, gs, blob.GetSigner(outer), "the outermost GetSigner must be returned", assert.ByIdentity())
		})

		for _, tt := range unsignedChains {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				gs, ok := blob.AsGetSigner(tt.give)
				assert.False(t, ok, "a chain without a GetSigner must not report one")
				assert.Nil(t, gs, "no GetSigner may be returned")
			})
		}
	})

	t.Run("AsCreateSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the store itself when it signs creates", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			cs, ok := blob.AsCreateSigner(s)
			assert.True(t, ok, "a store that signs creates must be found")
			assert.Equal(t, cs, blob.CreateSigner(s), "the store itself must be returned", assert.ByIdentity())
		})

		t.Run("returns a CreateSigner behind two decorators", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			cs, ok := blob.AsCreateSigner(decorated{decorated{s}})
			assert.True(t, ok, "a CreateSigner behind decorators must be found")
			assert.Equal(t, cs, blob.CreateSigner(s), "the wrapped store must be returned", assert.ByIdentity())
		})

		t.Run("returns a decorator that signs creates before the store it wraps", func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			outer := selfSigning{decorated: decorated{s}, gets: s, creates: s}
			cs, ok := blob.AsCreateSigner(outer)
			assert.True(t, ok, "the decorator must be found")
			assert.Equal(t, cs, blob.CreateSigner(outer), "the outermost CreateSigner must be returned",
				assert.ByIdentity())
		})

		for _, tt := range unsignedChains {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cs, ok := blob.AsCreateSigner(tt.give)
				assert.False(t, ok, "a chain without a CreateSigner must not report one")
				assert.Nil(t, cs, "no CreateSigner may be returned")
			})
		}
	})

	t.Run("SignedRequest", func(t *testing.T) {
		t.Parallel()

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name    string
				giveURL string
				want    string
			}{
				{
					name:    "returns the method, the URL without its query and the expiry",
					giveURL: signedURL,
					want:    methodPut + " " + unsignedURL + " " + expiryText,
				},
				{
					name:    "returns a URL without a query as it is",
					giveURL: unsignedURL,
					want:    methodPut + " " + unsignedURL + " " + expiryText,
				},
				{
					name:    "leaves out a fragment without a query",
					giveURL: unsignedURL + "#" + fragmentMark,
					want:    methodPut + " " + unsignedURL + " " + expiryText,
				},
				{
					name:    "leaves out a query at the start of the URL",
					giveURL: "?X-Signature=" + queryMark,
					want:    methodPut + "  " + expiryText,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					req := textRequest
					req.URL = tt.giveURL
					assert.Equal(t, req.String(), tt.want, "String must leave out the query and the fragment")
				})
			}
		})

		t.Run("GoString", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the text of String", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, textRequest.GoString(), textRequest.String(), "GoString must return the text of String")
			})

			t.Run("keeps the query out of the Go syntax verb of fmt", func(t *testing.T) {
				t.Parallel()
				expect.That(t, fmt.Sprintf("%#v", textRequest)).
					NotContains(queryMark, "the %#v form must leave out the query").
					NotContains(fragmentMark, "the %#v form must leave out the fragment")
			})
		})

		t.Run("LogValue", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the method, the URL without its query and the expiry", func(t *testing.T) {
				t.Parallel()
				attrs := textRequest.LogValue().Group()
				assert.Length(t, attrs, 3, "the group must have the method, the URL and the expiry")
				expect.Equal(t, attrs[0].String(), "method="+methodPut, "the first attribute must be the method")
				expect.Equal(t, attrs[1].String(), "url="+unsignedURL, "the second attribute must be the URL")
				expect.Equal(t, attrs[2].Value.Time(), requestExpiry, "the third attribute must be the expiry")
			})

			tests := []struct {
				name string
				give func(*bytes.Buffer) slog.Handler
			}{
				{
					name: "keeps the query out of a text handler",
					give: func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
				},
				{
					name: "keeps the query out of a JSON handler",
					give: func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					var b bytes.Buffer
					slog.New(tt.give(&b)).Info("signed", "request", textRequest)
					expect.That(t, b.String()).
						Contains(unsignedURL, "the log line must contain the URL without its query").
						NotContains(queryMark, "the log line must leave out the query").
						NotContains(fragmentMark, "the log line must leave out the fragment")
				})
			}
		})
	})

	t.Run("MaxSignedLifetime", func(t *testing.T) {
		t.Parallel()

		t.Run("is the 7 days of every backend", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, blob.MaxSignedLifetime, 7*24*time.Hour,
				"the lifetime bound must be the 7 days of S3, Cloud Storage and a user delegation SAS")
		})
	})

	t.Run("MaxCreateSize", func(t *testing.T) {
		t.Parallel()

		t.Run("is the 5 GB of one PUT of Amazon S3", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, blob.MaxCreateSize, 5_000_000_000,
				"the size bound must be the 5 GB of one PUT of S3 in decimal bytes")
		})
	})
}

// TestSignAllocs checks the allocation contracts of AsGetSigner,
// AsCreateSigner and the text forms of SignedRequest. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
func TestSignAllocs(t *testing.T) {
	t.Run("AsGetSigner", func(t *testing.T) {
		var s blob.Store = decorated{decorated{newServer(t)}}

		var got bool
		expect.MaxAllocs(t, func() { _, got = blob.AsGetSigner(s) }, 0, "AsGetSigner must not allocate")
		assert.True(t, got, "the test must measure a GetSigner behind decorators")
	})

	t.Run("AsCreateSigner", func(t *testing.T) {
		var s blob.Store = decorated{decorated{newServer(t)}}

		var got bool
		expect.MaxAllocs(t, func() { _, got = blob.AsCreateSigner(s) }, 0, "AsCreateSigner must not allocate")
		assert.True(t, got, "the test must measure a CreateSigner behind decorators")
	})

	t.Run("SignedRequest", func(t *testing.T) {
		req := textRequest

		t.Run("String", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = req.String() }, 1, "String must allocate its text alone")
			assert.NotEmpty(t, got, "the test must measure a text")
		})

		t.Run("GoString", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = req.GoString() }, 1, "GoString must allocate its text alone")
			assert.NotEmpty(t, got, "the test must measure a text")
		})

		t.Run("LogValue", func(t *testing.T) {
			var got slog.Value
			expect.MaxAllocs(t, func() { got = req.LogValue() }, 1,
				"LogValue must allocate the attributes of its group alone")
			assert.Equal(t, got.Kind(), slog.KindGroup, "the test must measure a group")
		})
	})
}

// BenchmarkSign reports the cost of AsGetSigner, AsCreateSigner and the text
// forms of SignedRequest, and fails when one of them allocates more than its
// contract states.
func BenchmarkSign(b *testing.B) {
	b.Run("AsGetSigner", func(b *testing.B) {
		var s blob.Store = decorated{decorated{newServer(b)}}

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, got = blob.AsGetSigner(s)
		}

		assert.True(b, got, "the benchmark must measure a GetSigner behind decorators")
	})

	b.Run("AsCreateSigner", func(b *testing.B) {
		var s blob.Store = decorated{decorated{newServer(b)}}

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, got = blob.AsCreateSigner(s)
		}

		assert.True(b, got, "the benchmark must measure a CreateSigner behind decorators")
	})

	b.Run("SignedRequest", func(b *testing.B) {
		req := textRequest

		b.Run("String", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = req.String()
			}

			assert.NotEmpty(b, got, "the benchmark must measure a text")
		})

		b.Run("GoString", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = req.GoString()
			}

			assert.NotEmpty(b, got, "the benchmark must measure a text")
		})

		b.Run("LogValue", func(b *testing.B) {
			var got slog.Value

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = req.LogValue()
			}

			assert.Equal(b, got.Kind(), slog.KindGroup, "the benchmark must measure a group")
		})
	})
}

// newServer returns a memory.Server over an empty store, whose requests
// address a host of the example domain.
func newServer(tb testing.TB) *memory.Server {
	tb.Helper()

	base := url.URL{Scheme: "https", Host: "blobs.example", Path: "/"}
	s, err := memory.NewServer(memory.New(fake.New(origin)), &base)
	assert.NoError(tb, err, "the base URL of the tests must be valid")

	return s
}
