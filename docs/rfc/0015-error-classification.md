---
rfc: 0015
title: Error Classification
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0015: Error Classification

## Summary

`errs` — a closed, `core`-owned taxonomy of what a caller should *do*
about an error, orthogonal to what went wrong and orthogonal to what a
remote caller should be told. One value, not a set, so contradictory
classifications are unrepresentable. Recognises the standard library's
own sentinels so producers that have never heard of the package still
classify correctly. Ships with the two `version` sentinels that a
conditional write needs in order to be classifiable at all.

## Motivation

Every library must answer one question about a failure that its caller
cannot answer for it: **is this worth retrying?**

Nothing in `core` lets it say so. The answer therefore gets encoded in
prose, in sentinel names, or in string matching on error text, and each
of those fails differently. Prose is not machine-readable. Sentinel
names are per-package vocabularies that no generic middleware can
consume. String matching breaks when a message is reworded.

The consequence lands hardest on generic code — a retry loop, a circuit
breaker, a work queue — which by construction cannot know the packages
whose errors it is handling. Such code currently either retries
everything, which turns a rejected malformed request into an infinite
loop, or retries nothing, which discards recoverable failures.

The standard library has one precedent here and it is a warning.
`net.Error.Temporary()` answered exactly this question and was
deprecated as ill-defined: "Most 'temporary' errors are timeouts, and
the few exceptions are surprising." That deprecation is the strongest
argument against this RFC and it is addressed directly below.

## Detailed design

```go
// Package errs is the error-classification seam: a closed,
// core-owned taxonomy of what a caller should DO about an error.
//
// It is orthogonal to what went wrong, which package-local sentinel
// errors answer and errs never learns. It is orthogonal to what a
// remote caller should be told, which is transport policy.
package errs

// Class is a single value, not a set. Contradictory classifications
// are unrepresentable by construction.
type Class uint8

const (
    // Unspecified is the reserved zero value: the error carries no
    // classification. Callers MUST treat it as non-retryable. An
    // unclassified error is one nobody has reasoned about, and
    // retrying it is a guess.
    Unspecified Class = iota

    // Transient means the identical operation may succeed if retried
    // after a delay. Nothing about the caller's state needs to change.
    Transient

    // Conflict means the state the operation was based on has moved.
    // Retrying identically fails identically; the caller must
    // re-read, re-apply, and retry.
    Conflict

    // NotFound means the addressed resource does not exist.
    NotFound

    // Invalid means the request itself is wrong. The same request will
    // never succeed.
    Invalid

    // Unsupported means the implementation cannot honour a method its
    // interface declares. Distinct from Invalid: the request is
    // well-formed, the implementation is narrower than the contract.
    // Producers SHOULD also satisfy errors.Is(err,
    // errors.ErrUnsupported).
    Unsupported

    // Denied means refusal by policy rather than technical failure.
    // Automated retry cannot succeed; the remedy is escalation or a
    // policy change.
    Denied

    // Integrity means data failed verification. Never retry —
    // retrying a corrupt read yields the same corruption, and
    // automated recovery risks propagating it.
    Integrity
)

func (c Class) String() string

// Classifier is implemented by errors carrying a Class.
type Classifier interface{ Class() Class }

// Classify walks err's tree and returns the first Class found.
func Classify(err error) Class

// Retryable reports whether Classify(err) == Transient. It is the
// only question most callers ask.
func Retryable(err error) bool

// WithClass returns err tagged with c, satisfying Classifier while
// remaining errors.Is-comparable to err.
func WithClass(err error, c Class) error
```

Imports: `errors`, `io/fs`, `context`.

### Why a closed enumeration rather than sentinels

`core` has twice declined to ship package sentinels as "declarations
without producers", and both refusals were right. A closed enumeration
is a different proposal and the difference is load-bearing.

It names no errors, so it cannot accrete a vocabulary — the set is
fixed at eight and extending it is an RFC. And it makes contradiction
unrepresentable: `errors.Join(ErrTransient, ErrPermanent)` compiles,
passes review, and means nothing, where a single `Class` value cannot
be two things.

### Why not a status vocabulary

Transport status vocabularies answer *what the caller should be told*,
and answer *what the caller should do* only by accident. Three distinct
statuses — unavailable, deadline exceeded, resource exhausted — carry
one handling answer between them, and any status meaning "the state
moved" is smuggling retry semantics into a transport concern.

`core` ships no transport and must not decide what maps to a 404. A
consumer that needs both axes carries both; they compose, because one
describes handling and the other describes reporting.

### Distinguishing this from `net.Error.Temporary()`

`Temporary()` failed for three reasons, and each is a constraint this
design meets rather than a reason to abandon the axis.

It was a *predicate*, so it forced every failure into two buckets and
had no way to say "the state moved, re-read and retry" — which is a
different action from both "retry as-is" and "give up". `Class`
distinguishes `Transient` from `Conflict`.

It was *open to interpretation*: each implementation decided what
temporary meant, and the meanings diverged. `Class` is closed, and each
constant's doc states the action rather than the condition.

It had *no defined default*, so an error that implemented `net.Error`
without thinking about temporariness returned something arbitrary.
`Unspecified` is the zero value, is documented as non-retryable, and is
what any error that has not been reasoned about produces.

### Recognising standard-library sentinels

`Classify` recognises `fs.ErrNotExist` as `NotFound`,
`errors.ErrUnsupported` as `Unsupported`, and `context.DeadlineExceeded`
and `context.Canceled` as `Unspecified`.

The context cases are deliberate and are the subtlest decision here. A
cancelled or expired context means the *caller's* deadline elapsed, and
retrying inside a context that is already done cannot succeed. Treating
them as `Transient` produces a retry loop that spins until the caller's
deadline is checked by something else.

### `version.ErrMismatch` and `version.ErrExists`

A conditional write whose precondition failed is a universal outcome.
`version` already defines the preconditions that produce it — `IfMatch`
and `IfNoneMatch` — and does not define what failing them looks like,
so every implementation invents a spelling. That is the divergence
`version` exists to prevent, reintroduced one layer down.

```go
// In package version, in errors.go.

// ErrMismatch reports that an [WriteOptions.IfMatch] precondition
// failed: the stored version was not the one supplied.
//
// ErrMismatch is the retry signal of the optimistic-concurrency loop.
// The correct response is to re-read, re-apply, and retry — not to
// repeat the identical write, which fails identically. Classifies as
// [errs.Conflict].
var ErrMismatch = errors.New("version: if-match precondition failed")

// ErrExists reports that an [WriteOptions.IfNoneMatch] precondition
// failed: a value already exists and the caller asked to create only.
// Classifies as [errs.Conflict].
var ErrExists = errors.New("version: already exists")
```

Both are referenced by existing godoc links in `version` that currently
resolve to nothing, so declaring them also repairs the package's
documentation.

`version` does not import `errs` — the classification is stated in the
doc comment and implemented by `Classify` recognising the two
sentinels, which keeps the dependency pointing one way.

### Allocation contract

`Class` is a value type; `String` returns a constant. `Classify` and
`Retryable` allocate nothing. `WithClass` allocates one wrapper.

## Alternatives considered

### A. A predicate, `Retryable(err) bool`, with no enumeration

**Why not:** this is `Temporary()` with a new name. It cannot express
`Conflict`, which needs a different action from both retry and
surrender, and it cannot express `Integrity`, where retrying is
actively harmful rather than merely useless.

### B. A set of classes rather than one

Let an error carry several classes so a producer can say "transient and
also a conflict".

**Why not:** the two examples that motivate it are both cases where one
class is correct and the producer has not decided which. A set restores
exactly the contradiction the closed enum was chosen to make
unrepresentable, and every consumer then needs a precedence rule.

### C. Sentinel errors, one per class

`var ErrTransient = errors.New(...)` and friends, matched with
`errors.Is`.

**Why not:** `errors.Join(ErrTransient, ErrDenied)` compiles and is
meaningless, and there is no way to detect it. The enumeration makes
the same mistake a type error.

### D. Leave classification to each consumer

**Why not:** the code that most needs the answer is generic — a retry
loop, a breaker, a queue — and generic code cannot know the packages
whose errors it handles. Per-consumer classification means per-consumer
string matching on other people's error text.

## Drawbacks

- Eight constants is a judgement about where the joints are, and it is
  permanent in a way the implementation is not. If a ninth is needed,
  every exhaustive switch in the ecosystem gains a case.
- `Unspecified` meaning "do not retry" makes every unclassified error
  from every unclassified library non-retryable. That is the safe
  default and it is also the wrong answer for a great deal of code that
  simply has not adopted the package.
- `WithClass` allocates, so classifying on a hot error path costs one
  allocation per error.
- Producers must remember that satisfying `Classifier` is not enough for
  `Unsupported`; they should also satisfy `errors.Is(err,
  errors.ErrUnsupported)`. Two obligations for one condition is a thing
  people forget.

## Open questions

None. Both questions this RFC previously carried are resolved.

There is no ninth class for caller-side cancellation. The motivating
case — a breaker must not count a caller's own timeout as a dependency
failure — is answered where it arises rather than in the taxonomy: code
holding the context can read `ctx.Err()` directly and knows more than a
class could tell it. RFC-0023 specifies exactly that. Adding a class
would put the same information in two places, and the copy in the error
would be the one that goes stale.

`Class` does not implement `error`, so `errors.Is(err, errs.Transient)`
does not compile. Making it work would conflate the taxonomy with the
errors it classifies, and it would reintroduce the contradiction the
closed enumeration exists to prevent: `errors.Join` of two classes
would compile again. `Classify(err) == errs.Transient` is the
comparison, and it is a comparison rather than a match because a class
is a value and not an error.

## Unresolved / future work

- A `slog.Attr` bridge so a classified error logs its class without the
  caller extracting it, once ADR-0009's bridge lands.
- Whether other `core` packages should declare their own sentinels and
  document a class, as `version` does here. `page` and `id` both have
  candidates.
