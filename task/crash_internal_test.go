// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"testing"

	"go.dokimi.dev/assert"
)

// recoveredValue is the value the tests hand crash as a recovered panic.
const recoveredValue = "task: recovered panic"

// TestCrashRaises calls crash in the process that runs the test and
// recovers its panic there. TestCrash observes the crash of a worker from
// a parent process, because that crash kills the process.
func TestCrashRaises(t *testing.T) {
	t.Parallel()

	t.Run("panics with the value it receives", func(t *testing.T) {
		t.Parallel()
		got := assert.Panics(t, func() { crash(recoveredValue) }, "crash must raise a recovered panic again")
		assert.Equal(t, got, any(recoveredValue), "crash must panic with the value it receives")
	})

	t.Run("returns when the value is nil", func(t *testing.T) {
		t.Parallel()
		// A worker that ended by runtime.Goexit recovers nil, and crash must
		// let it finish.
		assert.NotPanics(t, func() { crash(nil) }, "crash must return for nil")
	})
}
