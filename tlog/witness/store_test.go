// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"io"
	"strings"
	"sync"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// op names a method of a blob.Store.
type op uint8

// The methods of a blob.Store that a hook intercepts.
const (
	opPut op = iota + 1
	opGet
	opStat
	opDelete
	opList
)

// hook intercepts the calls of a method of a store to the keys that start
// with prefix. before runs before the call, and an error that it returns
// fails the call without running it. after runs after a call that ran,
// with its error, and returns the error that the call returns.
type hook struct {
	before func(ctx context.Context, key string) error
	after  func(key string, err error) error
	prefix string
	op     op
}

// store is a blob.Store over blob/memory whose hooks fail, delay or
// observe its calls.
type store struct {
	*memory.Store

	hooks []hook
	mu    sync.Mutex
}

// newStore returns an empty store whose objects have the time of c.
func newStore(c clock.Clock) *store {
	return &store{Store: memory.New(c)}
}

// intercept adds h to the hooks of s.
func (s *store) intercept(h hook) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hooks = append(s.hooks, h)
}

// reset removes every hook of s.
func (s *store) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hooks = nil
}

// call runs fn, the call of o to key, through the hooks of s.
func (s *store) call(ctx context.Context, o op, key string, fn func() error) error {
	s.mu.Lock()
	hooks := s.hooks
	s.mu.Unlock()

	for _, h := range hooks {
		if h.op == o && strings.HasPrefix(key, h.prefix) && h.before != nil {
			if err := h.before(ctx, key); err != nil {
				return err
			}
		}
	}

	err := fn()

	for _, h := range hooks {
		if h.op == o && strings.HasPrefix(key, h.prefix) && h.after != nil {
			err = h.after(key, err)
		}
	}

	return err
}

// Put stores the object through the hooks of opPut.
func (s *store) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	var info blob.Info

	err := s.call(ctx, opPut, key, func() error {
		var err error

		info, err = s.Store.Put(ctx, key, r, opts)

		return err
	})

	return info, err
}

// Get opens the object through the hooks of opGet.
func (s *store) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	var (
		rc   io.ReadCloser
		info blob.Info
	)

	err := s.call(ctx, opGet, key, func() error {
		var err error

		rc, info, err = s.Store.Get(ctx, key)

		return err
	})

	return rc, info, err
}

// Stat returns the metadata through the hooks of opStat.
func (s *store) Stat(ctx context.Context, key string) (blob.Info, error) {
	var info blob.Info

	err := s.call(ctx, opStat, key, func() error {
		var err error

		info, err = s.Store.Stat(ctx, key)

		return err
	})

	return info, err
}

// Delete removes the object through the hooks of opDelete.
func (s *store) Delete(ctx context.Context, key string, ifMatch version.Version) error {
	return s.call(ctx, opDelete, key, func() error { return s.Store.Delete(ctx, key, ifMatch) })
}

// List lists the objects through the hooks of opList, with the prefix as
// the key.
func (s *store) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	var cur page.Cursor[blob.Info]

	err := s.call(ctx, opList, prefix, func() error {
		var err error

		cur, err = s.Store.List(ctx, prefix, p)

		return err
	})

	return cur, err
}
