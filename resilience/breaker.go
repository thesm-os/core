// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
)

// State is a circuit's position in the breaker's state machine.
type State uint8

const (
	// Closed passes every call through. The steady state.
	Closed State = iota

	// Open refuses every call without reaching the dependency, until
	// the configured interval elapses.
	Open

	// HalfOpen has admitted one probe and is waiting for its outcome.
	// Every other caller is refused meanwhile.
	HalfOpen
)

// String returns the state name.
func (s State) String() string {
	switch s {
	case Closed:
		return "Closed"
	case Open:
		return "Open"
	case HalfOpen:
		return "HalfOpen"
	default:
		return "State(" + strconv.Itoa(int(s)) + ")"
	}
}

// BreakerConfig has no defaults. Every field is required, because a
// threshold that is wrong is wrong silently: a breaker that never
// opens looks exactly like a healthy dependency.
type BreakerConfig struct {
	// Clock reads elapsed time. Under a virtual clock the state
	// transitions are exact, which is the property that makes a
	// breaker testable at all.
	Clock clock.Clock

	// TripOn are the error classes [Call] counts as a dependency
	// failure. Must be non-empty.
	//
	// Ignored by [Breaker.Allow] and [Breaker.Record], where the
	// caller judges for itself.
	//
	// Whether [errs.Unspecified] belongs here is the decision that
	// determines whether the breaker protects anything: an
	// unclassified error may be a failing dependency or a caller-side
	// bug, and this package cannot tell them apart. Including it
	// means every unclassified error trips the circuit.
	TripOn []errs.Class

	// FailureThreshold is the count of consecutive failures that
	// opens a circuit. Must be > 0.
	//
	// Consecutive rather than a rate: a dependency that fails this
	// many times in a row is down, whereas a rate needs a window to
	// be meaningful and a window needs traffic to fill it.
	FailureThreshold int

	// SuccessThreshold is the count of consecutive probe successes
	// that closes a circuit. Must be > 0.
	//
	// Above one because a dependency that answers a single request
	// and falls over should not receive the whole traffic back.
	SuccessThreshold int

	// OpenFor is how long a circuit stays open before admitting a
	// probe. Must be > 0.
	OpenFor time.Duration
}

// Breaker holds one circuit per target.
//
// Per target because a caller has several dependencies and one being
// down says nothing about the others. A single shared circuit would
// either open for all of them when one fails, or never open.
//
// # Growth
//
// Circuits are never evicted, so targets must come from the caller's
// own configuration. A Breaker keyed on anything a client supplies is
// a memory leak, and the failure is silent until the process runs out
// of memory.
//
// # Concurrency
//
// Safe for concurrent use.
type Breaker struct {
	clock            clock.Clock
	circuits         map[string]*circuit
	tripOn           []errs.Class
	failureThreshold int
	successThreshold int
	openFor          time.Duration

	mu sync.Mutex
}

// circuit is one target's state.
type circuit struct {
	openUntil time.Time
	failures  int
	successes int

	// probing records that a probe has been admitted and its outcome
	// is outstanding. It is what limits a half-open circuit to one
	// call rather than the full load.
	probing bool
}

// NewBreaker returns a Breaker over cfg.
//
// Returns [ErrConfig] when any required field is missing: a threshold
// left at zero would make the breaker either useless or permanently
// open, and that is a wiring error worth catching at construction.
func NewBreaker(cfg BreakerConfig) (*Breaker, error) {
	if cfg.Clock == nil ||
		cfg.FailureThreshold <= 0 ||
		cfg.SuccessThreshold <= 0 ||
		cfg.OpenFor <= 0 ||
		len(cfg.TripOn) == 0 {

		return nil, ErrConfig
	}

	return &Breaker{
		clock:            cfg.Clock,
		circuits:         make(map[string]*circuit),
		tripOn:           cfg.TripOn,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		openFor:          cfg.OpenFor,
	}, nil
}

// Allow reports whether a call to target may proceed, claiming the
// probe slot when the circuit has just become eligible for one.
//
// A caller that receives true MUST report the outcome with
// [Breaker.Record]. A probe slot taken and never recorded leaves the
// circuit half-open forever, admitting nothing further.
//
// Use this with Record when the caller judges failure itself — which
// is the common case for a transport where failure is not an error.
// [Call] pairs them for callers whose failures do arrive as errors.
func (b *Breaker) Allow(target string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, ok := b.circuits[target]
	if !ok {
		c = &circuit{}
		b.circuits[target] = c
	}

	if c.openUntil.IsZero() {
		return true
	}

	if b.clock.Time().Before(c.openUntil) {
		return false
	}

	// The interval has elapsed. Admit one probe and refuse everyone
	// else until its outcome is recorded — releasing the full load
	// here would be indistinguishable from never having opened, for a
	// dependency that is still down.
	if c.probing {
		return false
	}
	c.probing = true

	return true
}

// Record folds one outcome into target's circuit.
//
// What counts as a failure is the caller's judgement. Recording a
// target that [Breaker.Allow] has never seen is a no-op.
func (b *Breaker) Record(target string, failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, ok := b.circuits[target]
	if !ok {
		return
	}

	wasProbe := c.probing
	c.probing = false

	switch {
	case failed:
		c.successes = 0
		c.failures++

		// A failed probe re-opens immediately, without waiting for
		// the threshold again: the circuit was already open and one
		// call has just confirmed the dependency is still down.
		if wasProbe || c.failures >= b.failureThreshold {
			c.openUntil = b.clock.Time().Add(b.openFor)
		}
	case wasProbe:
		c.successes++

		if c.successes >= b.successThreshold {
			*c = circuit{}
		}
	default:
		c.failures = 0
	}
}

// State reports target's current state, for emission as a gauge.
//
// A point-in-time observation, and racy by nature: a caller that
// branches on it rather than calling [Breaker.Allow] is
// reimplementing the breaker badly. A target with no circuit reports
// [Closed].
func (b *Breaker) State(target string) State {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, ok := b.circuits[target]
	if !ok || c.openUntil.IsZero() {
		return Closed
	}

	if c.probing {
		return HalfOpen
	}

	if b.clock.Time().Before(c.openUntil) {
		return Open
	}

	// The interval has elapsed but no probe has been claimed yet.
	// Reporting HalfOpen describes what the next caller will find.
	return HalfOpen
}

// Call runs fn under target's circuit, returning [ErrOpen] without
// calling fn when the circuit is open.
//
// Failure is judged by [errs.Classify] against the configured
// TripOn classes. A dependency correctly rejecting a bad request is
// not a failing dependency, so [errs.Invalid], [errs.NotFound] and
// [errs.Denied] should not normally appear there.
//
// A call whose context ended is never counted as a failure: the
// dependency did not fail, the caller stopped waiting. Counting it
// would open a circuit against a healthy dependency during a
// cancellation storm, and the resulting ErrOpen responses would then
// look like the outage that was not happening.
//
// Callers whose failures do not arrive as errors — an HTTP status, an
// RPC trailer — use [Breaker.Allow] and [Breaker.Record] instead.
func Call[T any](
	ctx context.Context, b *Breaker, target string,
	fn func(context.Context) (T, error),
) (T, error) {
	if !b.Allow(target) {
		var zero T

		return zero, ErrOpen
	}

	v, err := fn(ctx)

	b.Record(target, b.tripped(ctx, err))

	return v, err
}

// tripped reports whether err counts as a dependency failure.
func (b *Breaker) tripped(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}

	return slices.Contains(b.tripOn, errs.Classify(err))
}
