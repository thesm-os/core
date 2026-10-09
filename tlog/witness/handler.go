// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"go.thesmos.sh/core/net/httpserver"
)

const (
	// maxBodyBytes bounds the body of an add-checkpoint request.
	maxBodyBytes = 1048576

	// readBytes is the room that the read of a body makes in its buffer
	// before each read.
	readBytes = 512

	// contentType is the canonical key of the Content-Type header.
	contentType = "Content-Type"
)

// The values of the Content-Type headers of the handlers. Each has a
// capacity of 1, so a handler that adds a value to a header allocates a
// slice of its own and leaves these intact.
var (
	sizeTypes  = []string{sizeContentType}
	linesTypes = []string{linesContentType}
)

// AddCheckpoint returns the handler of add-checkpoint, which a caller
// mounts for POST at the submission prefix followed by "/add-checkpoint".
//
// The handler reads at most 1 MiB of the body, parses it, runs checks 1 to
// 4 of the protocol, and queues one update whose note is the request's
// note and whose prefix is empty. It responds with the status of the
// protocol for each check that fails: 400, 404, 403, 409 with the
// committed size, and 422. A failure that the protocol does not list, such
// as an error of the clock, of the state store or of a cosigner, goes
// through [httpserver.Error] with its class. A body above 1 MiB gets a 413.
// The handler writes the 200 once the signatures exist. The commit stores
// the lines after that, as [Server.Advance] describes.
//
// # Allocation contract
//
// Allocates what net/http allocates, and what the commit allocates, which
// the calls of the commit share, as [Server.Advance] states. The handler
// reads the body into pooled memory, and waits on a pooled result. It
// parses the note and its body into those of a pooled call, under the
// allocation contracts of [note.Note.UnmarshalText] and
// [checkpoint.Body.UnmarshalText].
func (s *Server) AddCheckpoint() http.Handler {
	return http.HandlerFunc(s.serveAddCheckpoint)
}

// serveAddCheckpoint serves one add-checkpoint request, as AddCheckpoint
// describes.
func (s *Server) serveAddCheckpoint(w http.ResponseWriter, r *http.Request) {
	p := s.pendings.Get()
	owned := true

	defer func() {
		if owned {
			p.reset()
			s.pendings.Put(p)
		}
	}()

	if err := p.parseRequest(r.Body); err != nil {
		httpserver.Error(w, r, err)

		return
	}

	if err := s.prepare(p); err != nil {
		httpserver.Error(w, r, err)

		return
	}

	// The commit runs under a context of its own, which no caller's
	// context ends and whose values are no caller's.
	s.enqueue(p)

	owned, err := s.wait(r.Context(), p)
	if err == nil {
		err = p.err
	}

	if err != nil {
		httpserver.Error(w, r, err)

		return
	}

	if len(p.failures) > 0 {
		writeFailure(w, r, p.failures[0].Err)

		return
	}

	//dokimi:mutate-skip sbr-delete: net/http sniffs the type text/plain; charset=utf-8 from the lines, which are text
	w.Header()[contentType] = linesTypes[:1:1]
	_, _ = w.Write(p.lines)
}

// parseRequest reads the body of an add-checkpoint request from body into
// p, and runs check 1 of the protocol: the form of the body, and a note
// whose text is a checkpoint body. p can contain the parses of an earlier
// body, which [pending.reset] keeps, and none of them stands in for a
// part of body that fails to parse.
//
// Error modes:
//   - an error that wraps [ErrRequest] for a body that fails the check.
//     For a note that does not parse, or a text that is not a checkpoint
//     body, the error also wraps the error of [note.Note.UnmarshalText]
//     or of [checkpoint.Body.UnmarshalText].
//   - an *http.MaxBytesError for a body above maxBodyBytes.
//   - the error of body.
func (p *pending) parseRequest(body io.Reader) error {
	buf, err := readBody(body, p.buf[:0])
	p.buf = buf

	if err != nil {
		return err
	}

	if err := p.req.parse(p.buf); err != nil {
		return err
	}

	p.msg = p.req.note
	if err := p.note.UnmarshalText(p.msg); err != nil {
		return fmt.Errorf("%w: %w", ErrRequest, err)
	}

	p.text = p.note.Text
	if err := p.body.UnmarshalText(p.text); err != nil {
		return fmt.Errorf("%w: %w", ErrRequest, err)
	}

	p.updates = append(p.updates, update{
		origin:  p.body.Origin,
		hash:    hashOrigin(p.body.Origin),
		proof:   p.req.proof,
		root:    p.body.Root,
		size:    p.body.Size,
		oldSize: p.req.oldSize,
	})

	return nil
}

// readBody appends r to dst, and grows dst by readBytes at a time.
//
// Returns an *http.MaxBytesError for a body above maxBodyBytes, and the
// error of r other than [io.EOF].
func readBody(r io.Reader, dst []byte) ([]byte, error) {
	for {
		dst = slices.Grow(dst, readBytes)

		n, err := r.Read(dst[len(dst):min(cap(dst), maxBodyBytes+1)])
		dst = dst[:len(dst)+n]

		if len(dst) > maxBodyBytes {
			return dst, &http.MaxBytesError{Limit: maxBodyBytes}
		}

		if errors.Is(err, io.EOF) {
			return dst, nil
		}

		if err != nil {
			return dst, fmt.Errorf("witness: read the body: %w", err)
		}
	}
}

// writeFailure writes the response of an update that failed its checks
// against the state: a 409 with the committed size for a *SizeError, a
// 422 for an error that wraps ErrInconsistent, and the response of
// httpserver.Error otherwise.
func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	if se, ok := errors.AsType[*SizeError](err); ok {
		var body [maxSizeBody]byte

		w.Header()[contentType] = sizeTypes[:1:1]
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write(appendSize(body[:0], se.Size))

		return
	}

	if errors.Is(err, ErrInconsistent) {
		w.WriteHeader(http.StatusUnprocessableEntity)

		return
	}

	httpserver.Error(w, r, err)
}
