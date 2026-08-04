---
rfc: 0025
title: Fixed-Point Decimals
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-04
updated: 2026-08-04
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0010, ADR-0011
---

# RFC-0025: Fixed-Point Decimals

## Summary

`fixed.Fixed64` — a decimal number stored as a single `int64` at a
fixed scale of eight places. All arithmetic is integer and checked;
ordering is integer comparison rather than tolerance comparison;
overflow is an error rather than a wrapped or saturated value. It is
what `core` offers wherever `float64` is the wrong type, and it names
no currency, no unit, and no domain.

## Motivation

`core` already prescribes that computed artefacts must be reproducible.
`crypto.Digest` exists so a hash survives algorithm rotation,
RFC-0014 gives the value types a byte-exact encoding, and RFC-0017 now
binds an algorithm into every ciphertext. All of that assumes the
numbers being hashed, signed and compared are themselves the same on
every machine that computes them.

With `float64` they are not.

Three separate failures, each sufficient on its own:

- **Representation.** `0.1 + 0.2` is `0.30000000000000004`. A value a
  caller typed as a decimal is not the value stored, and the difference
  compounds across a ledger.
- **Architecture.** ARM64 contracts multiply-add into a single FMA
  instruction; AMD64 does not. The Go spec permits this explicitly, so
  the same expression over the same inputs differs by a unit in the
  last place. That is enough to flip a comparison at a threshold, and
  enough to make two nodes disagree about a value they both computed
  correctly.
- **Ordering.** Floating-point addition is not associative, so summing
  a set in a different order gives a different total. Any code that
  parallelises, batches, or reorders is exposed.

None of these is exotic and none announces itself. They surface as a
reconciliation that is off by a cent, a replica that disagrees, or a
signature that does not verify against a recomputed value — long after
the code that caused them shipped.

The standard library offers no answer. `math/big.Rat` is exact but
allocates, is not comparable, and has no fixed scale, so it cannot be a
map key, a struct field compared with `==`, or a fixed-width wire
field. `int64` of minor units works and is what careful code already
does by hand — inconsistently, with the scale in a comment, and with
overflow unchecked.

## Detailed design

```go
// Package fixed provides decimal arithmetic at a fixed scale of eight
// places, stored as int64 and checked on every operation.
package fixed

// Fixed64 is a decimal at [Scale] places, stored as one int64.
type Fixed64 int64

const (
    // Scale is the number of decimal places. One logical unit is
    // 100,000,000 raw units, which is the value of [One].
    Scale = 8

    Zero     Fixed64 = 0
    One      Fixed64 = 1e8
    Smallest Fixed64 = 1               // smallest representable step
    Max      Fixed64 = math.MaxInt64   //  92233720368.54775807
    Min      Fixed64 = -math.MaxInt64  // -92233720368.54775807
)

// Construction. All are checked; math.MinInt64 is outside the domain.
func FromInt(v int64) (Fixed64, error)
func FromRaw(raw int64) (Fixed64, error)
func Parse(s string) (Fixed64, error)

// Inspection. None can fail.
func (f Fixed64) Raw() int64
func (f Fixed64) Int() int64          // truncated toward zero
func (f Fixed64) IsZero() bool
func (f Fixed64) Sign() int
func (f Fixed64) Compare(g Fixed64) int
func (f Fixed64) String() string

// Sign inversion. Total: the domain is symmetric.
func (f Fixed64) Neg() Fixed64
func (f Fixed64) Abs() Fixed64

// Arithmetic. Every operation is checked.
func (f Fixed64) Add(g Fixed64) (Fixed64, error)
func (f Fixed64) Sub(g Fixed64) (Fixed64, error)
func (f Fixed64) Mul(g Fixed64) (Fixed64, error)
func (f Fixed64) Div(g Fixed64) (Fixed64, error)

// Rounding away from zero, for callers who must not round down.
func (f Fixed64) MulAway(g Fixed64) (Fixed64, error)
func (f Fixed64) DivAway(g Fixed64) (Fixed64, error)

// Quantisation to fewer places, for callers emitting at a coarser
// scale than they computed at. places is in [0, Scale].
func (f Fixed64) Round(places int) (Fixed64, error)
func (f Fixed64) RoundAway(places int) (Fixed64, error)

// Encoding, per RFC-0014.
func (f Fixed64) AppendBinary(dst []byte) ([]byte, error)
func (f Fixed64) MarshalBinary() ([]byte, error)
func (f *Fixed64) UnmarshalBinary(data []byte) error
func (f Fixed64) AppendText(dst []byte) ([]byte, error)
func (f Fixed64) MarshalText() ([]byte, error)
func (f *Fixed64) UnmarshalText(data []byte) error

// Errors. Every one classifies as errs.Invalid: the same input will
// never succeed.
var (
    ErrOverflow  = errors.New("fixed: result out of range")
    ErrDivZero   = errors.New("fixed: division by zero")
    ErrRange     = errors.New("fixed: value out of range")
    ErrSyntax    = errors.New("fixed: malformed decimal")
    ErrPrecision = errors.New("fixed: more than 8 decimal places")
    ErrSize      = errors.New("fixed: encoded value must be 8 bytes")
)
```

### The package name

`core` already has two packages called `fixed` —
`go.thesmos.sh/core/id/fixed` and `go.thesmos.sh/core/rand/fixed` —
and in both the word means *constant-output test double*, not
*fixed-point*.

This RFC does not settle that; ADR-0010 and ADR-0011 do. Their outcome
is a prerequisite for the package proposed here:

| from | to |
|---|---|
| `core/id/fixed` | `core/id/constant` |
| `core/rand/fixed` | `core/rand/constant` |

Both are renames of published import paths, accepted because neither
has an external consumer yet and the window closes at the first one.
`crypto.Framer.Fixed` keeps its name — a fixed-width field is a third
meaning of the word, but it is a method on an unrelated type and cannot
collide at an import site.

### A defined type, not a struct

`Fixed64` is `int64` underneath and exposed as such. That makes it
comparable, usable as a map key, directly ordered by `<` and `>`, and —
because its underlying type satisfies `cmp.Ordered` — sortable by
`slices.Sort` with no comparison function at all. A struct wrapper would
buy nothing: there is one field, and hiding it would only force an
accessor.

`Compare` exists for the `slices.SortFunc` and `cmp.Compare` plumbing
that wants a three-way result, and because every other ordered value
type in `core` — `epoch.Epoch`, `clock.Instant`, `id.ID`,
`crypto.Digest` — spells it `Compare`. It is not the primary way to
order two values; `<` is.

Unlike `id.ID`, which must stay a struct to keep its size field, there
is nothing here a caller could set wrongly *within the domain*. The
domain is the caveat, and it is the subject of the next section.

### The domain is symmetric, and excludes `math.MinInt64`

`Min` is `-math.MaxInt64`, not `math.MinInt64`. One representable value
is given up, and in exchange:

- `Neg` and `Abs` are **total**. There is no input for which the
  negation of a `Fixed64` is not a `Fixed64`, so neither returns an
  error and neither has a saturating branch that hides one input.
- The sign handling in `Mul` and `Div` loses its worst case.
  `math/bits.Mul64` and `Div64` are unsigned, so both operations take
  the magnitude of each operand and reapply the sign at the end.
  `math.MinInt64` is precisely the value whose magnitude is not an
  `int64`, and excluding it removes that branch rather than testing it.

The alternative — keeping `math.MinInt64` and returning an error from
`Neg` and `Abs` — is Alternative E below.

The cost is that `FromRaw` is no longer total. It returns `ErrRange`
for `math.MinInt64`, as do `Parse` and `UnmarshalBinary`. That is one
comparison on the decode path, not a free one.

It also means the type has an out-of-contract value. `Fixed64` is a
defined `int64`, so `Fixed64(math.MinInt64)` compiles; no constructor
and no decode path will produce it, and the behaviour of arithmetic on
it is unspecified — `Neg` and `Abs` return it unchanged, which is
wrong, and nothing detects that. Every route into the type rejects it;
an unchecked conversion is not a route, it is a bypass. This is the
same bargain the type already makes by being a defined `int64` rather
than a struct, and it is stated here rather than discovered later.

### The zero value is zero

The zero `Fixed64` is the number zero, and every operation on it works.

This is the opposite of what a money type wants, and the reason `money`
stays out of `core`: an amount with no currency is not a monetary zero,
so its zero value must be invalid. That distinction belongs to the type
that carries a currency, not to the arithmetic underneath it. `Fixed64`
is a number; numbers have a zero.

### Scale is eight, and fixed

Eight places, chosen to be exact for everything a foundation type is
asked to hold rather than inherited:

| use | places needed |
|---|---|
| money, minor units | 2 |
| basis points | 4 |
| interest and FX rates | 6 |
| per-unit pricing | 8 |

Eight covers all four exactly and leaves a range of ±92.2 billion,
which is beyond any single value a process reasons about. Nine places
would mirror `time.Duration`'s nanoseconds and drop the range to ±9.2
billion for a decimal nothing above needs.

The scale is a constant rather than a type parameter. Go cannot
parameterise on a value, so a per-scale type would need one type per
scale, and two of them could not be added without a conversion that is
exactly the bug this package exists to prevent. One scale means one
type, and `==` means what it says.

### Every operation is checked

`Add`, `Sub`, `Mul` and `Div` return `ErrOverflow` rather than wrapping,
saturating, or panicking.

Wrapping is how a billing bug becomes a credit. Saturating is worse: it
produces a plausible number with no signal, and `Max` is a value
somebody will store. Panicking is out because this module does not
panic in production paths — and unlike `Hasher.Combine`, an overflow is
a runtime condition reachable from ordinary input, not a programmer
error.

**Precision.** `Add` and `Sub` are exact: the result is the true sum or
difference whenever it is in range. `Mul` and `Div` cannot be, and the
package does not claim they are. `Fixed64` is closed under neither:
`Smallest.Mul(Smallest)` is 10⁻¹⁶, which is not representable at eight
places and rounds to `Zero`. What `Mul` and `Div` guarantee is that no
precision is lost *before* the rounding: both compute through a 128-bit
intermediate via `math/bits.Mul64` and `Div64`, so `a × b ÷ 10^Scale`
is evaluated with a single directed rounding at the end rather than an
intermediate overflow in the middle. Computing at 64 bits and dividing
afterwards would overflow for values a caller would consider ordinary.

**The 128-bit path is not panic-free by default.** `bits.Div64` panics
when the divisor is zero and when the quotient does not fit in 64 bits.
Both are reachable from ordinary input, so `Div` and `DivAway`
pre-check both before calling it: a zero divisor returns `ErrDivZero`,
and a high word not less than the divisor returns `ErrOverflow`. This
is the non-obvious part of the implementation and the reason the
128-bit intermediate is not free.

### Rounding is named, not configured

Two directions: truncation toward zero, which is what `Mul`, `Div` and
`Round` do, and away from zero, which is what `MulAway`, `DivAway` and
`RoundAway` do.

A rounding-mode enum was the alternative. It defers the decision to a
runtime value, which means every call site can differ and none of them
says which it chose in a way a reader can see. Two named methods make
the choice visible in the code that made it.

`Round` and `RoundAway` take a place count rather than operating on the
last raw unit, because quantisation is the common case and the one the
directional pair alone does not cover: a caller who computed at eight
places and must emit two has to reach a 2-place value, not shift a
single unit. `places` outside `[0, Scale]` returns `ErrRange`.
`RoundAway` can overflow — `Max.RoundAway(0)` exceeds `Max` — so both
return an error.

**Half-to-even is absent, and it is a rounding mode.** An earlier draft
of this RFC dismissed banker's rounding as a residue-allocation policy
rather than a property of one operation. That was wrong: round-half-to-
even is a quantisation rule for a single value, it is IEEE 754's default
mode and the standard library's `math.RoundToEven`, and it is the
statutory default in a good deal of financial reporting. It is left out
for a narrower reason: the two directional modes answer "must not create
units" and "must not destroy units", which is what a foundation type is
asked for, and half-to-even's benefit — an unbiased mean across many
roundings — is a reporting policy belonging to the type that knows it is
reporting. It is also purely additive; `RoundHalfEven` can land later
without changing a signature. See future work.

**Residue allocation is a different thing and stays out.** Splitting a
value across parts so they sum back to the original with no unit created
or destroyed is a real need, and deliberately absent: it takes a ratio
set, and how the remainder is handed out is the caller's policy. It
belongs to the type that has the ratios.

### Text is exact and round-trips

`String` renders all eight places — `"1.00000000"`, never `"1"` — so
that for every in-domain `f`, parsing its rendering yields `f` again:

```go
g, err := fixed.Parse(f.String())   // err == nil && g == f
```

Trimming would make the text shorter and the round trip conditional,
and would let two renderings of one number differ.

`Parse` accepts exactly this grammar and nothing else:

```
decimal  = [ "-" ] int [ "." frac ]
int      = digit { digit }
frac     = digit { digit }
digit    = "0" … "9"
```

Concretely: no leading `+`, no exponent, no underscores, no surrounding
whitespace, at least one digit on each side of the point (`".5"` and
`"1."` are both `ErrSyntax`), and `"-0"` parses to `Zero`. Anything
outside the grammar is `ErrSyntax`; a value outside `[Min, Max]` is
`ErrRange`. This is a trust boundary, so the accepted language is part
of the contract rather than a property of the implementation.

`Parse` rejects a non-zero digit beyond the eighth fractional place
with `ErrPrecision` rather than silently truncating. Trailing zeroes
beyond the eighth place are not a loss and are accepted, so
`"1.000000000"` parses and `"0.000000001"` does not. A caller who wrote
a significant ninth digit meant it, and dropping it is the
representation error this package exists to avoid, reintroduced at the
boundary.

`UnmarshalText` accepts exactly what `Parse` accepts and returns the
same errors, sharing one implementation. Two decode paths that disagree
about which inputs are valid is the defect RFC-0014 exists to close.

There is no `FromFloat`. A `float64` reaching this package has already
lost whatever it lost, and offering a conversion invites it into
exactly the arithmetic that must not touch one. Callers at a boundary
that speaks float parse the text form instead; where there is no text
form, they convert deliberately and own the result.

`Float` is likewise absent for the same reason in reverse: a caller
formatting for a human uses `String`, and a caller doing anything else
with a float has left the guarantee.

**JSON follows from `MarshalText`.** `encoding/json` honours
`encoding.TextMarshaler`, so a `Fixed64` encodes as the JSON *string*
`"1.00000000"` and not as a number. This is deliberate and is the whole
point: JSON numbers decode to `float64` by default, so a numeric
encoding would hand the value back to the type this package exists to
displace, at the one boundary where it is hardest to notice. The cost
is that it changes the shape of every API carrying one, which is a
consequence worth stating in an API's own docs.

### The binary encoding

```go
// AppendBinary appends the canonical 8-byte encoding of f's raw
// value to dst:
//
//     Raw   int64   8 bytes, big-endian, two's complement
//
// The encoding is a stable wire contract. Fixed64 values are signed
// over and persisted in artefacts that must verify across builds and
// across years; the layout will not change within a major version.
//
// Returns ErrRange for the out-of-contract math.MinInt64, so that
// every value on the wire is one a decoder will accept.
func (f Fixed64) AppendBinary(dst []byte) ([]byte, error)
```

Three properties are contract:

- **The scale is not encoded.** `Scale` is a constant of the type, not
  a property of a value. A decoder that could disagree with an encoder
  about the scale is the bug this package prevents; a field that can
  disagree is one somebody will set. When the scale changes, the major
  version changes with it.
- **`UnmarshalBinary` returns `ErrSize` unless `len(data) == 8`**, and
  `ErrRange` if the decoded raw value is `math.MinInt64`. A truncated
  read is a decode error, never a panic and never a partial value.
- **Byte order is not numeric order.** Big-endian two's complement puts
  `-1` at `ff ff ff ff ff ff ff ff` and `+1` at `00 …  01`, so the
  encoded form sorts negatives above positives. Callers who need an
  order-preserving key must apply their own sign-bit flip. The
  alternative — encoding in offset-binary so that byte order *is*
  numeric order — was rejected because it makes the wire form something
  other than the obvious `int64` encoding, which every cross-language
  port then has to be told about, and because ordering in memory is
  already available through `<` and `Compare`.

`AppendBinary` and `AppendText` are the primitives; `MarshalBinary` and
`MarshalText` are defined in terms of them. That is the shape
`encoding.BinaryAppender` and `encoding.TextAppender` were added for: a
caller assembling a larger buffer pays no intermediate allocation.

### Allocation contract

`Fixed64` is a value type; pass by value. Construction, inspection,
comparison, all arithmetic, and both rounding families are
zero-allocation — the arithmetic is register-resident integer work with
no heap traffic and no `math/big` fallback path.

`String` and `MarshalText` each allocate one buffer; `MarshalBinary`
allocates one 8-byte slice. `AppendText` and `AppendBinary` allocate
nothing when `dst` has capacity, which is what a caller with an
`arena.Arena` or a pooled buffer should use.

## Alternatives considered

### A. `math/big.Rat` or `big.Int` with a scale

**Why not:** exact, but not comparable, not fixed-width, and it
allocates. None of those is acceptable for a value that goes in a map
key, a struct compared with `==`, or a wire field, and the allocation
is on every operation rather than at a boundary.

### B. `int64` minor units by hand, no type

What careful code already does.

**Why not:** the scale ends up in a comment, two call sites disagree
about it, and nothing checks overflow. A type makes the scale part of
the value and the check part of the operation. It is also what lets
`==` be trusted, which a bare `int64` cannot promise.

### C. 128-bit storage

**Why not:** it doubles the width of every field to buy range beyond
±92.2 billion at eight places, and nothing in the motivating cases
needs it. A caller who does needs `big.Int` and knows it.

### D. Unchecked arithmetic with a separate overflow query

`f.Add(g)` returning a `Fixed64`, with `f.AddOverflows(g)` alongside.

**Why not:** it makes the safe path the longer one, and every call site
that skips the query is silently wrong. The error return puts the check
where it cannot be forgotten.

### E. Keep `math.MinInt64` in the domain

Use the full `int64` range, keep `FromRaw` total and unchecked, and
have `Neg` and `Abs` return an error for the one input whose negation
is not representable.

**Why not:** it buys one representable value — `-92233720368.54775808`,
a magnitude no motivating case reaches — and pays for it in the two
places that matter most. `Neg` and `Abs` acquire an error return that
is unreachable for every real input, which is the shape callers learn
to ignore, and the ones who ignore it are wrong exactly once. The sign
handling in `Mul` and `Div` acquires a branch for the value whose
magnitude is not an `int64`, and that branch is the one an attacker
picks and the test suite forgets. Saturating instead — returning `Max`
from `Neg(Min)`, as `foundation/fixed` does — is worse than either: it
hides exactly one input, silently, and it is the same input.

Excluding the value deletes the case rather than handling it. The price
is a checked `FromRaw`, which is one comparison on a path that is
already doing a bounds check on its input length.

## Drawbacks

- Every arithmetic operation returns an error, so a chained expression
  becomes several statements. That is the cost of not panicking and not
  wrapping, and it is most visible exactly where the arithmetic is
  densest.
- Eight places is a compromise. A caller needing more precision than
  eight, or more range than ±92.2 billion, cannot use this type at all
  and gets no partial answer.
- **A checked sum is order-dependent even though integer addition is
  associative.** A sequence whose total is representable can still fail
  depending on the order it is added in: `Max.Add(Max)` errors where
  `Max.Add(Min)` then `.Add(Max)` succeeds. The *value* is
  order-independent, which is the guarantee the motivation asks for;
  the *error* is not. Until `Sum` lands (see future work), a caller
  reordering a batch can change whether it fails.
- `Fixed64` is a defined `int64`, so a caller can convert one in and
  out with a cast and bypass every constructor. Since the domain
  excludes `math.MinInt64`, that cast can now produce a value no
  constructor would return and on which `Neg` and `Abs` are silently
  wrong. Nothing can forbid the cast — only document it.
- The absence of `FromFloat` and `Float` will read as missing rather
  than deliberate, and somebody will add them downstream.
- Renaming `id/fixed` and `rand/fixed` is churn in two packages that
  have nothing to do with decimals, done now only because the cost of
  doing it later is higher.

## Open questions

None. Two were resolved in drafting:

**Should the scale be configurable?** No — see above. Go cannot express
it without one type per scale, and cross-scale arithmetic is the bug
being prevented.

**Should an accumulator type carry the error through a chain**, the way
`crypto.Framer` accumulates bytes? It reads better for long expressions
and it is speculative until one exists. Deferred to future work rather
than shipped on a guess.

## Unresolved / future work

- `Sum` and `Product` over a slice, accumulating in a 128-bit
  intermediate and checking once at the end. This is not only a
  convenience: it is what makes a representable total succeed
  regardless of the order the terms arrive in, which the per-operation
  API cannot promise (see drawbacks).
- `RoundHalfEven`, when a consumer needs an unbiased mean across many
  roundings rather than a directional guarantee. Purely additive.
- An accumulator for chained arithmetic, if call sites show the
  statement-per-operation shape is a real cost rather than an
  aesthetic one.
- `driver.Valuer` and `sql.Scanner` are deliberately absent: `core` is
  stdlib-only by charter but `database/sql` drags a driver registry and
  a connection pool into any binary that links it, for a type that has
  a text form a storage adapter can already use. If a consumer shows
  the adapter boilerplate is real, it belongs in a `fixed/fixedsql`
  sub-package, not here.
