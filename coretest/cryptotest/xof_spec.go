// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// XOFContractAssertions returns the assertions every [crypto.XOF]
// implementation must satisfy: determinism, output that extends
// rather than repeats, and the absorb-then-squeeze state machine —
// including that a write after a read is an ERROR and not a panic,
// which is the property that separates a conforming implementation
// from a bare standard-library value.
func XOFContractAssertions() []XOFOption {
	return []XOFOption{
		// --- determinism and extension ---

		XOFCustom("output is deterministic for one input", func(t *testing.T, x crypto.XOF) {
			testkit.Equal(t, squeeze(t, x, []byte("absorb me"), 64),
				squeeze(t, x, []byte("absorb me"), 64),
				"one input must always produce the same output stream")
		}),

		XOFCustom("distinct inputs produce distinct output", func(t *testing.T, x crypto.XOF) {
			testkit.NotEqual(t, squeeze(t, x, []byte("alpha"), 32), squeeze(t, x, []byte("beta"), 32),
				"distinct inputs must not squeeze to the same bytes")
		}),

		XOFCustom("a longer read extends the same stream", func(t *testing.T, x crypto.XOF) {
			// The defining property of an XOF: reading n then m bytes
			// gives the same prefix as reading n+m at once. An
			// implementation that restarts the stream per Read would
			// fail here and nowhere else.
			short := squeeze(t, x, []byte("absorb me"), 32)
			long := squeeze(t, x, []byte("absorb me"), 96)
			testkit.Equal(t, long[:32], short, "a longer read must share the shorter read's prefix")
		}),

		XOFCustom("successive reads continue one stream", func(t *testing.T, x crypto.XOF) {
			s := x.NewXOFStream()
			mustWrite(t, s, []byte("absorb me"))

			first := mustRead(t, s, 32)
			second := mustRead(t, s, 32)

			whole := squeeze(t, x, []byte("absorb me"), 64)
			testkit.Equal(t, append(first, second...), whole,
				"two reads must equal one read of the combined length")
		}),

		XOFCustom("absorbing in pieces equals absorbing at once", func(t *testing.T, x crypto.XOF) {
			s := x.NewXOFStream()
			mustWrite(t, s, []byte("absorb"))
			mustWrite(t, s, []byte(" me"))

			testkit.Equal(t, mustRead(t, s, 64), squeeze(t, x, []byte("absorb me"), 64),
				"a split write must absorb identically to a single write")
		}),

		XOFCustom("an empty absorb is well defined", func(t *testing.T, x crypto.XOF) {
			testkit.Equal(t, len(squeeze(t, x, nil, 32)), 32,
				"an XOF over no input must still squeeze")
		}),

		// --- the state machine ---

		XOFCustom("a write after a read is an error, not a panic", func(t *testing.T, x crypto.XOF) {
			// The load-bearing assertion. The standard library's SHAKE
			// panics here; a conforming implementation converts that
			// into an error at the boundary, because a caller can
			// reach this state by threading a stream through code
			// that does not know its phase.
			s := x.NewXOFStream()
			mustWrite(t, s, []byte("absorb me"))
			mustRead(t, s, 8)

			_, err := s.Write([]byte("too late"))
			testkit.ErrorIs(t, err, crypto.ErrXOFSqueezing,
				"writing after squeezing has begun must report ErrXOFSqueezing")
		}),

		XOFCustom("Reset returns the stream to absorbing", func(t *testing.T, x crypto.XOF) {
			s := x.NewXOFStream()
			mustWrite(t, s, []byte("first"))
			mustRead(t, s, 8)

			s.Reset()
			mustWrite(t, s, []byte("absorb me"))

			testkit.Equal(t, mustRead(t, s, 64), squeeze(t, x, []byte("absorb me"), 64),
				"a reset stream must behave as a fresh one")
		}),

		XOFCustom("Reset discards absorbed input", func(t *testing.T, x crypto.XOF) {
			s := x.NewXOFStream()
			mustWrite(t, s, []byte("discard me"))
			s.Reset()
			mustWrite(t, s, []byte("absorb me"))

			testkit.Equal(t, mustRead(t, s, 32), squeeze(t, x, []byte("absorb me"), 32),
				"input absorbed before Reset must not affect the output")
		}),

		XOFCustom("streams are independent", func(t *testing.T, x crypto.XOF) {
			// NewXOFStream must not hand out shared state; two
			// concurrent callers absorbing different input would
			// otherwise corrupt each other.
			a, b := x.NewXOFStream(), x.NewXOFStream()
			mustWrite(t, a, []byte("alpha"))
			mustWrite(t, b, []byte("beta"))

			testkit.Equal(t, mustRead(t, a, 32), squeeze(t, x, []byte("alpha"), 32),
				"one stream's output must not depend on another's input")
		}),

		// --- identity ---

		XOFCustom("Algorithm and ID are non-zero and stable", func(t *testing.T, x crypto.XOF) {
			var zero crypto.ID
			testkit.NotEqual(t, string(x.Algorithm()), "", "Algorithm must name the function")
			testkit.NotEqual(t, x.ID(), zero, "ID must identify the implementation")
			testkit.Equal(t, x.Algorithm(), x.Algorithm(), "Algorithm must be stable")
			testkit.Equal(t, x.ID(), x.ID(), "ID must be stable")
		}),
	}
}

// XOFIDAssertion asserts the implementation reports want from
// [crypto.XOF.ID].
func XOFIDAssertion(want crypto.ID) XOFOption {
	return XOFCustom("ID matches the implementation constant", func(t *testing.T, x crypto.XOF) {
		testkit.Equal(t, x.ID(), want, "ID must match the implementation's constant")
	})
}

// XOFAlgorithmAssertion asserts the implementation reports want from
// [crypto.XOF.Algorithm]. This is persisted alongside squeezed output,
// so a change to it is a wire-format change.
func XOFAlgorithmAssertion(want crypto.Algorithm) XOFOption {
	return XOFCustom("Algorithm matches the implementation constant", func(t *testing.T, x crypto.XOF) {
		testkit.Equal(t, x.Algorithm(), want, "Algorithm must match the implementation's constant")
	})
}

// XOFCrossStdlibAssertion asserts the implementation is byte-exact
// against a reference squeeze — the standard library's own one-shot
// helper. Without it the contract suite would pass against any
// self-consistent sponge.
func XOFCrossStdlibAssertion(reference func(data []byte, n int) []byte) XOFOption {
	return XOFCustom("output matches the standard library", func(t *testing.T, x crypto.XOF) {
		for _, input := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte{0x5A}, 512)} {
			for _, n := range []int{1, 32, 200} {
				testkit.Equal(t, squeeze(t, x, input, n), reference(input, n),
					"squeezed output must be byte-exact against the standard library")
			}
		}
	})
}

// squeeze absorbs data into a fresh stream and reads n bytes.
func squeeze(t *testing.T, x crypto.XOF, data []byte, n int) []byte {
	t.Helper()

	s := x.NewXOFStream()
	mustWrite(t, s, data)

	return mustRead(t, s, n)
}

// mustWrite absorbs p or fails the test.
func mustWrite(t *testing.T, s crypto.XOFStream, p []byte) {
	t.Helper()

	n, err := s.Write(p)
	testkit.NoError(t, err, "Write must succeed while absorbing")
	testkit.Equal(t, n, len(p), "Write must report every byte absorbed")
}

// mustRead squeezes exactly n bytes or fails the test.
//
// An XOF stream never ends, so a short read or an io.EOF is a defect
// rather than a boundary condition.
func mustRead(t *testing.T, s crypto.XOFStream, n int) []byte {
	t.Helper()

	out := make([]byte, n)
	got, err := io.ReadFull(s, out)
	testkit.NoError(t, err, "Read must fill the buffer")
	testkit.False(t, errors.Is(err, io.EOF), "an XOF stream must not end")
	testkit.Equal(t, got, n, "Read must squeeze the requested length")

	return out
}
