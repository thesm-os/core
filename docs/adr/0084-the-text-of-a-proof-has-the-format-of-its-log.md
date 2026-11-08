---
adr: 0084
title: The Text of a Proof Has the Format of Its Log
status: Accepted
date: 2026-11-09
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0084: The Text of a Proof Has the Format of Its Log

## Status

Accepted

## Context

RFC 9162 makes the hash algorithm of the Merkle tree a parameter of a log.
Every function of `tlog` that builds, proves or verifies a tree takes a
`crypto.Hasher`. A log under CNSA 2.0 proves and verifies its records with
`tlog` and SHA-384.

`tlog.Proof` writes and reads the text of C2SP tlog-proof v1. Its header line
is `c2sp.org/tlog-proof@v1`, and each hash line is the base64 of one SHA-256
hash. `AppendText` refuses a hash of another size, so the log under
CNSA 2.0 cannot write the text of its proofs with core. A writer of such
proofs copies the codec under a header of its own, and each verifier copies
the parser.

C2SP defines no header for another hash. A format with another hash needs a
header line that its owner names. C2SP signed-note reserves the type 0xff for
signature types without an assigned byte, followed by a longer identifier,
and `note.NewType` takes that identifier.

`Proof` also has a canonical kanon codec, the form of a proof in a kanon
record. That codec writes each field whose value is not zero.

## Decision

We will pass the text format of a proof to its codec as a parameter. The
hash of a proof is a parameter of its log, as the `crypto.Hasher` of the
functions of `tlog` is.

```go
// ProofFormat is a text format of a Proof: its header line and the size of
// each hash.
type ProofFormat struct {
	Header string // the first line of the text
	Size   int    // the size of each hash in bytes
}

func (p *Proof) AppendFormat(dst []byte, f ProofFormat) ([]byte, error)
func (p *Proof) UnmarshalFormat(text []byte, f ProofFormat) error
```

- An empty Header is the header line of C2SP tlog-proof v1, and a Size of 0
  is 32 bytes. The zero ProofFormat is C2SP tlog-proof v1. `AppendText` and
  `UnmarshalText` write and read that format, with every text and every
  error that they had before.
- Every other rule of the text is the rule of C2SP tlog-proof: the extra
  line, the index line, the canonical base64 of each hash, the empty line
  and the checkpoint.
- `AppendFormat` and `UnmarshalFormat` return `ErrProofText` for a format
  whose Size is not 32, 48 or 64 bytes, whose Header contains a newline, or
  whose Header is the header line of C2SP tlog-proof v1 with a Size other
  than 32.
- A reader of more than one format reads the first line of a text, and
  passes the format of that header line to `UnmarshalFormat`.
- The kanon form of `Proof` does not change.

## Alternatives Considered

### A field of the format in Proof

`Proof` would gain a field `Format ProofFormat`, which `AppendText` and
`UnmarshalText` read.

Rejected. The kanon codec of `Proof` would write the field into the record
of each proof of another format, so every stored proof of a log would repeat
the header line of the log. A field outside the kanon form would drop the
format at each decode. `AppendText` of the decoded proof would then refuse
its hashes.

### The size of each hash from its line

`UnmarshalText` would take the size of each hash from the length of its
line, under any header line.

Rejected. A format has one size of hash, because its log has one hash. A
text with hashes of two sizes would then parse.

### A registry of formats by their header line

The package would keep the formats that callers register, and
`UnmarshalText` would look up the format of the header line of each text.

Rejected. A registry is state of the process that every caller shares, and
two callers could register one header line with two sizes.

## Consequences

**Positive:**

- A log over SHA-384 or SHA-512 writes and reads the text of its proofs
  with core, with the allocation contract of C2SP tlog-proof.
- The zero format keeps every text and every error of `AppendText` and
  `UnmarshalText`.

**Negative:**

- A caller of a format other than C2SP tlog-proof passes the format on each
  call. A container that writes a Proof through `encoding.TextAppender`
  writes C2SP tlog-proof alone.
- Core does not check that the header line of another format is unique. Its
  owner names it under a domain that the owner controls, as signed-note
  recommends for an identifier.
- The codec checks the format on each call and computes the length of each
  hash line from the size. On one core of an AMD Ryzen 9 9950X3D with Go
  1.27.3, in four alternating rounds of three runs per side, `AppendText` of
  the example of the specification took 109.1 ns against 104.3 ns before
  (4.6% more), and `UnmarshalText` took 243.1 ns against 225.8 ns (7.6%
  more). Neither allocates.

**Neutral:**

- `Proof` has two more methods, and `ProofFormat` is a new type.

## References

- Issue 132, <https://github.com/thesm-os/core/issues/132>.
- RFC 9162, section 2.1.1 and the registry of hash algorithms of section
  10.2.1, <https://www.rfc-editor.org/rfc/rfc9162#section-2.1.1>.
- C2SP tlog-proof at 2bc1f46, the format of a proof,
  <https://github.com/C2SP/C2SP/blob/2bc1f467fc41d1f6605d73bd217a481ede0565e0/tlog-proof.md#format>.
- C2SP signed-note at 2bc1f46, the type 0xff,
  <https://github.com/C2SP/C2SP/blob/2bc1f467fc41d1f6605d73bd217a481ede0565e0/signed-note.md>.
- `note.NewType`, the identifier of a signature type without a byte of
  signed-note.
- Go's `time.Time.AppendFormat`, a codec that takes its layout as a
  parameter, <https://pkg.go.dev/time#Time.AppendFormat>.
