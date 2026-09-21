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
)

func TestMemoryStoreConformance(t *testing.T) {
	t.Parallel()

	blobtest.AssertStore(t, func(c clock.Clock) blob.Store {
		return memory.New(c)
	})
}

func TestPut(t *testing.T) {
	t.Parallel()

	t.Run("versions are the documented monotonic counter", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: the docblock
		// promises a counter that starts ascending from one and
		// never resets. Asserting the concrete tokens — by equality,
		// never by order — pins the counter's direction and start.
		s := memory.New(fake.New(time.Unix(0, 0).UTC()))

		first, err := s.Put(t.Context(), "a", bytes.NewReader(nil), blob.PutOptions{})
		testkit.NoError(t, err, "Put must succeed")
		testkit.Equal(t, first.Version, "1", "the counter must start at one")

		second, err := s.Put(t.Context(), "b", bytes.NewReader(nil), blob.PutOptions{})
		testkit.NoError(t, err, "Put must succeed")
		testkit.Equal(t, second.Version, "2", "the counter must ascend by one")
	})
}

func TestList(t *testing.T) {
	t.Parallel()

	t.Run("applies the default page size to an unset limit", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: the page package
		// says a limit at or below zero means the implementation's
		// default, and the default must actually bound the page.
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
			"a bounded page with a remainder must carry a continuation token")
	})

	t.Run("an exactly-full final page carries no token", func(t *testing.T) {
		t.Parallel()

		// Implementation contract beyond the seam: a token exists
		// only when the page was truncated, so a walk over six
		// objects at page size three costs exactly two calls — a
		// spurious third empty page would tax every walker.
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
			testkit.True(t, calls <= 3, "the walk must not exceed the minimal page count")

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
			"six objects at page size three must walk in exactly two calls")
	})
}
