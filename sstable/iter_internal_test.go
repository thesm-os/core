// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable

import (
	"cmp"
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
)

// refContract is the contract of refSteps.
const refContract = "each block of a lending cache must have its own reference and one for each cursor that keeps it"

// refClients is the number of clients of the machine of refSteps besides the
// first.
const refClients = 2

// The names of the actions of the machine of refSteps: the calls of a cursor
// that change its references.
const (
	callNewIter = "NewIter"
	callReset   = "Reset"
	callFirst   = "First"
	callSeekGE  = "SeekGE"
	callNext    = "Next"
	callClose   = "Close"
)

// TestIterReferences checks the references of cursors to the blocks of a
// LendingCache. An open cursor keeps a reference to the block of its current
// entry and to its index partition, and releases each reference once.
func TestIterReferences(t *testing.T) {
	t.Parallel()

	t.Run("keeps the references of the cursors under any sequence of the calls of three clients", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, refContract, refSteps)
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("releases the block and the partition of an open cursor", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			r := refTable(t, cache, 1)
			it := r.NewIter(IterOptions{})
			defer it.Close()
			assert.True(t, it.SeekGE(t.Context(), []byte("key 100"), 1), "SeekGE lends the block of the key")
			assert.NotNil(t, it.part, "the cursor keeps the index partition of the key")
			it.Reset(r, IterOptions{})
			assert.Equal(t, cache.refs(), cache.want(), "each block has the reference of the cache alone")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("releases the block and the partition of the cursor", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			it := refTable(t, cache, 1).NewIter(IterOptions{})
			assert.True(t, it.SeekGE(t.Context(), []byte("key 100"), 1), "SeekGE lends the block of the key")
			assert.NotNil(t, it.part, "the cursor keeps the index partition of the key")
			assert.NoError(t, it.Close(), "Close returns nil for a cursor without an error")
			assert.Equal(t, cache.refs(), cache.want(), "each block has the reference of the cache alone")
		})

		t.Run("releases no block at a second Close", func(t *testing.T) {
			t.Parallel()
			cache := &refCache{}
			it := refTable(t, cache, 1).NewIter(IterOptions{})
			assert.True(t, it.SeekGE(t.Context(), []byte("key 100"), 1), "SeekGE lends the block of the key")
			assert.NoError(t, it.Close(), "the first Close releases the block and the partition")
			assert.NoError(t, it.Close(), "the second Close returns nil")
			assert.Equal(t, cache.refs(), cache.want(), "each block keeps the reference of the cache")
		})
	})
}

// refSteps opens two tables through one refCache, and runs a machine of three
// clients that each keep a cursor. A step of a client calls NewIter, Reset,
// First, SeekGE, Next or Close on the cursor of the client. NewIter closes
// the cursor of the client and takes a new one. Reset sets the cursor on a
// drawn table, or closes it with a nil Reader. After each sequential step,
// each block of the cache has the reference of the cache and one for each
// open cursor that keeps the block as its current block or index partition.
// Settle closes every cursor, so each block then has the reference of the
// cache alone.
//
// A client keeps the first error of its Close calls, and the invariant checks
// it on the goroutine of the case. The tables have no fault, so every Close
// returns nil.
func refSteps(c *prop.Case) {
	cache := &refCache{}
	readers := []*Reader{refTable(c, cache, 1), refTable(c, cache, 2)}
	cursors := make([]*Iter, refClients+1)
	for i := range cursors {
		cursors[i] = readers[0].NewIter(IterOptions{})
	}
	closeErrs := make([]error, len(cursors))

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  callNewIter,
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, len(readers)-1), "table") },
				Run: func(_ *prop.Case, client int, in any) {
					closeErrs[client] = cmp.Or(closeErrs[client], cursors[client].Close())
					cursors[client] = readers[in.(int)].NewIter(IterOptions{})
				},
			},
			{
				Name:  callReset,
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(-1, len(readers)-1), "table") },
				Run: func(_ *prop.Case, client int, in any) {
					var r *Reader
					if i := in.(int); i >= 0 {
						r = readers[i]
					}
					cursors[client].Reset(r, IterOptions{})
				},
			},
			{
				Name: callFirst,
				Run:  func(c *prop.Case, client int, _ any) { cursors[client].First(c.Context()) },
			},
			{
				Name:  callSeekGE,
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, refEntries-1), "key") },
				Run: func(c *prop.Case, client int, in any) {
					cursors[client].SeekGE(c.Context(), fmt.Appendf(nil, "key %03d", in.(int)), 1)
				},
			},
			{
				Name:   callNext,
				Weight: 3,
				Run:    func(c *prop.Case, client int, _ any) { cursors[client].Next(c.Context()) },
			},
			{
				Name: callClose,
				Run: func(_ *prop.Case, client int, _ any) {
					closeErrs[client] = cmp.Or(closeErrs[client], cursors[client].Close())
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			kept := make([]*Block, 0, 2*len(cursors))
			for i, it := range cursors {
				assert.NoError(c, closeErrs[i], "Close returns nil for a cursor that reads its table without a fault")
				kept = append(kept, it.lent, it.part)
			}
			assert.Equal(c, cache.refs(), cache.want(kept...),
				"each block has the reference of the cache and one for each cursor that keeps it")
		},
		Settle: func(c *prop.Case, _ struct{}) {
			for _, it := range cursors {
				assert.NoError(c, it.Close(), "Close returns nil for a cursor that reads its table without a fault")
			}
		},
	}, stateful.Clients(refClients))
}
