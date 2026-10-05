// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// ServerConfig configures a [Server]. Every field is required.
type ServerConfig struct {
	// Logs returns the log of an origin, and false for an origin that the
	// witness does not accept. It is a function, so a deployment can
	// accept every origin under a prefix.
	Logs func(checkpoint.Origin) (Log, bool)

	// Breaker keeps one circuit per cosigner, whose target is the key name
	// of the cosigner, a plus sign, and its key ID in eight lowercase
	// hexadecimal digits. A commit does not write while the circuit of a
	// cosigner is open.
	Breaker *resilience.Breaker

	// Logger receives a record of each inconsistent checkpoint, each commit
	// or snapshot that fails, each new origin beyond MaxOrigins, and each
	// snapshot of a state above 90% of MaxOrigins.
	Logger *slog.Logger

	// Resolver builds the Verifier of each key of the logs.
	Resolver note.Resolver

	// State keeps the journal. The Server writes only the keys of the
	// journal, and no other writer may write them.
	State blob.Store

	// UTC times the cosignatures. Each commit reads it once.
	UTC clock.UTCSource

	// Clock measures the durations of the metrics and the age of the state
	// that the monitor retrieval route serves.
	Clock clock.Clock

	// Reporter records the witness's metrics.
	Reporter telemetry.Reporter

	// Cosigners sign each cosignature at the time of its commit, one per
	// algorithm of the witness, such as a checkpoint.CosignatureV1Signer
	// or a checkpoint.SubtreeV1Signer.
	Cosigners []checkpoint.Cosigner

	// MaxOrigins bounds the number of origins in the state. A checkpoint of
	// a new origin beyond it gets a 404.
	MaxOrigins int

	// Retention is how long an origin that Logs refuses remains in the
	// state after its latest update. A snapshot retires it after that.
	Retention time.Duration

	// MaxError is the largest error bound of a reading that a commit signs
	// with.
	MaxError time.Duration

	// Timeout bounds the work of each commit, repair, snapshot and refresh
	// on the store. Each runs under a context of its own. A commit, a
	// repair and a snapshot can sign between their writes, so their
	// context has the deadline Timeout + SignTimeout.
	Timeout time.Duration

	// SignTimeout bounds the signatures of each commit and each repair,
	// under a context that no work on the store shares.
	SignTimeout time.Duration
}

// lineKey is the key name and the key ID of the signature lines of a
// cosigner.
type lineKey struct {
	// Name is the key name.
	Name note.Name

	// ID is the key ID.
	ID uint32
}

// Server is a witness of C2SP tlog-witness. [Server.AddCheckpoint] is the
// handler of add-checkpoint, [Server.Checkpoint] the handler of the
// monitor retrieval route, and [Server.Advance] advances the origins of
// many checkpoints under one note in one commit.
//
// A Server commits each checkpoint to the journal in its State before it
// cosigns it, so it never cosigns a checkpoint that the journal does not
// contain. Concurrent calls of one process commit together: a call joins
// the queue of the calls that wait, and one commit at a time takes the
// calls of the queue, up to the bounds of a commit. The calls of a commit
// receive their lines once the signatures exist. The commit stores the
// lines after that, while the next commit runs. The monitor retrieval
// route serves an update once its lines are stored.
//
// # Concurrency
//
// Safe for concurrent use. Processes that share one State commit through
// the journal's head, and never advance one origin from one stored state
// twice. A Server starts one goroutine per commit, per repair and per
// refresh of its state. Each cosigner signs the notes of one commit on up
// to GOMAXPROCS goroutines. One write of lines runs at a time. Each
// commit, repair and snapshot ends within Timeout + SignTimeout. The
// deadline of a commit covers the write of its lines. Each refresh ends
// within Timeout, so a Server has no Close.
//
// # Allocation contract
//
// Each method documents its allocations. A commit allocates 9 objects of
// its own on Go 1.27.1, which the calls of the commit share:
//   - 4 for each of its two contexts with a deadline: the context, its
//     timer, the timer's function and the cancel function.
//   - The string of the keys of its record, which the state keeps.
//
// The concurrent signatures of a commit allocate the state, the contexts
// and the closures of task.Each:
//   - Nothing for one cosigner and one call.
//   - 8 objects for one cosigner and two or more calls, and 8 for two
//     cosigners and one call.
//   - 21 objects for two cosigners and two or more calls.
type Server struct {
	// metrics are the instruments.
	metrics metrics

	// store is the State, utc the UTC and clock the Clock of the
	// configuration.
	store blob.Store
	utc   clock.UTCSource
	clock clock.Clock

	// logs is the Logs of the configuration.
	logs func(checkpoint.Origin) (Log, bool)

	// committer is the method value of commit, bound once. A go statement
	// of a function value without arguments allocates nothing, where one of
	// the method would allocate its closure.
	committer func()

	// st is the state, which mu guards.
	st *state

	// breaker keeps the circuits of the cosigners, and logger receives the
	// records of the configuration.
	breaker *resilience.Breaker
	logger  *slog.Logger

	// objects caches the records and the groups that the route reads.
	objects *cache.Cache[string, *loaded]

	// repairs contains the repair of each record that this process ran or
	// runs, which rmu guards.
	repairs map[string]*repairing

	// cosigners are the Cosigners of the configuration, lineKeys the key
	// of the lines of each, and targets the target of its circuit.
	cosigners []checkpoint.Cosigner
	lineKeys  []lineKey
	targets   []string

	// queue are the calls that wait for a commit, which qmu guards.
	queue []*pending

	// pendings contains the calls of the Server, with the memory of their
	// checks and of their results. A call that leaves the pool is its
	// caller's, or its commit's, until it returns to the pool.
	pendings *pool.Pool[*pending]

	// keys builds the Verifier of each key of the logs once.
	keys keyring

	// lines is the write of the lines of the latest commit that took one.
	// lmu guards it while a commit takes the write, stores the lines and
	// moves the served positions.
	lines linesWrite

	// batch is the memory of the running commit.
	batch batch

	// signers bounds the goroutines on which a cosigner signs the notes of
	// one commit or one repair. NewServer sets it to GOMAXPROCS.
	signers int

	// maxOrigins, retention, maxError, timeout and signTimeout are the
	// bounds of the configuration.
	maxOrigins  int
	retention   time.Duration
	maxError    time.Duration
	timeout     time.Duration
	signTimeout time.Duration

	// snapshotLimit is the read limit of a snapshot: 1 KiB per origin of
	// MaxOrigins, and at least the limit of the other objects.
	snapshotLimit int64

	// mu guards st, qmu guards queue and committing, rmu guards repairs,
	// and lmu guards lines, so one write of lines runs at a time.
	mu  sync.RWMutex
	qmu sync.Mutex
	rmu sync.Mutex
	lmu sync.Mutex

	// refreshing and snapshotting report whether a refresh and a snapshot
	// of this process run.
	refreshing, snapshotting atomic.Bool

	// committing reports whether a commit runs.
	committing bool
}

// NewServer returns a Server of cfg, with the state that the journal in
// cfg.State commits: the installed snapshot and the records after it.
//
// NewServer reads the head, the head's snapshot, and the records from the
// head back to that snapshot. Another process can install a newer
// snapshot during the walk, and delete the objects that the walk reads
// next. A walk that finds an object missing starts again from a new
// reading of the head.
//
// Error modes:
//   - ErrConfig, classified Invalid, for a nil cfg, a missing field, no
//     cosigner, a cosigner whose key name is not Valid, two cosigners with
//     one key name and key ID, a negative MaxError, and a MaxOrigins,
//     Retention, Timeout or SignTimeout that is not positive.
//   - ErrJournal, classified Integrity, for an object of the journal that
//     does not decode or whose hash is not its name, and for a chain that
//     ends before its snapshot, each found twice from one version of the
//     head.
//   - The errors of State, and the cause of ctx when ctx ends.
//
// # Allocation contract
//
// Allocates the Server, its pool of calls, its cache, its state and the
// bindings of its instruments, and what the walk reads: 29 objects over an
// empty store with the reporter of telemetry/noop on Go 1.27.1.
func NewServer(ctx context.Context, cfg *ServerConfig) (*Server, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}

	s := &Server{
		logs:        cfg.Logs,
		st:          newState(),
		breaker:     cfg.Breaker,
		logger:      cfg.Logger,
		repairs:     make(map[string]*repairing),
		store:       cfg.State,
		utc:         cfg.UTC,
		clock:       cfg.Clock,
		cosigners:   cfg.Cosigners,
		lineKeys:    make([]lineKey, len(cfg.Cosigners)),
		targets:     make([]string, len(cfg.Cosigners)),
		pendings:    pool.NewPool(func() *pending { return &pending{done: make(chan struct{}, 1)} }),
		metrics:     newMetrics(cfg.Reporter),
		maxOrigins:  cfg.MaxOrigins,
		retention:   cfg.Retention,
		maxError:    cfg.MaxError,
		timeout:     cfg.Timeout,
		signTimeout: cfg.SignTimeout,
		signers:     runtime.GOMAXPROCS(0),

		snapshotLimit: math.MaxInt64,
	}

	s.committer = s.commit

	if n := int64(cfg.MaxOrigins); n <= math.MaxInt64/snapshotOriginBytes {
		s.snapshotLimit = max(n*snapshotOriginBytes, maxObjectBytes)
	}

	for i, c := range cfg.Cosigners {
		k := c.Key()
		s.lineKeys[i] = lineKey{Name: k.Name, ID: k.ID()}

		var id [4]byte

		binary.BigEndian.PutUint32(id[:], s.lineKeys[i].ID)
		s.targets[i] = string(k.Name) + "+" + hex.EncodeToString(id[:])
	}

	s.keys.ring.Reset(cfg.Resolver)

	// cache.New refuses only a nil Clock, which check refuses, and a
	// Capacity that is not positive.
	s.objects, _ = cache.New(cache.Config[string, *loaded]{ //nolint:errcheck // see above
		Clock:    cfg.Clock,
		Capacity: cacheBytes,
		Cost:     func(l *loaded) int64 { return l.size },
	})

	if _, err := s.sync(ctx, ""); err != nil {
		return nil, err
	}

	return s, nil
}

// check returns nil for a configuration that NewServer accepts.
//
// Returns an error that wraps [ErrConfig] for any other.
func (c *ServerConfig) check() error {
	if c == nil || c.Logs == nil || c.Breaker == nil || c.Logger == nil || c.Resolver == nil || c.State == nil ||
		c.UTC == nil || c.Clock == nil || c.Reporter == nil || len(c.Cosigners) == 0 {

		return fmt.Errorf("%w: a server needs Logs, Breaker, Logger, Resolver, State, UTC, Clock, Reporter and "+
			"Cosigners", ErrConfig)
	}

	if c.MaxError < 0 || c.MaxOrigins <= 0 || c.Retention <= 0 || c.Timeout <= 0 || c.SignTimeout <= 0 {
		return fmt.Errorf("%w: a negative MaxError, or a MaxOrigins, Retention, Timeout or SignTimeout that is "+
			"not positive", ErrConfig)
	}

	for i, cs := range c.Cosigners {
		if cs == nil {
			return fmt.Errorf("%w: the cosigner %d is nil", ErrConfig, i)
		}

		k := cs.Key()
		if !k.Name.Valid() {
			return fmt.Errorf("%w: the cosigner %d has the key name %q", ErrConfig, i, k.Name)
		}

		for _, other := range c.Cosigners[:i] {
			if o := other.Key(); o.Name == k.Name && o.ID() == k.ID() {
				return fmt.Errorf("%w: two cosigners named %s with one key ID", ErrConfig, k.Name)
			}
		}
	}

	return nil
}
