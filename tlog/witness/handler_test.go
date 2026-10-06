// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// The content types of the responses of add-checkpoint.
const (
	sizeType  = "text/x.tlog.size"
	linesType = "text/plain; charset=utf-8"
)

// failingReader is a request body whose reads fail.
type failingReader struct{}

// Read returns an error.
func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("the body failed")
}

// discard is an http.ResponseWriter of the benchmarks. It keeps the status
// and the length of the body of its responses, and reuses its header, so a
// response through it allocates nothing.
type discard struct {
	// header is the header of the responses.
	header http.Header

	// code is the status of the last response, and n the bytes of the
	// bodies since the last reset of n.
	code, n int
}

// Header returns the header of w.
func (w *discard) Header() http.Header {
	return w.header
}

// WriteHeader keeps code.
func (w *discard) WriteHeader(code int) {
	w.code = code
}

// Write counts the bytes of p, and keeps the status 200 for a response
// whose handler wrote no status.
func (w *discard) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}

	w.n += len(p)

	return len(p), nil
}

func TestHandler(t *testing.T) {
	t.Parallel()

	t.Run("AddCheckpoint", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		t.Run("responds with 200 and the lines of the cosigners", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			rec := post(t, newServer(t, f.config()), requestBody(0, nil, l.notes[5]))
			testkit.Equal(t, rec.Code, http.StatusOK, "the handler must cosign the checkpoint")
			testkit.Equal(t, rec.Header().Get("Content-Type"), linesType, "the lines must be text")

			text := append([]byte(nil), noteText(t, l.notes[5])...)
			n := mustParse(t, append(append(text, '\n'), rec.Body.Bytes()...))
			testkit.True(t, f.cosigners[0].Verify(n.Text, n.Signatures[0].Value), "the line must verify")
		})

		t.Run("responds with 200 for a request with a proof", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			testkit.Equal(t, post(t, s, requestBody(0, nil, l.notes[5])).Code, http.StatusOK,
				"the handler must cosign the first checkpoint")

			u := l.update(t, 5, 9)
			testkit.Equal(t, post(t, s, requestBody(5, u.Proof, l.notes[9])).Code, http.StatusOK,
				"the handler must cosign the consistent checkpoint")
		})

		t.Run("responds with 409, the committed size and its content type", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			post(t, s, requestBody(0, nil, l.notes[5]))

			rec := post(t, s, requestBody(0, nil, l.notes[6]))
			testkit.Equal(t, rec.Code, http.StatusConflict, "the handler must refuse the old size")
			testkit.Equal(t, rec.Header().Get("Content-Type"), sizeType, "the body must be a size")
			testkit.Equal(t, rec.Body.String(), "5\n", "the body must contain the committed size")
		})

		t.Run("responds with 422 for a proof that does not verify", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())
			post(t, s, requestBody(0, nil, l.notes[5]))

			proof := l.update(t, 5, 9).Proof
			proof[0] = l.leaves[0]
			testkit.Equal(t, post(t, s, requestBody(5, proof, l.notes[9])).Code, http.StatusUnprocessableEntity,
				"the handler must refuse the proof")
		})

		t.Run("responds with 404 for a new origin beyond MaxOrigins", func(t *testing.T) {
			t.Parallel()
			other := newTestLog(t, "example.com/other")
			f := newFixture(t, l, other)
			cfg := f.config()
			cfg.MaxOrigins = 1
			s := newServer(t, cfg)
			post(t, s, requestBody(0, nil, l.notes[5]))

			testkit.Equal(t, post(t, s, requestBody(0, nil, other.notes[5])).Code, http.StatusNotFound,
				"the handler must refuse a new origin beyond MaxOrigins")
			testkit.Len(t, f.logger.messages(slog.LevelWarn), 1, "the server must log the origin")
		})

		tests := []struct {
			give func(tb testing.TB) []byte
			name string
			want int
		}{
			{
				name: "responds with 400 for a body without an old size line", want: http.StatusBadRequest,
				give: func(testing.TB) []byte { return []byte("size 0\n\n") },
			},
			{
				name: "responds with 400 for a note that is not a signed note", want: http.StatusBadRequest,
				give: func(testing.TB) []byte { return []byte("old 0\n\nnot a note\n") },
			},
			{
				name: "responds with 400 for a note whose text is not a checkpoint body", want: http.StatusBadRequest,
				give: func(tb testing.TB) []byte {
					tb.Helper()

					n, err := note.Sign(tb.Context(), []byte("a text\n"), l.signer)
					testkit.NoError(tb, err, "Sign must sign the text")

					msg, err := n.MarshalText()
					testkit.NoError(tb, err, "MarshalText must write the note")

					return requestBody(0, nil, msg)
				},
			},
			{
				name: "responds with 400 for an old size above the size", want: http.StatusBadRequest,
				give: func(testing.TB) []byte { return requestBody(6, nil, l.notes[5]) },
			},
			{
				name: "responds with 400 for a proof hash of 48 bytes", want: http.StatusBadRequest,
				give: func(testing.TB) []byte {
					return requestBody(3, []crypto.Digest{crypto.NewDigest384([48]byte{1})}, l.notes[5])
				},
			},
			{
				name: "responds with 404 for an origin that Logs refuses", want: http.StatusNotFound,
				give: func(tb testing.TB) []byte {
					tb.Helper()

					return requestBody(0, nil, newTestLog(tb, "example.com/other").notes[5])
				},
			},
			{
				name: "responds with 403 for a note with an invalid line of the log", want: http.StatusForbidden,
				give: func(testing.TB) []byte {
					return requestBody(
						0,
						nil,
						append(append([]byte(nil), l.notes[5]...), garbageLine(l.signer.Key())...),
					)
				},
			},
			{
				name: "responds with 403 for a note with an invalid line of the log before a valid one",
				want: http.StatusForbidden,
				give: func(tb testing.TB) []byte {
					tb.Helper()

					text := append([]byte(nil), noteText(tb, l.notes[5])...)
					msg := append(append(append(text, '\n'), garbageLine(l.signer.Key())...),
						cosignLines(tb, noteText(tb, l.notes[5]), l.signer)...)

					return requestBody(0, nil, msg)
				},
			},
			{
				name: "responds with 200 for a note with a line of an unknown key", want: http.StatusOK,
				give: func(tb testing.TB) []byte {
					tb.Helper()

					other := newTestLog(tb, "example.com/other")
					msg := append(append([]byte(nil), l.notes[5]...), garbageLine(other.signer.Key())...)

					return requestBody(0, nil, msg)
				},
			},
			{
				name: "responds with 400 for a body of 1 MiB that is not a request", want: http.StatusBadRequest,
				give: func(testing.TB) []byte { return make([]byte, 1<<20) },
			},
			{
				name: "responds with 413 for a body above 1 MiB", want: http.StatusRequestEntityTooLarge,
				give: func(testing.TB) []byte { return make([]byte, 1<<20+1) },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				rec := post(t, newServer(t, newFixture(t, l).config()), tt.give(t))
				testkit.Equal(t, rec.Code, tt.want, "the handler must respond with "+strconv.Itoa(tt.want))
			})
		}

		t.Run("responds with 403 for a hybrid key set with a line of one key", func(t *testing.T) {
			t.Parallel()
			pq := newTestLog(t, "example.com/hybrid")
			f := newFixture(t)
			f.accepted[l.origin] = witness.Log{
				Hasher: l.log.Hasher,
				Keys:   [][]note.Key{{l.signer.Key(), pq.signer.Key()}},
			}
			s := newServer(t, f.config())

			testkit.Equal(t, post(t, s, requestBody(0, nil, l.notes[5])).Code, http.StatusForbidden,
				"the handler must require both keys of the set")
			testkit.Equal(t, post(t, s, requestBody(0, nil, signBody(t, l.body(5), l.signer, pq.signer))).Code,
				http.StatusOK, "the handler must accept both keys of the set")
		})

		t.Run("responds with 400 for a note that a cosigner does not sign", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			f.cosigners = []checkpoint.Cosigner{mldsaCosigner(t, witnessName, f.clock)}

			body := l.body(5)
			body.Extensions = []checkpoint.Extension{"an extension line"}
			testkit.Equal(t, post(t, newServer(t, f.config()), requestBody(0, nil, signBody(t, body, l.signer))).Code,
				http.StatusBadRequest, "the handler must refuse the note")
		})

		t.Run("responds with 503 for a reading of UTC outside MaxError", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			f.clock.SetUTCError(time.Second, true)

			testkit.Equal(t, post(t, s, requestBody(0, nil, l.notes[5])).Code, http.StatusServiceUnavailable,
				"the handler must report the clock")
		})

		t.Run("responds with 500 for a body that it cannot read", func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(bounded(t), http.MethodPost, "/add-checkpoint", failingReader{})
			newServer(t, newFixture(t, l).config()).AddCheckpoint().ServeHTTP(rec, req)
			testkit.Equal(t, rec.Code, http.StatusInternalServerError, "the handler must report the read")
		})

		t.Run("returns to the client the error of each status", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			c := newWitnessClient(t, serve(t, s), f.cosigners[0].Key())

			_, err := c.AddCheckpoint(bounded(t), l.notes[5], 0, nil, nil)
			testkit.NoError(t, err, "the client must receive the lines")

			_, err = c.AddCheckpoint(bounded(t), l.notes[6], 0, nil, nil)
			se, ok := errors.AsType[*witness.SizeError](err)
			testkit.True(t, ok, "the client must receive a SizeError for a 409")
			testkit.Equal(t, se.Size, uint64(5), "the SizeError must contain the committed size")

			proof := l.update(t, 5, 9).Proof
			proof[0] = l.leaves[0]
			_, err = c.AddCheckpoint(bounded(t), l.notes[9], 5, proof, nil)
			testkit.ErrorIs(t, err, witness.ErrInconsistent, "the client must receive ErrInconsistent for a 422")

			other := newTestLog(t, "example.com/other")
			_, err = c.AddCheckpoint(bounded(t), other.notes[5], 0, nil, nil)
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "the client must receive NotFound for a 404")

			_, err = c.AddCheckpoint(bounded(t), signBody(t, l.body(6), other.signer), 5, l.update(t, 5, 6).Proof, nil)
			testkit.Equal(t, errs.Classify(err), errs.Denied, "the client must receive Denied for a 403")

			_, err = c.AddCheckpoint(bounded(t), l.notes[5], 6, nil, nil)
			status, ok := errors.AsType[*httpclient.StatusError](err)
			testkit.True(t, ok, "the client must receive a StatusError for a 400")
			testkit.Equal(t, status.Status, http.StatusBadRequest, "the status must be 400")
		})

		t.Run("returns to the client a cosignature of Ed25519 and of ML-DSA-44 that tlog/checkpoint verifies",
			func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, l)
				f.cosigners = append(f.cosigners, mldsaCosigner(t, "example.com/pq", f.clock))
				s := newServer(t, f.config())
				c := newWitnessClient(t, serve(t, s), f.cosigners[0].Key(), f.cosigners[1].Key())

				lines, err := c.AddCheckpoint(bounded(t), l.notes[5], 0, nil, nil)
				testkit.NoError(t, err, "the client must verify both lines")

				text := append([]byte(nil), noteText(t, l.notes[5])...)
				n := mustParse(t, append(append(text, '\n'), lines...))
				testkit.Len(t, n.Signatures, 2, "the response must contain a line of each cosigner")

				for i, cs := range f.cosigners {
					v, err := resolver().Verifier(cs.Key())
					testkit.NoError(t, err, "the resolver must resolve the key")
					testkit.True(t, v.Verify(n.Text, n.Signatures[i].Value), "the line must verify")
				}
			})
	})
}

func BenchmarkHandler(b *testing.B) {
	b.Run("AddCheckpoint", func(b *testing.B) {
		l := newTestLog(b, logName)
		s := newServer(b, newFixture(b, l).config())
		h := s.AddCheckpoint()

		// A checkpoint of the committed size and root commits again, so each
		// request commits a record. blob/memory formats a version below 100
		// without an allocation, so 40 commits move the versions past it
		// first.
		post(b, s, requestBody(0, nil, l.notes[5]))

		body := requestBody(5, nil, l.notes[5])
		for range 40 {
			testkit.Equal(b, post(b, s, body).Code, http.StatusOK, "the handler must cosign the checkpoint")
		}

		r := bytes.NewReader(body)
		req := httptest.NewRequestWithContext(b.Context(), http.MethodPost, submissionPath+"/add-checkpoint", nil)
		req.Body = io.NopCloser(r)
		w := &discard{header: make(http.Header)}

		// The handler allocates the 15 objects of the commit, as Advance
		// does, and nothing of its own.
		c := bench.Start(b).MaxAllocs(15)
		defer c.End()

		for c.Loop() {
			r.Reset(body)
			w.code, w.n = 0, 0
			h.ServeHTTP(w, req)
		}

		testkit.Equal(b, w.code, http.StatusOK, "the benchmark must measure a cosigned checkpoint")
		testkit.NotEqual(b, w.n, 0, "the benchmark must measure a response with lines")
	})
}

// requestBody returns the body of an add-checkpoint request of tlog-witness
// from oldSize with proof for msg.
func requestBody(oldSize uint64, proof []crypto.Digest, msg []byte) []byte {
	body := []byte("old " + strconv.FormatUint(oldSize, 10) + "\n")
	for _, h := range proof {
		body = append(append(body, base64.StdEncoding.EncodeToString(h.Bytes())...), '\n')
	}

	return append(append(body, '\n'), msg...)
}

// post sends body to the add-checkpoint handler of s, and returns the
// response.
func post(tb testing.TB, s *witness.Server, body []byte) *httptest.ResponseRecorder {
	tb.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(bounded(tb), http.MethodPost, "/add-checkpoint", bytes.NewReader(body))
	s.AddCheckpoint().ServeHTTP(rec, req)

	return rec
}

// serve returns a test server that mounts the handlers of s at the
// prefixes of the tests. The test closes it.
func serve(tb testing.TB, s *witness.Server) *httptest.Server {
	tb.Helper()

	mux := http.NewServeMux()
	mux.Handle("POST "+submissionPath+"/add-checkpoint", s.AddCheckpoint())
	mux.Handle("GET "+monitoringPath+"/{hash}/checkpoint", s.Checkpoint())

	ts := httptest.NewServer(mux)
	tb.Cleanup(ts.Close)

	return ts
}

// newWitnessClient returns a Client of the witness of ts with keys.
func newWitnessClient(tb testing.TB, ts *httptest.Server, keys ...note.Key) *witness.Client {
	tb.Helper()

	h, err := httpclient.New("witness", required, httpclient.WithTimeout(patience))
	testkit.NoError(tb, err, "httpclient.New must accept the options")

	c, err := witness.NewClient(&witness.ClientConfig{
		HTTP:       h,
		Resolver:   resolver(),
		Submission: ts.URL + submissionPath,
		Monitoring: ts.URL + monitoringPath,
		Keys:       keys,
	})
	testkit.NoError(tb, err, "NewClient must accept the configuration")

	return c
}
