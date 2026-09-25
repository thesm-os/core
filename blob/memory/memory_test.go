// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/version"
)

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

// benchObjectSize and benchRangeSize are the object and the range that
// BenchmarkReadRange reads: 4 KiB from a 64 KiB object.
const (
	benchObjectSize = 64 << 10
	benchRangeSize  = 4 << 10
)

// BenchmarkReadRange reports the cost and the allocations of ReadRange
// on its happy path.
func BenchmarkReadRange(b *testing.B) {
	s := memory.New(fake.New(time.Unix(0, 0).UTC()))
	_, err := s.Put(b.Context(), "k", bytes.NewReader(make([]byte, benchObjectSize)), blob.PutOptions{})
	testkit.NoError(b, err, "Put must succeed")

	ctx := b.Context()
	dst := make([]byte, benchRangeSize)

	b.ReportAllocs()
	b.SetBytes(benchRangeSize)
	for b.Loop() {
		_, _, _ = s.ReadRange(ctx, "k", benchRangeSize, dst, version.Unspecified)
	}
}

// TestPut covers a contract of the memory Store beyond the seam. Its
// docblock documents versions as a counter that starts at one and never
// resets. The test compares the tokens by equality, never by order, as
// the version package requires.
func TestPut(t *testing.T) {
	t.Parallel()

	t.Run("versions are the documented counter", func(t *testing.T) {
		t.Parallel()

		s := memory.New(fake.New(time.Unix(0, 0).UTC()))

		first, err := s.Put(t.Context(), "a", bytes.NewReader(nil), blob.PutOptions{})
		testkit.NoError(t, err, "Put must succeed")
		testkit.Equal(t, first.Version, "1", "the counter must start at one")

		second, err := s.Put(t.Context(), "b", bytes.NewReader(nil), blob.PutOptions{})
		testkit.NoError(t, err, "Put must succeed")
		testkit.Equal(t, second.Version, "2", "the counter must ascend by one")
	})
}

// TestList covers contracts of the memory Store beyond the seam:
//
//   - A limit at or below zero means the default page size, as the page
//     package documents, and the default bounds the page.
//   - A page has a token only when the store truncated it, so a walk
//     of six objects at page size three takes two calls and no empty
//     third page.
func TestList(t *testing.T) {
	t.Parallel()

	t.Run("applies the default page size to an unset limit", func(t *testing.T) {
		t.Parallel()

		s := memory.New(fake.New(time.Unix(0, 0).UTC()))
		for i := range 60 {
			_, err := s.Put(t.Context(), fmt.Sprintf("k/%02d", i),
				bytes.NewReader(nil), blob.PutOptions{})
			testkit.NoError(t, err, "Put must succeed")
		}

		cur, err := s.List(t.Context(), "", page.Page{})
		testkit.NoError(t, err, "List with an unset limit must succeed")

		n := 0
		for _, ierr := range cur.Seq(t.Context()) {
			testkit.NoError(t, ierr, "iteration must not fail")
			n++
		}
		testkit.NoError(t, cur.Close(), "the cursor must close")

		testkit.Equal(t, n, 50, "the default page size must bound the page")
		testkit.NotEqual(t, cur.NextPage(), "",
			"a truncated page must carry a continuation token")
	})

	t.Run("an exactly full final page carries no token", func(t *testing.T) {
		t.Parallel()

		s := memory.New(fake.New(time.Unix(0, 0).UTC()))
		for i := range 6 {
			_, err := s.Put(t.Context(), fmt.Sprintf("k/%02d", i),
				bytes.NewReader(nil), blob.PutOptions{})
			testkit.NoError(t, err, "Put must succeed")
		}

		calls := 0
		p := page.Page{Limit: 3}
		for {
			calls++
			testkit.True(t, calls <= 3, "the walk must not take more than three calls")

			cur, err := s.List(t.Context(), "", p)
			testkit.NoError(t, err, "List must succeed")
			for _, ierr := range cur.Seq(t.Context()) {
				testkit.NoError(t, ierr, "iteration must not fail")
			}
			tok := cur.NextPage()
			testkit.NoError(t, cur.Close(), "the cursor must close")
			if tok == "" {
				break
			}
			p.Token = tok
		}

		testkit.Equal(t, calls, 2,
			"six objects at page size three must take exactly two calls")
	})
}
