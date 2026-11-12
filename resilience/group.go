// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"sync/atomic"
)

// groupClosed is the bit of the state of a [Group] that Finally sets. The
// bits below it count the members.
const groupClosed int64 = 1 << 62

// Group counts the goroutines of a component, and runs the last section of
// the Close of the component once every member has returned. A wait of
// [Group.Finally] for the members ends at the end of the owner of the Group.
// The owner is a context, such as the context that an engine cancels when
// its disk fails. A member in a call that hangs then does not keep the Close.
//
// [Group.Go] starts a member on a goroutine of its own. [Group.Add] adds the
// goroutine of its caller as a member, and [Group.Done] ends it. Once
// Finally has started, Go and Add refuse new members.
//
// Before the owner ends, Finally waits for the members and runs the section.
// Once the owner has ended, Finally does not wait. It runs the section at
// once when every member has returned. Otherwise the last member runs the
// section in its Done.
//
// A waiting Finally receives a wake on a channel of the Group, so a
// [testing/synctest] bubble counts the wait as durable when the bubble
// created the Group and its owner.
//
// The zero Group has no channel and no owner, so it refuses members. Its
// Finally runs f at once. [NewGroup] returns a Group that takes members.
//
// # Concurrency
//
// Safe for concurrent use. The Group keeps the count of its members and the
// bit of Finally in one atomic word.
//
// # Allocation contract
//
// [Group.Add] and [Group.Done] do not allocate. [Group.Go] allocates the
// goroutine of the member and its function. [Group.Finally] allocates once
// the owner has ended.
type Group struct {
	owner context.Context //nolint:containedctx // the owner ends the wait of Finally

	// idle is closed when the last member ends after Finally has started.
	idle chan struct{}

	// done is the Done channel of the owner.
	done <-chan struct{}

	// final is nil, or the section that a Finally marked for the last
	// member. Finally marks a section once the owner has ended.
	final atomic.Pointer[section]

	// state is the count of the members, plus groupClosed once Finally has
	// started.
	state atomic.Int64
}

// NewGroup returns a Group without a member whose wait ends at the end of
// owner. A Group of a component that does not fail takes
// [context.Background] as its owner.
//
// # Allocation contract
//
// Allocates the Group and its channel, and the Done channel of owner when
// owner has none yet.
func NewGroup(owner context.Context) *Group {
	return &Group{owner: owner, idle: make(chan struct{}), done: owner.Done()}
}

// Go runs f on a goroutine of its own as a member of g, and reports whether
// it started f. Once Finally has started, Go does not run f and reports
// false.
//
// # Allocation contract
//
// Allocates the goroutine and its function.
func (g *Group) Go(f func()) bool {
	if !g.Add() {
		return false
	}

	go func() {
		defer g.Done()

		f()
	}()

	return true
}

// Add adds the goroutine of the caller as a member of g, which [Group.Done]
// ends. Once Finally has started, Add does not add a member and reports
// false. Add of the zero Group reports false too.
//
// # Allocation contract
//
// Zero-alloc.
func (g *Group) Add() bool {
	for {
		s := g.state.Load()
		if s&groupClosed != 0 || g.idle == nil {
			return false
		}

		if g.state.CompareAndSwap(s, s+1) {
			return true
		}
	}
}

// Done ends a member that [Group.Add] added. The last member that ends after
// Finally has started wakes a waiting Finally. It also runs the section that
// a Finally marked once the owner had ended. Done of a Group without a member
// does not change g.
//
// # Allocation contract
//
// Zero-alloc.
func (g *Group) Done() {
	for {
		s := g.state.Load()
		if s&^groupClosed == 0 {
			return
		}

		if g.state.CompareAndSwap(s, s-1) {
			if s == groupClosed|1 {
				g.last()
			}

			return
		}
	}
}

// Finally runs f once every member of g has returned, and reports whether f
// ran before Finally returned. A Close runs its last section with it:
//
//	if !c.members.Finally(c.release) {
//	    return errBusy
//	}
//
// Finally stops new members first. Before the owner ends, Finally waits for
// the members, and then runs f. Once the owner has ended, Finally does not
// wait. It runs f at once when every member has returned. Otherwise it marks
// f for the last member and returns false. That member runs f in its
// [Group.Done].
//
// A Group marks one section at a time, so a Finally that finds another
// section marked does not run f and returns false. f runs on the goroutine
// of the caller of Finally or of the last member.
//
// # Allocation contract
//
// Allocates the mark of f once the owner has ended. f escapes to the heap.
func (g *Group) Finally(f func()) bool {
	g.state.Or(groupClosed)

	// Once the owner has ended, the hand-off runs f as the loop below does
	// when every member has returned. This check picks the hand-off.
	//dokimi:mutate-skip sbr-delete: the hand-off and the loop both run f when every member has returned and report true
	select {
	case <-g.done:
		return g.handOff(f)
	default:
	}

	for g.state.Load() != groupClosed {
		select {
		case <-g.idle:
		case <-g.done:
			return g.handOff(f)
		}
	}

	f()

	return true
}

// last wakes the Finally that waits for the members, and runs the section
// that a Finally marked. The last member calls it after Finally has started.
//
// Finally marks its section once it found a member, and then counts the
// members again. The swap after the decrement of the last member and that
// second count leave the section to one of the two callers whatever their
// order.
func (g *Group) last() {
	close(g.idle)

	//dokimi:mutate-skip ror-true: Swap returns nil only when Finally ran the section between the decrement and the Swap, an order that no test can force
	if sec := g.final.Swap(nil); sec != nil {
		sec.f()
		sec.ran.Store(true)
	}
}

// handOff marks f for the last member once the owner has ended, and reports
// whether f ran before handOff returned. It marks nothing while another
// section is marked.
//
// The last member can end between the end of the owner and the mark.
// handOff then runs the marked section itself.
func (g *Group) handOff(f func()) bool {
	s := &section{f: f}
	if !g.final.CompareAndSwap(nil, s) {
		return false
	}

	if g.state.Load() == groupClosed {
		//dokimi:mutate-skip ror-true: Swap returns nil only when the last member ran the section between the count and the Swap, an order that no test can force
		if sec := g.final.Swap(nil); sec != nil {
			sec.f()
			sec.ran.Store(true)
		}
	}

	return s.ran.Load()
}
