// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

module go.thesmos.sh/core

go 1.27.3

require (
	go.dokimi.dev/assert v0.10.0
	go.dokimi.dev/conform v0.8.0
	go.dokimi.dev/stub v0.1.1
	go.thesmos.sh/kanon v0.3.0
)

require go.dokimi.dev/fault v0.2.0 // indirect

tool go.thesmos.sh/kanon/cmd/kanon
