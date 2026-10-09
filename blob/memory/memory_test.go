// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

// Sizes of the allocation checks and the benchmarks.
const (
	// benchObjectSize is the size of the object that ReadRange reads from.
	benchObjectSize = 64 << 10

	// benchRangeSize is the size of the range that ReadRange reads.
	benchRangeSize = 4 << 10

	// benchBodySize is the size of the body of a measured Put, which
	// io.ReadAll reads into its first buffer.
	benchBodySize = 256

	// benchObjects is the number of objects of the store that List pages
	// through. Their versions are past 100, so each Put formats its version
	// into a new string.
	benchObjects = 1 << 16
)

// Allocation counts of the methods of a Store.
const (
	// putAllocs is the number of allocations of a Put of a body of
	// benchBodySize bytes under a present key: the buffer of io.ReadAll and
	// the digits of the version.
	putAllocs = 2

	// getAllocs is the number of allocations of a Get: the reader.
	getAllocs = 1

	// listAllocs is the number of allocations of a List of one page: the
	// snapshot of the page and its cursor.
	listAllocs = 2
)

// The operations of the concurrent history of a Store.
const (
	opPut    = "put"
	opGet    = "get"
	opDelete = "delete"
)

// write is one write of a sequence that a property draws: a Put of key, or
// a Delete of it.
type write struct {
	key string
	put bool
}

// TestMemoryStoreConformance runs the conformance suite with every
// option. The storage of a memory Store is the Store itself, so a
// reopen returns it unchanged and a crash comes after write returns.
func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	blobtest.AssertStore(t,
		func(c clock.Clock) blob.Store { return memory.New(c) },
		blobtest.WithReopen(func(_ *testing.T, s blob.Store) blob.Store { return s }),
		blobtest.WithCrash(func(_ *testing.T, s blob.Store, write func()) blob.Store {
			write()

			return s
		}),
		blobtest.WithRangeReader(),
	)
}

func TestStore(t *testing.T) {
	t.Parallel()

	t.Run("Put", func(t *testing.T) {
		t.Parallel()

		t.Run("numbers the versions of its writes from one upward across every Delete", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "each Put must take the next number of a counter that no Delete resets", func(c *prop.Case) {
				s := memory.New(fake.New(time.Unix(0, 0).UTC()))
				writes := c.Draw(prop.List(prop.Composite(func(c *prop.Case) write {
					return write{
						key: c.Draw(prop.SampledFrom("a", "b", "c"), "key"),
						put: c.Draw(prop.Boolean(), "put"),
					}
				}), prop.MaxSize(50)), "writes")
				var got, want []version.Version
				for _, w := range writes {
					if !w.put {
						assert.NoError(c, s.Delete(c.Context(), w.key, version.Unspecified), "Delete must succeed")

						continue
					}
					info, err := s.Put(c.Context(), w.key, bytes.NewReader(nil), blob.PutOptions{})
					assert.NoError(c, err, "Put must succeed")
					got = append(got, info.Version)
					want = append(want, version.Version(strconv.Itoa(len(want)+1)))
				}
				assert.Equal(c, got, want, "the versions must count the writes", assert.EquateEmpty())
			})
		})
	})

	t.Run("List", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the default page size to an unset limit", func(t *testing.T) {
			t.Parallel()
			s := filled(t, 60)
			cur, err := s.List(t.Context(), "", page.Page{})
			assert.NoError(t, err, "List with an unset limit must succeed")
			n := 0
			for _, err := range cur.Seq(t.Context()) {
				assert.NoError(t, err, "the iteration must not fail")
				n++
			}
			expect.NoError(t, cur.Close(), "the cursor must close")
			expect.Equal(t, n, 50, "the default page size must bound the page")
			expect.NotEqual(t, cur.NextPage(), "", "a truncated page must have a continuation token")
		})

		t.Run("returns no token after a full last page", func(t *testing.T) {
			t.Parallel()
			s := filled(t, 6)
			var tokens []string
			p := page.Page{Limit: 3}
			for range 3 {
				cur, err := s.List(t.Context(), "", p)
				assert.NoError(t, err, "List must succeed")
				p.Token = cur.NextPage()
				assert.NoError(t, cur.Close(), "the cursor must close")
				if tokens = append(tokens, p.Token); p.Token == "" {
					break
				}
			}
			assert.Equal(t, tokens, []string{"k/00002", ""}, "six objects at a page size of three must take two calls")
		})
	})

	t.Run("orders concurrent calls as one sequence of calls", func(t *testing.T) {
		t.Parallel()
		s := memory.New(fake.New(time.Unix(0, 0).UTC()))
		keys := []string{"a", "b"}
		h := history.New()
		outcomes := history.Concurrently(4, 10*time.Second, func(client int) (any, error) {
			var last any
			for i := range 30 {
				key := keys[(client+i)%len(keys)]
				switch i % 3 {
				case 0:
					data := fmt.Sprintf("%d/%d", client, i)
					call := h.Invoke(client, opPut, []any{data}, key)
					if _, err := s.Put(t.Context(), key, bytes.NewReader([]byte(data)), blob.PutOptions{}); err != nil {
						call.Unknown(err)

						return nil, err
					}
					call.OK(nil)
				case 1:
					call := h.Invoke(client, opGet, nil, key)
					rc, _, err := s.Get(t.Context(), key)
					if errs.Classify(err) == errs.NotFound {
						call.OK("")
						last = ""

						continue
					}
					if err != nil {
						call.Unknown(err)

						return nil, err
					}
					data, err := io.ReadAll(rc)
					if err == nil {
						err = rc.Close()
					}
					if err != nil {
						call.Unknown(err)

						return nil, err //nolint:wrapcheck // the error of the reader is the outcome of the client
					}
					call.OK(string(data))
					last = string(data)
				default:
					call := h.Invoke(client, opDelete, nil, key)
					if err := s.Delete(t.Context(), key, version.Unspecified); err != nil {
						call.Unknown(err)

						return nil, err
					}
					call.OK(nil)
				}
			}

			return last, nil
		})
		for _, o := range outcomes {
			expect.True(t, o.Finished, "every client must finish")
			expect.NoError(t, o.Error, "every call must succeed")
		}
		// The state of a key is its body, and the empty string for an
		// absent key. Every Put writes a body that is not empty.
		history.Linearizable(t, h, history.Spec[string]{
			Initial: func() string { return "" },
			Next: func(state string, op history.Operation) []string {
				if op.Name == opPut {
					return []string{op.Args[0].(string)}
				}
				if op.Name == opDelete {
					return []string{""}
				}
				if op.Returned(state) {
					return []string{state}
				}

				return nil
			},
		}, "each Get must return the body of the last Put or Delete in one order of the calls")
	})
}

// TestStoreAllocs checks the allocation contract of each method of a
// Store. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestStoreAllocs(t *testing.T) {
	s := filled(t, benchObjects)
	ctx := t.Context()
	_, err := s.Put(ctx, "k", bytes.NewReader(make([]byte, benchObjectSize)), blob.PutOptions{})
	assert.NoError(t, err, "Put must succeed")

	t.Run("Put", func(t *testing.T) {
		body, payload := bytes.NewReader(nil), make([]byte, benchBodySize)
		var info blob.Info
		expect.MaxAllocs(t, func() {
			body.Reset(payload)
			info, err = s.Put(ctx, "p", body, blob.PutOptions{})
		}, putAllocs, "Put must allocate the body and the digits of the version")
		assert.NoError(t, err, "the test must measure a Put that succeeds")
		assert.Equal(t, info.Size, int64(benchBodySize), "the test must measure a Put of the whole body")
	})

	t.Run("Get", func(t *testing.T) {
		var rc io.ReadCloser
		expect.MaxAllocs(t, func() { rc, _, err = s.Get(ctx, "k") }, getAllocs, "Get must allocate only the reader")
		assert.NoError(t, err, "the test must measure a Get that succeeds")
		assert.NoError(t, rc.Close(), "the reader must close")
	})

	t.Run("ReadRange", func(t *testing.T) {
		dst := make([]byte, benchRangeSize)
		var n int
		expect.MaxAllocs(t, func() {
			n, _, err = s.ReadRange(ctx, "k", benchRangeSize, dst, version.Unspecified)
		}, 0, "ReadRange must not allocate")
		assert.NoError(t, err, "the test must measure a ReadRange that succeeds")
		assert.Equal(t, n, benchRangeSize, "the test must measure a full range")
	})

	t.Run("Stat", func(t *testing.T) {
		var info blob.Info
		expect.MaxAllocs(t, func() { info, err = s.Stat(ctx, "k") }, 0, "Stat must not allocate")
		assert.NoError(t, err, "the test must measure a Stat that succeeds")
		assert.Equal(t, info.Size, int64(benchObjectSize), "the test must measure the stored object")
	})

	t.Run("Delete", func(t *testing.T) {
		body := bytes.NewReader(nil)
		expect.MaxAllocsWithSetup(t, func() string {
			_, perr := s.Put(ctx, "d", body, blob.PutOptions{})
			assert.NoError(t, perr, "Put must succeed")

			return "d"
		}, func(key string) { err = s.Delete(ctx, key, version.Unspecified) }, 0, "Delete must not allocate")
		assert.NoError(t, err, "the test must measure a Delete that succeeds")
	})

	t.Run("List", func(t *testing.T) {
		p := page.Page{Token: fmt.Sprintf("k/%05d", benchObjects/2)}
		var cur page.Cursor[blob.Info]
		expect.MaxAllocs(t, func() { cur, err = s.List(ctx, "k/", p) }, listAllocs, "List must allocate the page")
		assert.NoError(t, err, "the test must measure a List that succeeds")
		assert.NotEqual(t, cur.NextPage(), "", "the test must measure a full page")
	})
}

// BenchmarkStore reports the cost of each method of a Store of 65,536
// objects, and fails when a method allocates more than its allocation
// contract allows.
func BenchmarkStore(b *testing.B) {
	s := filled(b, benchObjects)
	ctx := b.Context()
	_, err := s.Put(ctx, "k", bytes.NewReader(make([]byte, benchObjectSize)), blob.PutOptions{})
	assert.NoError(b, err, "Put must succeed")

	b.Run("Put", func(b *testing.B) {
		body, payload := bytes.NewReader(nil), make([]byte, benchBodySize)
		var err error

		c := bench.Start(b).MaxAllocs(putAllocs)
		defer c.End()

		for c.Loop() {
			body.Reset(payload)
			_, err = s.Put(ctx, "p", body, blob.PutOptions{})
		}

		assert.NoError(b, err, "the benchmark must measure a Put that succeeds")
	})

	b.Run("Get", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(getAllocs)
		defer c.End()

		for c.Loop() {
			_, _, err = s.Get(ctx, "k")
		}

		assert.NoError(b, err, "the benchmark must measure a Get that succeeds")
	})

	b.Run("ReadRange", func(b *testing.B) {
		dst := make([]byte, benchRangeSize)
		var n int
		var err error

		b.SetBytes(benchRangeSize)
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			n, _, err = s.ReadRange(ctx, "k", benchRangeSize, dst, version.Unspecified)
		}

		assert.NoError(b, err, "the benchmark must measure a ReadRange that succeeds")
		assert.Equal(b, n, benchRangeSize, "the benchmark must measure a full range")
	})

	b.Run("Stat", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = s.Stat(ctx, "k")
		}

		assert.NoError(b, err, "the benchmark must measure a Stat that succeeds")
	})

	b.Run("Delete", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = s.Delete(ctx, "absent", version.Unspecified)
		}

		assert.NoError(b, err, "the benchmark must measure a Delete that succeeds")
	})

	b.Run("List", func(b *testing.B) {
		p := page.Page{Token: fmt.Sprintf("k/%05d", benchObjects/2)}
		var cur page.Cursor[blob.Info]
		var err error

		c := bench.Start(b).MaxAllocs(listAllocs)
		defer c.End()

		for c.Loop() {
			cur, err = s.List(ctx, "k/", p)
		}

		assert.NoError(b, err, "the benchmark must measure a List that succeeds")
		assert.NotEqual(b, cur.NextPage(), "", "the benchmark must measure a full page")
	})
}

// filled returns a Store of n empty objects under the keys that k/%05d
// formats for 0 to n-1.
func filled(tb testing.TB, n int) *memory.Store {
	tb.Helper()
	s := memory.New(fake.New(time.Unix(0, 0).UTC()))
	for i := range n {
		_, err := s.Put(tb.Context(), fmt.Sprintf("k/%05d", i), bytes.NewReader(nil), blob.PutOptions{})
		assert.NoError(tb, err, "Put must succeed")
	}

	return s
}
