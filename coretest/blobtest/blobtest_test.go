// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package blobtest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// brokenEnv names the environment variable that tells a child test
// process which run of the suite to perform.
const brokenEnv = "BLOBTEST_BROKEN"

// reference names the run against the memory store with every option.
const reference = "reference"

// caseLine matches the line that go test -v prints when a top-level
// case of the suite starts.
var caseLine = regexp.MustCompile(`(?m)^=== RUN\s+TestAssertStore/([^/\s]+)$`)

// run is a store, and the options for it, that the suite runs against.
type run struct {
	newStore func(c clock.Clock) blob.Store
	options  []blobtest.Option
}

// hooked is a memory store whose methods a breakage replaces one at a
// time. A nil hook calls the memory store.
type hooked struct {
	*memory.Store

	put    func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error)
	get    func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error)
	stat   func(ctx context.Context, key string) (blob.Info, error)
	delete func(ctx context.Context, key string, ifMatch version.Version) error
	list   func(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	if h.put != nil {
		return h.put(ctx, key, r, opts)
	}

	return h.Store.Put(ctx, key, r, opts)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	if h.get != nil {
		return h.get(ctx, key)
	}

	return h.Store.Get(ctx, key)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Stat(ctx context.Context, key string) (blob.Info, error) {
	if h.stat != nil {
		return h.stat(ctx, key)
	}

	return h.Store.Stat(ctx, key)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) Delete(ctx context.Context, key string, ifMatch version.Version) error {
	if h.delete != nil {
		return h.delete(ctx, key, ifMatch)
	}

	return h.Store.Delete(ctx, key, ifMatch)
}

//nolint:wrapcheck // the test double passes the error through
func (h *hooked) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	if h.list != nil {
		return h.list(ctx, prefix, p)
	}

	return h.Store.List(ctx, prefix, p)
}

// lateReader opens the object on its first Read, so it serves the
// object the key names then and not the one it named when Get
// returned.
type lateReader struct {
	rc   io.ReadCloser
	open func() (io.ReadCloser, error)
}

//nolint:wrapcheck // the test double passes the error through
func (r *lateReader) Read(p []byte) (int, error) {
	if r.rc == nil {
		rc, err := r.open()
		if err != nil {
			return 0, err
		}
		r.rc = rc
	}

	return r.rc.Read(p)
}

//nolint:wrapcheck // the test double passes the error through
func (r *lateReader) Close() error {
	if r.rc == nil {
		return nil
	}

	return r.rc.Close()
}

// skipFirst drops the first object of its page, as a cursor that loses
// an object on a page boundary does.
type skipFirst struct {
	page.Cursor[blob.Info]
}

func (c skipFirst) Seq(ctx context.Context) iter.Seq2[blob.Info, error] {
	return func(yield func(blob.Info, error) bool) {
		skipped := false
		for info, err := range c.Cursor.Seq(ctx) {
			if !skipped && err == nil {
				skipped = true

				continue
			}
			if !yield(info, err) {
				return
			}
		}
	}
}

// newFake returns a fake clock at the Unix epoch.
func newFake() clock.Clock { return fake.New(time.Unix(0, 0).UTC()) }

// breaking returns a run against the memory store with the hooks that
// hook installs.
func breaking(hook func(s *memory.Store, h *hooked)) run {
	return run{newStore: func(c clock.Clock) blob.Store {
		h := &hooked{Store: memory.New(c)}
		hook(h.Store, h)

		return h
	}}
}

// runs maps reference to the passing run, and the name of each case of
// [blobtest.AssertStore] to a run that breaks the law of that case.
//
//nolint:wrapcheck // the test doubles pass errors through
func runs() map[string]run {
	return map[string]run{
		reference: {
			newStore: func(c clock.Clock) blob.Store { return memory.New(c) },
			options: []blobtest.Option{
				blobtest.WithReopen(func(_ *testing.T, s blob.Store) blob.Store { return s }),
				blobtest.WithCrash(func(_ *testing.T, s blob.Store, write func()) blob.Store {
					write()

					return s
				}),
			},
		},
		"Put, Get and Stat agree on the body and metadata": breaking(func(s *memory.Store, h *hooked) {
			h.stat = func(ctx context.Context, key string) (blob.Info, error) {
				info, err := s.Stat(ctx, key)
				info.ModTime = time.Time{}

				return info, err
			}
		}),
		"an overwrite issues a new version": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				info, err := s.Put(ctx, key, r, opts)
				info.Version = "always"

				return info, err
			}
		}),
		"IfMatch guards the version it names": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				opts.Write.IfMatch = ""

				return s.Put(ctx, key, r, opts)
			}
		}),
		"IfNoneMatch wildcard creates exactly once": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				if opts.Write.IfNoneMatch == version.Wildcard {
					opts.Write.IfNoneMatch = ""
				}

				return s.Put(ctx, key, r, opts)
			}
		}),
		"IfNoneMatch naming a version guards that version": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				if opts.Write.IfNoneMatch != version.Wildcard {
					opts.Write.IfNoneMatch = ""
				}

				return s.Put(ctx, key, r, opts)
			}
		}),
		"Delete follows the conditional table": breaking(func(s *memory.Store, h *hooked) {
			h.delete = func(ctx context.Context, key string, _ version.Version) error {
				return s.Delete(ctx, key, "")
			}
		}),
		"a failed Put leaves the previous object in place": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				body, err := io.ReadAll(r)
				if err != nil {
					_, _ = s.Put(ctx, key, bytes.NewReader(body), opts)

					return blob.Info{}, err
				}

				return s.Put(ctx, key, bytes.NewReader(body), opts)
			}
		}),
		"an open reader serves the version its Info named": breaking(func(s *memory.Store, h *hooked) {
			h.get = func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
				info, err := s.Stat(ctx, key)
				if err != nil {
					return nil, blob.Info{}, err
				}

				return &lateReader{open: func() (io.ReadCloser, error) {
					rc, _, err := s.Get(ctx, key)

					return rc, err
				}}, info, nil
			}
		}),
		"an empty body is an object": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				body, err := io.ReadAll(r)
				if err == nil && len(body) == 0 {
					return blob.Info{Key: key, Version: "empty"}, nil
				}

				return s.Put(ctx, key, bytes.NewReader(body), opts)
			}
		}),
		"a cursor chain yields every key exactly once": breaking(func(s *memory.Store, h *hooked) {
			h.list = func(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
				cur, err := s.List(ctx, prefix, p)
				if err != nil || p.Token == "" {
					return cur, err
				}

				return skipFirst{cur}, nil
			}
		}),
		"every method rejects an invalid key as Invalid": breaking(func(s *memory.Store, h *hooked) {
			h.stat = func(ctx context.Context, key string) (blob.Info, error) {
				if !blob.ValidKey(key) {
					return blob.Info{}, errs.WithClass(errors.New("blobtest: no such key"), errs.NotFound)
				}

				return s.Stat(ctx, key)
			}
		}),
		"a key and a longer key it prefixes exist together": breaking(func(s *memory.Store, h *hooked) {
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				info, err := s.Put(ctx, key, r, opts)
				for i, c := range key {
					if c == '/' {
						_ = s.Delete(ctx, key[:i], "")
					}
				}

				return info, err
			}
		}),
		"absence classifies as NotFound": breaking(func(s *memory.Store, h *hooked) {
			h.get = func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
				rc, info, err := s.Get(ctx, key)
				if errs.Classify(err) == errs.NotFound {
					return nil, blob.Info{}, errors.New("blobtest: gone")
				}

				return rc, info, err
			}
		}),
		"every method returns the error of a done context": breaking(func(s *memory.Store, h *hooked) {
			h.list = func(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
				return s.List(context.WithoutCancel(ctx), prefix, p)
			}
		}),
		"a recreated key never reuses a version": breaking(func(s *memory.Store, h *hooked) {
			var (
				mu          sync.Mutex
				generations = map[string]int{}
			)
			h.put = func(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
				info, err := s.Put(ctx, key, r, opts)
				mu.Lock()
				generations[key]++
				info.Version = version.Version(key + "#" + strconv.Itoa(generations[key]))
				mu.Unlock()

				return info, err
			}
			h.delete = func(ctx context.Context, key string, ifMatch version.Version) error {
				mu.Lock()
				delete(generations, key)
				mu.Unlock()

				return s.Delete(ctx, key, ifMatch)
			}
		}),
		"a reopened store keeps every write that returned": {
			newStore: func(c clock.Clock) blob.Store { return memory.New(c) },
			options: []blobtest.Option{
				blobtest.WithReopen(func(*testing.T, blob.Store) blob.Store { return memory.New(newFake()) }),
			},
		},
		"a crash leaves each key as it was or whole": {
			newStore: func(c clock.Clock) blob.Store { return memory.New(c) },
			options: []blobtest.Option{
				blobtest.WithCrash(func(_ *testing.T, _ blob.Store, write func()) blob.Store {
					write()

					return memory.New(newFake())
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
func TestAssertStore(t *testing.T) {
	t.Parallel()

	if name, ok := os.LookupEnv(brokenEnv); ok {
		r := runs()[name]
		blobtest.AssertStore(t, r.newStore, r.options...)

		return
	}

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
