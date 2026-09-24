// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/rand"
)

// Every calls fn, waits period plus a random delay below jitter, and
// calls fn again, until ctx ends or fn returns an error. The first call
// runs at once.
//
// The wait starts when fn returns, so calls never overlap, and a slow
// call does not bring the next one forward. The jitter spreads the
// calls of instances that started together. Every reads it from r,
// which may be nil when jitter is zero. Every reads time only through
// c, so a test can run the loop against a fake clock.
//
// Every returns nil when ctx ends, which is how a caller stops the
// loop, and returns nil without calling fn when ctx has already ended.
// When fn returns an error, Every returns it unchanged. The exception
// is an error that is ctx's error or cause after ctx has ended: a call
// cut short by the end of ctx also stops the loop with nil, so the
// result of a shutdown does not depend on when it arrives. A caller
// that wants the loop to continue after a failure handles the failure
// inside fn.
//
// Returns [ErrPeriod] for a period that is not positive, a negative
// jitter, or a positive jitter with a nil r.
//
// # Panics
//
// fn runs on the caller's goroutine, so a panic in fn continues to the
// caller, as a panic in the body passed to [Run] does. Inside a task
// started by [Group.Go], the panic crashes the process as a panic in
// any task does.
//
// # Allocation contract
//
// Each wait allocates one [clock.Timer] from c.
func Every(
	ctx context.Context,
	c clock.Clock,
	r rand.Rand,
	period, jitter time.Duration,
	fn func(ctx context.Context) error,
) error {
	if period <= 0 || jitter < 0 {
		return ErrPeriod
	}

	if jitter > 0 && r == nil {
		return fmt.Errorf("%w: a positive jitter needs a random source", ErrPeriod)
	}

	for ctx.Err() == nil {
		if err := fn(ctx); err != nil {
			if stopped(ctx, err) {
				return nil
			}

			return err
		}

		wait := period
		if jitter > 0 {
			// jitter is positive, so the conversion keeps its value, and
			// the draw is below jitter, so it fits a Duration.
			wait += time.Duration(rand.Uint64N(r, uint64(jitter))) //nolint:gosec // see comment above
		}

		// Wait fails only when ctx has ended, and the loop condition then
		// stops the loop.
		_ = clock.Wait(ctx, c, wait)
	}

	return nil
}

// stopped reports whether err is the end of ctx: ctx has ended, and err
// is its error or its cause.
func stopped(ctx context.Context, err error) bool {
	return ctx.Err() != nil && (errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx)))
}
