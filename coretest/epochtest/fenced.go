// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package epochtest holds the conformance suite for the fencing laws
// of [go.thesmos.sh/core/epoch]: admit-equal, zero-bypass, and
// rejection without mutation, asserted against a consumer's entire
// fenced write surface. Hand-rolled; nothing here is generated.
package epochtest

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/epoch"
	"go.thesmos.sh/core/errs"
)

// FencedScope is one isolated scope of a consumer's fenced writer,
// as constructed by [FencedSystem.NewScope].
//
// Open constructs a handle bound to fence epoch e against this
// scope. Supersede advances the scope's authority out-of-band, as an
// election would. Snapshot returns a fingerprint of the scope's
// observable state, compared with testkit.Equal to assert rejection
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
func AssertFencedWriter[H any](t *testing.T, sys FencedSystem[H]) {
	t.Helper()

	for name, write := range sys.Writes {
		t.Run(name, func(t *testing.T) {
			scope := sys.NewScope()

			t.Run("admitted at current epoch", func(t *testing.T) {
				h, err := scope.Open(2)
				testkit.NoError(t, err, "Open at the current epoch must succeed")
				testkit.NoError(t, write(h), "a write at the current epoch must be admitted")
			})

			t.Run("admitted at equal epoch after re-open", func(t *testing.T) {
				// Admit-equal: a second handle at the same epoch is
				// the same authority, and one tenure performs many
				// writes.
				h, err := scope.Open(2)
				testkit.NoError(t, err, "re-Open at the same epoch must succeed")
				testkit.NoError(t, write(h), "a write at an equal epoch must be admitted")
			})

			t.Run("rejected without mutation once superseded", func(t *testing.T) {
				stale, err := scope.Open(2)
				testkit.NoError(t, err, "Open must succeed before supersession")

				scope.Supersede(3)

				before := scope.Snapshot()

				werr := write(stale)

				testkit.ErrorIs(t, werr, epoch.ErrFenced,
					"a write through a superseded handle must return ErrFenced")
				testkit.Equal(t, errs.Classify(werr), errs.Conflict,
					"a fenced rejection must classify as Conflict")
				testkit.Equal(t, scope.Snapshot(), before,
					"a rejected write must mutate nothing")
			})

			t.Run("fresh handle at the new epoch succeeds", func(t *testing.T) {
				h, err := scope.Open(3)
				testkit.NoError(t, err, "Open at the superseding epoch must succeed")
				testkit.NoError(t, write(h),
					"the new authority's writes must be admitted")
			})

			t.Run("zero-epoch handle bypasses fencing", func(t *testing.T) {
				h, err := scope.Open(epoch.Zero)
				testkit.NoError(t, err, "Open at Zero must succeed")
				testkit.NoError(t, write(h),
					"an unfenced write must be admitted regardless of the watermark")

				// Not advancing is the other half of the bypass law:
				// the current authority must still be admitted after
				// an unfenced write, and the epoch below it must
				// still be fenced.
				cur, err := scope.Open(3)
				testkit.NoError(t, err, "Open at the current epoch must succeed")
				testkit.NoError(t, write(cur),
					"an unfenced write must not advance the watermark")

				stale, err := scope.Open(2)
				testkit.NoError(t, err, "Open below the watermark must construct")
				testkit.ErrorIs(t, write(stale), epoch.ErrFenced,
					"an unfenced write must not reset the watermark either")
			})
		})
	}
}
