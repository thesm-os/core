// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

func TestClassString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		class errs.Class
		want  string
	}{
		{errs.Unspecified, "Unspecified"},
		{errs.Transient, "Transient"},
		{errs.Conflict, "Conflict"},
		{errs.NotFound, "NotFound"},
		{errs.Invalid, "Invalid"},
		{errs.Unsupported, "Unsupported"},
		{errs.Denied, "Denied"},
		{errs.Integrity, "Integrity"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.class.String(), tc.want, "String must name the class")
		})
	}

	t.Run("an out-of-range value renders numerically", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Class(99).String(), "Class(99)",
			"an unrecognised class must stay distinguishable in a log line")
	})

	t.Run("every defined class has a distinct name", func(t *testing.T) {
		t.Parallel()
		seen := make(map[string]errs.Class, len(cases))
		for _, tc := range cases {
			prior, dup := seen[tc.class.String()]
			testkit.False(t, dup,
				"two classes share the name "+tc.class.String()+
					" (also "+prior.String()+")")
			seen[tc.class.String()] = tc.class
		}
	})
}

func TestClassOrdering(t *testing.T) {
	t.Parallel()

	// Unspecified must stay the zero value: an error nobody has
	// reasoned about has to be non-retryable by default, and that
	// property comes from its position, not from its name.
	t.Run("Unspecified is the zero value", func(t *testing.T) {
		t.Parallel()
		var zero errs.Class
		testkit.Equal(t, zero, errs.Unspecified, "the zero Class must be Unspecified")
	})
}

func BenchmarkClassString(b *testing.B) {
	b.ReportAllocs()
	var sink string
	for b.Loop() {
		sink = errs.Transient.String()
	}
	runtime.KeepAlive(sink)
}
