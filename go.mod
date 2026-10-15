// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

module go.thesmos.sh/core

go 1.27.2

require (
	go.dokimi.dev/assert v0.3.0
	go.thesmos.sh/kanon v0.2.0
	go.thesmos.sh/testkit v0.10.0
)

require (
	github.com/anishathalye/porcupine v1.1.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	pgregory.net/rapid v1.3.0 // indirect
)

tool go.thesmos.sh/kanon/cmd/kanon
