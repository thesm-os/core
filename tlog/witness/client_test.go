// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/telemetry/w3c"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// The names of the log and the witness of the tests.
const (
	logName     = "example.com/log"
	witnessName = "example.com/witness"
)

// The paths of the prefixes of the test witnesses.
const (
	submissionPath = "/submit"
	monitoringPath = "/monitor"
)

// pipeHost is the host of the witness that the clients of the benchmarks
// call over a pipe.
const pipeHost = "witness.test"

// clockTime is the time of the fake clocks of the tests.
var clockTime = time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

// The markers that the responder of the benchmarks finds in a request: the
// end of its header, and the field of the length of its body.
var (
	endOfHeader   = []byte("\r\n\r\n")
	contentLength = []byte("\r\nContent-Length: ")
)

// required bundles the four options of the dependencies that
// httpclient.New requires, and admits the loopback hosts of the test
// servers.
var required = httpclient.Options(
	httpclient.WithClock(fake.New(clockTime)),
	httpclient.WithLogger(slog.New(slog.DiscardHandler)),
	httpclient.WithReporter(noop.Reporter{}),
	httpclient.WithPropagator(w3c.Propagator{}),
	httpclient.WithHosts("127.0.0.1"),
	httpclient.WithReach(httpclient.ReachPrivate),
)

// fakeWitness is a test witness: an httptest server whose handler records
// each request and responds with the status and the body of its reply.
type fakeWitness struct {
	// reply returns the status and the body of the response to a request
	// with body.
	reply func(body []byte) (int, []byte)

	// server serves the handler.
	server *httptest.Server

	// requests counts the requests.
	requests atomic.Int64

	// method, path and body are the method, the path and the body of the
	// last request.
	method, path atomic.Value
	body         atomic.Value
}

func TestClient(t *testing.T) {
	t.Parallel()

	t.Run("NewClient", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Client of a valid configuration", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, err := witness.NewClient(clientConfig(t, w, ed25519Cosigner(t, witnessName).Key()))
			testkit.NoError(t, err, "NewClient must accept the configuration")
		})

		t.Run("returns ErrConfig for a nil configuration", func(t *testing.T) {
			t.Parallel()
			c, err := witness.NewClient(nil)
			testkit.ErrorIs(t, err, witness.ErrConfig, "NewClient must refuse a nil configuration")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, c == nil, "NewClient must return a nil Client with an error")
		})

		key := ed25519Cosigner(t, witnessName).Key()
		tests := []struct {
			edit func(cfg *witness.ClientConfig)
			name string
		}{
			{name: "returns ErrConfig without HTTP", edit: func(cfg *witness.ClientConfig) { cfg.HTTP = nil }},
			{
				name: "returns ErrConfig without a Resolver",
				edit: func(cfg *witness.ClientConfig) { cfg.Resolver = nil },
			},
			{
				name: "returns ErrConfig without Submission",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "" },
			},
			{
				name: "returns ErrConfig without Monitoring",
				edit: func(cfg *witness.ClientConfig) { cfg.Monitoring = "" },
			},
			{name: "returns ErrConfig without Keys", edit: func(cfg *witness.ClientConfig) { cfg.Keys = nil }},
			{
				name: "returns ErrConfig for a key that is not Valid",
				edit: func(cfg *witness.ClientConfig) { cfg.Keys = []note.Key{{Name: "a"}} },
			},
			{
				name: "returns ErrConfig for two keys with one name and one key ID",
				edit: func(cfg *witness.ClientConfig) { cfg.Keys = []note.Key{key, key} },
			},
			{
				name: "returns ErrConfig for a relative Submission",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "witness.example/submit" },
			},
			{
				name: "returns ErrConfig for a Submission of another scheme",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "ftp://witness.example" },
			},
			{
				name: "returns ErrConfig for a Submission without a host",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https:///submit" },
			},
			{
				name: "returns ErrConfig for a Submission with a query",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https://witness.example?a=b" },
			},
			{
				name: "returns ErrConfig for a Submission with an empty query",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https://witness.example?" },
			},
			{
				name: "returns ErrConfig for a Submission with a fragment",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https://witness.example#a" },
			},
			{
				name: "returns ErrConfig for a Submission with a trailing slash",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https://witness.example/" },
			},
			{
				name: "returns ErrConfig for a Submission that does not parse",
				edit: func(cfg *witness.ClientConfig) { cfg.Submission = "https://witness.example/%zz" },
			},
			{
				name: "returns ErrConfig for a Monitoring with a trailing slash",
				edit: func(cfg *witness.ClientConfig) { cfg.Monitoring = "https://witness.example/monitor/" },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := clientConfig(t, newFakeWitness(t, nil), key)
				tt.edit(cfg)
				c, err := witness.NewClient(cfg)
				testkit.ErrorIs(t, err, witness.ErrConfig, "NewClient must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, c == nil, "NewClient must return a nil Client with an error")
			})
		}

		t.Run("returns the error of the Resolver for a key that it does not resolve", func(t *testing.T) {
			t.Parallel()
			cfg := clientConfig(t, newFakeWitness(t, nil), key)
			cfg.Resolver = note.Resolver{}
			_, err := witness.NewClient(cfg)
			testkit.ErrorIs(t, err, note.ErrUnknownType, "NewClient must return the error of the Resolver")
		})
	})

	t.Run("AddCheckpoint", func(t *testing.T) {
		t.Parallel()

		ed := ed25519Cosigner(t, witnessName)
		pq := mldsaCosigner(t, witnessName, fake.New(clockTime))
		msg := logNote(t, logName, 5)
		text := noteText(t, msg)
		proof := []crypto.Digest{
			crypto.NewDigest256(sha256.Sum256([]byte("1"))),
			crypto.NewDigest256(sha256.Sum256([]byte("2"))),
		}

		t.Run("sends the body of tlog-witness to the URL of add-checkpoint", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, ed) })
			c := newClient(t, w, ed.Key())
			_, err := c.AddCheckpoint(t.Context(), msg, 3, proof, nil)
			testkit.NoError(t, err, "AddCheckpoint must succeed")
			testkit.Equal(t, w.method.Load().(string), http.MethodPost, "AddCheckpoint must POST")
			testkit.Equal(t, w.path.Load().(string), submissionPath+"/add-checkpoint",
				"AddCheckpoint must call add-checkpoint")
			want := "old 3\n" + base64.StdEncoding.EncodeToString(proof[0].Bytes()) + "\n" +
				base64.StdEncoding.EncodeToString(proof[1].Bytes()) + "\n\n" + string(msg)
			testkit.Equal(t, string(w.body.Load().([]byte)), want, "AddCheckpoint must send the body of the protocol")
		})

		t.Run("appends the line of each key in the order of the configuration", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, pq, ed) })
			c := newClient(t, w, ed.Key(), pq.Key())
			got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
			testkit.NoError(t, err, "AddCheckpoint must succeed")
			testkit.True(t, strings.HasPrefix(string(got), "prefix:"), "AddCheckpoint must keep dst")
			lines := mustParse(t, append(append(append([]byte(nil), text...), '\n'), got[len("prefix:"):]...))
			testkit.Len(t, lines.Signatures, 2, "AddCheckpoint must append one line per key")
			testkit.Equal(t, lines.Signatures[0].ID, ed.Key().ID(), "the line of the first key must come first")
			testkit.Equal(t, lines.Signatures[1].ID, pq.Key().ID(), "the line of the second key must come second")
		})

		t.Run("keeps the body of a request intact after it returns", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, ed) })

			// The transport can read a body until it closes it, after the
			// call returns. GetBody reads the memory of the body, as the
			// transport does.
			var replays []func() (io.ReadCloser, error)

			h, err := httpclient.New("witness", required, httpclient.WithTimeout(10*time.Second),
				httpclient.WithPrepare(func(r *http.Request) error {
					replays = append(replays, r.GetBody)

					return nil
				}))
			testkit.NoError(t, err, "httpclient.New must accept the options")

			cfg := clientConfig(t, w, ed.Key())
			cfg.HTTP = h
			c, err := witness.NewClient(cfg)
			testkit.NoError(t, err, "NewClient must accept the configuration")

			_, err = c.AddCheckpoint(t.Context(), msg, 3, proof, nil)
			testkit.NoError(t, err, "the first call must succeed")
			_, err = c.AddCheckpoint(t.Context(), msg, 4, proof, nil)
			testkit.NoError(t, err, "the second call must succeed")

			rc, err := replays[0]()
			testkit.NoError(t, err, "GetBody must return the body")
			got, err := io.ReadAll(rc)
			testkit.NoError(t, err, "the body must read")
			testkit.True(t, strings.HasPrefix(string(got), "old 3\n"),
				"the second call must leave the first body intact")
		})

		t.Run("ignores the lines of other keys", func(t *testing.T) {
			t.Parallel()
			other := ed25519Cosigner(t, "example.com/other")
			w := newFakeWitness(t, func([]byte) (int, []byte) {
				return http.StatusOK, append(garbageLine(other.Key()), cosignLines(t, text, ed)...)
			})
			c := newClient(t, w, ed.Key())
			got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, nil)
			testkit.NoError(t, err, "AddCheckpoint must ignore a line of another key")
			testkit.Equal(t, string(got), string(cosignLines(t, text, ed)),
				"AddCheckpoint must append the line of the key")
		})

		failures := []struct {
			reply func(body []byte) (int, []byte)
			name  string
		}{
			{
				name: "returns ErrCosignature for an invalid line of a key",
				reply: func([]byte) (int, []byte) {
					return http.StatusOK, garbageLine(ed.Key())
				},
			},
			{
				name: "returns ErrCosignature for an invalid line of a key after a valid one",
				reply: func([]byte) (int, []byte) {
					return http.StatusOK, append(cosignLines(t, text, ed), garbageLine(ed.Key())...)
				},
			},
			{
				name: "returns ErrCosignature for a line whose timestamp is 0",
				reply: func([]byte) (int, []byte) {
					return http.StatusOK, cosignLines(t, text, mldsaCosigner(t, witnessName, nil))
				},
			},
			{
				name: "returns ErrCosignature for a key without a line",
				reply: func([]byte) (int, []byte) {
					return http.StatusOK, cosignLines(t, text, ed25519Cosigner(t, "example.com/other"))
				},
			},
			{
				name: "returns ErrCosignature for a response that is not a list of signature lines",
				reply: func([]byte) (int, []byte) {
					return http.StatusOK, []byte("not a signature line\n")
				},
			},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := newClient(t, newFakeWitness(t, tt.reply), ed.Key(), pq.Key())
				got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
				testkit.ErrorIs(t, err, witness.ErrCosignature, "AddCheckpoint must refuse the response")
				testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
				testkit.Equal(t, string(got), "prefix:", "AddCheckpoint must return dst unchanged")
			})
		}

		t.Run("returns a SizeError with the size of a 409", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusConflict, []byte("20852163\n") })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se, ok := errors.AsType[*witness.SizeError](err)
			testkit.True(t, ok, "AddCheckpoint must return a SizeError for a 409")
			testkit.Equal(t, se.Size, uint64(20852163), "the SizeError must contain the size of the body")
			testkit.Equal(t, errs.Classify(err), errs.Conflict, "the error must classify as Conflict")
		})

		t.Run("returns the StatusError of a 409 without a size", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusConflict, []byte("conflict") })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se, ok := errors.AsType[*httpclient.StatusError](err)
			testkit.True(t, ok, "AddCheckpoint must return the StatusError")
			testkit.Equal(t, se.Status, http.StatusConflict, "the StatusError must have the status 409")
		})

		t.Run("returns ErrInconsistent with the StatusError of a 422", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusUnprocessableEntity, nil })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			testkit.ErrorIs(t, err, witness.ErrInconsistent, "AddCheckpoint must return ErrInconsistent for a 422")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			se, ok := errors.AsType[*httpclient.StatusError](err)
			testkit.True(t, ok, "the error must wrap the StatusError")
			testkit.Equal(t, se.Status, http.StatusUnprocessableEntity, "the StatusError must have the status 422")
		})

		t.Run("returns the StatusError of a 403", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusForbidden, nil })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se, ok := errors.AsType[*httpclient.StatusError](err)
			testkit.True(t, ok, "AddCheckpoint must return the StatusError")
			testkit.Equal(t, se.Status, http.StatusForbidden, "the StatusError must have the status 403")
			testkit.Equal(t, errs.Classify(err), errs.Denied, "the error must classify as Denied")
		})

		t.Run("returns the error of a call to a witness whose server closed", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			c := newClient(t, w, ed.Key())
			w.server.Close()
			got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
			testkit.Error(t, err, "AddCheckpoint must fail without a witness")
			testkit.Equal(t, string(got), "prefix:", "AddCheckpoint must return dst unchanged")
		})

		t.Run("returns ErrRequest for a proof of 64 hashes before a request", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, make([]crypto.Digest, 64), nil)
			testkit.ErrorIs(t, err, witness.ErrRequest, "AddCheckpoint must refuse a proof of 64 hashes")
			testkit.Equal(t, w.requests.Load(), int64(0), "AddCheckpoint must not send the request")
		})

		t.Run("returns ErrRequest for a msg that is not a signed note before a request", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), []byte("not a note"), 0, nil, nil)
			testkit.ErrorIs(t, err, witness.ErrRequest, "AddCheckpoint must refuse a msg that is not a note")
			testkit.ErrorIs(t, err, note.ErrNote, "the error must wrap the error of the note")
			testkit.Equal(t, w.requests.Load(), int64(0), "AddCheckpoint must not send the request")
		})

		t.Run("returns the error of a request that net/http does not build", func(t *testing.T) {
			t.Parallel()
			var none context.Context
			_, err := newClient(t, newFakeWitness(t, nil), ed.Key()).AddCheckpoint(none, msg, 0, nil, nil)
			testkit.Error(t, err, "AddCheckpoint must refuse a nil context")
		})
	})

	t.Run("Checkpoint", func(t *testing.T) {
		t.Parallel()

		served := "the served checkpoint\n"
		sum := sha256.Sum256([]byte(logName))
		hash := hex.EncodeToString(sum[:])

		t.Run("appends what the witness serves for the origin", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, []byte(served) })
			got, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), logName,
				[]byte("prefix:"))
			testkit.NoError(t, err, "Checkpoint must succeed")
			testkit.True(t, ok, "Checkpoint must report the served checkpoint")
			testkit.Equal(t, string(got), "prefix:"+served, "Checkpoint must append the body")
			testkit.Equal(t, w.method.Load().(string), http.MethodGet, "Checkpoint must GET")
			testkit.Equal(t, w.path.Load().(string), monitoringPath+"/"+hash+"/checkpoint",
				"Checkpoint must request the route of the origin hash")
		})

		t.Run("reports false for a 404", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusNotFound, nil })
			got, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), logName,
				[]byte("prefix:"))
			testkit.NoError(t, err, "Checkpoint must not fail for a 404")
			testkit.False(t, ok, "Checkpoint must report false for a 404")
			testkit.Equal(t, string(got), "prefix:", "Checkpoint must return dst unchanged")
		})

		t.Run("returns the StatusError of a 503", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusServiceUnavailable, nil })
			got, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), logName,
				[]byte("prefix:"))
			se, isStatus := errors.AsType[*httpclient.StatusError](err)
			testkit.True(t, isStatus, "Checkpoint must return the StatusError")
			testkit.Equal(t, se.Status, http.StatusServiceUnavailable, "the StatusError must have the status 503")
			testkit.False(t, ok, "Checkpoint must report false with an error")
			testkit.Equal(t, string(got), "prefix:", "Checkpoint must return dst unchanged")
		})

		t.Run("returns ErrRequest for an origin that is not Valid", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), "", nil)
			testkit.ErrorIs(t, err, witness.ErrRequest, "Checkpoint must refuse an empty origin")
			testkit.False(t, ok, "Checkpoint must report false with an error")
			testkit.Equal(t, w.requests.Load(), int64(0), "Checkpoint must not send the request")
		})

		t.Run("returns the error of a request that net/http does not build", func(t *testing.T) {
			t.Parallel()
			var none context.Context
			_, _, err := newClient(t, newFakeWitness(t, nil), ed25519Cosigner(t, witnessName).Key()).Checkpoint(none,
				logName, nil)
			testkit.Error(t, err, "Checkpoint must refuse a nil context")
		})
	})
}

func BenchmarkClient(b *testing.B) {
	ed := ed25519Cosigner(b, witnessName)
	msg := logNote(b, logName, 5)
	lines := cosignLines(b, noteText(b, msg), ed)

	b.Run("NewClient", func(b *testing.B) {
		cfg := pipedConfig(b, nil, ed.Key())

		// The Client, its Verifiers and the URL of add-checkpoint, the URL of
		// the check of each prefix, and the 2 objects of the Verifier of an
		// Ed25519 cosignature key.
		c := bench.Start(b).MaxAllocs(7)
		defer c.End()

		var (
			got *witness.Client
			err error
		)

		for c.Loop() {
			got, err = witness.NewClient(cfg)
		}

		testkit.NoError(b, err, "the benchmark must measure a Client")
		testkit.True(b, got != nil, "the benchmark must measure a Client")
	})

	b.Run("AddCheckpoint", func(b *testing.B) {
		reply := append([]byte("HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(lines))+"\r\n\r\n"), lines...)
		client, err := witness.NewClient(pipedConfig(b, reply, ed.Key()))
		testkit.NoError(b, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, err = client.AddCheckpoint(b.Context(), msg, 0, nil, dst[:0])
		testkit.NoError(b, err, "AddCheckpoint must verify the lines")

		// The copy of the body and its reader, the 5 objects of the request
		// that net/http builds, and 59 of AppendFetch for a POST.
		c := bench.Start(b).MaxAllocs(66)
		defer c.End()

		var got []byte
		for c.Loop() {
			got, err = client.AddCheckpoint(b.Context(), msg, 0, nil, dst[:0])
		}

		testkit.NoError(b, err, "the benchmark must measure verified lines")
		testkit.Equal(b, string(got), string(lines), "the benchmark must measure the lines of the witness")
	})

	b.Run("Checkpoint", func(b *testing.B) {
		served := append(append([]byte(nil), msg...), lines...)
		reply := append([]byte("HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(served))+"\r\n\r\n"), served...)
		client, err := witness.NewClient(pipedConfig(b, reply, ed.Key()))
		testkit.NoError(b, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, _, err = client.Checkpoint(b.Context(), logName, dst[:0])
		testkit.NoError(b, err, "Checkpoint must read the served checkpoint")

		// The text of the URL, the 3 objects of the request that net/http
		// builds, and 55 of AppendFetch.
		c := bench.Start(b).MaxAllocs(59)
		defer c.End()

		var (
			got []byte
			ok  bool
		)

		for c.Loop() {
			got, ok, err = client.Checkpoint(b.Context(), logName, dst[:0])
		}

		testkit.NoError(b, err, "the benchmark must measure a served checkpoint")
		testkit.True(b, ok, "the benchmark must measure a served checkpoint")
		testkit.Equal(b, string(got), string(served), "the benchmark must measure what the witness serves")
	})
}

// newFakeWitness returns a test witness that responds to each request
// with reply, and with a 500 for a nil reply. The test closes its server.
func newFakeWitness(tb testing.TB, reply func(body []byte) (int, []byte)) *fakeWitness {
	tb.Helper()

	w := &fakeWitness{reply: reply}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)

			return
		}

		w.requests.Add(1)
		w.method.Store(r.Method)
		w.path.Store(r.URL.Path)
		w.body.Store(body)

		if w.reply == nil {
			rw.WriteHeader(http.StatusInternalServerError)

			return
		}

		status, out := w.reply(body)
		rw.WriteHeader(status)
		_, _ = rw.Write(out)
	}))
	tb.Cleanup(w.server.Close)

	return w
}

// clientConfig returns the configuration of a Client of the test witness
// w with keys, through a client of the HTTP of the tests.
func clientConfig(tb testing.TB, w *fakeWitness, keys ...note.Key) *witness.ClientConfig {
	tb.Helper()

	h, err := httpclient.New("witness", required, httpclient.WithTimeout(10*time.Second))
	testkit.NoError(tb, err, "httpclient.New must accept the options")

	return &witness.ClientConfig{
		HTTP:       h,
		Resolver:   resolver(),
		Submission: w.server.URL + submissionPath,
		Monitoring: w.server.URL + monitoringPath,
		Keys:       keys,
	}
}

// pipedConfig returns the configuration of a Client with keys of a witness
// at pipeHost. Its HTTP client dials, through httpclient.WithDialContext, a
// pipe to respond, which writes reply to every request, so its calls run
// through net/http's Client and Transport and connect to no network. The
// cleanup of tb closes the pipes.
func pipedConfig(tb testing.TB, reply []byte, keys ...note.Key) *witness.ClientConfig {
	tb.Helper()

	h, err := httpclient.New("witness", required, httpclient.WithHosts(pipeHost),
		httpclient.WithTimeout(10*time.Second),
		httpclient.WithDialContext(func(context.Context, string, string) (net.Conn, error) {
			conn, peer := net.Pipe()
			tb.Cleanup(func() { _ = peer.Close() })

			go respond(peer, reply)

			return conn, nil
		}))
	testkit.NoError(tb, err, "httpclient.New must accept the options")

	return &witness.ClientConfig{
		HTTP:       h,
		Resolver:   resolver(),
		Submission: "http://" + pipeHost + submissionPath,
		Monitoring: "http://" + pipeHost + monitoringPath,
		Keys:       keys,
	}
}

// respond writes reply to conn after each request that it reads from conn,
// until conn closes or a request fills its buffer of 16 KiB. It reads the
// header of a request and the body of the length of its Content-Length,
// and allocates nothing per request. It yields before each reply, so the
// transport reports the request written before it reads the response, as
// over a network.
func respond(conn net.Conn, reply []byte) {
	buf := make([]byte, 0, 16384)

	for {
		if size := requestSize(buf); size > 0 && len(buf) >= size {
			buf = buf[:copy(buf, buf[size:])]

			runtime.Gosched()

			if _, err := conn.Write(reply); err != nil {
				return
			}

			continue
		}

		if len(buf) == cap(buf) {
			return
		}

		n, err := conn.Read(buf[len(buf):cap(buf)])
		if err != nil {
			return
		}

		buf = buf[:len(buf)+n]
	}
}

// requestSize returns the length of the request at the start of buf: its
// header, and the body of the length of its Content-Length. It returns 0
// while buf contains no end of a header.
func requestSize(buf []byte) int {
	end := bytes.Index(buf, endOfHeader)
	if end < 0 {
		return 0
	}

	size := end + len(endOfHeader)

	i := bytes.Index(buf[:end], contentLength)
	if i < 0 {
		return size
	}

	n := 0

	for _, c := range buf[i+len(contentLength) : end] {
		if c < '0' || c > '9' {
			break
		}

		n = 10*n + int(c-'0')
	}

	return size + n
}

// newClient returns a Client of the test witness w with keys, and fails
// the test when NewClient refuses the configuration.
func newClient(tb testing.TB, w *fakeWitness, keys ...note.Key) *witness.Client {
	tb.Helper()

	c, err := witness.NewClient(clientConfig(tb, w, keys...))
	testkit.NoError(tb, err, "NewClient must accept the configuration")

	return c
}

// resolver returns the note.Resolver of the tests: type 0x01 over Ed25519,
// and the two cosignature types of tlog-cosignature.
func resolver() note.Resolver {
	return note.Resolver{
		note.TypeEd25519:                  note.Text(ed25519.Resolve),
		checkpoint.TypeEd25519Cosignature: checkpoint.CosignatureV1(ed25519.Resolve),
		checkpoint.TypeMLDSA44Cosignature: checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, "")),
	}
}

// ed25519Signer returns the Ed25519 signer whose seed is SHA-256 of label.
func ed25519Signer(tb testing.TB, label string) *ed25519.Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(label))

	s, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	testkit.NoError(tb, err, "the seed must give a key")

	return s
}

// ed25519Cosigner returns a cosigner of type 0x04 named name, over the
// Ed25519 key of name, with the time of a fake clock at clockTime.
func ed25519Cosigner(tb testing.TB, name note.Name) *checkpoint.CosignatureV1Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer(name, checkpoint.TypeEd25519Cosignature,
		ed25519Signer(tb, string(name)), fake.New(clockTime), time.Second)
	testkit.NoError(tb, err, "NewCosignatureV1Signer must accept the Ed25519 signer")

	return s
}

// mldsaCosigner returns a cosigner of type 0x06 named name, over the
// ML-DSA-44 key whose seed is SHA-256 of name, with the time of utc, or
// the timestamp 0 for a nil utc.
func mldsaCosigner(tb testing.TB, name note.Name, utc *fake.Clock) *checkpoint.SubtreeV1Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(name))

	inner, err := mldsa.New(mldsa.MLDSA44, seed[:], "")
	testkit.NoError(tb, err, "the seed must give a key")

	// A nil *fake.Clock in a clock.UTCSource would not be a nil source.
	var src clock.UTCSource
	if utc != nil {
		src = utc
	}

	s, err := checkpoint.NewSubtreeV1Signer(name, checkpoint.TypeMLDSA44Cosignature, inner, src, time.Second)
	testkit.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")

	return s
}

// logNote returns a checkpoint note of the log name for the tree of size
// leaves whose root is SHA-256 of the decimal of size, signed by the
// Ed25519 key of name.
func logNote(tb testing.TB, name note.Name, size uint64) []byte {
	tb.Helper()

	root := crypto.NewDigest256(sha256.Sum256([]byte(strconv.FormatUint(size, 10))))
	body := checkpoint.Body{Origin: checkpoint.Origin(name), Size: size, Root: root}

	text, err := body.MarshalText()
	testkit.NoError(tb, err, "MarshalText must write the body")

	s, err := note.NewTextSigner(name, note.TypeEd25519, ed25519Signer(tb, string(name)))
	testkit.NoError(tb, err, "NewTextSigner must accept the key")

	n, err := note.Sign(tb.Context(), text, s)
	testkit.NoError(tb, err, "Sign must sign the text")

	msg, err := n.MarshalText()
	testkit.NoError(tb, err, "MarshalText must write the note")

	return msg
}

// noteText returns the text of the signed note msg.
func noteText(tb testing.TB, msg []byte) []byte {
	tb.Helper()

	text, err := note.TextOf(msg)
	testkit.NoError(tb, err, "TextOf must accept the note")

	return text
}

// mustParse returns the note of msg, and fails the test when note.Parse
// refuses it.
func mustParse(tb testing.TB, msg []byte) *note.Note {
	tb.Helper()

	n, err := note.Parse(msg)
	testkit.NoError(tb, err, "note.Parse must accept the note")

	return &n
}

// cosignLines returns the signature lines of each cosigner over text, in
// the order of cosigners.
func cosignLines(tb testing.TB, text []byte, cosigners ...note.Signer) []byte {
	tb.Helper()

	n, err := note.Sign(tb.Context(), text, cosigners...)
	testkit.NoError(tb, err, "Sign must cosign the text")

	var lines []byte
	for _, s := range n.Signatures {
		lines, err = s.AppendText(lines)
		testkit.NoError(tb, err, "AppendText must write the line")
	}

	return lines
}

// garbageLine returns a signature line of k whose value is 72 zero bytes,
// a timestamp and a signature that no key made.
func garbageLine(k note.Key) []byte {
	line, _ := note.Signature{Name: k.Name, ID: k.ID(), Value: make([]byte, 72)}.AppendText(nil)

	return line
}
