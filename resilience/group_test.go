// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/resilience"
)

func TestGroup(t *testing.T) {
	t.Parallel()

	t.Run("NewGroup", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Group without a member", func(t *testing.T) {
			t.Parallel()
			g := resilience.NewGroup(t.Context())
			var runs atomic.Int32
			expect.True(t, g.Finally(func() { runs.Add(1) }), "Finally must run f at once for a Group without a member")
			expect.Equal(t, runs.Load(), 1, "Finally must run f once")
		})
	})

	t.Run("Go", func(t *testing.T) {
		t.Parallel()

		t.Run("runs f on a goroutine of its own as a member", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := resilience.NewGroup(t.Context())
				release := make(chan struct{})
				assert.True(t, g.Go(func() { <-release }), "Go must start a member")
				got := startGroupFinally(g, func() {})
				synctest.Wait()
				assert.Empty(t, got, "Finally must wait for the member that Go started")

				close(release)
				synctest.Wait()
				assert.True(t, <-got, "Finally must run f once the member returned")
			})
		})

		t.Run("reports false once Finally has started", func(t *testing.T) {
			t.Parallel()
			g := resilience.NewGroup(t.Context())
			assert.True(t, g.Finally(func() {}), "Finally must run f for a Group without a member")

			var ran atomic.Bool
			expect.False(t, g.Go(func() { ran.Store(true) }), "Go must refuse a member after Finally")
			expect.False(t, ran.Load(), "Go must not run f after Finally")
		})
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the caller as a member", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := resilience.NewGroup(t.Context())
				assert.True(t, g.Add(), "Add must add a member")
				got := startGroupFinally(g, func() {})
				synctest.Wait()
				assert.Empty(t, got, "Finally must wait for the member that Add added")

				g.Done()
				synctest.Wait()
				assert.True(t, <-got, "Finally must run f once the member ended")
			})
		})

		t.Run("reports false once Finally has started", func(t *testing.T) {
			t.Parallel()
			g := resilience.NewGroup(t.Context())
			assert.True(t, g.Finally(func() {}), "Finally must run f for a Group without a member")
			assert.False(t, g.Add(), "Add must refuse a member after Finally")
		})

		t.Run("reports false for the zero Group", func(t *testing.T) {
			t.Parallel()
			var g resilience.Group
			assert.False(t, g.Add(), "the zero Group must refuse a member")
		})
	})

	t.Run("Done", func(t *testing.T) {
		t.Parallel()

		t.Run("runs the section that Finally marked when the last member ends", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			g := resilience.NewGroup(owner)
			assert.True(t, g.Add(), "Add must add the first member")
			assert.True(t, g.Add(), "Add must add the second member")
			fail(errDisk)
			var runs atomic.Int32
			assert.False(t, g.Finally(func() { runs.Add(1) }), "Finally must leave the section to the last member")

			g.Done()
			expect.Equal(t, runs.Load(), 0, "a member that is not the last must not run the section")
			g.Done()
			expect.Equal(t, runs.Load(), 1, "the last member must run the section once")
		})

		t.Run("does not change a Group without a member", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := resilience.NewGroup(t.Context())
				g.Done()
				assert.True(t, g.Add(), "Add must add a member")
				got := startGroupFinally(g, func() {})
				synctest.Wait()
				assert.Empty(t, got, "a Done without a member must not end the member of a later Add")

				g.Done()
				synctest.Wait()
				assert.True(t, <-got, "Finally must run f once the member ended")
			})
		})
	})

	t.Run("Finally", func(t *testing.T) {
		t.Parallel()

		t.Run("runs f at once for the zero Group", func(t *testing.T) {
			t.Parallel()
			var g resilience.Group
			var runs atomic.Int32
			expect.True(t, g.Finally(func() { runs.Add(1) }), "Finally must report that f ran")
			expect.Equal(t, runs.Load(), 1, "Finally must run f once")
		})

		t.Run("returns false when the owner ends during the wait", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				owner, fail := context.WithCancelCause(t.Context())
				g := resilience.NewGroup(owner)
				assert.True(t, g.Add(), "Add must add a member")
				var runs atomic.Int32
				got := startGroupFinally(g, func() { runs.Add(1) })
				synctest.Wait()

				fail(errDisk)
				synctest.Wait()
				expect.False(t, <-got, "Finally must return at the end of the owner")
				expect.Equal(t, runs.Load(), 0, "Finally must leave f to the last member")
				g.Done()
				expect.Equal(t, runs.Load(), 1, "the last member must run f")
			})
		})

		t.Run("returns false once the owner ended while a member runs", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			g := resilience.NewGroup(owner)
			assert.True(t, g.Add(), "Add must add a member")
			fail(errDisk)
			var runs atomic.Int32
			expect.False(t, g.Finally(func() { runs.Add(1) }), "Finally must not wait once the owner ended")
			expect.Equal(t, runs.Load(), 0, "Finally must leave f to the last member")
		})

		t.Run("runs f at once after the owner ended when no member runs", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			fail(errDisk)
			g := resilience.NewGroup(owner)
			var runs atomic.Int32
			expect.True(t, g.Finally(func() { runs.Add(1) }), "Finally must report that f ran")
			expect.Equal(t, runs.Load(), 1, "Finally must run f once")
		})

		t.Run("does not run f while another section is marked", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			g := resilience.NewGroup(owner)
			assert.True(t, g.Add(), "Add must add a member")
			fail(errDisk)
			var first, second atomic.Int32
			assert.False(t, g.Finally(func() { first.Add(1) }), "the first Finally must mark its section")

			expect.False(t, g.Finally(func() { second.Add(1) }),
				"Finally must not mark f while another section is marked")
			g.Done()
			expect.Equal(t, first.Load(), 1, "the last member must run the section of the first Finally")
			expect.Equal(t, second.Load(), 0, "Finally must not run f while another section is marked")
		})

		// The last member ends while the owner ends and Finally runs, so the
		// trials cover the orders of the three calls.
		t.Run("runs f once when the last member ends as the owner ends", func(t *testing.T) {
			t.Parallel()
			const trials = 200

			for range trials {
				owner, fail := context.WithCancelCause(t.Context())
				g := resilience.NewGroup(owner)
				assert.True(t, g.Add(), "Add must add a member")

				var (
					runs   atomic.Int32
					ran    atomic.Bool
					before atomic.Int32
				)
				outcomes := history.Concurrently(3, patience, func(client int) (any, error) {
					switch client {
					case 0:
						g.Done()
					case 1:
						fail(errDisk)
					default:
						ran.Store(g.Finally(func() { runs.Add(1) }))
						before.Store(runs.Load())
					}

					return client, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every call must return")
				}

				assert.Equal(t, runs.Load(), 1, "f must run once")
				if ran.Load() {
					assert.Equal(t, before.Load(), 1, "Finally must report true only after f ran")
				}
			}
		})
	})
}

// TestGroupAllocs checks the allocation contract of Add and Done. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestGroupAllocs(t *testing.T) {
	t.Run("Add", func(t *testing.T) {
		g := resilience.NewGroup(t.Context())

		var got bool
		expect.MaxAllocs(t, func() {
			got = g.Add()
			g.Done()
		}, 0, "Add and Done must not allocate")
		assert.True(t, got, "the test must measure a member that Add added")
	})
}

// BenchmarkGroup reports the cost of Add and Done, and fails when they
// allocate.
func BenchmarkGroup(b *testing.B) {
	b.Run("Add", func(b *testing.B) {
		g := resilience.NewGroup(b.Context())

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = g.Add()
			g.Done()
		}

		assert.True(b, got, "the benchmark must measure a member that Add added")
	})
}

// startGroupFinally calls g.Finally(f) on a goroutine of its own, and returns
// the channel that receives its result. A case inside a bubble reads the
// channel after synctest.Wait.
func startGroupFinally(g *resilience.Group, f func()) <-chan bool {
	got := make(chan bool, 1)
	go func() { got <- g.Finally(f) }()

	return got
}
