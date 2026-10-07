// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// crashEnv names the environment variable that tells a child test
// process which entry to crash.
const crashEnv = "TASK_TEST_CRASH"

// childTimeout is the -test.timeout of a child process of TestCrash. A
// child crashes in milliseconds. The bound ends a child whose parent
// has died, which no context of the parent can cancel.
const childTimeout = 30 * time.Second

// returnedMarker is printed by a child process whose call returned
// after its task panicked, which the contract forbids.
const returnedMarker = "task: the call returned after its task panicked"

// panickingTask panics, and its name must appear in the crash trace.
func panickingTask() {
	panic("task: panickingTask") //nolint:forbidigo // the test is about a task that panics.
}

// TestCrash runs every function in a child process whose task panics.
// The child must die with the task's frame in its trace, before the
// function that started the task returns. A crash cannot be observed
// by the process it kills, so the parent observes the child.
func TestCrash(t *testing.T) {
	t.Parallel()

	for _, e := range entries() {
		t.Run(e.name+" crashes from the task and does not return", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(crashEnv) == e.name {
				_ = e.run(t.Context(), 2, 1, func(context.Context, int) error {
					panickingTask()

					return nil
				})
				_, _ = os.Stdout.WriteString(returnedMarker + "\n")

				return
			}

			pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(t.Name()), "/", "$/^") + "$"
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run="+pattern, "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), crashEnv+"="+e.name)

			out, err := cmd.CombinedOutput()
			trace := string(out)

			assert.HasError(t, err, "the child process must die of the panic")
			expect.NotContains(t, trace, returnedMarker, "the call must not return before the crash")
			expect.Contains(t, trace, "panickingTask", "the crash trace must include the task's frame")
		})
	}
}
