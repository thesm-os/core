// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

// stubError is an error type that classifies itself, the shape a
// producer owning its error type should use in preference to
// [errs.WithClass].
type stubError struct{ class errs.Class }

func (stubError) Error() string       { return "stub" }
func (s stubError) Class() errs.Class { return s.class }

var errSentinel = errors.New("errs_test: sentinel")

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
		{"errors.ErrUnsupported is Unsupported", errors.ErrUnsupported, errs.Unsupported},
		{"context.Canceled is Unspecified", context.Canceled, errs.Unspecified},
		{"context.DeadlineExceeded is Unspecified", context.DeadlineExceeded, errs.Unspecified},
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
			"Classify must return the first Classifier in the tree")
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
