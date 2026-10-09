// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

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

// collidingNames are two names whose keys of type 0x04 over the Ed25519
// key of witnessName have one key ID, 0x741f352e.
var collidingNames = [2]note.Name{"example.com/w61272", "example.com/w143085"}

// The allocation contracts of a Client of one Ed25519 cosignature key, over
// a pipe to a witness that writes one reply to every request.
const (
	// newClientAllocs is the allocation contract of NewClient: the Client,
	// its Verifiers and the URL of add-checkpoint, the URL of the check of
	// each prefix, and the 2 objects of the Verifier of the key.
	newClientAllocs = 7

	// clientAddCheckpointAllocs is the allocation contract of
	// AddCheckpoint: the 3 objects of the request that net/http builds, and
	// 63 of httpclient's AppendFetchBody for a POST of a body of 100 bytes
	// or more.
	clientAddCheckpointAllocs = 66

	// clientCheckpointAllocs is the allocation contract of Checkpoint: the
	// text of the URL, the 3 objects of the request that net/http builds,
	// and 56 of httpclient's AppendFetch.
	clientCheckpointAllocs = 60

	// pipeWarmup is the number of calls that a benchmark over the pipe
	// makes before it measures. They fill the runtime's per-processor
	// caches of the sudogs on which the goroutines of the pipe block. The
	// caches start empty, and the first 200 calls at 4 CPUs allocate about
	// 235 sudogs, one more allocation per call in their rounded mean.
	pipeWarmup = 1000
)

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
			assert.NoError(t, err, "NewClient must accept the configuration")
		})

		t.Run("returns ErrConfig for a nil configuration", func(t *testing.T) {
			t.Parallel()
			c, err := witness.NewClient(nil)
			assert.ErrorIs(t, err, witness.ErrConfig, "NewClient must refuse a nil configuration")
			assert.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			assert.Nil(t, c, "NewClient must return a nil Client with an error")
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
				assert.ErrorIs(t, err, witness.ErrConfig, "NewClient must refuse the configuration")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				assert.Nil(t, c, "NewClient must return a nil Client with an error")
			})
		}

		t.Run("returns a Client of two keys of other names with one key ID", func(t *testing.T) {
			t.Parallel()
			first, second := key, key
			first.Name, second.Name = collidingNames[0], collidingNames[1]
			assert.Equal(t, first.ID(), second.ID(), "the keys must share their key ID")

			_, err := witness.NewClient(clientConfig(t, newFakeWitness(t, nil), first, second))
			assert.NoError(t, err, "NewClient must accept keys of other names")
		})

		t.Run("returns a Client of two keys of one name with two key IDs", func(t *testing.T) {
			t.Parallel()
			pq := mldsaCosigner(t, witnessName, nil).Key()
			_, err := witness.NewClient(clientConfig(t, newFakeWitness(t, nil), key, pq))
			assert.NoError(t, err, "NewClient must accept two keys of one name")
		})

		t.Run("returns a Client of a prefix of the https scheme", func(t *testing.T) {
			t.Parallel()
			cfg := clientConfig(t, newFakeWitness(t, nil), key)
			cfg.Submission = "https://witness.example/submit"
			_, err := witness.NewClient(cfg)
			assert.NoError(t, err, "NewClient must accept an https prefix")
		})

		t.Run("returns the error of the Resolver for a key that it does not resolve", func(t *testing.T) {
			t.Parallel()
			cfg := clientConfig(t, newFakeWitness(t, nil), key)
			cfg.Resolver = note.Resolver{}
			_, err := witness.NewClient(cfg)
			assert.ErrorIs(t, err, note.ErrUnknownType, "NewClient must return the error of the Resolver")
		})
	})

	t.Run("AddCheckpoint", func(t *testing.T) {
		t.Parallel()

		ed := ed25519Cosigner(t, witnessName)
		pq := mldsaCosigner(t, witnessName, fake.New(clockTime))
		msg := logNote(t)
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
			assert.NoError(t, err, "AddCheckpoint must succeed")
			assert.Equal(t, w.method.Load().(string), http.MethodPost, "AddCheckpoint must POST")
			assert.Equal(t, w.path.Load().(string), submissionPath+"/add-checkpoint",
				"AddCheckpoint must call add-checkpoint")
			want := "old 3\n" + base64.StdEncoding.EncodeToString(proof[0].Bytes()) + "\n" +
				base64.StdEncoding.EncodeToString(proof[1].Bytes()) + "\n\n" + string(msg)
			assert.Equal(t, string(w.body.Load().([]byte)), want, "AddCheckpoint must send the body of the protocol")
		})

		t.Run("appends the line of each key in the order of the configuration", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, pq, ed) })
			c := newClient(t, w, ed.Key(), pq.Key())
			got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
			assert.NoError(t, err, "AddCheckpoint must succeed")
			assert.HasPrefix(t, string(got), "prefix:", "AddCheckpoint must keep dst")
			lines := mustParse(t, append(append(append([]byte(nil), text...), '\n'), got[len("prefix:"):]...))
			assert.Length(t, lines.Signatures, 2, "AddCheckpoint must append one line per key")
			assert.Equal(t, lines.Signatures[0].ID, ed.Key().ID(), "the line of the first key must come first")
			assert.Equal(t, lines.Signatures[1].ID, pq.Key().ID(), "the line of the second key must come second")
		})

		t.Run("leaves no byte of the body of its request to read after it returns", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, ed) })

			// The body goes back to the pool of the client when the call
			// returns, and net/http can read a body after its round trip
			// returned, through the body itself and through GetBody.
			var sent []*http.Request

			h, err := httpclient.New("witness", required, httpclient.WithTimeout(10*time.Second),
				httpclient.WithPrepare(func(r *http.Request) error {
					sent = append(sent, r)

					return nil
				}))
			assert.NoError(t, err, "httpclient.New must accept the options")

			cfg := clientConfig(t, w, ed.Key())
			cfg.HTTP = h
			c, err := witness.NewClient(cfg)
			assert.NoError(t, err, "NewClient must accept the configuration")

			_, err = c.AddCheckpoint(t.Context(), msg, 3, proof, nil)
			assert.NoError(t, err, "AddCheckpoint must succeed")
			assert.Length(t, sent, 1, "AddCheckpoint must send one request")

			n, err := sent[0].Body.Read(make([]byte, 16))
			expect.HasError(t, err, "a Read of the body after the call must fail")
			expect.Equal(t, n, 0, "a Read of the body after the call must read no byte")

			replayed, err := sent[0].GetBody()
			expect.HasError(t, err, "a GetBody after the call must fail")
			expect.Nil(t, replayed, "a GetBody after the call must return no body")
		})

		t.Run("returns the ErrCosignature of lines that AppendCosignatures refuses", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, garbageLine(ed.Key()) })
			got, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
			assert.ErrorIs(t, err, witness.ErrCosignature, "AddCheckpoint must refuse the response")
			assert.Equal(t, string(got), "prefix:", "AddCheckpoint must return dst unchanged")
		})

		t.Run("returns ErrInconsistent for a 422 whose body is a size", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusUnprocessableEntity, []byte("5\n") })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			assert.ErrorIs(t, err, witness.ErrInconsistent, "AddCheckpoint must return ErrInconsistent for a 422")
		})

		t.Run("returns a SizeError with the size of a 409", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusConflict, []byte("20852163\n") })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se := assert.ErrorAs[*witness.SizeError](t, err, "AddCheckpoint must return a SizeError for a 409")
			assert.Equal(t, se.Size, uint64(20852163), "the SizeError must contain the size of the body")
			assert.Equal(t, errs.Classify(err), errs.Conflict, "the error must classify as Conflict")
		})

		t.Run("returns the StatusError of a 409 without a size", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusConflict, []byte("conflict") })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "AddCheckpoint must return the StatusError")
			assert.Equal(t, se.Status, http.StatusConflict, "the StatusError must have the status 409")
		})

		t.Run("returns ErrInconsistent with the StatusError of a 422", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusUnprocessableEntity, nil })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			assert.ErrorIs(t, err, witness.ErrInconsistent, "AddCheckpoint must return ErrInconsistent for a 422")
			assert.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "the error must wrap the StatusError")
			assert.Equal(t, se.Status, http.StatusUnprocessableEntity, "the StatusError must have the status 422")
		})

		t.Run("returns the StatusError of a 403", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusForbidden, nil })
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, nil, nil)
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "AddCheckpoint must return the StatusError")
			assert.Equal(t, se.Status, http.StatusForbidden, "the StatusError must have the status 403")
			assert.Equal(t, errs.Classify(err), errs.Denied, "the error must classify as Denied")
		})

		t.Run("returns the error of a call to a witness whose server closed", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			c := newClient(t, w, ed.Key())
			w.server.Close()
			got, err := c.AddCheckpoint(t.Context(), msg, 0, nil, []byte("prefix:"))
			assert.HasError(t, err, "AddCheckpoint must fail without a witness")
			assert.Equal(t, string(got), "prefix:", "AddCheckpoint must return dst unchanged")
		})

		t.Run("sends a proof of 63 hashes", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusOK, cosignLines(t, text, ed) })

			long := make([]crypto.Digest, 63)
			for i := range long {
				long[i] = crypto.NewDigest256(sha256.Sum256([]byte(strconv.Itoa(i))))
			}

			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 3, long, nil)
			assert.NoError(t, err, "AddCheckpoint must send a proof of 63 hashes")
			assert.Equal(t, w.requests.Load(), int64(1), "AddCheckpoint must send the request")
		})

		t.Run("returns ErrRequest for a proof of 64 hashes before a request", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), msg, 0, make([]crypto.Digest, 64), nil)
			assert.ErrorIs(t, err, witness.ErrRequest, "AddCheckpoint must refuse a proof of 64 hashes")
			assert.Equal(t, w.requests.Load(), int64(0), "AddCheckpoint must not send the request")
		})

		t.Run("returns ErrRequest for a msg that is not a signed note before a request", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, err := newClient(t, w, ed.Key()).AddCheckpoint(t.Context(), []byte("not a note"), 0, nil, nil)
			assert.That(t, err).
				ErrorIs(witness.ErrRequest, "AddCheckpoint must refuse a msg that is not a note").
				ErrorIs(note.ErrNote, "the error must wrap the error of the note")
			assert.Equal(t, w.requests.Load(), int64(0), "AddCheckpoint must not send the request")
		})

		t.Run("returns the error of a request that net/http does not build", func(t *testing.T) {
			t.Parallel()
			var none context.Context
			_, err := newClient(t, newFakeWitness(t, nil), ed.Key()).AddCheckpoint(none, msg, 0, nil, nil)
			assert.HasError(t, err, "AddCheckpoint must refuse a nil context")
		})
	})

	t.Run("AppendCosignatures", func(t *testing.T) {
		t.Parallel()

		ed := ed25519Cosigner(t, witnessName)
		pq := mldsaCosigner(t, witnessName, fake.New(clockTime))
		text := noteText(t, logNote(t))

		t.Run("appends the line of each key in the order of the configuration", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, newFakeWitness(t, nil), ed.Key(), pq.Key())
			got, err := c.AppendCosignatures([]byte("prefix:"), text, cosignLines(t, text, pq, ed))
			assert.NoError(t, err, "AppendCosignatures must accept the lines")
			assert.HasPrefix(t, string(got), "prefix:", "AppendCosignatures must keep dst")
			lines := mustParse(t, append(append(append([]byte(nil), text...), '\n'), got[len("prefix:"):]...))
			assert.Length(t, lines.Signatures, 2, "AppendCosignatures must append one line per key")
			assert.Equal(t, lines.Signatures[0].ID, ed.Key().ID(), "the line of the first key must come first")
			assert.Equal(t, lines.Signatures[1].ID, pq.Key().ID(), "the line of the second key must come second")
		})

		t.Run("appends the first line of a key with two lines", func(t *testing.T) {
			t.Parallel()
			later, err := checkpoint.NewCosignatureV1Signer(witnessName, checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, witnessName), fake.New(clockTime.Add(time.Hour)), time.Second)
			assert.NoError(t, err, "NewCosignatureV1Signer must accept the key")

			lines := append(cosignLines(t, text, ed), cosignLines(t, text, later)...)
			got, err := newClient(t, newFakeWitness(t, nil), ed.Key()).AppendCosignatures(nil, text, lines)
			assert.NoError(t, err, "AppendCosignatures must accept both lines")
			assert.Equal(t, string(got), string(cosignLines(t, text, ed)),
				"AppendCosignatures must append the first line of the key")
		})

		t.Run("ignores the lines of other keys", func(t *testing.T) {
			t.Parallel()
			other := ed25519Cosigner(t, "example.com/other")
			lines := append(garbageLine(other.Key()), cosignLines(t, text, ed)...)
			got, err := newClient(t, newFakeWitness(t, nil), ed.Key()).AppendCosignatures(nil, text, lines)
			assert.NoError(t, err, "AppendCosignatures must ignore a line of another key")
			assert.Equal(t, string(got), string(cosignLines(t, text, ed)),
				"AppendCosignatures must append the line of the key")
		})

		t.Run("ignores a line of another name with the key ID of a key", func(t *testing.T) {
			t.Parallel()
			sig := note.Signature{Name: "example.com/other", ID: ed.Key().ID(), Value: make([]byte, 72)}
			lines, _ := sig.AppendText(nil)
			lines = append(lines, cosignLines(t, text, ed)...)
			got, err := newClient(t, newFakeWitness(t, nil), ed.Key()).AppendCosignatures(nil, text, lines)
			assert.NoError(t, err, "AppendCosignatures must ignore the line of another name")
			assert.Equal(t, string(got), string(cosignLines(t, text, ed)),
				"AppendCosignatures must append the line of the key")
		})

		failures := []struct {
			lines func(tb testing.TB) []byte
			name  string
		}{
			{
				name:  "returns ErrCosignature for an invalid line of a key",
				lines: func(testing.TB) []byte { return garbageLine(ed.Key()) },
			},
			{
				name: "returns ErrCosignature for an invalid line of a key after a valid one",
				lines: func(tb testing.TB) []byte {
					tb.Helper()

					return append(cosignLines(tb, text, ed), garbageLine(ed.Key())...)
				},
			},
			{
				name: "returns ErrCosignature for an invalid line of a key beside a valid line of every other key",
				lines: func(tb testing.TB) []byte {
					tb.Helper()

					return append(garbageLine(ed.Key()), cosignLines(tb, text, pq)...)
				},
			},
			{
				name: "returns ErrCosignature for a line whose timestamp is 0 beside a valid line of every other key",
				lines: func(tb testing.TB) []byte {
					tb.Helper()

					return append(cosignLines(tb, text, ed),
						cosignLines(tb, text, mldsaCosigner(tb, witnessName, nil))...)
				},
			},
			{
				name: "returns ErrCosignature for a key without a line",
				lines: func(tb testing.TB) []byte {
					tb.Helper()

					return cosignLines(tb, text, ed25519Cosigner(tb, "example.com/other"))
				},
			},
			{
				name:  "returns ErrCosignature for lines that are not signature lines",
				lines: func(testing.TB) []byte { return []byte("not a signature line\n") },
			},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := newClient(t, newFakeWitness(t, nil), ed.Key(), pq.Key())
				got, err := c.AppendCosignatures([]byte("prefix:"), text, tt.lines(t))
				assert.ErrorIs(t, err, witness.ErrCosignature, "AppendCosignatures must refuse the lines")
				assert.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
				assert.Equal(t, string(got), "prefix:", "AppendCosignatures must return dst unchanged")
			})
		}

		t.Run("returns an ErrCosignature that names lines that are not signature lines", func(t *testing.T) {
			t.Parallel()
			c := newClient(t, newFakeWitness(t, nil), ed.Key())
			_, err := c.AppendCosignatures(nil, text, []byte("not a signature line\n"))
			assert.ErrorIs(t, err, witness.ErrCosignature, "AppendCosignatures must refuse the lines")
			assert.Contains(t, err.Error(), "not a list of signature lines", "the error must name the lines")
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
			assert.NoError(t, err, "Checkpoint must succeed")
			assert.True(t, ok, "Checkpoint must report the served checkpoint")
			assert.Equal(t, string(got), "prefix:"+served, "Checkpoint must append the body")
			assert.Equal(t, w.method.Load().(string), http.MethodGet, "Checkpoint must GET")
			assert.Equal(t, w.path.Load().(string), monitoringPath+"/"+hash+"/checkpoint",
				"Checkpoint must request the route of the origin hash")
		})

		t.Run("reports false for a 404", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusNotFound, nil })
			got, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), logName,
				[]byte("prefix:"))
			assert.NoError(t, err, "Checkpoint must not fail for a 404")
			assert.False(t, ok, "Checkpoint must report false for a 404")
			assert.Equal(t, string(got), "prefix:", "Checkpoint must return dst unchanged")
		})

		t.Run("returns the StatusError of a 503", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, func([]byte) (int, []byte) { return http.StatusServiceUnavailable, nil })
			got, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), logName,
				[]byte("prefix:"))
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "Checkpoint must return the StatusError")
			assert.Equal(t, se.Status, http.StatusServiceUnavailable, "the StatusError must have the status 503")
			assert.False(t, ok, "Checkpoint must report false with an error")
			assert.Equal(t, string(got), "prefix:", "Checkpoint must return dst unchanged")
		})

		t.Run("returns ErrRequest for an origin that is not Valid", func(t *testing.T) {
			t.Parallel()
			w := newFakeWitness(t, nil)
			_, ok, err := newClient(t, w, ed25519Cosigner(t, witnessName).Key()).Checkpoint(t.Context(), "", nil)
			assert.ErrorIs(t, err, witness.ErrRequest, "Checkpoint must refuse an empty origin")
			assert.False(t, ok, "Checkpoint must report false with an error")
			assert.Equal(t, w.requests.Load(), int64(0), "Checkpoint must not send the request")
		})

		t.Run("returns the error of a request that net/http does not build", func(t *testing.T) {
			t.Parallel()
			var none context.Context
			_, _, err := newClient(t, newFakeWitness(t, nil), ed25519Cosigner(t, witnessName).Key()).Checkpoint(none,
				logName, nil)
			assert.HasError(t, err, "Checkpoint must refuse a nil context")
		})
	})
}

// TestClientAllocs checks the allocation contracts of the methods of Client
// that BenchmarkClient states. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestClientAllocs(t *testing.T) {
	ed := ed25519Cosigner(t, witnessName)
	msg := logNote(t)
	lines := cosignLines(t, noteText(t, msg), ed)

	t.Run("NewClient", func(t *testing.T) {
		cfg := pipedConfig(t, nil, ed.Key())

		var (
			got *witness.Client
			err error
		)

		expect.MaxAllocs(t, func() { got, err = witness.NewClient(cfg) }, newClientAllocs,
			"NewClient must allocate the objects of its contract alone")
		assert.NoError(t, err, "the test must measure a Client")
		assert.NotNil(t, got, "the test must measure a Client")
	})

	t.Run("AddCheckpoint", func(t *testing.T) {
		client, err := witness.NewClient(pipedConfig(t, lines, ed.Key()))
		assert.NoError(t, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, err = client.AddCheckpoint(t.Context(), msg, 0, nil, dst[:0])
		assert.NoError(t, err, "AddCheckpoint must verify the lines")

		var got []byte
		expect.MaxAllocs(t, func() { got, err = client.AddCheckpoint(t.Context(), msg, 0, nil, dst[:0]) },
			clientAddCheckpointAllocs, "AddCheckpoint must allocate the objects of its contract alone")
		assert.NoError(t, err, "the test must measure verified lines")
		assert.Equal(t, string(got), string(lines), "the test must measure the lines of the witness")
	})

	t.Run("AppendCosignatures", func(t *testing.T) {
		client, err := witness.NewClient(pipedConfig(t, nil, ed.Key()))
		assert.NoError(t, err, "NewClient must accept the configuration")

		text := noteText(t, msg)
		dst := make([]byte, 0, len(lines))

		// The pools of the package keep the buffer and the note of the call
		// that MaxAllocs makes first, and the measured calls reuse them.
		runtime.GC()
		runtime.GC()

		var got []byte
		expect.MaxAllocs(t, func() { got, err = client.AppendCosignatures(dst[:0], text, lines) }, 0,
			"AppendCosignatures must not allocate into a buffer with room")
		assert.NoError(t, err, "the test must measure verified lines")
		assert.Equal(t, string(got), string(lines), "the test must measure the lines of the witness")
	})

	t.Run("Checkpoint", func(t *testing.T) {
		served := append(append([]byte(nil), msg...), lines...)
		client, err := witness.NewClient(pipedConfig(t, served, ed.Key()))
		assert.NoError(t, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, _, err = client.Checkpoint(t.Context(), logName, dst[:0])
		assert.NoError(t, err, "Checkpoint must read the served checkpoint")

		var (
			got []byte
			ok  bool
		)

		expect.MaxAllocs(t, func() { got, ok, err = client.Checkpoint(t.Context(), logName, dst[:0]) },
			clientCheckpointAllocs, "Checkpoint must allocate the objects of its contract alone")
		assert.NoError(t, err, "the test must measure a served checkpoint")
		assert.True(t, ok, "the test must measure a served checkpoint")
		assert.Equal(t, string(got), string(served), "the test must measure what the witness serves")
	})
}

func BenchmarkClient(b *testing.B) {
	ed := ed25519Cosigner(b, witnessName)
	msg := logNote(b)
	lines := cosignLines(b, noteText(b, msg), ed)

	b.Run("NewClient", func(b *testing.B) {
		cfg := pipedConfig(b, nil, ed.Key())

		c := bench.Start(b).MaxAllocs(newClientAllocs)
		defer c.End()

		var (
			got *witness.Client
			err error
		)

		for c.Loop() {
			got, err = witness.NewClient(cfg)
		}

		assert.NoError(b, err, "the benchmark must measure a Client")
		assert.NotNil(b, got, "the benchmark must measure a Client")
	})

	b.Run("AddCheckpoint", func(b *testing.B) {
		client, err := witness.NewClient(pipedConfig(b, lines, ed.Key()))
		assert.NoError(b, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, err = client.AddCheckpoint(b.Context(), msg, 0, nil, dst[:0])
		assert.NoError(b, err, "AddCheckpoint must verify the lines")

		c := bench.Start(b).Warmup(pipeWarmup).MaxAllocs(clientAddCheckpointAllocs)
		defer c.End()

		var got []byte
		for c.Loop() {
			got, err = client.AddCheckpoint(b.Context(), msg, 0, nil, dst[:0])
		}

		assert.NoError(b, err, "the benchmark must measure verified lines")
		assert.Equal(b, string(got), string(lines), "the benchmark must measure the lines of the witness")
	})

	b.Run("AppendCosignatures", func(b *testing.B) {
		client, err := witness.NewClient(pipedConfig(b, nil, ed.Key()))
		assert.NoError(b, err, "NewClient must accept the configuration")

		text := noteText(b, msg)
		dst := make([]byte, 0, len(lines))

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got []byte
		for c.Loop() {
			got, err = client.AppendCosignatures(dst[:0], text, lines)
		}

		assert.NoError(b, err, "the benchmark must measure verified lines")
		assert.Equal(b, string(got), string(lines), "the benchmark must measure the lines of the witness")
	})

	b.Run("Checkpoint", func(b *testing.B) {
		served := append(append([]byte(nil), msg...), lines...)
		client, err := witness.NewClient(pipedConfig(b, served, ed.Key()))
		assert.NoError(b, err, "NewClient must accept the configuration")

		// The first call dials the pipe, which the later calls reuse.
		dst := make([]byte, 0, 1024)
		_, _, err = client.Checkpoint(b.Context(), logName, dst[:0])
		assert.NoError(b, err, "Checkpoint must read the served checkpoint")

		c := bench.Start(b).Warmup(pipeWarmup).MaxAllocs(clientCheckpointAllocs)
		defer c.End()

		var (
			got []byte
			ok  bool
		)

		for c.Loop() {
			got, ok, err = client.Checkpoint(b.Context(), logName, dst[:0])
		}

		assert.NoError(b, err, "the benchmark must measure a served checkpoint")
		assert.True(b, ok, "the benchmark must measure a served checkpoint")
		assert.Equal(b, string(got), string(served), "the benchmark must measure what the witness serves")
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
	assert.NoError(tb, err, "httpclient.New must accept the options")

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
// pipe to respond, which writes a 200 with body to every request, so its
// calls run through net/http's Client and Transport and connect to no
// network. The cleanup of tb closes the pipes.
func pipedConfig(tb testing.TB, body []byte, keys ...note.Key) *witness.ClientConfig {
	tb.Helper()

	reply := append([]byte("HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"), body...)

	h, err := httpclient.New("witness", required, httpclient.WithHosts(pipeHost),
		httpclient.WithTimeout(10*time.Second),
		httpclient.WithDialContext(func(context.Context, string, string) (net.Conn, error) {
			conn, peer := net.Pipe()
			tb.Cleanup(func() { _ = peer.Close() })

			go respond(peer, reply)

			return conn, nil
		}))
	assert.NoError(tb, err, "httpclient.New must accept the options")

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
	assert.NoError(tb, err, "NewClient must accept the configuration")

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
	assert.NoError(tb, err, "the seed must give a key")

	return s
}

// ed25519Cosigner returns a cosigner of type 0x04 named name, over the
// Ed25519 key of name, with the time of a fake clock at clockTime.
func ed25519Cosigner(tb testing.TB, name note.Name) *checkpoint.CosignatureV1Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer(name, checkpoint.TypeEd25519Cosignature,
		ed25519Signer(tb, string(name)), fake.New(clockTime), time.Second)
	assert.NoError(tb, err, "NewCosignatureV1Signer must accept the Ed25519 signer")

	return s
}

// mldsaCosigner returns a cosigner of type 0x06 named name, over the
// ML-DSA-44 key whose seed is SHA-256 of name, with the time of utc, or
// the timestamp 0 for a nil utc.
func mldsaCosigner(tb testing.TB, name note.Name, utc *fake.Clock) *checkpoint.SubtreeV1Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(name))

	inner, err := mldsa.New(mldsa.MLDSA44, seed[:], "")
	assert.NoError(tb, err, "the seed must give a key")

	// A nil *fake.Clock in a clock.UTCSource would not be a nil source.
	var src clock.UTCSource
	if utc != nil {
		src = utc
	}

	s, err := checkpoint.NewSubtreeV1Signer(name, checkpoint.TypeMLDSA44Cosignature, inner, src, time.Second)
	assert.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")

	return s
}

// logNote returns the checkpoint note of the log logName for a tree of 5
// leaves, whose root is the SHA-256 of "5", signed by the Ed25519 key of
// logName.
func logNote(tb testing.TB) []byte {
	tb.Helper()

	root := crypto.NewDigest256(sha256.Sum256([]byte("5")))
	body := checkpoint.Body{Origin: logName, Size: 5, Root: root}

	text, err := body.MarshalText()
	assert.NoError(tb, err, "MarshalText must write the body")

	s, err := note.NewTextSigner(logName, note.TypeEd25519, ed25519Signer(tb, logName))
	assert.NoError(tb, err, "NewTextSigner must accept the key")

	n, err := note.Sign(tb.Context(), text, s)
	assert.NoError(tb, err, "Sign must sign the text")

	msg, err := n.MarshalText()
	assert.NoError(tb, err, "MarshalText must write the note")

	return msg
}

// noteText returns the text of the signed note msg.
func noteText(tb testing.TB, msg []byte) []byte {
	tb.Helper()

	text, err := note.TextOf(msg)
	assert.NoError(tb, err, "TextOf must accept the note")

	return text
}

// mustParse returns the note of msg, and fails the test when note.Parse
// refuses it.
func mustParse(tb testing.TB, msg []byte) *note.Note {
	tb.Helper()

	n, err := note.Parse(msg)
	assert.NoError(tb, err, "note.Parse must accept the note")

	return &n
}

// cosignLines returns the signature lines of each cosigner over text, in
// the order of cosigners.
func cosignLines(tb testing.TB, text []byte, cosigners ...note.Signer) []byte {
	tb.Helper()

	n, err := note.Sign(tb.Context(), text, cosigners...)
	assert.NoError(tb, err, "Sign must cosign the text")

	var lines []byte
	for _, s := range n.Signatures {
		lines, err = s.AppendText(lines)
		assert.NoError(tb, err, "AppendText must write the line")
	}

	return lines
}

// garbageLine returns a signature line of k whose value is the timestamp of
// clockTime and 64 zero bytes, a signature that no key made.
func garbageLine(k note.Key) []byte {
	value := binary.BigEndian.AppendUint64(nil, uint64(clockTime.Unix()))
	line, _ := note.Signature{Name: k.Name, ID: k.ID(), Value: append(value, make([]byte, 64)...)}.AppendText(nil)

	return line
}
