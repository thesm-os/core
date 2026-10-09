// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	coresha256 "go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/tlog"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The names of the witness and of the log of the internal tests.
const (
	internalWitness = "example.com/witness"
	internalLogName = "example.com/log"
)

// internalTime is the time of the fake clocks of the internal tests.
var internalTime = time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

// collidingNames are two names whose keys of type 0x04 over the Ed25519
// key of internalWitness have one key ID, 0x741f352e.
var collidingNames = [2]note.Name{"example.com/w61272", "example.com/w143085"}

// internalLog is a log of the internal tests: an Ed25519 key over SHA-256
// trees of 64 leaves.
type internalLog struct {
	log    Log
	origin checkpoint.Origin
	signer *note.TextSigner
	leaves []crypto.Digest
}

// internalFixture is the configuration of the servers of an internal test:
// one store, one fake clock, one breaker, and the logs that the witness
// accepts.
type internalFixture struct {
	store    *memory.Store
	clock    *fake.Clock
	breaker  *resilience.Breaker
	accepted map[checkpoint.Origin]Log
	signer   checkpoint.Cosigner
	mu       sync.Mutex
}

func TestServerInternal(t *testing.T) {
	t.Parallel()

	t.Run("NewServer", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want int64
		}{
			{name: "bounds a snapshot at 64 MiB for few origins", give: 100, want: maxObjectBytes},
			{name: "bounds a snapshot at 1 KiB per origin for many origins", give: 1 << 20, want: 1 << 30},
			{
				name: "bounds a snapshot at 1 KiB per origin for the largest MaxOrigins whose product is an int64",
				give: math.MaxInt64 / snapshotOriginBytes,
				want: math.MaxInt64 / snapshotOriginBytes * snapshotOriginBytes,
			},
			{
				name: "bounds a snapshot at the largest int64 for the largest MaxOrigins", give: math.MaxInt,
				want: math.MaxInt64,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newInternalFixture(t)
				cfg := f.config()
				cfg.MaxOrigins = tt.give
				s, err := NewServer(bounded(t), cfg)
				assert.NoError(t, err, "NewServer must accept the configuration")
				assert.Equal(t, s.snapshotLimit, tt.want, "NewServer must bound the snapshots")
			})
		}

		// cosigner returns the Ed25519 cosigner named name over the key of
		// the SHA-256 of label.
		cosigner := func(tb testing.TB, f *internalFixture, name note.Name, label string) checkpoint.Cosigner {
			tb.Helper()

			seed := sha256.Sum256([]byte(label))

			k, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
			assert.NoError(tb, err, "the seed must give a key")

			c, err := checkpoint.NewCosignatureV1Signer(name, checkpoint.TypeEd25519Cosignature, k, f.clock,
				time.Second)
			assert.NoError(tb, err, "NewCosignatureV1Signer must accept the key")

			return c
		}

		t.Run("returns a Server of two cosigners of other names with one key ID", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			cfg := f.config()
			cfg.Cosigners = []checkpoint.Cosigner{
				cosigner(t, f, collidingNames[0], internalWitness), cosigner(t, f, collidingNames[1], internalWitness),
			}
			assert.Equal(t, cfg.Cosigners[0].Key().ID(), cfg.Cosigners[1].Key().ID(),
				"the cosigners must share a key ID")

			_, err := NewServer(bounded(t), cfg)
			assert.NoError(t, err, "NewServer must accept cosigners of other names")
		})

		t.Run("returns a Server of two cosigners of one name with two key IDs", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			cfg := f.config()
			cfg.Cosigners = append(cfg.Cosigners, cosigner(t, f, internalWitness, "another key"))

			_, err := NewServer(bounded(t), cfg)
			assert.NoError(t, err, "NewServer must accept cosigners of one name")
		})
	})
}

// newInternalLog returns a log of origin, whose key is the Ed25519 key of
// the SHA-256 of origin.
func newInternalLog(tb testing.TB, origin string) *internalLog {
	tb.Helper()

	seed := sha256.Sum256([]byte(origin))

	k, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	assert.NoError(tb, err, "the seed must give a key")

	s, err := note.NewTextSigner(note.Name(origin), note.TypeEd25519, k)
	assert.NoError(tb, err, "NewTextSigner must accept the key")

	h := coresha256.New()
	l := &internalLog{
		log:    Log{Hasher: h, Keys: [][]note.Key{{s.Key()}}},
		origin: checkpoint.Origin(origin),
		signer: s,
		leaves: make([]crypto.Digest, 64),
	}

	for i := range l.leaves {
		l.leaves[i] = tlog.LeafHash(h, []byte(origin+"/"+strconv.Itoa(i)))
	}

	return l
}

// note returns the signed checkpoint note of the tree of l at size.
func (l *internalLog) note(tb testing.TB, size uint64) []byte {
	tb.Helper()

	body := checkpoint.Body{Origin: l.origin, Size: size, Root: tlog.Root(l.log.Hasher, l.leaves[:size])}

	text, err := body.MarshalText()
	assert.NoError(tb, err, "MarshalText must write the body")

	n, err := note.Sign(tb.Context(), text, l.signer)
	assert.NoError(tb, err, "Sign must sign the text")

	msg, err := n.MarshalText()
	assert.NoError(tb, err, "MarshalText must write the note")

	return msg
}

// update returns the update of l from oldSize to size, with its prefix.
func (l *internalLog) update(tb testing.TB, oldSize, size uint64, prefix []byte) Update {
	tb.Helper()

	u := Update{
		Body:    checkpoint.Body{Origin: l.origin, Size: size, Root: tlog.Root(l.log.Hasher, l.leaves[:size])},
		OldSize: oldSize,
		Prefix:  prefix,
	}

	if oldSize > 0 && oldSize < size {
		proof, err := tlog.ConsistencyProof(l.log.Hasher, l.leaves[:size], oldSize, nil)
		assert.NoError(tb, err, "ConsistencyProof must prove the old size")

		u.Proof = proof
	}

	return u
}

// advance commits the update of l from oldSize to size through s, with
// prefix, and fails the test when Advance fails or returns no lines. It
// returns once the commit has ended the write of its lines. The commit
// locks the lmu of s before it delivers the call, and unlocks it when that
// write ends.
func (l *internalLog) advance(tb testing.TB, s *Server, oldSize, size uint64, prefix []byte) []byte {
	tb.Helper()

	lines, failures, err := s.Advance(bounded(tb), l.note(tb, size), []Update{l.update(tb, oldSize, size, prefix)},
		nil)
	assert.NoError(tb, err, "Advance must commit the update")
	assert.Empty(tb, failures, "Advance must return no failure")
	assert.NotEmpty(tb, lines, "Advance must return the cosignature lines")

	// The lock waits for the commit to unlock the lmu, which it does when
	// the write of its lines ends.
	func() {
		s.lmu.Lock()
		defer s.lmu.Unlock()
	}()

	return lines
}

// newInternalFixture returns a fixture that accepts logs, with an Ed25519
// cosigner of the witness.
func newInternalFixture(tb testing.TB, logs ...*internalLog) *internalFixture {
	tb.Helper()

	c := fake.New(internalTime)

	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Clock:            c,
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenFor:          time.Minute,
	})
	assert.NoError(tb, err, "NewBreaker must accept the configuration")

	seed := sha256.Sum256([]byte(internalWitness))

	k, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	assert.NoError(tb, err, "the seed must give a key")

	cs, err := checkpoint.NewCosignatureV1Signer(internalWitness, checkpoint.TypeEd25519Cosignature, k, c, time.Second)
	assert.NoError(tb, err, "NewCosignatureV1Signer must accept the key")

	f := &internalFixture{
		store:    memory.New(c),
		clock:    c,
		breaker:  b,
		accepted: make(map[checkpoint.Origin]Log),
		signer:   cs,
	}

	for _, l := range logs {
		f.accepted[l.origin] = l.log
	}

	return f
}

// logs returns the log of origin that f accepts.
func (f *internalFixture) logs(origin checkpoint.Origin) (Log, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	l, ok := f.accepted[origin]

	return l, ok
}

// refuse makes f refuse origin.
func (f *internalFixture) refuse(origin checkpoint.Origin) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.accepted, origin)
}

// config returns the configuration of a server of f.
func (f *internalFixture) config() *ServerConfig {
	return &ServerConfig{
		Logs:        f.logs,
		Breaker:     f.breaker,
		Logger:      slog.New(slog.DiscardHandler),
		Resolver:    note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)},
		State:       f.store,
		UTC:         f.clock,
		Clock:       f.clock,
		Reporter:    noop.Reporter{},
		Cosigners:   []checkpoint.Cosigner{f.signer},
		MaxOrigins:  20000,
		Retention:   time.Hour,
		MaxError:    10 * time.Millisecond,
		Timeout:     5 * time.Second,
		SignTimeout: 5 * time.Second,
	}
}

// server returns a server of f, and fails the test when NewServer refuses
// it.
func (f *internalFixture) server(tb testing.TB) *Server {
	tb.Helper()

	s, err := NewServer(bounded(tb), f.config())
	assert.NoError(tb, err, "NewServer must accept the configuration")

	return s
}
