// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/tlog"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

// The bounds of the servers of the tests.
const (
	testMaxOrigins  = 100
	testRetention   = time.Hour
	testMaxError    = 10 * time.Millisecond
	testTimeout     = 5 * time.Second
	testSignTimeout = 5 * time.Second
)

// treeLeaves is the number of leaves of the tree of each test log.
const treeLeaves = 64

// testLog is a log of the tests: an Ed25519 key over SHA-256 trees, the
// leaves of its tree, and the note of each size of the tree.
type testLog struct {
	// log is the witness.Log of the log.
	log witness.Log

	// origin is the origin, and signer signs its notes.
	origin checkpoint.Origin
	signer *note.TextSigner

	// leaves are the leaf hashes of the tree, and notes the checkpoint
	// note of each size from 0 to treeLeaves.
	leaves []crypto.Digest
	notes  [][]byte
}

// newTestLog returns a test log of origin, whose key is the Ed25519 key of
// origin.
func newTestLog(tb testing.TB, origin string) *testLog {
	tb.Helper()

	s, err := note.NewTextSigner(note.Name(origin), note.TypeEd25519, ed25519Signer(tb, origin))
	testkit.NoError(tb, err, "NewTextSigner must accept the key")

	h := coresha256.New()
	l := &testLog{
		log:    witness.Log{Hasher: h, Keys: [][]note.Key{{s.Key()}}},
		origin: checkpoint.Origin(origin),
		signer: s,
		leaves: make([]crypto.Digest, treeLeaves),
		notes:  make([][]byte, treeLeaves+1),
	}

	for i := range l.leaves {
		l.leaves[i] = tlog.LeafHash(h, []byte(origin+"/"+strconv.Itoa(i)))
	}

	for size := range l.notes {
		l.notes[size] = signBody(tb, l.body(uint64(size)), s)
	}

	return l
}

// body returns the checkpoint body of the tree of l at size.
func (l *testLog) body(size uint64) checkpoint.Body {
	root := tlog.Root(l.log.Hasher, l.leaves[:size])

	return checkpoint.Body{Origin: l.origin, Size: size, Root: root}
}

// update returns the update of l from oldSize to size, with its
// consistency proof.
func (l *testLog) update(tb testing.TB, oldSize, size uint64) witness.Update {
	tb.Helper()

	u := witness.Update{Body: l.body(size), OldSize: oldSize}
	if oldSize == 0 || oldSize == size {
		return u
	}

	proof, err := tlog.ConsistencyProof(l.log.Hasher, l.leaves[:size], oldSize, nil)
	testkit.NoError(tb, err, "ConsistencyProof must prove the old size")

	u.Proof = proof

	return u
}

// signBody returns the signed note of the text of body, with a line of
// each signer.
func signBody(tb testing.TB, body checkpoint.Body, signers ...note.Signer) []byte {
	tb.Helper()

	text, err := body.MarshalText()
	testkit.NoError(tb, err, "MarshalText must write the body")

	n, err := note.Sign(tb.Context(), text, signers...)
	testkit.NoError(tb, err, "Sign must sign the text")

	msg, err := n.MarshalText()
	testkit.NoError(tb, err, "MarshalText must write the note")

	return msg
}

// records is a slog.Handler that keeps every record.
type records struct {
	list []slog.Record
	mu   sync.Mutex
}

// Enabled reports true for every level.
func (*records) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle keeps a clone of r.
func (h *records) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // the signature of slog.Handler
	h.mu.Lock()
	defer h.mu.Unlock()

	h.list = append(h.list, r.Clone())

	return nil
}

// WithAttrs returns h, whose records keep no attributes of the logger.
func (h *records) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

// WithGroup returns h.
func (h *records) WithGroup(string) slog.Handler {
	return h
}

// messages returns the messages of the records at level.
func (h *records) messages(level slog.Level) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []string

	for i := range h.list {
		if h.list[i].Level == level {
			out = append(out, h.list[i].Message)
		}
	}

	return out
}

// fixture is the configuration of the servers of a test: one store, one
// fake clock as the clock and the UTC source, one breaker, the logs that
// the witness accepts, and the cosigners.
type fixture struct {
	store     *store
	clock     *fake.Clock
	breaker   *resilience.Breaker
	logger    *records
	accepted  map[checkpoint.Origin]witness.Log
	cosigners []checkpoint.Cosigner
	mu        sync.Mutex
}

// newFixture returns a fixture that accepts logs, with an Ed25519 cosigner
// of the witness.
func newFixture(tb testing.TB, logs ...*testLog) *fixture {
	tb.Helper()

	c := fake.New(clockTime)

	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Clock:            c,
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenFor:          time.Minute,
	})
	testkit.NoError(tb, err, "NewBreaker must accept the configuration")

	f := &fixture{
		store:     newStore(c),
		clock:     c,
		breaker:   b,
		logger:    new(records),
		accepted:  make(map[checkpoint.Origin]witness.Log),
		cosigners: []checkpoint.Cosigner{ed25519Cosigner(tb, witnessName)},
	}

	for _, l := range logs {
		f.accepted[l.origin] = l.log
	}

	return f
}

// logs returns the log of origin that f accepts.
func (f *fixture) logs(origin checkpoint.Origin) (witness.Log, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	l, ok := f.accepted[origin]

	return l, ok
}

// config returns the configuration of a server of f.
func (f *fixture) config() *witness.ServerConfig {
	return &witness.ServerConfig{
		Logs:        f.logs,
		Breaker:     f.breaker,
		Logger:      slog.New(f.logger),
		Resolver:    resolver(),
		State:       f.store,
		UTC:         f.clock,
		Clock:       f.clock,
		Reporter:    noop.Reporter{},
		Cosigners:   f.cosigners,
		MaxOrigins:  testMaxOrigins,
		Retention:   testRetention,
		MaxError:    testMaxError,
		Timeout:     testTimeout,
		SignTimeout: testSignTimeout,
	}
}

// newServer returns a server of cfg, and fails the test when NewServer
// refuses it.
func newServer(tb testing.TB, cfg *witness.ServerConfig) *witness.Server {
	tb.Helper()

	s, err := witness.NewServer(tb.Context(), cfg)
	testkit.NoError(tb, err, "NewServer must accept the configuration")

	return s
}

// advance advances the updates of l under the note of the size of the last
// update, through s, and fails the test when Advance fails.
func advance(tb testing.TB, s *witness.Server, l *testLog, updates ...witness.Update) []byte {
	tb.Helper()

	lines, failures, err := s.Advance(tb.Context(), l.notes[updates[len(updates)-1].Body.Size], updates, nil)
	testkit.NoError(tb, err, "Advance must commit the updates")
	testkit.Len(tb, failures, 0, "Advance must return no failure")

	return lines
}

// refuseLines makes the store of f refuse every write of lines, and
// returns the number of writes that it refused. A commit writes its lines
// after Advance returns, so a test waits for that number.
func refuseLines(f *fixture) *atomic.Int64 {
	var refused atomic.Int64

	f.store.intercept(hook{op: opPut, prefix: "lines/", before: func(context.Context, string) error {
		refused.Add(1)

		return errors.New("the store refuses the lines")
	}})

	return &refused
}

func TestServer(t *testing.T) {
	t.Parallel()

	t.Run("NewServer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Server of an empty store", func(t *testing.T) {
			t.Parallel()
			newServer(t, newFixture(t).config())
		})

		t.Run("returns ErrConfig for a nil configuration", func(t *testing.T) {
			t.Parallel()
			s, err := witness.NewServer(t.Context(), nil)
			testkit.ErrorIs(t, err, witness.ErrConfig, "NewServer must refuse a nil configuration")
			testkit.True(t, s == nil, "NewServer must return a nil Server with an error")
		})

		cosigner := ed25519Cosigner(t, witnessName)
		tests := []struct {
			edit func(cfg *witness.ServerConfig)
			name string
		}{
			{name: "returns ErrConfig without Logs", edit: func(cfg *witness.ServerConfig) { cfg.Logs = nil }},
			{name: "returns ErrConfig without a Breaker", edit: func(cfg *witness.ServerConfig) { cfg.Breaker = nil }},
			{name: "returns ErrConfig without a Logger", edit: func(cfg *witness.ServerConfig) { cfg.Logger = nil }},
			{
				name: "returns ErrConfig without a Resolver",
				edit: func(cfg *witness.ServerConfig) { cfg.Resolver = nil },
			},
			{name: "returns ErrConfig without a State", edit: func(cfg *witness.ServerConfig) { cfg.State = nil }},
			{name: "returns ErrConfig without a UTC source", edit: func(cfg *witness.ServerConfig) { cfg.UTC = nil }},
			{name: "returns ErrConfig without a Clock", edit: func(cfg *witness.ServerConfig) { cfg.Clock = nil }},
			{
				name: "returns ErrConfig without a Reporter",
				edit: func(cfg *witness.ServerConfig) { cfg.Reporter = nil },
			},
			{
				name: "returns ErrConfig without a cosigner",
				edit: func(cfg *witness.ServerConfig) { cfg.Cosigners = nil },
			},
			{
				name: "returns ErrConfig for a nil cosigner",
				edit: func(cfg *witness.ServerConfig) { cfg.Cosigners = []checkpoint.Cosigner{nil} },
			},
			{
				name: "returns ErrConfig for a cosigner without a key name",
				edit: func(cfg *witness.ServerConfig) {
					cfg.Cosigners = []checkpoint.Cosigner{&checkpoint.CosignatureV1Signer{}}
				},
			},
			{
				name: "returns ErrConfig for two cosigners with one key name and key ID",
				edit: func(cfg *witness.ServerConfig) { cfg.Cosigners = []checkpoint.Cosigner{cosigner, cosigner} },
			},
			{
				name: "returns ErrConfig for a negative MaxError",
				edit: func(cfg *witness.ServerConfig) { cfg.MaxError = -1 },
			},
			{
				name: "returns ErrConfig for a MaxOrigins of 0",
				edit: func(cfg *witness.ServerConfig) { cfg.MaxOrigins = 0 },
			},
			{
				name: "returns ErrConfig for a Retention of 0",
				edit: func(cfg *witness.ServerConfig) { cfg.Retention = 0 },
			},
			{name: "returns ErrConfig for a Timeout of 0", edit: func(cfg *witness.ServerConfig) { cfg.Timeout = 0 }},
			{
				name: "returns ErrConfig for a SignTimeout of 0",
				edit: func(cfg *witness.ServerConfig) { cfg.SignTimeout = 0 },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := newFixture(t).config()
				tt.edit(cfg)

				s, err := witness.NewServer(t.Context(), cfg)
				testkit.ErrorIs(t, err, witness.ErrConfig, "NewServer must refuse the configuration")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, s == nil, "NewServer must return a nil Server with an error")
			})
		}

		t.Run("accepts a MaxError of 0", func(t *testing.T) {
			t.Parallel()
			cfg := newFixture(t).config()
			cfg.MaxError = 0
			newServer(t, cfg)
		})
	})
}

func BenchmarkServer(b *testing.B) {
	b.Run("NewServer", func(b *testing.B) {
		cfg := newFixture(b).config()

		// Over an empty store and the reporter of telemetry/noop: 6 objects
		// of the Server, its maps, its slices, the target of a circuit and
		// the method value of its commit; 2 of its pool of calls; the 12
		// attribute sets that its instruments bind; 5 of its cache; 3 of its
		// state; and the error of blob/memory for the missing head.
		c := bench.Start(b).MaxAllocs(29)
		defer c.End()

		var (
			s   *witness.Server
			err error
		)

		for c.Loop() {
			s, err = witness.NewServer(b.Context(), cfg)
		}

		testkit.NoError(b, err, "the benchmark must measure a Server")
		testkit.True(b, s != nil, "the benchmark must measure a Server")
	})
}
