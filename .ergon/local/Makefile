# Copyright ThesmOS B.V. 2026
# SPDX-License-Identifier: Apache-2.0

# The targets of core, which ergon init appends to the Makefile.

##@ Repository

.PHONY: check-numbers end-to-end

# The branch whose field numbers check-numbers keeps.
BASE ?= origin/main

check-numbers: ## Fail when a change renumbers a field that BASE, origin/main by default, records
	KANON_CHECK=$(BASE) $(GO) generate -run 'go tool kanon' ./...

end-to-end: ## Run the end-to-end tests, which the build tag e2e selects
	$(GO) test -tags=e2e ./...
