// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/epoch"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

// File names and the directory mode of the tests that classify the
// failures of real file-system calls.
const (
	missingFile = "missing"
	closedFile  = "closed"
	dirMode     = 0o750
)

// stubError is an error type that classifies itself, the shape a
// producer owning its error type should use in preference to
// [errs.WithClass].
type stubError struct{ class errs.Class }

func (stubError) Error() string       { return "stub" }
func (s stubError) Class() errs.Class { return s.class }

// nilUnwrapError unwraps to nil, which is legal for the Unwrap
// contract and drives classOf's loop to a nil error.
type nilUnwrapError struct{}

func (nilUnwrapError) Error() string { return "nil unwrap" }
func (nilUnwrapError) Unwrap() error { return nil }

// twoSentinelsError matches both fs.ErrNotExist and fs.ErrPermission,
// as an error of a layer that reports one failure under two names can.
type twoSentinelsError struct{}

func (twoSentinelsError) Error() string { return "errs_test: two sentinels" }

func (twoSentinelsError) Is(target error) bool {
	return target == fs.ErrNotExist || target == fs.ErrPermission
}

// classPair is the input of a property over two classes.
type classPair struct {
	A, B errs.Class
}

var errSentinel = errors.New("errs_test: sentinel")

// byRank lists the classes from the lowest rank to the highest, in the
// order that the documentation of [errs.Classify] gives for joins.
var byRank = []errs.Class{
	errs.Unspecified,
	errs.Transient,
	errs.Conflict,
	errs.NotFound,
	errs.Unsupported,
	errs.Invalid,
	errs.Denied,
	errs.Integrity,
}

func TestClassify(t *testing.T) {
	t.Parallel()

	t.Run("returns Unspecified for nil", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, errs.Classify(nil), errs.Unspecified, "Classify(nil) must be Unspecified")
	})

	t.Run("returns Unspecified for an unclassified error", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, errs.Classify(errSentinel), errs.Unspecified,
			"an error nobody classified must be Unspecified")
	})

	t.Run("returns the class of a Classifier", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t, func(c errs.Class) errs.Class { return errs.Classify(stubError{class: c}) },
			func(c errs.Class) errs.Class { return c },
			"Classify must return the Classifier's class", prop.Using(defined))
	})

	t.Run("returns the class of each recognised core sentinel", func(t *testing.T) {
		t.Parallel()

		// The recognised set, asserted one by one: these are plain
		// sentinels returned by adapters that may never import errs,
		// so recognition here is the only route to their class.
		cases := map[string]struct {
			err  error
			want errs.Class
		}{
			"version mismatch":        {version.ErrMismatch, errs.Conflict},
			"version exists":          {version.ErrExists, errs.Conflict},
			"version outcome unknown": {version.ErrOutcomeUnknown, errs.Transient},
			"epoch fenced":            {epoch.ErrFenced, errs.Conflict},
			"epoch size":              {epoch.ErrSize, errs.Invalid},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, errs.Classify(tc.err), tc.want,
					"a bare core sentinel must classify")
				assert.Equal(t, errs.Classify(fmt.Errorf("adapter: %w", tc.err)), tc.want,
					"a wrapped core sentinel must classify through the tree")
			})
		}
	})

	t.Run("prefers a Classifier to a recognised core sentinel", func(t *testing.T) {
		t.Parallel()

		// A producer that classified its own error has reasoned about
		// it; recognition is the fallback, not the override.
		tagged := errs.WithClass(epoch.ErrFenced, errs.Denied)
		assert.Equal(t, errs.Classify(tagged), errs.Denied,
			"WithClass must win over sentinel recognition")
	})

	t.Run("finds a Classifier beneath a wrap", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("outer: %w", stubError{class: errs.Integrity})
		assert.Equal(t, errs.Classify(wrapped), errs.Integrity,
			"Classify must walk the error tree")
	})

	stdlib := []struct {
		name string
		err  error
		want errs.Class
	}{
		{"fs.ErrNotExist is NotFound", fs.ErrNotExist, errs.NotFound},
		{"fs.ErrExist is Conflict", fs.ErrExist, errs.Conflict},
		{"fs.ErrPermission is Denied", fs.ErrPermission, errs.Denied},
		{"fs.ErrInvalid is Invalid", fs.ErrInvalid, errs.Invalid},
		{"fs.ErrClosed is Invalid", fs.ErrClosed, errs.Invalid},
		{"errors.ErrUnsupported is Unsupported", errors.ErrUnsupported, errs.Unsupported},
		{"context.Canceled is Unspecified", context.Canceled, errs.Unspecified},
		{"context.DeadlineExceeded is Unspecified", context.DeadlineExceeded, errs.Unspecified},
		{"EACCES is Denied", syscall.EACCES, errs.Denied},
		{"EPERM is Denied", syscall.EPERM, errs.Denied},
		{"EEXIST is Conflict", syscall.EEXIST, errs.Conflict},
		{"ENOTEMPTY is Conflict", syscall.ENOTEMPTY, errs.Conflict},
		{"ENOENT is NotFound", syscall.ENOENT, errs.NotFound},
	}
	for _, tc := range stdlib {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, errs.Classify(tc.err), tc.want,
				"Classify must recognise the standard library sentinel")
			assert.Equal(t, errs.Classify(fmt.Errorf("wrapped: %w", tc.err)), tc.want,
				"recognition must survive wrapping")
		})
	}

	t.Run("returns Conflict for os.Mkdir of a directory that exists", func(t *testing.T) {
		t.Parallel()
		err := os.Mkdir(t.TempDir(), dirMode)
		assert.HasError(t, err, "the directory must exist already")
		assert.Equal(t, errs.Classify(err), errs.Conflict,
			"a create-only call that finds its target must classify as Conflict")
	})

	t.Run("returns NotFound for os.Open of a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := os.Open(filepath.Join(t.TempDir(), missingFile))
		assert.HasError(t, err, "the file must not exist")
		assert.Equal(t, errs.Classify(err), errs.NotFound, "a missing file must classify as NotFound")
	})

	t.Run("returns Invalid for a second Close of a file", func(t *testing.T) {
		t.Parallel()
		f, err := os.Create(filepath.Join(t.TempDir(), closedFile))
		assert.NoError(t, err, "os.Create must succeed")
		assert.FailsAfterClose(t, f.Close, f.Close, fs.ErrClosed, "a second Close must report a closed file")
		assert.Equal(t, errs.Classify(f.Close()), errs.Invalid, "a use after Close must classify as Invalid")
	})

	t.Run("returns the class of higher rank for an error that matches two sentinels", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, errs.Classify(twoSentinelsError{}), errs.Denied,
			"fs.ErrPermission must outrank fs.ErrNotExist")
	})

	// Classify walks a tree, not just a chain: errors.Join produces
	// an error whose Unwrap returns []error, and a Classifier in any
	// branch must be found.
	t.Run("finds a Classifier in the first join branch", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Denied}, errSentinel)
		assert.Equal(t, errs.Classify(joined), errs.Denied,
			"Classify must search join branches")
	})

	t.Run("finds a Classifier in a later join branch", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, stubError{class: errs.Integrity})
		assert.Equal(t, errs.Classify(joined), errs.Integrity,
			"Classify must search past an unclassified branch")
	})

	t.Run("finds a Classifier nested inside a join branch", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(
			errSentinel,
			fmt.Errorf("wrapped: %w", stubError{class: errs.Conflict}),
		)
		assert.Equal(t, errs.Classify(joined), errs.Conflict,
			"Classify must recurse into join branches")
	})

	t.Run("returns Unspecified for a join without a Classifier", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, errors.New("errs_test: other"))
		assert.Equal(t, errs.Classify(joined), errs.Unspecified,
			"a tree without a Classifier must be Unspecified")
	})

	t.Run("returns the class of higher rank for a join of any two classes", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(p classPair) errs.Class {
				return errs.Classify(errors.Join(stubError{class: p.A}, stubError{class: p.B}))
			},
			func(p classPair) errs.Class {
				return byRank[max(slices.Index(byRank, p.A), slices.Index(byRank, p.B))]
			},
			"a join of two classes must take the class of higher rank", prop.Using(defined))
	})

	t.Run("returns a class of a join that does not depend on the order of its branches", func(t *testing.T) {
		t.Parallel()
		prop.Commutative(t, func(a, b errs.Class) errs.Class {
			return errs.Classify(errors.Join(stubError{class: a}, stubError{class: b}))
		}, "the class of a join must not depend on the order of its branches", prop.Using(defined))
	})

	t.Run("returns the class of a recognised sentinel in a join branch", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, fmt.Errorf("lookup: %w", fs.ErrNotExist))
		assert.Equal(t, errs.Classify(joined), errs.NotFound,
			"a branch must classify as it would alone, recognition included")
	})

	t.Run("returns the class of higher rank for a join inside a join", func(t *testing.T) {
		t.Parallel()
		inner := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Denied})
		joined := errors.Join(inner, stubError{class: errs.Conflict})
		assert.Equal(t, errs.Classify(joined), errs.Denied, "a nested join must classify as its branches rank")
	})

	t.Run("returns the class of higher rank for a join beneath a wrap", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Integrity})
		assert.Equal(t, errs.Classify(fmt.Errorf("op: %w", joined)), errs.Integrity,
			"Classify must find a join through a wrap")
	})

	t.Run("returns the class of a Classifier above a join", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Integrity})
		assert.Equal(t, errs.Classify(errs.WithClass(joined, errs.Transient)), errs.Transient,
			"a layer that tags a join must reclassify it")
	})

	t.Run("ignores a class outside the eight in a join", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(p classPair) errs.Class {
				return errs.Classify(errors.Join(stubError{class: p.A}, stubError{class: p.B}))
			},
			func(p classPair) errs.Class { return p.B },
			"a value past Integrity must rank with Unspecified",
			prop.Using(prop.Composite(func(c *prop.Case) classPair {
				return classPair{A: c.Draw(undefined, "outside"), B: c.Draw(defined, "class")}
			})))
	})

	t.Run("returns Unspecified for an error that unwraps to nil", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, errs.Classify(nilUnwrapError{}), errs.Unspecified,
			"a chain ending in nil must be Unspecified")
	})

	t.Run("prefers a Classifier to a recognised standard library sentinel", func(t *testing.T) {
		t.Parallel()
		// The producer classified this deliberately; fs.ErrNotExist
		// is incidental to how it was implemented.
		err := errs.WithClass(fmt.Errorf("lookup: %w", fs.ErrNotExist), errs.Denied)
		assert.Equal(t, errs.Classify(err), errs.Denied,
			"an explicit classification must beat an inferred one")
	})
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	t.Run("reports true for Transient alone", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t, func(c errs.Class) bool { return errs.Retryable(stubError{class: c}) },
			func(c errs.Class) bool { return c == errs.Transient },
			"Retryable must report true for Transient and false for every other class",
			prop.Using(defined))
	})

	t.Run("reports false for nil", func(t *testing.T) {
		t.Parallel()
		assert.False(t, errs.Retryable(nil), "Retryable(nil) must be false")
	})

	t.Run("reports true for a join whose classified branches are all Transient", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, errSentinel, stubError{class: errs.Transient})
		assert.True(t, errs.Retryable(joined), "an unclassified branch must not stop the retry")
	})

	t.Run("reports false for a join with an Integrity branch after a Transient one", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Integrity})
		assert.False(t, errs.Retryable(joined), "one Integrity failure must stop the retry")
	})
}

func TestWithClass(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, errs.WithClass(nil, errs.Transient),
			"tagging the absence of an error must not produce one")
	})

	t.Run("sets the class of the result", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t, func(c errs.Class) errs.Class { return errs.Classify(errs.WithClass(errSentinel, c)) },
			func(c errs.Class) errs.Class { return c },
			"WithClass must set the class", prop.Using(defined))
	})

	t.Run("keeps the wrapped error visible to errors.Is", func(t *testing.T) {
		t.Parallel()
		tagged := errs.WithClass(errSentinel, errs.Conflict)
		assert.ErrorIs(t, tagged, errSentinel,
			"tagging must not break matching on the underlying sentinel")
	})

	t.Run("keeps the message of the error", func(t *testing.T) {
		t.Parallel()
		tagged := errs.WithClass(errSentinel, errs.Denied)
		assert.Equal(t, tagged.Error(), errSentinel.Error(),
			"the class must not leak into the error text")
	})

	t.Run("keeps the class beneath a further wrap", func(t *testing.T) {
		t.Parallel()
		outer := fmt.Errorf("outer: %w", errs.WithClass(errSentinel, errs.Integrity))
		assert.Equal(t, errs.Classify(outer), errs.Integrity,
			"a tag must remain visible under an outer wrap")
		assert.ErrorIs(t, outer, errSentinel, "the chain must stay intact")
	})

	t.Run("returns the outermost class for an error tagged twice", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(p classPair) errs.Class {
				return errs.Classify(errs.WithClass(errs.WithClass(errSentinel, p.A), p.B))
			},
			func(p classPair) errs.Class { return p.B },
			"Classify must return the outermost Classifier on the chain", prop.Using(defined))
	})
}

// TestClassifyAllocs checks the allocation contracts of Classify,
// Retryable and WithClass. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestClassifyAllocs(t *testing.T) {
	t.Run("Classify", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", stubError{class: errs.Transient})

		var got errs.Class
		expect.MaxAllocs(t, func() { got = errs.Classify(err) }, 0, "Classify must not allocate")
		assert.Equal(t, got, errs.Transient, "the test must measure a classified error")
	})

	t.Run("Classify of a join", func(t *testing.T) {
		err := errors.Join(
			fmt.Errorf("item 0: %w", stubError{class: errs.Transient}),
			fmt.Errorf("item 1: %w", fs.ErrNotExist),
			fmt.Errorf("item 2: %w", stubError{class: errs.Integrity}),
		)

		var got errs.Class
		expect.MaxAllocs(t, func() { got = errs.Classify(err) }, 0, "Classify of a join must not allocate")
		assert.Equal(t, got, errs.Integrity, "the test must measure the join's rank")
	})

	t.Run("Retryable", func(t *testing.T) {
		err := stubError{class: errs.Transient}

		var got bool
		expect.MaxAllocs(t, func() { got = errs.Retryable(err) }, 0, "Retryable must not allocate")
		assert.True(t, got, "the test must measure a retryable error")
	})

	t.Run("WithClass", func(t *testing.T) {
		var got error
		expect.MaxAllocs(t, func() { got = errs.WithClass(errSentinel, errs.Transient) }, 1,
			"WithClass must allocate only its wrapper")
		assert.Equal(t, errs.Classify(got), errs.Transient, "the test must measure a tagged error")
	})
}

func BenchmarkClassify(b *testing.B) {
	b.Run("Classify", func(b *testing.B) {
		err := fmt.Errorf("outer: %w", stubError{class: errs.Transient})

		var got errs.Class

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = errs.Classify(err)
		}

		assert.Equal(b, got, errs.Transient, "the benchmark must measure a classified error")
	})

	b.Run("Classify of a join", func(b *testing.B) {
		err := errors.Join(
			fmt.Errorf("item 0: %w", stubError{class: errs.Transient}),
			fmt.Errorf("item 1: %w", fs.ErrNotExist),
			fmt.Errorf("item 2: %w", stubError{class: errs.Integrity}),
		)

		var got errs.Class

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = errs.Classify(err)
		}

		assert.Equal(b, got, errs.Integrity, "the benchmark must measure the join's rank")
	})

	b.Run("Retryable", func(b *testing.B) {
		err := stubError{class: errs.Transient}

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = errs.Retryable(err)
		}

		assert.True(b, got, "the benchmark must measure a retryable error")
	})

	b.Run("WithClass", func(b *testing.B) {
		var got error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = errs.WithClass(errSentinel, errs.Transient)
		}

		assert.Equal(b, errs.Classify(got), errs.Transient, "the benchmark must measure a tagged error")
	})
}
