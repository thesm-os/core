// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package epochtest holds the conformance suite for the fencing laws
// of [go.thesmos.sh/core/epoch]: admit-equal, zero-bypass, and
// rejection without mutation, asserted against a consumer's entire
// fenced write surface. A writer that grants each scope to one writer
// at a time declares that it refuses epoch.Zero instead of the
// zero-bypass law. Hand-rolled; nothing here is generated.
package epochtest

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/epoch"
	"go.thesmos.sh/core/errs"
)

// FencedScope is one isolated scope of a consumer's fenced writer,
// as constructed by [FencedSystem.NewScope].
//
// Open constructs a handle bound to fence epoch e against this
// scope. Supersede advances the scope's authority out-of-band, as an
// election would. Snapshot returns a fingerprint of the scope's
// observable state, compared with assert.Equal to assert rejection
// without mutation — return whatever value captures every
// write-visible effect.
type FencedScope[H any] struct {
	Open      func(e epoch.Epoch) (H, error)
	Supersede func(e epoch.Epoch)
	Snapshot  func() any
}

// FencedSystem describes a consumer's fenced writer to
// [AssertFencedWriter].
//
// NewScope returns a fresh, independent scope; the suite constructs
// one per write case, so cases cannot observe each other's
// watermarks. Writes names every write operation the handle
// exposes. The suite's central law is that no write escapes
// validation, so an omitted write is an untested hole.
type FencedSystem[H any] struct {
	NewScope func() FencedScope[H]
	Writes   map[string]func(H) error

	// RefusesZero declares a writer that grants each scope to one writer
	// at a time, so a scope with an authority refuses a handle at
	// epoch.Zero.
	RefusesZero bool
}

// AssertFencedWriter drives every named write through a fenced
// handle's lifecycle on its own fresh scope and asserts the fence
// laws hold for each:
//
//   - a write through a handle whose epoch has been superseded
//     returns an error matching [epoch.ErrFenced], classifying as
//     [errs.Conflict], and leaves the scope unmutated;
//   - a write at an epoch equal to the current authority is
//     admitted;
//   - a handle opened at [epoch.Zero] neither validates nor
//     advances the watermark;
//   - after supersession, a fresh handle at the new epoch succeeds
//     where the superseded handle fails.
//
// A writer that declares [FencedSystem.RefusesZero] replaces the law of
// epoch.Zero: once the scope has an authority, Open at Zero or a write
// through its handle returns an error matching epoch.ErrFenced, which
// classifies as errs.Conflict, and leaves the scope unmutated.
func AssertFencedWriter[H any](t *testing.T, sys FencedSystem[H]) {
	t.Helper()

	for name, write := range sys.Writes {
		t.Run(name, func(t *testing.T) {
			scope := sys.NewScope()

			t.Run("admitted at current epoch", func(t *testing.T) {
				h, err := scope.Open(2)
				assert.NoError(t, err, "Open at the current epoch must succeed")
				assert.NoError(t, write(h), "a write at the current epoch must be admitted")
			})

			t.Run("admitted at equal epoch after re-open", func(t *testing.T) {
				// Admit-equal: a second handle at the same epoch is
				// the same authority, and one tenure performs many
				// writes.
				h, err := scope.Open(2)
				assert.NoError(t, err, "re-Open at the same epoch must succeed")
				assert.NoError(t, write(h), "a write at an equal epoch must be admitted")
			})

			t.Run("rejected without mutation once superseded", func(t *testing.T) {
				stale, err := scope.Open(2)
				assert.NoError(t, err, "Open must succeed before supersession")

				scope.Supersede(3)

				var werr error
				assert.Pure(t, scope.Snapshot, func() { werr = write(stale) },
					"a rejected write must mutate nothing")
				assert.ErrorIs(t, werr, epoch.ErrFenced,
					"a write through a superseded handle must return ErrFenced")
				assert.Equal(t, errs.Classify(werr), errs.Conflict,
					"a fenced rejection must classify as Conflict")
			})

			t.Run("fresh handle at the new epoch succeeds", func(t *testing.T) {
				h, err := scope.Open(3)
				assert.NoError(t, err, "Open at the superseding epoch must succeed")
				assert.NoError(t, write(h),
					"the new authority's writes must be admitted")
			})

			if sys.RefusesZero {
				t.Run("zero-epoch handle is refused once the scope has an authority", func(t *testing.T) {
					var werr error

					assert.Pure(t, scope.Snapshot, func() {
						h, err := scope.Open(epoch.Zero)
						if err != nil {
							werr = err

							return
						}

						werr = write(h)
					}, "a refused zero-epoch handle must mutate nothing")
					assert.ErrorIs(t, werr, epoch.ErrFenced,
						"Open at Zero or its write must return ErrFenced once the scope has an authority")
					assert.Equal(t, errs.Classify(werr), errs.Conflict, "the refusal must classify as Conflict")

					cur, err := scope.Open(3)
					assert.NoError(t, err, "Open at the current epoch must succeed")
					assert.NoError(t, write(cur), "the refusal must leave the current authority admitted")
				})

				return
			}

			t.Run("zero-epoch handle bypasses fencing", func(t *testing.T) {
				h, err := scope.Open(epoch.Zero)
				assert.NoError(t, err, "Open at Zero must succeed")
				assert.NoError(t, write(h),
					"an unfenced write must be admitted regardless of the watermark")

				// Not advancing is the other half of the bypass law:
				// the current authority must still be admitted after
				// an unfenced write, and the epoch below it must
				// still be fenced.
				cur, err := scope.Open(3)
				assert.NoError(t, err, "Open at the current epoch must succeed")
				assert.NoError(t, write(cur),
					"an unfenced write must not advance the watermark")

				stale, err := scope.Open(2)
				assert.NoError(t, err, "Open below the watermark must construct")
				assert.ErrorIs(t, write(stale), epoch.ErrFenced,
					"an unfenced write must not reset the watermark either")
			})
		})
	}
}
