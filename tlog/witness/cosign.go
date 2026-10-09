// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/task"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// signing is the memory of the signatures of one commit or one repair:
// the value of each signature, and the error of each cosigner.
type signing struct {
	// values are the values of the signatures: values[j][i] is the value of
	// the cosigner j over the text i. Each value keeps its capacity for the
	// signature at its index in the next cosign.
	values [][][]byte

	// errs are the errors of the cosigners, at the index of each.
	errs []error
}

// cosign signs each text of texts with every cosigner of s at t, and
// keeps the values in g. One cosigner signs on the calling goroutine.
// Two or more cosigners sign concurrently with [task.Each], one goroutine
// each. A cosigner signs one text on its goroutine, and two or more texts
// on up to s.signers goroutines, as signWith describes. texts is not
// empty: a commit and a record have at least one call.
//
// Returns the error of the first cosigner that failed, in the order of the
// cosigners: an error that wraps [resilience.ErrOpen] for a circuit that
// refuses the cosigner, and the error of AppendSignAt otherwise.
//
// # Allocation contract
//
// Allocates what the cosigners allocate when g has room for the values.
// Each task.Each costs 5 objects on Go 1.27.2: its context, the cancel
// function of that context, its state, the closure of its goroutines and
// the closure of the signatures. The first task.Each under a context
// costs 3 more, for the done channel and the map of children of that
// context. Under a ctx without children:
//   - One cosigner and one text cost nothing more.
//   - One cosigner and two or more texts cost 8 objects.
//   - Two cosigners and one text cost 8 objects.
//   - Two cosigners and two or more texts cost 21 objects: 8 for the
//     cosigners, and 8 and 5 for the texts of the two cosigners.
func (s *Server) cosign(ctx context.Context, texts [][]byte, t time.Time, g *signing) error {
	n := len(s.cosigners)
	g.values = slices.Grow(g.values[:0], n)[:n]
	g.errs = slices.Grow(g.errs[:0], n)[:n]

	if n == 1 {
		s.signWith(ctx, 0, texts, t, g)
	} else {
		// fn returns no error, so a failed cosigner does not end the context
		// of the others.
		_ = task.Each(ctx, n, s.cosigners, func(ctx context.Context, j int, _ checkpoint.Cosigner) error {
			s.signWith(ctx, j, texts, t, g)

			return nil
		})
	}

	for j, err := range g.errs[:n] {
		if err != nil {
			return fmt.Errorf("witness: cosign with %s: %w", s.lineKeys[j].Name, err)
		}
	}

	return nil
}

// signWith signs each text of texts at t with the cosigner j of s. It
// keeps the value of text i in g.values[j][i], and the error of the
// cosigner in g.errs[j], in the slices that cosign sized. It signs one
// text on the calling goroutine. It signs two or more texts with
// [task.Each] on up to s.signers goroutines. Each goroutine takes the next
// text that no other goroutine has taken, so the cosigner signs at most
// s.signers texts at once.
//
// The cosigner signs only after [resilience.Breaker.Allow] admits it.
// [resilience.Breaker.Record] then counts any error of its signatures as
// one failure, the end of ctx included. The first signature that fails
// ends the context of the other signatures of the cosigner, through
// task.Each. The other cosigners still sign every text, so that each
// circuit counts the outcome of its own cosigner.
func (s *Server) signWith(ctx context.Context, j int, texts [][]byte, t time.Time, g *signing) {
	values := slices.Grow(g.values[j][:0], len(texts))[:len(texts)]
	g.values[j] = values

	var err error
	if !s.breaker.Allow(s.targets[j]) {
		err = resilience.ErrOpen
	} else {
		c := s.cosigners[j]

		if len(texts) == 1 {
			values[0], err = c.AppendSignAt(ctx, values[0][:0], texts[0], t)
		} else {
			workers := min(s.signers, len(texts))
			err = task.Each(ctx, workers, texts, func(ctx context.Context, i int, text []byte) error {
				v, serr := c.AppendSignAt(ctx, values[i][:0], text, t)
				values[i] = v

				return serr
			})
		}

		s.breaker.Record(s.targets[j], err != nil)
	}

	g.errs[j] = err
}

// appendLines appends to dst the signature line of every cosigner for
// text i of the last cosign into g, in the order of the cosigners.
func (s *Server) appendLines(dst []byte, g *signing, i int) []byte {
	for j, k := range s.lineKeys {
		// The name of a cosigner is Valid, which NewServer checks, and its
		// value is not empty, which note.Signer requires, so AppendText
		// returns no error.
		dst, _ = note.Signature{Name: k.Name, ID: k.ID, Value: g.values[j][i]}.AppendText(dst)
	}

	return dst
}
