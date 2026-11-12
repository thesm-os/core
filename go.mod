// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

module go.thesmos.sh/core

go 1.27.4

require (
	go.dokimi.dev/assert v0.22.0
	go.dokimi.dev/conform v0.17.1
	go.dokimi.dev/spec v0.5.1
	go.dokimi.dev/stub v0.3.3
	go.thesmos.sh/kanon v0.5.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	go.dokimi.dev/fault v0.2.4 // indirect
	go.dokimi.dev/host v0.2.2 // indirect
	go.dokimi.dev/medium v0.2.1 // indirect
)

tool go.thesmos.sh/kanon/cmd/kanon
