// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"sync/atomic"
)

// The states of a [Lock]. The zero state belongs to the zero Lock, which is
// never free.
const (
	// lockFree is the state of a free lock.
	lockFree int32 = iota + 1

	// lockTaken is the state of a lock that a caller has while no caller
	// waits.
	lockTaken

	// lockContended is the state of a lock that a caller has while callers
	// can wait. The Unlock of a contended lock wakes one waiter.
	lockContended
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
// The Lock keeps its state in one atomic word. A caller takes a free lock
// with one compare-and-swap, and the Unlock of a lock without waiters frees
// it with another. A caller that waits receives a wake on a channel of the
// Lock, so a [testing/synctest] bubble counts the wait as durable when the
// bubble created the Lock and the contexts of the wait. The Lock does not
// order its waiters. A caller that finds the lock free takes it ahead of a
// caller that waits, as with a [sync.Mutex].
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

	// wake receives a value when the Unlock of a contended lock frees it. It
	// has room for one value, so the channel keeps a wake until a waiter
	// receives it. A waiter that receives a wake tries the lock again.
	wake chan struct{}

	// done is the Done channel of the owner.
	done <-chan struct{}

	// final is nil, or the section that a Finally marked for the caller that
	// has the lock. Finally marks a section once the owner has ended. A
	// caller clears the mark while it has the lock.
	final atomic.Pointer[section]

	// state is lockFree, lockTaken or lockContended, and zero for the zero
	// Lock.
	state atomic.Int32
}

// section is the last section of a Close that a Finally marked for another
// caller. That caller has a [Lock] or is the last member of a [Group]. The
// caller that runs f sets ran after f returns.
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
	l := &Lock{owner: owner, wake: make(chan struct{}, 1), done: owner.Done()}
	l.state.Store(lockFree)

	return l
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

	if l.state.CompareAndSwap(lockFree, lockTaken) {
		return nil
	}

	return l.acquire(ctx)
}

// TryLock takes the lock when it is free, and reports whether it took it.
// TryLock does not wait, so the end of the owner does not change its result.
//
// # Allocation contract
//
// Zero-alloc.
func (l *Lock) TryLock() bool {
	return l.state.CompareAndSwap(lockFree, lockTaken)
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
	for {
		s := l.state.Load()
		if s != lockTaken && s != lockContended {
			return
		}

		if l.state.CompareAndSwap(s, lockFree) {
			if s == lockContended {
				select {
				case l.wake <- struct{}{}:
				default:
				}
			}

			break
		}
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
// Before the owner ends, Finally takes the lock, and waits while another
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
	// Once the owner has ended, the hand-off runs f under a free lock as the
	// compare-and-swap below does. This check picks the hand-off.
	//dokimi:mutate-skip sbr-delete: the hand-off and a free lock both run f under the lock and report true
	select {
	case <-l.done:
		return l.handOff(f)
	default:
	}

	// The context of the wait never ends, so acquire returns an error only
	// at the end of the owner.
	if !l.state.CompareAndSwap(lockFree, lockTaken) && l.acquire(context.Background()) != nil {
		return l.handOff(f)
	}

	f()
	l.Unlock()

	return true
}

// acquire takes a lock that the compare-and-swap of its caller found taken,
// and waits while another caller has it. Each pass swaps the state with
// lockContended, so the Unlock of the caller that has the lock wakes a
// waiter. A swap that finds the lock free takes it contended, because other
// callers can still wait for it. The zero Lock has no channel of wakes, so
// acquire leaves its state alone and waits for the end of ctx.
//
// Error modes, none of which takes the lock: the [context.Cause] of ctx or of
// the owner when either ends before acquire takes the lock.
func (l *Lock) acquire(ctx context.Context) error {
	for {
		if l.wake != nil && l.state.Swap(lockContended) == lockFree {
			return nil
		}

		select {
		case <-l.wake:
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-l.done:
			return context.Cause(l.owner) //nolint:contextcheck // Cause only reads the error of the owner
		}
	}
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
