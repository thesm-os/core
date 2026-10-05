// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/net/httpclient"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// buffers contains the scratch buffers of a Client: the body of a request
// before its copy, the response that the Client reads, the text and the
// lines that it parses as a note, and the URL of a monitor retrieval
// route.
var buffers = pool.NewPool(func() *[]byte { return new([]byte) })

// notes contains the notes that a Client parses responses into, so that a
// parse reuses the names and the values of the lines of the last one.
var notes = pool.NewPool(func() *note.Note { return new(note.Note) })

// ClientConfig configures a [Client]. Every field is required.
type ClientConfig struct {
	// HTTP calls the witness. It admits the witness's hosts, and keeps the
	// default classification of the responses, which the client reads.
	HTTP *httpclient.Client

	// Resolver builds the Verifier of each key of Keys.
	Resolver note.Resolver

	// Submission is the witness's submission prefix: an absolute http or
	// https URL without a query, a fragment or a trailing slash. The
	// client sends add-checkpoint to Submission followed by
	// "/add-checkpoint".
	Submission string

	// Monitoring is the witness's monitoring prefix, an absolute http or
	// https URL of the form of Submission, which may equal Submission.
	Monitoring string

	// Keys are the witness's cosignature keys, whose signatures start with
	// a timestamp: one key for a witness that cosigns with one algorithm,
	// and one key per algorithm for a hybrid witness.
	Keys []note.Key
}

// Client calls one witness of C2SP tlog-witness: add-checkpoint at its
// submission prefix, and the monitor retrieval route at its monitoring
// prefix.
//
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// Each method documents its allocations. A call allocates the request that
// net/http builds, and what [httpclient.Client.AppendFetch] allocates.
type Client struct {
	// http calls the witness.
	http *httpclient.Client

	// submission is the URL of add-checkpoint.
	submission string

	// monitoring is the monitoring prefix.
	monitoring string

	// keys are the witness's keys, in the order of the configuration.
	keys []note.Key

	// verifiers are the Verifiers of keys, at the index of their key.
	verifiers []note.Verifier
}

// NewClient returns a Client of cfg. It checks the prefixes, and builds the
// Verifier of each key through cfg.Resolver.
//
// Error modes:
//   - ErrConfig, classified Invalid, for a nil cfg, a missing field, a key
//     that is not Valid, two keys with one name and one key ID, and a
//     prefix that is not an absolute http or https URL without a query, a
//     fragment or a trailing slash.
//   - The error of the Resolver for a key that it does not resolve.
//
// # Allocation contract
//
// Allocates the Client, its slice of Verifiers, the URL of add-checkpoint,
// the parse of each prefix, and what the Resolver allocates: 7 objects for
// one Ed25519 cosignature key on Go 1.27.1.
func NewClient(cfg *ClientConfig) (*Client, error) {
	if cfg == nil || cfg.HTTP == nil || cfg.Resolver == nil || cfg.Submission == "" || cfg.Monitoring == "" ||
		len(cfg.Keys) == 0 {

		return nil, fmt.Errorf("%w: a client needs HTTP, Resolver, Submission, Monitoring and Keys", ErrConfig)
	}

	for _, prefix := range []string{cfg.Submission, cfg.Monitoring} {
		if err := checkPrefix(prefix); err != nil {
			return nil, err
		}
	}

	verifiers := make([]note.Verifier, len(cfg.Keys))

	for i, k := range cfg.Keys {
		if !k.Valid() {
			return nil, fmt.Errorf("%w: the key %d is not valid", ErrConfig, i)
		}

		for _, other := range cfg.Keys[:i] {
			if other.Name == k.Name && other.ID() == k.ID() {
				return nil, fmt.Errorf("%w: two keys named %s with one key ID", ErrConfig, k.Name)
			}
		}

		v, err := cfg.Resolver.Verifier(k)
		if err != nil {
			return nil, fmt.Errorf("witness: resolve the key of %s: %w", k.Name, err)
		}

		verifiers[i] = v
	}

	return &Client{
		http:       cfg.HTTP,
		submission: cfg.Submission + addCheckpointPath,
		monitoring: cfg.Monitoring,
		keys:       cfg.Keys,
		verifiers:  verifiers,
	}, nil
}

// AddCheckpoint asks the witness to cosign msg, a checkpoint note that the
// log signed, with proof, a consistency proof from oldSize to the
// checkpoint's size. It appends the witness's cosignature lines to dst:
// for each key of the configuration in order, the first line of that key,
// verified over the text of msg. It verifies every other line of the keys
// too, and ignores the lines of other keys.
//
// The HTTP client sends the request once, because a POST has no
// idempotency key. A caller that lost a response and sends the checkpoint
// again with the same old size receives a 409 with the new size.
//
// Error modes, each with dst unchanged:
//   - A *SizeError, classified Conflict, for a 409, with the size that the
//     witness committed last. The caller sends a proof from that size.
//   - An error that wraps ErrInconsistent, classified Integrity, and the
//     *httpclient.StatusError of a 422: the witness refused the proof, or
//     committed another root at an equal size.
//   - ErrCosignature, classified Integrity, for a 200 without a valid line
//     of every key, with an invalid line of a key, or with a line whose
//     timestamp is 0.
//   - ErrRequest, classified Invalid, before any request, for a msg that
//     is not a signed note and a proof of more than 63 hashes.
//   - The errors of the HTTP client's AppendFetch, such as a
//     *httpclient.StatusError for 400, 403 and 404.
//
// # Allocation contract
//
// Allocates the copy of the request body and its reader, the 5 objects of
// the request that net/http builds, and what AppendFetch allocates for a
// POST, when dst has room for the lines: 66 objects on a connection that
// the transport reuses, with Go 1.27.1. It parses the response in pooled
// memory, and the Verifier of an Ed25519 key allocates nothing.
func (c *Client) AddCheckpoint(
	ctx context.Context, msg []byte, oldSize uint64, proof []crypto.Digest, dst []byte,
) ([]byte, error) {
	if len(proof) > maxProof {
		return dst, fmt.Errorf("%w: a proof of %d hashes, above %d", ErrRequest, len(proof), maxProof)
	}

	text, err := note.TextOf(msg)
	if err != nil {
		return dst, fmt.Errorf("%w: %w", ErrRequest, err)
	}

	// The transport can read the body after the call returns, until it
	// closes the body, as http.RoundTripper permits. The body is a copy of
	// the scratch buffer in memory of its own, of its exact length.
	scratch := buffers.Get()
	*scratch = appendRequest((*scratch)[:0], oldSize, proof, msg)
	body := bytes.Clone(*scratch)
	buffers.Put(scratch)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.submission, bytes.NewReader(body))
	if err != nil {
		return dst, fmt.Errorf("witness: build the request: %w", err)
	}

	resp := buffers.Get()
	defer buffers.Put(resp)

	// AppendFetch reads the response under the context of req, which is
	// ctx.
	*resp, err = c.http.AppendFetch((*resp)[:0], req) //nolint:contextcheck // see above
	if err != nil {
		return dst, statusError(err)
	}

	return c.appendLines(dst, text, *resp)
}

// appendLines appends to dst the first line of each key of c among lines,
// the cosignature lines of a response, after it verifies every line of
// the keys of c over text.
//
// It parses the lines as the note of text and lines in pooled memory: the
// text, a blank line and lines.
//
// Returns dst unchanged with an error that wraps ErrCosignature for lines
// that are not signature lines, an invalid line of a key, a line of a key
// whose timestamp is 0, and a key without a line.
func (c *Client) appendLines(dst, text, lines []byte) ([]byte, error) {
	buf := buffers.Get()
	defer buffers.Put(buf)

	*buf = append(append(append((*buf)[:0], text...), '\n'), lines...)

	n := notes.Get()
	defer notes.Put(n)

	if err := n.UnmarshalText(*buf); err != nil {
		return dst, fmt.Errorf("%w: a response that is not a list of signature lines: %w", ErrCosignature, err)
	}

	for _, s := range n.Signatures {
		i := c.index(s)
		if i < 0 {
			continue
		}

		if !c.verifiers[i].Verify(n.Text, s.Value) {
			return dst, fmt.Errorf("%w: an invalid line of %s", ErrCosignature, s.Name)
		}

		if t, err := checkpoint.Timestamp(s.Value); err != nil || t.IsZero() {
			return dst, fmt.Errorf("%w: a line of %s with the timestamp 0", ErrCosignature, s.Name)
		}
	}

	out := dst

	for _, k := range c.keys {
		s, ok := n.Find(k)
		if !ok {
			return dst, fmt.Errorf("%w: no line of %s", ErrCosignature, k.Name)
		}

		// UnmarshalText parsed s, so its name is valid and its value is
		// not empty, and AppendText returns no error.
		out, _ = s.AppendText(out)
	}

	return out, nil
}

// index returns the index of the key of c whose name and key ID are those
// of s, and -1 for a line of another key.
func (c *Client) index(s note.Signature) int {
	for i, k := range c.keys {
		if k.Name == s.Name && k.ID() == s.ID {
			return i
		}
	}

	return -1
}

// Checkpoint appends to dst what the witness serves for origin on its
// monitor retrieval route, and reports whether the witness served it. It
// reports false without an error for a 404, with which the witness states
// that it has not cosigned the origin. It does not verify the response:
// a monitor verifies it against its policy, and a log format with a prefix
// before its note serves bytes that only the monitors of that format
// parse.
//
// Error modes, each with dst unchanged:
//   - ErrRequest, classified Invalid, for an origin that is not Valid.
//   - The errors of the HTTP client's AppendFetch.
//
// # Allocation contract
//
// Allocates the text of the URL, which it builds in pooled memory, the 3
// objects of the request that net/http builds, and what AppendFetch
// allocates, when dst has room for the response: 59 objects on a
// connection that the transport reuses, with Go 1.27.1.
func (c *Client) Checkpoint(ctx context.Context, origin checkpoint.Origin, dst []byte) ([]byte, bool, error) {
	if !origin.Valid() {
		return dst, false, fmt.Errorf("%w: the origin %q is not a line", ErrRequest, origin)
	}

	h := hashOrigin(origin)

	target := buffers.Get()
	defer buffers.Put(target)

	*target = append(append((*target)[:0], c.monitoring...), '/')
	*target = append(hex.AppendEncode(*target, h[:]), checkpointPath...)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, string(*target), http.NoBody)
	if err != nil {
		return dst, false, fmt.Errorf("witness: build the request: %w", err)
	}

	// AppendFetch reads the response under the context of req, which is
	// ctx.
	got, err := c.http.AppendFetch(dst, req) //nolint:contextcheck // see above
	if se, ok := errors.AsType[*httpclient.StatusError](err); ok && se.Status == http.StatusNotFound {
		return dst, false, nil
	}

	if err != nil {
		return dst, false, err
	}

	return got, true, nil
}

// statusError returns the error of a call whose response the HTTP client
// refused: a *SizeError for a 409 with the size in its body, an error that
// wraps ErrInconsistent and err for a 422, and err otherwise.
func statusError(err error) error {
	se, ok := errors.AsType[*httpclient.StatusError](err)
	if !ok {
		return err
	}

	if se.Status == http.StatusConflict {
		if size, ok := parseSizeBody(se.Body); ok {
			return &SizeError{Size: size}
		}
	}

	if se.Status == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: %w", ErrInconsistent, err)
	}

	return err
}

// checkPrefix returns nil for a prefix of a witness: an absolute http or
// https URL with a host, and without a query, a fragment or a trailing
// slash.
//
// Returns an error that wraps ErrConfig for any other prefix.
func checkPrefix(prefix string) error {
	u, err := url.Parse(prefix)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" ||
		u.Fragment != "" || u.ForceQuery || strings.HasSuffix(prefix, "/") {

		return fmt.Errorf("%w: the prefix %q is not an absolute http or https URL without a query, a fragment "+
			"or a trailing slash", ErrConfig, prefix)
	}

	return nil
}
