// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import "context"

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
type Lock struct {
	owner context.Context //nolint:containedctx // the owner ends every wait of the lock, and Lock returns its cause

	// slot contains a token while a caller has the lock. Its capacity is
	// one, so Lock takes the lock with a send and Unlock frees it with a
	// receive.
	slot chan struct{}

	// done is the Done channel of the owner.
	done <-chan struct{}
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
// # Allocation contract
//
// Zero-alloc.
func (l *Lock) Unlock() {
	select {
	case <-l.slot:
	default:
	}
}
