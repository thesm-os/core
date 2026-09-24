// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package castest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/coretest/castest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/crypto/sha3"
	"go.thesmos.sh/core/errs"
)

// brokenEnv names the environment variable that tells a child test
// process which run of the suite to perform.
const brokenEnv = "CASTEST_BROKEN"

// reference names the run against the memory store with every option.
const reference = "reference"

// caseLine matches the line that go test -v prints when a top-level
// case of the suite starts.
var caseLine = regexp.MustCompile(`(?m)^=== RUN\s+TestAssertStore/([^/\s]+)$`)

// run is a store, and the options for it, that the suite runs against.
type run struct {
	newStore func(h crypto.Hasher) cas.Store
	options  []castest.Option
}

// hooked is a memory store whose methods a breakage replaces one at a
// time. A nil hook calls the memory store. hooked implements
// [cas.Streamer], as the memory store does.
type hooked struct {
	*memory.Store

	hasher    func() crypto.Hasher
	put       func(ctx context.Context, d crypto.Digest, data []byte) (bool, error)
	get       func(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error)
	has       func(ctx context.Context, d crypto.Digest) (bool, error)
	putStream func(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error)
	getStream func(ctx context.Context, d crypto.Digest) (io.ReadCloser, error)
}

func (h *hooked) Hasher() crypto.Hasher {
	if h.hasher != nil {
		return h.hasher()
	}

	return h.Store.Hasher()
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Put(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
	if h.put != nil {
		return h.put(ctx, d, data)
	}

	return h.Store.Put(ctx, d, data)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Get(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error) {
	if h.get != nil {
		return h.get(ctx, d, dst)
	}

	return h.Store.Get(ctx, d, dst)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Has(ctx context.Context, d crypto.Digest) (bool, error) {
	if h.has != nil {
		return h.has(ctx, d)
	}

	return h.Store.Has(ctx, d)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) PutStream(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
	if h.putStream != nil {
		return h.putStream(ctx, d, r)
	}

	return h.Store.PutStream(ctx, d, r)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) GetStream(ctx context.Context, d crypto.Digest) (io.ReadCloser, error) {
	if h.getStream != nil {
		return h.getStream(ctx, d)
	}

	return h.Store.GetStream(ctx, d)
}

// unverified stores bytes under any address, as a store that skips
// verification does.
type unverified struct {
	m  map[crypto.Digest][]byte
	mu sync.Mutex
}

func (u *unverified) put(d crypto.Digest, data []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.m == nil {
		u.m = map[crypto.Digest][]byte{}
	}
	u.m[d] = bytes.Clone(data)
}

func (u *unverified) has(d crypto.Digest) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	_, ok := u.m[d]

	return ok
}

// counter counts calls per address.
type counter struct {
	n  map[crypto.Digest]int
	mu sync.Mutex
}

// next returns the number of calls for d, including this one.
func (c *counter) next(d crypto.Digest) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.n == nil {
		c.n = map[crypto.Digest]int{}
	}
	c.n[d]++

	return c.n[d]
}

// breaking returns a run against the memory store with the hooks that
// hook installs.
func breaking(hook func(s *memory.Store, h *hooked)) run {
	return run{newStore: func(hasher crypto.Hasher) cas.Store {
		h := &hooked{Store: memory.New(hasher)}
		hook(h.Store, h)

		return h
	}}
}

// runs maps reference to the passing run, and the name of each case of
// [castest.AssertStore] to a run that breaks the law of that case.
//
//nolint:wrapcheck // the test doubles pass errors through
func runs() map[string]run {
	newMemory := func(h crypto.Hasher) cas.Store { return memory.New(h) }

	return map[string]run{
		reference: {
			newStore: newMemory,
			options: []castest.Option{
				castest.WithReopen(func(_ *testing.T, s cas.Store) cas.Store { return s }),
				castest.WithCrash(func(_ *testing.T, s cas.Store, write func()) cas.Store {
					write()

					return s
				}),
				castest.WithZeroAllocGet(),
			},
		},
		"Get into a buffer with room does not allocate": {
			newStore: breaking(func(s *memory.Store, h *hooked) {
				h.get = func(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error) {
					v, err := s.Get(ctx, d, nil)

					return append(dst, v...), err
				}
			}).newStore,
			options: []castest.Option{castest.WithZeroAllocGet()},
		},
		"Hasher computes the addresses the store accepts": breaking(func(_ *memory.Store, h *hooked) {
			h.hasher = func() crypto.Hasher { return sha3.New256() }
		}),
		"Put and Get round-trip an independent slice": breaking(func(s *memory.Store, h *hooked) {
			var (
				mu     sync.Mutex
				served = map[crypto.Digest][]byte{}
			)
			h.get = func(ctx context.Context, d crypto.Digest, _ []byte) ([]byte, error) {
				mu.Lock()
				defer mu.Unlock()

				if v, ok := served[d]; ok {
					return v, nil
				}
				v, err := s.Get(ctx, d, nil)
				if err == nil {
					served[d] = v
				}

				return v, err
			}
		}),
		"Get appends to the caller's buffer": breaking(func(s *memory.Store, h *hooked) {
			h.get = func(ctx context.Context, d crypto.Digest, _ []byte) ([]byte, error) {
				return s.Get(ctx, d, nil)
			}
		}),
		"a second Put of identical bytes reports wrote=false": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
				_, err := s.Put(ctx, d, data)

				return err == nil, err
			}
		}),
		"a wrong digest is Integrity and stores nothing": breaking(func(s *memory.Store, h *hooked) {
			var kept unverified
			h.put = func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
				wrote, err := s.Put(ctx, d, data)
				if errs.Classify(err) == errs.Integrity {
					kept.put(d, data)
				}

				return wrote, err
			}
			h.has = func(ctx context.Context, d crypto.Digest) (bool, error) {
				if kept.has(d) {
					return true, nil
				}

				return s.Has(ctx, d)
			}
		}),
		"a digest from another algorithm is Integrity": breaking(func(s *memory.Store, h *hooked) {
			var kept unverified
			h.put = func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
				if sha3.New256().Hash(data).Equal(d) {
					kept.put(d, data)

					return true, nil
				}

				return s.Put(ctx, d, data)
			}
		}),
		"Has agrees with Get on presence and absence": breaking(func(_ *memory.Store, h *hooked) {
			h.has = func(context.Context, crypto.Digest) (bool, error) { return true, nil }
		}),
		"every method rejects the zero digest as Invalid": breaking(func(s *memory.Store, h *hooked) {
			h.has = func(ctx context.Context, d crypto.Digest) (bool, error) {
				if d.IsZero() {
					return false, nil
				}

				return s.Has(ctx, d)
			}
		}),
		"the digest of zero bytes is a valid address": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
				if len(data) == 0 {
					return false, errs.WithClass(errors.New("castest: empty value"), errs.Invalid)
				}

				return s.Put(ctx, d, data)
			}
		}),
		"every method returns the error of a done context": breaking(func(s *memory.Store, h *hooked) {
			h.has = func(ctx context.Context, d crypto.Digest) (bool, error) {
				return s.Has(context.WithoutCancel(ctx), d)
			}
		}),
		"concurrent Puts of one address write exactly once": breaking(func(s *memory.Store, h *hooked) {
			var calls counter
			h.put = func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
				wrote, err := s.Put(ctx, d, data)

				return wrote || (err == nil && calls.next(d) <= 2), err
			}
		}),
		"the streamed and whole-value paths agree": breaking(func(_ *memory.Store, h *hooked) {
			var streamed unverified
			h.putStream = func(_ context.Context, d crypto.Digest, r io.Reader) (bool, error) {
				data, err := io.ReadAll(r)
				if err != nil {
					return false, err
				}
				streamed.put(d, data)

				return true, nil
			}
		}),
		"a second streamed Put of identical bytes reports wrote=false": breaking(func(s *memory.Store, h *hooked) {
			h.putStream = func(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
				_, err := s.PutStream(ctx, d, r)

				return err == nil, err
			}
		}),
		"a failed streamed Put stores nothing": breaking(func(s *memory.Store, h *hooked) {
			var kept unverified
			h.putStream = func(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
				data, err := io.ReadAll(r)
				kept.put(d, data)
				if err != nil {
					return false, err
				}

				return s.PutStream(ctx, d, bytes.NewReader(data))
			}
			h.has = func(ctx context.Context, d crypto.Digest) (bool, error) {
				if kept.has(d) {
					return true, nil
				}

				return s.Has(ctx, d)
			}
		}),
		"concurrent streamed Puts of one address write exactly once": breaking(func(s *memory.Store, h *hooked) {
			var calls counter
			h.putStream = func(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
				wrote, err := s.PutStream(ctx, d, r)

				return wrote || (err == nil && calls.next(d) <= 2), err
			}
		}),
		"GetStream of an absent address is NotFound": breaking(func(s *memory.Store, h *hooked) {
			h.getStream = func(ctx context.Context, d crypto.Digest) (io.ReadCloser, error) {
				rc, err := s.GetStream(ctx, d)
				if errs.Classify(err) == errs.NotFound {
					return nil, errors.New("castest: gone")
				}

				return rc, err
			}
		}),
		"a reopened store keeps every write that returned": {
			newStore: newMemory,
			options: []castest.Option{
				castest.WithReopen(func(*testing.T, cas.Store) cas.Store { return memory.New(sha256.New()) }),
			},
		},
		"a crash leaves each address absent or whole": {
			newStore: newMemory,
			options: []castest.Option{
				castest.WithCrash(func(_ *testing.T, _ cas.Store, write func()) cas.Store {
					write()

					return memory.New(sha256.New())
				}),
			},
		},
	}
}

// runSuite runs the suite in a child process against the run named
// name, and returns the output of go test -v and the exit error.
func runSuite(t *testing.T, name string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAssertStore$", "-test.v")
	cmd.Env = append(os.Environ(), brokenEnv+"="+name)
	out, err := cmd.CombinedOutput()

	return string(out), err //nolint:wrapcheck // the exit error is the result
}

// TestAssertStore checks that the suite detects the breach of each law
// it states. A *testing.T cannot be observed failing in its own
// process, so the test runs the suite in a child process and reads the
// output of go test -v.
//
// The list of cases comes from a child run against the memory store with
// every option. A case without a broken store fails the test, and so
// does a broken store for a case the suite no longer has.
//
// The child does not call t.Parallel, because
// [castest.WithZeroAllocGet] measures with testing.AllocsPerRun.
//
//nolint:tparallel // see comment above
func TestAssertStore(t *testing.T) {
	if name, ok := os.LookupEnv(brokenEnv); ok {
		r := runs()[name]
		castest.AssertStore(t, r.newStore, r.options...)

		return
	}

	t.Parallel()

	out, err := runSuite(t, reference)

	t.Run("passes the memory store with every option", func(t *testing.T) {
		t.Parallel()
		testkit.NoError(t, err, "the suite must pass the memory store:\n"+out)
	})

	t.Run("has a broken store for every case", func(t *testing.T) {
		t.Parallel()

		matches := caseLine.FindAllStringSubmatch(out, -1)
		cases := make([]string, 0, len(matches))
		for _, m := range matches {
			cases = append(cases, m[1])
		}

		var broken []string
		for name := range maps.Keys(runs()) {
			if name != reference {
				broken = append(broken, strings.ReplaceAll(name, " ", "_"))
			}
		}

		slices.Sort(cases)
		slices.Sort(broken)
		testkit.Equal(t, broken, cases, "each case must have exactly one broken store")
	})

	// The children run one at a time, so the check runs at most one
	// extra test binary at once.
	t.Run("fails each case against the store that breaks it", func(t *testing.T) {
		t.Parallel()

		for name := range maps.Keys(runs()) {
			if name == reference {
				continue
			}
			t.Run(name, func(t *testing.T) { //nolint:paralleltest // see comment above
				out, err := runSuite(t, name)
				testkit.Error(t, err, "the suite must fail")
				testkit.Contains(t, out, "--- FAIL: TestAssertStore/"+strings.ReplaceAll(name, " ", "_")+" (",
					"the case must fail")
			})
		}
	})
}
