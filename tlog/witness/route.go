// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpserver"
)

const (
	// refreshAge is the age of the state after which a GET of the route
	// starts a refresh.
	refreshAge = time.Minute

	// cacheBytes bounds the bytes of the records, lines and groups that
	// the route caches.
	cacheBytes = 67108864
)

// errNotServed reports an origin that the route cannot serve now: a
// snapshot added it without a served position, or garbage collection
// deleted the object of its served position.
var errNotServed = errs.WithClass(errors.New("witness: no served checkpoint of the origin"), errs.Transient)

// loaded is a record or a group that the route read: its calls, the lines
// of each call, and the bytes that it read.
type loaded struct {
	// calls are the calls of the object, and lines the cosignature lines
	// of each call.
	calls []call
	lines [][]byte

	// size is the length of the encodings that the route read, the cost of
	// the object in the cache.
	size int64
}

// Checkpoint returns the handler of the monitor retrieval route, which a
// caller mounts for GET at the monitoring prefix followed by
// "/{hash}/checkpoint". It reads the origin hash from the path element
// before the last.
//
// The handler responds with the latest checkpoint of the origin whose
// lines are stored: the update's prefix, the call's note with the log's
// signatures, and the witness's cosignature lines. A commit stores its
// lines after its calls return, so until then the handler serves the
// earlier update of the origin. It responds with 404
// for a hash that is not the lowercase hexadecimal of an origin of the
// state, and for an origin whose first commit has no lines yet. It
// responds with 503 for an origin that a snapshot added without a served
// position.
//
// The handler moves the served position of the origin to its latest
// update when the lines of that update exist, and repairs a record without
// lines that is older than the repair age by a reading of UTC that is
// synchronised within MaxError. It refreshes the state on a goroutine of
// its own when the state is older than one minute.
//
// # Allocation contract
//
// Allocates what net/http allocates, and nothing for an origin whose
// served update is the latest and whose object the cache contains.
func (s *Server) Checkpoint() http.Handler {
	return http.HandlerFunc(s.serveCheckpoint)
}

// serveCheckpoint serves one GET of the monitor retrieval route, as
// Checkpoint describes.
func (s *Server) serveCheckpoint(w http.ResponseWriter, r *http.Request) {
	h, ok := parseRoute(r.URL.Path)
	if !ok {
		httpserver.Error(w, r, ErrUnknownOrigin)

		return
	}

	// A refresh outlives the GET that starts it, under a context of its
	// own.
	s.refreshIfStale() //nolint:contextcheck // see above

	// An origin that the state does not contain has the zero state, whose
	// served position the check below refuses with ErrUnknownOrigin.
	s.mu.RLock()
	o, _ := s.st.origins.Get(h)
	s.mu.RUnlock()

	// A snapshot that makes a group position latest makes it served too,
	// so a latest position that is not served is in a record.
	ctx := r.Context()
	if o.latest != o.served {
		o = s.advanceServed(ctx, h, o)
	}

	if o.served.key == "" {
		if o.snapshot {
			httpserver.Error(w, r, errNotServed)

			return
		}

		httpserver.Error(w, r, ErrUnknownOrigin)

		return
	}

	l, err := s.load(ctx, o)
	if errs.Classify(err) == errs.NotFound {
		l, err = s.reload(ctx, h)
	}

	if err != nil {
		httpserver.Error(w, r, err)

		return
	}

	c := &l.calls[o.served.call]

	w.Header()[contentType] = linesTypes[:1:1]
	_, _ = w.Write(c.Updates[o.served.update].Prefix)
	_, _ = w.Write(c.Note)
	_, _ = w.Write(l.lines[o.served.call])
}

// parseRoute returns the origin hash of path, the path of the monitor
// retrieval route: the lowercase hexadecimal of the hash, in the element
// before the last, and "checkpoint" as the last. It decodes the
// hexadecimal 16 digits at a time with strconv, which allocates nothing.
func parseRoute(path string) (originHash, bool) {
	rest, ok := strings.CutSuffix(path, checkpointPath)
	if !ok || len(rest) < hashText+1 || rest[len(rest)-hashText-1] != '/' {
		return originHash{}, false
	}

	digits := rest[len(rest)-hashText:]
	if strings.ContainsFunc(digits, func(c rune) bool { return (c < '0' || c > '9') && (c < 'a' || c > 'f') }) {
		return originHash{}, false
	}

	var h originHash

	for i := range len(h) / 8 {
		// The digits are lowercase hexadecimal, which ParseUint accepts.
		v, _ := strconv.ParseUint(digits[16*i:16*i+16], 16, 64)
		binary.BigEndian.PutUint64(h[8*i:], v)
	}

	return h, true
}

// advanceServed moves the served position of o, the state of the origin of
// hash h, to its latest position when the lines of the latest update's
// record exist, and returns the state after the move. When the lines are
// missing and the record is older than the repair age, it repairs the
// record first. Otherwise it returns o, whose served position the route
// serves.
func (s *Server) advanceServed(ctx context.Context, h originHash, o originState) originState {
	name := o.latest.key[len(recordPrefix):]

	_, err := s.store.Stat(ctx, linesPrefix+name)
	if errs.Classify(err) == errs.NotFound && s.repairDue(o.time) {
		err = s.repair(ctx, o.latest.key)
	}

	if err != nil {
		return o
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.st.origins.Get(h)
	if !ok {
		return o
	}

	if cur.served.seq < o.latest.seq {
		cur.served = o.latest
		s.st.origins.Set(h, cur)
	}

	return cur
}

// repairDue reports whether a record of the time t is older than the
// repair age, the longest that a commit can still run after the time of
// its record, by a reading of UTC that is synchronised within MaxError:
// Timeout + SignTimeout, one second for the whole seconds of the time, and
// twice MaxError for the distance between two readings.
func (s *Server) repairDue(t uint64) bool {
	r, err := s.utc.ReadUTC()
	if err != nil || !r.Within(s.maxError) {
		return false
	}

	age := s.timeout + s.signTimeout + time.Second + 2*s.maxError

	return r.Time.Sub(time.Unix(int64(t), 0)) > age //nolint:gosec // the time of a record is the seconds of a reading
}

// load returns the object of the served position of o, from the cache of
// s or from the store, after it checks that the position names an update
// of the origin of o in it.
//
// Returns the error of the store, whose class is [errs.NotFound] for a
// missing object, and an error that wraps [ErrJournal] for an object that
// does not decode, whose hash is not its name, whose lines do not cover
// its calls, or that has no update of the origin at the position.
func (s *Server) load(ctx context.Context, o originState) (*loaded, error) {
	p := o.served

	l, ok := s.objects.Get(p.key)
	if !ok {
		var err error

		if l, err = s.readLoaded(ctx, p); err != nil {
			return nil, err
		}

		s.objects.Set(p.key, l, time.Time{})
	}

	if p.call >= len(l.calls) || p.update >= len(l.calls[p.call].Updates) ||
		l.calls[p.call].Updates[p.update].Origin != o.origin {

		return nil, fmt.Errorf("%w: the object %s has no update of %s at %d.%d", ErrJournal, p.key, o.origin, p.call,
			p.update)
	}

	return l, nil
}

// readLoaded reads the object of p: a group, or a record and its lines.
//
// Returns the errors of load.
func (s *Server) readLoaded(ctx context.Context, p position) (*loaded, error) {
	if p.group {
		name := p.key[len(groupPrefix):]

		data, err := readNamed(ctx, s.store, groupPrefix, name, maxObjectBytes)
		if err != nil {
			return nil, err
		}

		var g group
		if err := g.DecodeKanon(data, kanon.Options{}); err != nil {
			return nil, fmt.Errorf("%w: the group %s does not decode: %w", ErrJournal, name, err)
		}

		l := &loaded{calls: g.Calls, lines: make([][]byte, len(g.Calls)), size: int64(len(data))}
		for i := range g.Calls {
			l.lines[i] = g.Calls[i].Lines
		}

		return l, nil
	}

	name := p.key[len(recordPrefix):]

	rec, size, err := s.readRecord(ctx, name)
	if err != nil {
		return nil, err
	}

	data, err := read(ctx, s.store, linesPrefix+name, maxObjectBytes)
	if err != nil {
		return nil, err
	}

	var ls lines
	if err := ls.DecodeKanon(data, kanon.Options{}); err != nil || len(ls.Calls) != len(rec.Calls) {
		return nil, fmt.Errorf("%w: the lines of %s do not cover the %d calls of the record", ErrJournal, name,
			len(rec.Calls))
	}

	l := &loaded{calls: rec.Calls, lines: ls.Calls, size: int64(size) + int64(len(data))} //nolint:gosec // a length

	return l, nil
}

// reload serves the origin of hash h after the object of its served
// position went missing: garbage collection deleted it after a newer
// snapshot. It refreshes the state, which takes the group positions of
// that snapshot, and loads the served position again.
//
// Returns errNotServed, classified Transient, when the object of the
// served position is still missing, and the errors of load.
func (s *Server) reload(ctx context.Context, h originHash) (*loaded, error) {
	if _, err := s.sync(ctx, ""); err != nil {
		return nil, err
	}

	// An origin that the refresh removed has the zero state, without a
	// served position. A served position that the refresh left as it was
	// names the missing object again, which load reports as NotFound.
	s.mu.RLock()
	cur, _ := s.st.origins.Get(h)
	s.mu.RUnlock()

	if cur.served.key == "" {
		return nil, errNotServed
	}

	l, err := s.load(ctx, cur)
	if errs.Classify(err) == errs.NotFound {
		return nil, errNotServed
	}

	return l, err
}

// refreshIfStale starts a refresh of the state on a goroutine of its own
// when the state is older than refreshAge and no refresh runs.
func (s *Server) refreshIfStale() {
	s.mu.RLock()
	stale := s.clock.Time().Sub(s.st.synced) > refreshAge
	s.mu.RUnlock()

	if stale && s.refreshing.CompareAndSwap(false, true) {
		go s.refresh()
	}
}

// refresh brings the state to the head of the store under a context of its
// own with the deadline Timeout, and logs a refresh that fails.
func (s *Server) refresh() {
	defer s.refreshing.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	if _, err := s.sync(ctx, ""); err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "witness: a refresh failed", slog.Any("error", err))
	}
}
