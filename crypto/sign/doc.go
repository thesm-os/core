// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sign defines the public-key signing seams: [Signer], which
// signs, and [Verifier], which checks a signature against a public key.
//
// Library code signs and verifies through an injected [Signer] or
// [Verifier] without binding to an algorithm. A verifier-only consumer,
// such as an audit service, a webhook receiver or an offline auditor,
// builds a [Verifier] from public-key bytes and never has a private
// key. The symmetric [crypto.MAC] seam cannot separate the two roles.
//
// # Provided implementations
//
//   - go.thesmos.sh/core/crypto/sign/ed25519: Ed25519 PureEdDSA per
//     RFC 8032 §5.1.6, backed by [crypto/ed25519]. Implements [Signer]
//     and [AppendSigner], and not [StreamingSigner], see Streaming.
//   - go.thesmos.sh/core/crypto/sign/ecdsap384: ECDSA over NIST P-384
//     with SHA-384 per FIPS 186-5 and ASN.1 DER signatures, backed by
//     [crypto/ecdsa]. Implements [Signer], [AppendSigner] and
//     [StreamingSigner].
//   - go.thesmos.sh/core/crypto/sign/mldsa: ML-DSA-44, ML-DSA-65 and
//     ML-DSA-87 per FIPS 204, backed by [crypto/mldsa]. Implements
//     [Signer] and [AppendSigner].
//
// The implementations run through Go's FIPS 140-3 module when
// GODEBUG=fips140=on is set. This package does not refuse to run
// outside FIPS mode. A caller that must refuse applies that policy
// itself.
//
// # Algorithm vocabulary
//
// Each [Signer] reports its algorithm through [Verifier.Algorithm] as a
// value of the open-string vocabulary [crypto.Algorithm], for example
// [crypto.AlgEd25519] or [crypto.AlgMLDSA87]. Receipts persist the
// algorithm string, so a verifier selects the matching [Verifier]
// implementation offline.
//
// # KeyID
//
// [KeyID] is a 16-byte value that identifies one public key. It differs
// from [crypto.ID], which identifies an implementation. Each
// implementation derives a canonical [KeyID] from its public-key bytes
// through a KeyIDFromPub function in its own package:
//
//   - Ed25519: SHA-256(raw 32 public-key bytes)[:16].
//   - ECDSA P-384: SHA-256(SEC 1 uncompressed point:
//     0x04 || X(48 BE) || Y(48 BE))[:16].
//   - ML-DSA: SHA-256(FIPS 204 public-key encoding)[:16].
//
// These derivations are part of the public contract. The same public
// key produces the same KeyID across builds, languages and verifier
// services. Each package's TestKeyIDStability pins its derivation with
// fixed vectors.
//
// # Streaming
//
// A hash-then-sign algorithm can sign a stream through the optional
// [StreamingSigner] and [StreamingVerifier] capabilities. ECDSA P-384
// implements them. A consumer that signs with any algorithm asserts for
// the capability and falls back to whole-message [Signer.Sign].
//
// Ed25519 PureEdDSA cannot stream. RFC 8032 §5.1.6 defines:
//
//	R = SHA-512(dom2 || prefix || M) mod L
//	S = (r + SHA-512(R || A || M) * s) mod L
//
// The message M appears in two SHA-512 computations, and the second
// depends on the first through R. A streaming signer would have to
// buffer M and hash it twice when the stream closes, so the Ed25519
// [Signer] does not implement [StreamingSigner].
//
// # Resolvers and policies
//
// A [Resolver] builds the [Verifier] a stored signature names, from a
// table of the algorithms the caller trusts. It has no default entries,
// so a stored name selects only an algorithm the caller listed.
//
// A [Policy] requires valid signatures from a tree of rules. An [AllOf]
// rule is a set of keys that must all sign: one signer, or a hybrid
// signer with a classical and a post-quantum key. An [AtLeast] rule
// requires a threshold of its children: a quorum of parties, or the
// alternatives of one party. [NewPolicy] builds a threshold of parties,
// and [NewPolicyTree] builds any tree, such as the nested groups of a
// C2SP tlog-policy. [Policy.Check] counts each key once and verifies at
// most one signature per key of the policy. [Policy.SatisfiedBy] runs the
// same check and reports its result without an error, for a caller that
// probes one set of signatures after another.
//
// [Policy.Check] takes keys to exclude, such as the key of the requester
// of an approval, and removes the whole party of an excluded key. The
// parties of a tree are the children of its root, or the rules that
// [Rule.AsParty] marks at any depth, such as each person of a policy of
// groups of people.
//
// A caller that builds its policy again, such as at each load of a
// configuration, builds the rules in a [Rules] and the policy with
// [Policy.Reset], both of which reuse the memory of the last build.
//
// # Encoding
//
// kanon generates the codec of [Signature], so a signature in the
// record of any consumer encodes with the field numbers that this
// package records. [Signature] explains the encoding.
//
// # Signing across a process boundary
//
// A [Signer] backed by a hosted key service or a hardware module
// implements [ContextSigner], so a caller can bound the wait with a
// context. Callers sign through [SignContext], which uses the
// capability when a signer has it and calls [Signer.Sign] otherwise.
// The in-process signers in this module do not take a context and do
// not implement it.
//
// # Signing into a caller's buffer
//
// A [Signer] that writes its signature into a buffer of the caller
// implements [AppendSigner]. Callers sign through [AppendSign], which uses
// the capability when a signer has it and appends a copy of the
// signature of [SignContext] otherwise. A caller that reuses its buffer
// signs with the Ed25519 signer of this module without an allocation.
//
// # Decorators
//
// A decorator that wraps a [Signer] implements Unwrap() Signer, and one
// that wraps a [Verifier] implements Unwrap() Verifier. [AsStreamingSigner],
// [AsStreamingVerifier], [AsContextSigner] and [AsAppendSigner] follow
// Unwrap to find a capability behind any number of decorators.
// [SignContext] uses AsContextSigner, and [AppendSign] uses
// AsAppendSigner.
//
// # Failure semantics
//
// The package separates two classes of failure:
//
//   - Runtime errors are returned. They include entropy exhaustion in
//     ECDSA signing and anything else the environment causes.
//   - A failed verification returns false. Separate results for a
//     malformed signature and a cryptographic mismatch would risk a
//     timing side channel, so [Verifier.Verify] collapses every
//     failure to false. A caller that needs per-failure diagnostics
//     checks the signature's length and format before it calls Verify.
//
// Constructors return errors for a wrong curve or a wrong key size.
// Those are runtime input and not programmer errors, and the module
// does not panic in production code.
//
// # Generate API asymmetry
//
// The constructors differ deliberately. Each one's signature reflects
// what the underlying standard library function honours:
//
//   - [go.thesmos.sh/core/crypto/sign/ed25519.Generate] and
//     [go.thesmos.sh/core/crypto/sign/mldsa.Generate] take a
//     [go.thesmos.sh/core/rand.Rand]. [crypto/ed25519.GenerateKey]
//     honours its [io.Reader], and the ML-DSA Generate reads its
//     32-byte seed from the source. A deterministic source, such as
//     [go.thesmos.sh/core/rand/seeded.Rand], produces deterministic
//     keys for tests.
//   - [go.thesmos.sh/core/crypto/sign/ecdsap384.Generate] takes no
//     argument. Since Go 1.26, [crypto/ecdsa.GenerateKey] ignores its
//     [io.Reader] and reads the runtime's internal entropy unless
//     GODEBUG=cryptocustomrand=1 is set. A source parameter that the
//     standard library ignores would mislead the caller. Tests that
//     need deterministic ECDSA keys use
//     [testing/cryptotest.SetGlobalRandom], which seeds the runtime's
//     internal generator.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey] and [Verifier.Algorithm] are
// zero-allocation. [Verifier.Verify] is zero-allocation for Ed25519 and
// ML-DSA. ECDSA P-384 verification allocates, because
// [crypto/ecdsa.VerifyASN1] performs big.Int arithmetic.
// Implementations document their own allocation behaviour.
//
// [Signer.Sign] allocates the returned signature. [AppendSign] into a
// buffer with room allocates nothing for Ed25519, because
// [crypto/ed25519.Sign] returns a signature that the compiler keeps on
// the stack of its caller. For ML-DSA and ECDSA P-384 it allocates what
// [crypto/mldsa.PrivateKey.Sign] and [crypto/ecdsa.SignASN1] allocate,
// since both return a new slice.
//
// [Policy.Check] allocates nothing when it returns nil for a policy of
// at most 64 keys and 128 rules, and [Policy.SatisfiedBy] allocates
// nothing for such a policy whatever its result. [NewPolicyTree]
// allocates the index of the keys, the keys and the rules at their exact
// sizes: four allocations for the example policy of tlog-policy.
// [Policy.Reset] and the methods of [Rules] allocate nothing when they
// reuse the memory of a build of the same size.
package sign
