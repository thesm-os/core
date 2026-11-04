// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

module go.thesmos.sh/core

go 1.27.3

require (
	go.dokimi.dev/assert v0.17.0
	go.dokimi.dev/conform v0.13.1
	go.dokimi.dev/spec v0.4.1
	go.dokimi.dev/stub v0.3.1
	go.thesmos.sh/kanon v0.5.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	go.dokimi.dev/fault v0.2.2 // indirect
	go.dokimi.dev/medium v0.1.1 // indirect
)

tool go.thesmos.sh/kanon/cmd/kanon
