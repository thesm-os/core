// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/witness"
)

func TestProtocol(t *testing.T) {
	t.Parallel()

	t.Run("SizeError", func(t *testing.T) {
		t.Parallel()

		t.Run("Error", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the committed size after the prefix of the package", func(t *testing.T) {
				t.Parallel()
				err := &witness.SizeError{Size: math.MaxUint64}
				testkit.Equal(t, err.Error(), "witness: the witness committed the size 18446744073709551615 last",
					"Error must contain the committed size")
				testkit.True(t, strings.HasPrefix(err.Error(), errorPrefix), "Error must start with the prefix")
			})
		})

		t.Run("Class", func(t *testing.T) {
			t.Parallel()

			t.Run("returns Conflict", func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, (&witness.SizeError{Size: 5}).Class(), errs.Conflict,
					"a SizeError must classify as Conflict")
			})

			t.Run("returns Conflict through a wrap", func(t *testing.T) {
				t.Parallel()
				err := fmt.Errorf("context: %w", &witness.SizeError{Size: 5})
				testkit.Equal(t, errs.Classify(err), errs.Conflict, "a wrapped SizeError must classify as Conflict")
				got, ok := errors.AsType[*witness.SizeError](err)
				testkit.True(t, ok, "errors.AsType must find the SizeError")
				testkit.Equal(t, got.Size, uint64(5), "the SizeError must keep its size")
			})
		})
	})
}

func BenchmarkProtocol(b *testing.B) {
	b.Run("SizeError", func(b *testing.B) {
		err := &witness.SizeError{Size: 20852014}

		b.Run("Error", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			var got string
			for c.Loop() {
				got = err.Error()
			}

			testkit.True(b, strings.HasSuffix(got, " 20852014 last"), "the benchmark must measure the text")
		})

		b.Run("Class", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			var got errs.Class
			for c.Loop() {
				got = err.Class()
			}

			testkit.Equal(b, got, errs.Conflict, "the benchmark must measure the class")
		})
	})
}
