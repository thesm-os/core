---
rfc: 0039
title: Signature Policies and Verifier Resolution
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0023
---

# RFC-0039: Signature Policies and Verifier Resolution

## Summary

We propose two additions to `crypto/sign`:

- A `Resolver`: a table, built by the caller, from an algorithm name to
  a constructor of verifiers. An offline verifier uses it to build the
  verifier a stored signature names. A name missing from the table is
  an error classified `errs.Unsupported`, never a valid result.
- A `Policy`: k of n parties must sign, where a party is one or more
  keys that must all sign. It expresses a threshold of approvers, a
  quorum of witnesses, and a hybrid signature that needs both a
  classical and a post-quantum key, and it counts each key once.

## Motivation

### Verifiers from stored names

A signature that must verify years later is stored with the name of
its algorithm and its key, and an offline verifier builds the matching
`Verifier` from them. Core has a constructor per algorithm package and
nothing that maps a name to one. Each caller writes the map, and each
decides what an unknown name means. An unknown name treated as a pass
turns an algorithm the verifier does not know into an accepted
signature.

### Thresholds and hybrid signatures

Three rules recur wherever signatures guard a decision:

- **A threshold of approvers.** k distinct keys of a trusted set must
  sign, and the requester's own key does not count.
- **A quorum of witnesses.** k of n independent parties must cosign.
- **A hybrid signature.** During the move to post-quantum
  cryptography, a signer has a classical key and an ML-DSA key, and
  a verifier requires a valid signature from both.

Each rule reduces to counting valid signatures by distinct key. The
counting has failed in production more than once:

- python-tuf, CVE-2020-6174, counted "each of multiple signatures with
  identical authorized keyids separately towards the threshold".
- go-tuf, GHSA-3633-5h82-39pq, counted the same public key twice when
  it appeared under two key IDs.
- awslabs/tough, CVE-2020-15093, did not verify "the uniqueness of keys
  in the signatures provided to meet the threshold".

## Detailed design

### Resolver

```go
// Resolver maps an algorithm name to a function that builds a
// Verifier from an encoded public key. The caller builds it from the
// algorithms it trusts. A Resolver has no global registry and no
// default entries.
type Resolver map[crypto.Algorithm]func(pub []byte) (Verifier, error)

// Verifier returns a Verifier for alg and the encoded public key pub.
//
// Returns ErrUnknownAlgorithm, classified errs.Unsupported, for a name
// the Resolver does not contain, and for an entry that builds no
// Verifier or a Verifier of another algorithm. Returns the
// constructor's error for a key it refuses.
func (r Resolver) Verifier(alg crypto.Algorithm, pub []byte) (Verifier, error)
```

Each algorithm package provides a function of the right shape, so a
Resolver is one literal:

```go
resolve := sign.Resolver{
    crypto.AlgEd25519:   ed25519.Resolve,
    crypto.AlgECDSAP384: ecdsap384.Resolve,
    crypto.AlgMLDSA87:   mldsa.Resolver(mldsa.MLDSA87, "example/checkpoint/v1"),
}
```

`ed25519.Resolve` wraps `NewVerifierFromBytes`, and
`ecdsap384.Resolve` wraps `NewVerifierFromPKIX`. `mldsa.Resolver`
binds a parameter set and a context string, because an ML-DSA verifier
needs both. `Resolver.Verifier` checks that the Verifier an entry
builds reports the requested algorithm, so a table that maps a name to
the wrong constructor fails instead of verifying under another
algorithm.

A Resolver builds verifiers from key material the caller trusts: its
configuration, its trust store, or a key it pinned earlier. It is never
applied to a key carried inside the artifact being verified. JOSE
shows both failures this rule prevents:

- **An embedded key.** node-jose trusted a key embedded in a token's
  header, so any attacker could sign a token with a key of their own
  (CVE-2018-0114).
- **Algorithm confusion.** jsonwebtoken accepted an HMAC token when it
  expected RSA or ECDSA, keyed with the server's public key
  (CVE-2015-9235).

RFC 8725, the JWT best current practice, states in section 3.1:
"Libraries MUST enable the caller to specify a supported set of
algorithms and MUST NOT use any other algorithms when performing
cryptographic operations." A Resolver is that set. An attacker who
controls a stored algorithm name can select only an algorithm the
caller listed, and the caller's trust store binds each key to its
algorithm.

### Policy

```go
// Signature is one signature with the identity of the key that made
// it.
type Signature struct {
    Algorithm crypto.Algorithm
    Value     []byte
    KeyID     KeyID
}

// Party is a signer that counts once toward a threshold. It has
// one or more keys, and it counts only when every one of its keys has
// a valid signature. A party with an Ed25519 key and an ML-DSA key is
// a hybrid signer.
type Party struct {
    Name string
    Keys []Verifier
}

// Policy requires valid signatures from at least Threshold of its
// parties. It is immutable and safe for concurrent use. The zero
// Policy refuses every check with ErrPolicy.
type Policy struct{ /* unexported fields */ }

// NewPolicy returns a Policy over parties with the given threshold.
//
// Returns ErrPolicy, classified errs.Invalid, when threshold is not
// between 1 and len(parties), when a party has no keys or a nil key,
// or when one key appears twice, in one party or in two. The key check
// compares KeyIDs and the encoded public keys, so one key cannot be
// listed under two identities.
func NewPolicy(threshold int, parties ...Party) (Policy, error)

// Check reports whether sigs satisfy p for message.
//
// Check considers at most one signature per key of p: the first in
// sigs whose KeyID names that key. It ignores later signatures for
// the same key and signatures for keys outside p without verifying
// them. A considered signature counts when its algorithm matches its
// key's and it verifies over message. A party counts when all of its
// keys have a counting signature. Parties with a key in exclude do not
// count, which keeps a requester from approving their own request.
//
// Check stops verifying as soon as Threshold parties count, and as
// soon as the remaining parties can no longer meet Threshold. It runs
// at most one verification per key of p, whatever the number of
// signatures a caller or an attacker supplies.
//
// Returns nil when at least Threshold parties count. Otherwise it
// returns ErrThreshold, classified errs.Integrity, wrapped with the
// most parties that could count and the threshold.
//
// # Allocation contract
//
// Zero-alloc when it returns nil for a policy of up to 64 keys, whose
// bookkeeping fits on the stack, apart from what each Verifier
// allocates. A failed check allocates the returned error.
func (p Policy) Check(message []byte, sigs []Signature, exclude ...KeyID) error
```

On the machine that measured RFC-0033's figures, ML-DSA-87 verifies in
183 µs and Ed25519 in 27 µs. Without the bound,
an attacker who can attach signatures to a request could attach 5,000
for one ML-DSA-87 key and cost the verifier 0.9 CPU-seconds per
request. With it, a policy of five parties with two keys each costs at
most ten verifications.

A hybrid signature is a policy of one party with two keys:

```go
hybrid, err := sign.NewPolicy(1, sign.Party{
    Name: "signer",
    Keys: []sign.Verifier{ed25519Key, mldsaKey},
})
```

Two approvals of five approvers, excluding the requester, are a policy
of five single-key parties with a threshold of two, checked with the
requester's KeyID in `exclude`.

### Errors

```go
var (
    // ErrUnknownAlgorithm reports an algorithm a Resolver does not
    // contain. It classifies as errs.Unsupported.
    ErrUnknownAlgorithm = errs.WithClass(errors.New("sign: unknown algorithm"), errs.Unsupported)

    // ErrPolicy reports a policy that cannot be satisfied or that
    // lists a key twice, and a check on the zero Policy. It
    // classifies as errs.Invalid.
    ErrPolicy = errs.WithClass(errors.New("sign: invalid policy"), errs.Invalid)

    // ErrThreshold reports signatures that satisfy fewer parties than
    // the threshold. It classifies as errs.Integrity.
    ErrThreshold = errs.WithClass(errors.New("sign: signature threshold not met"), errs.Integrity)
)
```

### Tests

- `Check` counts two signatures from one key once, and ignores a
  signature from a key outside the policy.
- A party with two keys counts only with both signatures present and
  valid.
- `NewPolicy` refuses one key in two parties, one key twice in a party,
  and a public key listed under a second KeyID.
- An excluded key removes its party, and a signature whose algorithm
  differs from its key's does not count.
- `Resolver.Verifier` returns `ErrUnknownAlgorithm` for a missing name
  and for an entry of the wrong algorithm, and a Verifier that verifies
  a known key's signature.
- The zero `Policy` refuses every check.
- `Check` runs at most one verification per key of the policy, counted
  through a Verifier that records its calls, when the caller passes a
  thousand signatures for one key. It stops once the threshold is met,
  and once the threshold can no longer be met.
- `TestZeroAlloc` covers `Check` for a policy of 64 keys.

## Alternatives considered

### A. A global registry

Algorithm packages would register their constructors in `init`, as
`database/sql` drivers and `image` formats do.

**Why not:** a registry makes every imported algorithm acceptable
everywhere in the process, including to a verifier that should accept
only one. The set of accepted algorithms is a security decision, and a
table the caller writes makes it visible at the call site.

### B. Threshold signatures

A threshold scheme such as FROST, RFC 9591, would produce one
signature from k of n key shares.

**Why not:** a threshold signature hides which parties signed, and an
audit needs to know. It also requires a distributed key-generation
ceremony, and the standard library has no implementation. Counting
individual signatures keeps each approver's signature attributable.

### C. A composite algorithm for hybrids

A hybrid would be one algorithm whose signature concatenates a
classical and a post-quantum signature, as the IETF LAMPS composite
signature drafts define.

**Why not:** a composite algorithm fixes the pair of algorithms in its
name, and every pair needs its own name and its own format. A party
with two keys expresses any pair with the algorithms core already has,
and each half verifies with its own standard.

## Drawbacks

- `Check` considers only the first signature for each key. A caller
  that puts an invalid signature before a valid one for the same key
  fails the check, where a lenient verifier would try the next. Honest
  signers never send two signatures for one key, and the rule is what
  bounds the work.
- A caller must keep a Resolver's table in step with the algorithms
  its stored signatures use. A missing entry fails verification of old
  signatures, by design.
- Hybrid signatures stored as two `Signature` values are larger than a
  composite encoding would be, by the two key IDs and names.

## Open questions

None.

## Unresolved / future work

- A text format for policies, such as C2SP tlog-policy for witness
  quorums, parsed into a `Policy`.
- Nested policies, where a party is itself a policy.

## References

- python-tuf, CVE-2020-6174.
- go-tuf, GHSA-3633-5h82-39pq,
  <https://github.com/advisories/GHSA-3633-5h82-39pq>.
- awslabs/tough, CVE-2020-15093,
  <https://github.com/awslabs/tough/security/advisories/GHSA-5q2r-92f9-4m49>.
- The Update Framework specification, thresholds of signing keys.
- RFC 8725, "JSON Web Token Best Current Practices", section 3.1,
  <https://www.rfc-editor.org/rfc/rfc8725.html>.
- jsonwebtoken, CVE-2015-9235, and node-jose, CVE-2018-0114.
- RFC 9591, "The Flexible Round-Optimized Schnorr Threshold (FROST)
  Protocol".
- RFC-0013, the signing seam.
- RFC-0033, ML-DSA signatures.
