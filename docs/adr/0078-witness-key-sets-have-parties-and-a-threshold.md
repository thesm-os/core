---
adr: 0078
title: The Key Sets of a Witness Log Have Parties and a Threshold
status: Accepted
date: 2026-11-05
supersedes: RFC-0054
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0078: The Key Sets of a Witness Log Have Parties and a Threshold

## Status

Accepted

Supersedes the key sets of `witness.Log`, the check of the signatures of a
log and its departure from tlog-witness in
[RFC-0054](../rfc/0054-checkpoint-witnesses.md). The rest of RFC-0054
remains in force.

## Context

A witness verifies the signatures of a log on each checkpoint before it
cosigns. `witness.Log` lists the keys of a log as key sets, which are
alternatives, such as the keys before and after a rotation. A checkpoint
needs a valid line of every key of one set. A set contains one key for a
log that signs with one algorithm, and one key per algorithm for a hybrid
log.

A log can also split its signing among n keys, so that a checkpoint needs
the signatures of any k of them. Each key is then under another custodian,
and fewer than k custodians cannot sign a checkpoint. A `Log` expresses
that rule only as the n choose k sets of k keys. The server computes the
key ID of every key of every set on each call, and each key ID is a
SHA-256. A benchmark of `Server.Advance` over `blob/memory`, with one
Ed25519 cosigner and one update per call, measured these times on an AMD
Ryzen 9 9950X3D with Go 1.27.3, in three runs of 200 calls each:

| Rule | Key sets | Time per call | Allocations per call |
|---|---|---|---|
| 1 of 1 | 1 | 45.5 to 46.5 µs | 15 |
| 2 of 3 | 3 | 74.1 to 79.9 µs | 15 |
| 3 of 5 | 10 | 105.9 to 107.7 µs | 17 |
| 5 of 10 | 252 | 320.0 to 323.5 µs | 18 to 19 |
| 8 of 16 | 12,870 | 14.53 to 14.69 ms | 20 |

C2SP signed-note requires a verifier to accept 16 signatures, so a note
with the lines of 16 log keys is a valid input.

These facts constrain a threshold:

- The sets of a log are alternatives, and a rotation can change the number
  of keys that must sign.
- C2SP tlog-checkpoint recommends the origin as the key name of a log. A
  deployment of many logs under one prefix then has one key name per
  origin.
- The thresholds of `crypto/sign` count parties. A party has one or more
  keys, and counts only when every one of its keys has a valid signature.
  A hybrid party counts only with both of its algorithms.
- A threshold counts each key once. Implementations of TUF counted one key
  more than once toward a threshold when metadata listed it under two key
  IDs. `crypto/sign` refuses a policy that lists one public key twice.

## Decision

We will give each key set of a `witness.Log` its parties and a threshold
of them, because each set of a rotation has a threshold of its own, and a
policy of `crypto/sign` counts parties.

```go
type Log struct {
	Hasher crypto.Hasher
	Keys   []KeySet
}

type KeySet struct {
	Parties   [][]note.Key
	Threshold int
}
```

- A party counts when every one of its keys has a valid signature line.
- A set counts when at least `Threshold` of its parties count. A
  `Threshold` of zero requires every party.
- A checkpoint needs one set that counts.
- The server verifies every line whose key name and key ID are those of a
  key of any set. One invalid line refuses the note.
- `Server.Advance` and the handler of `add-checkpoint` refuse a `Log` with
  an error that wraps `ErrConfig` when one of its sets has no party, a
  party without a key, a `Threshold` below zero or above the number of its
  parties, or one public key twice. One public key can be in more than one
  set.

## Alternatives Considered

### A threshold of the Log, counted per signature type

The `Log` gains one `Threshold`. Each set lists the n keys of a log, and a
checkpoint needs valid lines of k distinct keys of each signature type of
one set.

Rejected. One threshold applies to every set, so a rotation from 2 of 3
keys to 3 of 5 keys applies the threshold of one set to the other. A count
per signature type accepts k classical lines from some custodians and k
post-quantum lines from others, which no k parties signed. The rule also
does not refuse one key listed twice.

### A signature policy of crypto/sign in the Log

The `Log` contains a `sign.Policy` over the Verifiers of the log keys. The
server checks each note against it.

Rejected. `Policy.Check` stops once its result is decided, and verifies
only the first line of each key. tlog-witness requires a 403 for every
invalid line of a trusted key. The server also verifies each line once for
all the updates of a note. A policy would need two more methods: one to
find the key of a line, and one to count over the lines that the caller
verified. A `Policy` also contains resolved Verifiers. A deployment that
gives each key the name of its origin would then build a Verifier per key
and a `Policy` per origin on every call. A `Log` of `note.Key` values
costs nothing to build.

### A faster walk of the key sets

The `Log` keeps its sets of `[]note.Key`, and the server walks them
faster, such as with each key ID computed once per call.

Rejected. The configuration itself has n choose k sets: 12,870 sets of 8
keys for 8 of 16, and 601,080,390 sets of 16 keys for 16 of 32. Every walk
reads every key of every set.

## Consequences

**Positive:**

- A rule of k of n keys is one set of n parties, and the server reads each
  key of the set once per update. The time of a call grows with the lines
  that the server verifies, and not with the number of key sets. The
  benchmark of the context compared one set of n parties with the
  threshold k against the n choose k sets of k keys, in three alternating
  rounds of two runs each. The median times per call were:

  | Rule | Sets of k keys | One set of n parties |
  |---|---|---|
  | 1 of 1 | 47.2 µs | 47.7 µs |
  | 5 of 10 | 321.1 µs | 162.4 µs |
  | 8 of 16 | 14.69 ms | 247.3 µs |

  benchstat did not find a difference for 1 of 1, 2 of 3 and 3 of 5, or in
  the allocations of any rule.
- Each set of a rotation has the threshold of its own rule.
- A hybrid party counts only with both of its algorithms, as a party of
  `crypto/sign` does.
- A key that a set lists twice is a configuration error, so it cannot count
  twice.

**Negative:**

- `Log.Keys` changes type, which breaks every `ServerConfig.Logs`. A set of
  one key becomes `KeySet{Parties: [][]note.Key{{k}}}`.
- The witness counts parties itself, without `crypto/sign`. Its rule is a
  choice of key sets with a threshold over parties each, so it cannot
  express a threshold of thresholds.
- Each call compares the public keys of each set in pairs, so the check of
  a set grows with the square of its number of keys.
- The rule departs further from tlog-witness, which accepts one valid
  signature of a trusted key.

**Neutral:**

- A set of one party with a `Threshold` of zero needs a valid line of every
  key of the party, as a set of `[]note.Key` did.
- The server refuses a note of more than 64 signature lines, so a set whose
  threshold needs more lines than 64 never counts.

## References

- Issue 120, <https://github.com/thesm-os/core/issues/120>.
- RFC-0054, checkpoint witnesses: `witness.Log`, the signatures of a log
  and the departures from tlog-witness.
- C2SP tlog-witness, <https://github.com/C2SP/C2SP/blob/main/tlog-witness.md>.
- C2SP tlog-checkpoint, the key name of a log,
  <https://github.com/C2SP/C2SP/blob/main/tlog-checkpoint.md>.
- C2SP signed-note, the 16 signatures that a verifier accepts,
  <https://github.com/C2SP/C2SP/blob/main/signed-note.md>.
- go-tuf, one public key under two key IDs counted twice toward a
  threshold, <https://github.com/advisories/GHSA-3633-5h82-39pq>.
- python-tuf, signatures of one key counted separately toward a threshold,
  <https://github.com/advisories/GHSA-pwqf-9h7j-7mv8>.
- `crypto/sign`, `Party`, `NewPolicy` and `Policy.Check`.
