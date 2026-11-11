// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"sync/atomic"
)

// Lock is a mutual exclusion lock whose waits end at the end of a context.
// It serializes a critical section that waits on a dependency, such as the
// admission of the writes of a volume to its disk.
//
// A wait for the lock ends when the context of its caller ends. It also ends
// when the owner of the lock ends. The owner is a context, such as the
// context that an engine cancels when its disk fails. The callers that wait
// behind a call that hangs then return at the failure of the dependency. A
// [sync.Mutex] keeps its waiters until the caller that has it returns.
//
// A wait is a send on a channel. A [testing/synctest] bubble counts it as
// durable when the bubble created the Lock and the contexts of the wait.
//
// [Lock.Finally] runs the last section of a Close under the lock. Once the
// owner has ended, Finally does not wait for a call that hangs. The caller
// that has the lock runs the section in its Unlock instead.
//
// A Lock is not associated with a goroutine. One goroutine can take it and
// another can free it, as with a [sync.Mutex]. The zero Lock is never free.
// [NewLock] returns a free Lock.
//
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// [Lock.Lock], [Lock.TryLock] and [Lock.Unlock] do not allocate.
// [Lock.Finally] allocates once the owner has ended.
type Lock struct {
	owner context.Context //nolint:containedctx // the owner ends every wait of the lock, and Lock returns its cause

	// slot contains a token while a caller has the lock. Its capacity is
	// one, so Lock takes the lock with a send and Unlock frees it with a
	// receive.
	slot chan struct{}

	// done is the Done channel of the owner.
	done <-chan struct{}

	// final is nil, or the section that a Finally marked for the caller that
	// has the lock. Finally marks a section once the owner has ended. A
	// caller clears the mark while it has the lock.
	final atomic.Pointer[section]
}

// section is a section that a Finally marked for the caller that has the
// lock. The caller that runs f sets ran before it frees the lock.
type section struct {
	f   func()
	ran atomic.Bool
}

// NewLock returns a free Lock whose waits end at the end of owner. A Lock
// of a component that does not fail takes [context.Background] as its owner.
//
// # Allocation contract
//
// Allocates the Lock and its channel, and the Done channel of owner when
// owner has none yet.
func NewLock(owner context.Context) *Lock {
	return &Lock{owner: owner, slot: make(chan struct{}, 1), done: owner.Done()}
}

// Lock takes the lock, and waits while another caller has it. A caller that
// took the lock frees it with Unlock:
//
//	if err := l.Lock(ctx); err != nil {
//	    return err
//	}
//	defer l.Unlock()
//
// Lock checks ctx and then the owner before it takes a free lock. It refuses
// a call whose ctx or owner ended before the call. When a context ends while
// Unlock frees the lock, Lock either takes the lock or returns the cause.
//
// Error modes, none of which takes the lock:
//
//   - The [context.Cause] of ctx when ctx ends before Lock takes the lock.
//   - The [context.Cause] of the owner when the owner ends before Lock takes
//     the lock.
//
// # Allocation contract
//
// Zero-alloc.
func (l *Lock) Lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}

	select {
	case <-l.done:
		return context.Cause(l.owner) //nolint:contextcheck // Cause only reads the error of the owner
	default:
	}

	// A free lock does not lock the channels of the contexts.
	select {
	case l.slot <- struct{}{}:
		return nil
	default:
	}

	select {
	case l.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-l.done:
		return context.Cause(l.owner) //nolint:contextcheck // see above
	}
}

// TryLock takes the lock when it is free, and reports whether it took it.
// TryLock does not wait, so the end of the owner does not change its result.
//
// # Allocation contract
//
// Zero-alloc.
func (l *Lock) TryLock() bool {
	select {
	case l.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock frees the lock for the next caller. Unlock of a free Lock does
// nothing.
//
// When a [Lock.Finally] marked a section, Unlock takes the lock again after
// it freed it. It runs the section and frees the lock. A caller that takes
// the lock first runs the section in its own Unlock.
//
// # Allocation contract
//
// Zero-alloc. Unlock reads the mark of a section with one atomic load.
func (l *Lock) Unlock() {
	select {
	case <-l.slot:
	default:
		return
	}

	// Finally marks its section once it found the lock taken, and then tries
	// the lock again. The check after the free and that second try leave the
	// section to one of the two callers whatever their order.
	if l.final.Load() != nil && l.TryLock() {
		//dokimi:mutate-skip ror-true: Swap returns nil only when another caller ran the section between the Load and the TryLock, an order that no test can force
		if s := l.final.Swap(nil); s != nil {
			s.f()
			s.ran.Store(true)
		}

		l.Unlock()
	}
}

// Finally runs f under the lock once, and reports whether f ran before
// Finally returned. A Close runs its last section with it:
//
//	if !c.lock.Finally(c.release) {
//	    return errHung
//	}
//
// While the owner lives, Finally takes the lock, and waits while another
// caller has it. It runs f and frees the lock. Once the owner has ended,
// Finally does not wait. It runs f at once when the lock is free. Otherwise
// it marks f for the caller that has the lock and returns false. That caller
// runs f under the lock in its [Lock.Unlock].
//
// A Lock marks one section at a time, so a Finally that finds another
// section marked does not run f and returns false. f runs on the goroutine
// of the caller of Finally or of the caller of Unlock. A panic of f leaves
// the lock taken.
//
// # Allocation contract
//
// Allocates the mark of f once the owner has ended. f escapes to the heap.
func (l *Lock) Finally(f func()) bool {
	// Once the owner has ended, the select below picks the hand-off or a
	// free lock at random. This check picks the hand-off.
	//dokimi:mutate-skip sbr-delete: the hand-off and a free lock both run f under the lock and report true
	select {
	case <-l.done:
		return l.handOff(f)
	default:
	}

	select {
	case l.slot <- struct{}{}:
	case <-l.done:
		return l.handOff(f)
	}

	f()
	l.Unlock()

	return true
}

// handOff marks f for the caller that has the lock once the owner has
// ended, and reports whether f ran before handOff returned. It marks
// nothing while another section is marked.
//
// The caller that has the lock can free it between the end of the owner and
// the mark. handOff then takes the free lock and frees it, so its own Unlock
// runs the section.
func (l *Lock) handOff(f func()) bool {
	s := &section{f: f}
	if !l.final.CompareAndSwap(nil, s) {
		return false
	}

	if l.TryLock() {
		l.Unlock()
	}

	return s.ran.Load()
}
