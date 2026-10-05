// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"fmt"

	"go.thesmos.sh/core/version"
)

// collect deletes the objects of the journal that neither snap, the
// snapshot under name that the writer of w installed, nor the base of w
// refers to, after the install at a head whose record has the Seq
// headSeq:
//
//   - A record and its lines, when the record's Seq is below that of the
//     base's record.
//   - A record and its lines off the chain, whose Seq is at most headSeq.
//     No replacement of the head can commit it any more. The names of the
//     chain in the state decide which records are off it, and a record
//     whose Seq the state has no name for remains.
//   - A snapshot other than these two, whose Seq is at most snap's.
//   - A group whose Seq is below snap's.
//
// It deletes no retired object, and no object whose key is not a key of
// the journal. The objects that the base refers to remain until the next
// install, so a process that still reads the positions of the base finds
// them.
//
// Returns the errors of the store.
func (s *Server) collect(ctx context.Context, w *snapper, snap *snapshot, name string, headSeq uint64) error {
	keep := make(map[string]bool, len(w.base.objects)+len(snap.Objects))
	for _, obj := range w.base.objects {
		keep[obj.Key] = true
	}

	for _, obj := range snap.Objects {
		keep[obj.Key] = true
	}

	doomed := func(n string) bool {
		seq, ok := parseName(n)
		if !ok || keep[recordPrefix+n] {
			return false
		}

		if seq < w.base.seq {
			return true
		}

		s.mu.RLock()
		chain, known := s.st.chain.Get(seq)
		s.mu.RUnlock()

		return seq <= headSeq && known && chain != n
	}

	sweeps := []struct {
		doomed func(n string) bool
		prefix string
	}{
		{prefix: recordPrefix, doomed: doomed},
		{prefix: linesPrefix, doomed: doomed},
		{prefix: snapshotPrefix, doomed: func(n string) bool {
			seq, ok := parseName(n)

			return ok && n != name && n != w.base.name && seq <= w.seq
		}},
		{prefix: groupPrefix, doomed: func(n string) bool {
			seq, ok := parseName(n)

			return ok && seq < w.seq && !keep[groupPrefix+n]
		}},
	}

	for _, sw := range sweeps {
		var keys []string

		err := s.list(ctx, sw.prefix, func(key string) error {
			if sw.doomed(key[len(sw.prefix):]) {
				keys = append(keys, key)
			}

			return nil
		})
		if err != nil {
			return err
		}

		for _, key := range keys {
			if err := s.store.Delete(ctx, key, version.Unspecified); err != nil {
				return fmt.Errorf("witness: delete %s: %w", key, err)
			}
		}
	}

	return nil
}
