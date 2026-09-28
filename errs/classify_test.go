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
	"runtime"
	"syscall"
	"testing"

	"go.thesmos.sh/testkit"

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
// contract and drives classOf's loop to its exit rather than to one
// of the switch's returns.
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

	t.Run("nil is Unspecified", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Classify(nil), errs.Unspecified, "Classify(nil) must be Unspecified")
	})

	t.Run("an unclassified error is Unspecified", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Classify(errSentinel), errs.Unspecified,
			"an error nobody classified must be Unspecified")
	})

	t.Run("a Classifier reports its own class", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Classify(stubError{class: errs.Denied}), errs.Denied,
			"Classify must return the Classifier's class")
	})

	t.Run("the core sentinels classify without wrapping", func(t *testing.T) {
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
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, errs.Classify(tc.err), tc.want,
					"a bare core sentinel must classify")
				testkit.Equal(t, errs.Classify(fmt.Errorf("adapter: %w", tc.err)), tc.want,
					"a wrapped core sentinel must classify through the tree")
			})
		}
	})

	t.Run("an explicit Classifier beats a recognised sentinel", func(t *testing.T) {
		t.Parallel()

		// A producer that classified its own error has reasoned about
		// it; recognition is the fallback, not the override.
		tagged := errs.WithClass(epoch.ErrFenced, errs.Denied)
		testkit.Equal(t, errs.Classify(tagged), errs.Denied,
			"WithClass must win over sentinel recognition")
	})

	t.Run("a wrapped Classifier is found through the tree", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("outer: %w", stubError{class: errs.Integrity})
		testkit.Equal(t, errs.Classify(wrapped), errs.Integrity,
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
			testkit.Equal(t, errs.Classify(tc.err), tc.want,
				"Classify must recognise the standard library sentinel")
			testkit.Equal(t, errs.Classify(fmt.Errorf("wrapped: %w", tc.err)), tc.want,
				"recognition must survive wrapping")
		})
	}

	t.Run("returns Conflict for os.Mkdir of a directory that exists", func(t *testing.T) {
		t.Parallel()
		err := os.Mkdir(t.TempDir(), dirMode)
		testkit.Error(t, err, "the directory must exist already")
		testkit.Equal(t, errs.Classify(err), errs.Conflict,
			"a create-only call that finds its target must classify as Conflict")
	})

	t.Run("returns NotFound for os.Open of a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := os.Open(filepath.Join(t.TempDir(), missingFile))
		testkit.Error(t, err, "the file must not exist")
		testkit.Equal(t, errs.Classify(err), errs.NotFound, "a missing file must classify as NotFound")
	})

	t.Run("returns Invalid for a second Close of a file", func(t *testing.T) {
		t.Parallel()
		f, err := os.Create(filepath.Join(t.TempDir(), closedFile))
		testkit.NoError(t, err, "os.Create must succeed")
		testkit.NoError(t, f.Close(), "the first Close must succeed")

		err = f.Close()
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "a use after Close must classify as Invalid")
	})

	t.Run("returns the class of higher rank for an error that matches two sentinels", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Classify(twoSentinelsError{}), errs.Denied,
			"fs.ErrPermission must outrank fs.ErrNotExist")
	})

	// Classify walks a tree, not just a chain: errors.Join produces
	// an error whose Unwrap returns []error, and a Classifier in any
	// branch must be found.
	t.Run("a Classifier in the first join branch is found", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Denied}, errSentinel)
		testkit.Equal(t, errs.Classify(joined), errs.Denied,
			"Classify must search join branches")
	})

	t.Run("a Classifier in a later join branch is found", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, stubError{class: errs.Integrity})
		testkit.Equal(t, errs.Classify(joined), errs.Integrity,
			"Classify must search past an unclassified branch")
	})

	t.Run("a Classifier nested inside a join branch is found", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(
			errSentinel,
			fmt.Errorf("wrapped: %w", stubError{class: errs.Conflict}),
		)
		testkit.Equal(t, errs.Classify(joined), errs.Conflict,
			"Classify must recurse into join branches")
	})

	t.Run("a join with no Classifier is Unspecified", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(errSentinel, errors.New("errs_test: other"))
		testkit.Equal(t, errs.Classify(joined), errs.Unspecified,
			"a tree without a Classifier must be Unspecified")
	})

	t.Run("returns the class of higher rank for a join of any two classes", func(t *testing.T) {
		t.Parallel()

		// Every ordered pair, so both branch orders of each pair are
		// covered and the result cannot depend on the order.
		for i, a := range byRank {
			for j, b := range byRank {
				joined := errors.Join(stubError{class: a}, stubError{class: b})
				testkit.Equal(t, errs.Classify(joined), byRank[max(i, j)],
					"a join of "+a.String()+" and "+b.String()+" must take the class of higher rank")
			}
		}
	})

	t.Run("returns the class of a recognised sentinel in a join branch", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, fmt.Errorf("lookup: %w", fs.ErrNotExist))
		testkit.Equal(t, errs.Classify(joined), errs.NotFound,
			"a branch must classify as it would alone, recognition included")
	})

	t.Run("returns the class of higher rank for a join inside a join", func(t *testing.T) {
		t.Parallel()
		inner := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Denied})
		joined := errors.Join(inner, stubError{class: errs.Conflict})
		testkit.Equal(t, errs.Classify(joined), errs.Denied, "a nested join must classify as its branches rank")
	})

	t.Run("returns the class of higher rank for a join beneath a wrap", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Integrity})
		testkit.Equal(t, errs.Classify(fmt.Errorf("op: %w", joined)), errs.Integrity,
			"Classify must find a join through a wrap")
	})

	t.Run("returns the class of a Classifier above a join", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Integrity})
		testkit.Equal(t, errs.Classify(errs.WithClass(joined, errs.Transient)), errs.Transient,
			"a layer that tags a join must reclassify it")
	})

	t.Run("ignores a class outside the eight in a join", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Class(99)}, stubError{class: errs.Transient})
		testkit.Equal(t, errs.Classify(joined), errs.Transient,
			"an out-of-range class must rank with Unspecified")
	})

	t.Run("returns Unspecified for a join whose only class is outside the eight", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Class(99)})
		testkit.Equal(t, errs.Classify(joined), errs.Unspecified,
			"a join without a class of the eight must be Unspecified")
	})

	t.Run("an error unwrapping to nil is Unspecified", func(t *testing.T) {
		t.Parallel()
		// Unwrap returning nil ends the walk without a Classifier
		// and without hitting the switch's default.
		testkit.Equal(t, errs.Classify(nilUnwrapError{}), errs.Unspecified,
			"a chain ending in nil must be Unspecified")
	})

	t.Run("an explicit Classifier wins over a recognised sentinel", func(t *testing.T) {
		t.Parallel()
		// The producer classified this deliberately; fs.ErrNotExist
		// is incidental to how it was implemented.
		err := errs.WithClass(fmt.Errorf("lookup: %w", fs.ErrNotExist), errs.Denied)
		testkit.Equal(t, errs.Classify(err), errs.Denied,
			"an explicit classification must beat an inferred one")
	})
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	t.Run("Transient is retryable", func(t *testing.T) {
		t.Parallel()
		testkit.True(t, errs.Retryable(stubError{class: errs.Transient}),
			"Transient must be retryable")
	})

	nonRetryable := []errs.Class{
		errs.Unspecified,
		errs.Conflict,
		errs.NotFound,
		errs.Invalid,
		errs.Unsupported,
		errs.Denied,
		errs.Integrity,
	}
	for _, class := range nonRetryable {
		t.Run(class.String()+" is not retryable", func(t *testing.T) {
			t.Parallel()
			testkit.False(t, errs.Retryable(stubError{class: class}),
				class.String()+" must not be retryable")
		})
	}

	t.Run("nil is not retryable", func(t *testing.T) {
		t.Parallel()
		testkit.False(t, errs.Retryable(nil), "Retryable(nil) must be false")
	})

	t.Run("reports true for a join whose classified branches are all Transient", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, errSentinel, stubError{class: errs.Transient})
		testkit.True(t, errs.Retryable(joined), "an unclassified branch must not stop the retry")
	})

	t.Run("reports false for a join with an Integrity branch after a Transient one", func(t *testing.T) {
		t.Parallel()
		joined := errors.Join(stubError{class: errs.Transient}, stubError{class: errs.Integrity})
		testkit.False(t, errs.Retryable(joined), "one Integrity failure must stop the retry")
	})
}

func TestWithClass(t *testing.T) {
	t.Parallel()

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.WithClass(nil, errs.Transient), nil,
			"tagging the absence of an error must not produce one")
	})

	t.Run("the result carries the class", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Classify(errs.WithClass(errSentinel, errs.Conflict)), errs.Conflict,
			"WithClass must set the class")
	})

	t.Run("errors.Is still reaches the wrapped error", func(t *testing.T) {
		t.Parallel()
		tagged := errs.WithClass(errSentinel, errs.Conflict)
		testkit.ErrorIs(t, tagged, errSentinel,
			"tagging must not break matching on the underlying sentinel")
	})

	t.Run("the message is unchanged", func(t *testing.T) {
		t.Parallel()
		tagged := errs.WithClass(errSentinel, errs.Denied)
		testkit.Equal(t, tagged.Error(), errSentinel.Error(),
			"the class must not leak into the error text")
	})

	t.Run("tagging survives further wrapping", func(t *testing.T) {
		t.Parallel()
		outer := fmt.Errorf("outer: %w", errs.WithClass(errSentinel, errs.Integrity))
		testkit.Equal(t, errs.Classify(outer), errs.Integrity,
			"a tag must remain visible under an outer wrap")
		testkit.ErrorIs(t, outer, errSentinel, "the chain must stay intact")
	})

	t.Run("the outermost tag wins when tagged twice", func(t *testing.T) {
		t.Parallel()
		inner := errs.WithClass(errSentinel, errs.Transient)
		outer := errs.WithClass(inner, errs.Denied)
		testkit.Equal(t, errs.Classify(outer), errs.Denied,
			"Classify must return the outermost Classifier on the chain")
	})
}

func BenchmarkClassify(b *testing.B) {
	err := fmt.Errorf("outer: %w", stubError{class: errs.Transient})
	b.ReportAllocs()
	var sink errs.Class
	for b.Loop() {
		sink = errs.Classify(err)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkClassifyJoin(b *testing.B) {
	err := errors.Join(
		fmt.Errorf("item 0: %w", stubError{class: errs.Transient}),
		fmt.Errorf("item 1: %w", fs.ErrNotExist),
		fmt.Errorf("item 2: %w", stubError{class: errs.Integrity}),
	)
	b.ReportAllocs()
	var sink errs.Class
	for b.Loop() {
		sink = errs.Classify(err)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkRetryable(b *testing.B) {
	err := stubError{class: errs.Transient}
	b.ReportAllocs()
	var sink bool
	for b.Loop() {
		sink = errs.Retryable(err)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkWithClass(b *testing.B) {
	b.ReportAllocs()
	var sink error
	for b.Loop() {
		sink = errs.WithClass(errSentinel, errs.Transient)
	}
	runtime.KeepAlive(sink)
}
