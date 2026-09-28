// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

// crash raises x again when x is a recovered panic. When x is nil, the
// worker ended by [runtime.Goexit], and crash returns.
//
// A worker calls crash from its deferred call, while its panic is still
// unwinding the worker's stack. The new panic keeps the task's frames
// in the crash trace. The worker's release does not run after it, so
// the function that started the task cannot return before the process
// exits.
//
// The crash of a worker kills the process, so a test observes it from a
// parent process. A test that calls crash directly and recovers the
// panic covers crash itself.
func crash(x any) {
	if x != nil {
		panic(x)
	}
}
