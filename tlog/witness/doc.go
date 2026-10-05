// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package witness implements both sides of C2SP tlog-witness, the protocol
// in which a transparency log sends a checkpoint to a witness and the
// witness returns its cosignature.
//
// # Client
//
// A [Client] calls one witness. [Client.AddCheckpoint] sends a checkpoint
// note and a consistency proof to add-checkpoint, verifies the cosignature
// lines that the witness returns with the witness's keys, and appends them
// to the buffer of the caller. [Client.Checkpoint] reads the checkpoint
// that the witness serves for an origin on its monitor retrieval route.
//
// # Server
//
// A [Server] is a witness. [Server.AddCheckpoint] returns the handler of
// add-checkpoint, [Server.Checkpoint] the handler of the monitor retrieval
// route, and [Server.Advance] advances the origins of up to 4,096
// checkpoints under one note in one commit. The server runs the checks of
// tlog-witness in their order: the form of the request, the origin, the
// signatures of the log, the bounds of the checkpoint, the room in the
// state, the old size and the consistency proof. A request that fails a
// check gets the status that the protocol assigns to it.
//
// The server commits each checkpoint to a journal in a [blob.Store] before
// it cosigns it, so it never cosigns a checkpoint that the journal does not
// contain:
//
//   - Concurrent calls of one process commit together. One commit at a time
//     takes the queued calls, up to 16 MiB of their encoding and 4,096
//     updates.
//   - A commit takes the time of its cosignatures from one reading of UTC
//     within MaxError, or from the head's record when that is later. It
//     creates its record and replaces the head of the journal under
//     IfMatch. Processes that share one store commit through that head.
//     No two calls advance one origin from one stored state.
//   - After the head, the commit signs the note of each call with every
//     cosigner at the time of the commit. Each cosigner signs on up to
//     GOMAXPROCS goroutines. The calls receive their lines then.
//   - The commit then stores the lines while the next commit runs. One
//     write of lines runs at a time, and the monitor retrieval route
//     serves an update once its lines are stored.
//   - A commit whose signature fails, or whose lines the store refuses,
//     leaves the record without lines. A later GET or snapshot repairs
//     the record at its time.
//   - A circuit of a [resilience.Breaker] per cosigner stops the commits
//     while the cosigner fails.
//
// # Journal
//
// The journal is a hash chain of records, which the head names:
//
//   - head names the latest record and the installed snapshot. It is the
//     one object that the server replaces.
//   - records/<seq>-<hash> is one commit, with the name of its predecessor.
//   - lines/<record name> contains the cosignature lines of a record.
//   - snapshots/<seq>-<hash> is the state as of a record.
//   - groups/<seq>-<hash> contains the latest updates of idle origins, which
//     a snapshot moved out of their records.
//   - retired/<origin hash>-<size> is the last checkpoint of an origin that
//     a snapshot retired.
//
// The server creates every other object once. The name of a record, a
// snapshot or a group contains the SHA-256 of its encoding. The server
// checks that hash on every read. A snapshot is due when the records
// after the installed snapshot amount to its size, and to at least
// 64 MiB. It retires each origin that Logs refuses once the latest update
// of the origin is older than Retention. It also compacts the groups.
// After its install, the writer deletes the records, lines, snapshots and
// groups that no reader of the two newest snapshots needs. It deletes no
// retired object.
//
// # Errors
//
// Every error classifies under [errs.Classify]:
//
//   - [ErrConfig] and [ErrRequest], classified [errs.Invalid], report a
//     configuration or a request that the package refuses.
//   - [ErrUnknownOrigin], classified [errs.NotFound], and [ErrSignature],
//     classified [errs.Denied], report a checkpoint that the witness does
//     not accept.
//   - A [*SizeError], classified [errs.Conflict], reports a 409: the
//     witness committed another size of the origin last.
//   - [ErrInconsistent], [ErrCosignature] and [ErrJournal], classified
//     [errs.Integrity], report data that failed verification.
//   - [ErrContention], classified [errs.Transient], reports a commit that
//     other processes overtook.
//
// The server also returns the errors of its store, of its cosigners and of
// the UTC source, and the cause of a caller's context that ended.
//
// # Concurrency
//
// A Client and a Server are safe for concurrent use. A Server starts one
// goroutine per commit, per repair and per refresh of its state, and
// signs on up to GOMAXPROCS goroutines per cosigner. Each commit, repair,
// snapshot and refresh runs under a context of its own. Its deadline is
// Timeout + SignTimeout for a commit, a repair and a snapshot, and Timeout
// for a refresh, so a Server has no Close. The deadline of a commit covers
// the write of its lines.
//
// # Allocation contract
//
// The benchmarks of the package measure these counts with Go 1.27.1:
//
//   - Server.Advance and the add-checkpoint handler allocate 15 objects
//     for a commit of one call with one Ed25519 cosigner over blob/memory,
//     9 of the commit and 6 of the store. The concurrent signatures add 8
//     for a second cosigner or for a commit of two or more calls, and 21
//     for both.
//   - The monitor retrieval route allocates nothing for an origin whose
//     served update is its latest and whose object the cache contains.
//   - Client.AddCheckpoint allocates 66 objects, and Client.Checkpoint 59,
//     on a connection that the transport reuses. net/http and httpclient
//     allocate 64 and 58 of them.
//   - NewServer allocates 29 objects over an empty store, and NewClient 7.
//
// # Dependency position
//
// Imports bytes, cmp, context, crypto/sha256, encoding/base64,
// encoding/binary, encoding/hex, errors, fmt, io, log/slog, math, net/http,
// net/url, runtime, slices, strconv, strings, sync, sync/atomic and time
// from the standard library; go.thesmos.sh/core/blob,
// go.thesmos.sh/core/btree, go.thesmos.sh/core/cache,
// go.thesmos.sh/core/clock, go.thesmos.sh/core/crypto,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/net/httpclient,
// go.thesmos.sh/core/net/httpserver, go.thesmos.sh/core/note,
// go.thesmos.sh/core/page, go.thesmos.sh/core/pool,
// go.thesmos.sh/core/resilience, go.thesmos.sh/core/task,
// go.thesmos.sh/core/telemetry, go.thesmos.sh/core/tlog,
// go.thesmos.sh/core/tlog/checkpoint and go.thesmos.sh/core/version from
// this module; and go.thesmos.sh/kanon and go.thesmos.sh/kanon/wire, whose
// codecs encode the journal.
package witness
