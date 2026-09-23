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
	"go.thesmos.sh/core/fsm"
)

// State is a circuit's position in the breaker's state machine.
type State uint8

const (
	// Closed passes every call through. A circuit starts in Closed.
	Closed State = iota

	// Open refuses every call until the configured interval elapses.
	// A refused call does not contact the dependency.
	Open

	// HalfOpen has admitted one probe and waits for its outcome. It
	// refuses every other call meanwhile, so a dependency that is still
	// down receives one call instead of the full load.
	HalfOpen
)

// String returns the state name, or State(n) for a value outside the
// three states.
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

// BreakerConfig configures a [Breaker]. Every field is required and
// has no default. A wrong threshold does not cause an error: a breaker
// that never opens is indistinguishable from a healthy dependency.
type BreakerConfig struct {
	// Clock is the time source for the open interval. A virtual clock
	// makes every transition deterministic in tests.
	Clock clock.Clock

	// TripOn lists the error classes that [Call] counts as a
	// dependency failure. Must be non-empty.
	//
	// [Breaker.Allow] and [Breaker.Record] ignore TripOn, because their
	// caller decides what a failure is.
	//
	// Including [errs.Unspecified] makes every unclassified error trip
	// the circuit. An unclassified error can come from a failing
	// dependency or from a bug in the caller, and this package cannot
	// tell the two apart.
	TripOn []errs.Class

	// FailureThreshold is the number of consecutive failures that
	// opens a circuit. Must be > 0.
	//
	// A success resets the count. A failure rate would need a time
	// window, and a window needs traffic before its rate reflects the
	// dependency.
	FailureThreshold int

	// SuccessThreshold is the number of consecutive probe successes
	// that closes a circuit. Must be > 0.
	//
	// A value above one keeps the circuit from closing on a dependency
	// that serves one request and then fails again.
	SuccessThreshold int

	// OpenFor is how long a circuit refuses calls before it admits a
	// probe. Must be > 0.
	OpenFor time.Duration
}

// Breaker keeps one circuit per target.
//
// Each target has its own circuit because one failing dependency
// implies nothing about another. A shared circuit would either open
// for every target when one fails, or never open, because successes
// from the healthy targets would reset its failure count.
//
// # Growth
//
// A Breaker never evicts a circuit, so targets must come from the
// caller's own configuration. A Breaker keyed on client-supplied
// values grows without bound, and nothing reports the growth before
// the process runs out of memory.
//
// # Concurrency
//
// A Breaker is safe for concurrent use. Calls for different targets
// contend on one mutex.
//
// # Allocation contract
//
// [Breaker.Allow] allocates one circuit the first time it sees a
// target. Allow, [Breaker.Record] and [Breaker.State] do not allocate
// for a target that has a circuit.
type Breaker struct {
	clock            clock.Clock
	circuits         map[string]*fsm.Machine[State, event, circuit]
	tripOn           []errs.Class
	failureThreshold int
	successThreshold int
	openFor          time.Duration

	mu sync.Mutex
}

// event is an input to a circuit: a caller asks to proceed, or reports
// the outcome of a call it was allowed to make.
type event uint8

const (
	allow event = iota
	success
	failure
)

// String returns the event name, which the circuit's Mermaid diagram
// and the errors of [fsm.Builder.Build] contain.
func (e event) String() string {
	switch e {
	case allow:
		return "Allow"
	case success:
		return "Success"
	default:
		return "Failure"
	}
}

// circuit is the data of one target's machine: the counters and the
// deadline that the guards read and the actions update. b points at
// the Breaker whose thresholds and clock the guards and actions use.
type circuit struct {
	openUntil time.Time
	b         *Breaker
	failures  int
	successes int

	// probing reports that a probe has been admitted and its outcome
	// is outstanding. A half-open circuit does not admit another call
	// while probing is set.
	probing bool
}

// circuitSpec is the state machine of every circuit. Guards and
// actions read the thresholds and the clock through circuit.b, so one
// Spec serves every Breaker and is built once, at package
// initialisation. errCircuitSpec is nil for a valid declaration.
//
// Open covers the whole interval and the moment after it elapses. The
// first Allow after the deadline claims the probe and moves the circuit
// to HalfOpen. An outcome recorded while the circuit is open belongs to
// a call admitted before it opened. It updates the counters and leaves
// the circuit open.
var circuitSpec, errCircuitSpec = fsm.NewBuilder[State, event, circuit](Closed).
	Edge(Closed, allow, Closed).
	Edge(Closed, success, Closed, fsm.Do(clearFailures)).
	Edge(Closed, failure, Open, fsm.If(atThreshold), fsm.Do(countFailure)).
	Edge(Closed, failure, Closed, fsm.Do(countFailure)).
	Edge(Open, allow, HalfOpen, fsm.If(elapsed), fsm.Do(claimProbe)).
	Edge(Open, success, Open, fsm.Do(clearFailures)).
	Edge(Open, failure, Open, fsm.Do(countLateFailure)).
	Edge(HalfOpen, allow, HalfOpen, fsm.If(idle), fsm.Do(claimProbe)).
	Edge(HalfOpen, failure, Open, fsm.If(probeFailed), fsm.Do(countFailure)).
	Edge(HalfOpen, failure, HalfOpen, fsm.Do(countFailure)).
	Edge(HalfOpen, success, Closed, fsm.If(recovered)).
	Edge(HalfOpen, success, HalfOpen, fsm.If(probing), fsm.Do(countSuccess)).
	Edge(HalfOpen, success, HalfOpen, fsm.Do(clearFailures)).
	OnEnter(Open, reopen).
	OnEnter(Closed, reset).
	Build()

// atThreshold reports whether one more failure opens the circuit.
func atThreshold(c *circuit) bool { return c.failures+1 >= c.b.failureThreshold }

// elapsed reports whether the open interval has ended.
func elapsed(c *circuit) bool { return !c.b.clock.Time().Before(c.openUntil) }

// idle reports whether no probe is outstanding.
func idle(c *circuit) bool { return !c.probing }

// probing reports whether a probe is outstanding.
func probing(c *circuit) bool { return c.probing }

// probeFailed reports whether a failure re-opens a half-open circuit.
// A failed probe always re-opens it, because the dependency is still
// down. A late failure re-opens it when the failure count meets the
// threshold.
func probeFailed(c *circuit) bool { return c.probing || atThreshold(c) }

// recovered reports whether the probe's success is the last one the
// circuit needs to close.
func recovered(c *circuit) bool { return c.probing && c.successes+1 >= c.b.successThreshold }

// countFailure records a failure and ends any outstanding probe.
func countFailure(c *circuit) {
	c.probing, c.successes = false, 0
	c.failures++
}

// countLateFailure records a failure that arrives while the circuit is
// open. When the failure count meets the threshold, it restarts the
// open interval.
func countLateFailure(c *circuit) {
	countFailure(c)

	if c.failures >= c.b.failureThreshold {
		reopen(c)
	}
}

// countSuccess records a probe success below the threshold.
func countSuccess(c *circuit) {
	c.probing = false
	c.successes++
}

// clearFailures resets the failure count after a success that is not
// a probe's.
func clearFailures(c *circuit) { c.failures = 0 }

// claimProbe admits the one probe a half-open circuit allows.
func claimProbe(c *circuit) { c.probing = true }

// reopen starts the open interval at the current time.
func reopen(c *circuit) { c.openUntil = c.b.clock.Time().Add(c.b.openFor) }

// reset clears the counters of a circuit that has closed.
func reset(c *circuit) { *c = circuit{b: c.b} }

// NewBreaker returns a Breaker configured by cfg.
//
// NewBreaker returns [ErrConfig] when cfg.Clock is nil, when TripOn is
// empty, or when a threshold or OpenFor is not positive. A zero
// threshold makes a breaker either useless or permanently open, so
// NewBreaker rejects it at construction.
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
		circuits:         make(map[string]*fsm.Machine[State, event, circuit]),
		tripOn:           cfg.TripOn,
		failureThreshold: cfg.FailureThreshold,
		successThreshold: cfg.SuccessThreshold,
		openFor:          cfg.OpenFor,
	}, nil
}

// Allow reports whether a call to target may proceed. When the open
// interval of target's circuit has elapsed, Allow admits one probe and
// refuses every other call until the probe's outcome is recorded.
//
// A caller that receives true MUST report the outcome with
// [Breaker.Record]. A probe that is admitted and never recorded leaves
// the circuit half-open, and the circuit admits no further call.
//
// Use Allow with Record when the caller decides what a failure is, as
// for a transport that reports failure in a status code. [Call] pairs
// them for a function whose failures are errors.
func (b *Breaker) Allow(target string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	m, ok := b.circuits[target]
	if !ok {
		c := circuitSpec.Start(circuit{b: b})
		m = &c
		b.circuits[target] = m
	}

	_, err := m.Fire(allow)

	return err == nil
}

// Record adds the outcome of one call to target's circuit. failed
// reports whether the caller counts the call as a failure.
//
// Record ignores a target that [Breaker.Allow] has never seen.
func (b *Breaker) Record(target string, failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	m, ok := b.circuits[target]
	if !ok {
		return
	}

	outcome := success
	if failed {
		outcome = failure
	}

	// Every state accepts both outcomes, so Fire cannot reject one.
	_, _ = m.Fire(outcome)
}

// State returns target's current state, for export as a gauge.
//
// The result is a snapshot. Another goroutine can change the state
// before the caller uses it, so use [Breaker.Allow] to decide whether
// to call the dependency. State returns [HalfOpen] for an open circuit
// whose interval has elapsed, because the next Allow admits a probe. A
// target with no circuit is [Closed].
func (b *Breaker) State(target string) State {
	b.mu.Lock()
	defer b.mu.Unlock()

	m, ok := b.circuits[target]
	if !ok {
		return Closed
	}

	if m.State() == Open && elapsed(m.Data()) {
		return HalfOpen
	}

	return m.State()
}

// Call runs fn under target's circuit. When the circuit refuses the
// call, Call returns [ErrOpen] and does not run fn.
//
// Call classifies fn's error with [errs.Classify] and counts it as a
// failure when its class is in TripOn. A dependency that rejects a bad
// request is working, so [errs.Invalid], [errs.NotFound] and
// [errs.Denied] do not normally belong in TripOn.
//
// Call does not count an error as a failure when ctx has ended. The
// caller stopped waiting, and the dependency did not fail. Counting
// these errors would open the circuit when many callers cancel at
// once, and the resulting ErrOpen errors would report an outage that
// did not happen.
//
// A caller whose failures are not errors, such as an HTTP status or an
// RPC trailer, uses [Breaker.Allow] and [Breaker.Record] instead.
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
