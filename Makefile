.PHONY: help bootstrap install fmt license vet lint lint-md \
        test test-race test-bench test-coverage \
        bench-baseline bench-compare \
        tidy check-tidy \
        check check-coverage check-vuln \
        build clean

# ─── Colors ──────────────────────────────────────────────────────
BLUE   := $(shell printf "\033[0;36m")
GREEN  := $(shell printf "\033[0;32m")
RED    := $(shell printf "\033[0;31m")
YELLOW := $(shell printf "\033[0;33m")
NC     := $(shell printf "\033[0m")

# ─── Go settings ─────────────────────────────────────────────────
GO    := go
FLAGS ?=

# ─── Paths ───────────────────────────────────────────────────────
COVERAGE_DIR := coverage
BENCH_DIR    := .bench

# ─── Test tuning ─────────────────────────────────────────────────
# TEST_TIMEOUT applies to test, test-race, and test-bench. Override
# from the command line for slower runners or longer suites:
#   make test TEST_TIMEOUT=30m
TEST_CPU         ?= 4
TEST_COUNT       ?= 3
TEST_TIMEOUT     ?= 10m
TEST_RACE_COUNT  ?= 3

# ─── License header ──────────────────────────────────────────────
GO_FILES := $(shell find . -type f -name '*.go' \
	! -path './vendor/*' \
	! -path './dist/*' \
	! -path './.git/*' \
	! -name '*.gen.go' \
	! -name '*.gen_test.go')

# ─── Help ────────────────────────────────────────────────────────
help:
	@echo "$(BLUE)core Build System$(NC)"
	@echo ""
	@echo "$(GREEN)Setup:$(NC)"
	@echo "  bootstrap          Install development tools"
	@echo "  install            Download and verify Go dependencies"
	@echo ""
	@echo "$(GREEN)Development:$(NC)"
	@echo "  fmt                Format Go + Markdown"
	@echo "  license            Apply license headers to all Go files"
	@echo "  lint               Full lint suite (fmt + vet + golangci-lint + markdownlint)"
	@echo "  lint-md            Lint Markdown files only"
	@echo "  vet                Run go vet"
	@echo "  tidy               Run go mod tidy"
	@echo ""
	@echo "$(GREEN)Testing:$(NC)"
	@echo "  test               Run tests with coverage"
	@echo "  test-race          Run tests with race detector"
	@echo "  test-bench         Run all benchmarks"
	@echo "  test-coverage      Generate HTML coverage report"
	@echo "  bench-baseline     Run benchmarks and save to .bench/baseline.txt"
	@echo "  bench-compare      Run benchmarks and benchstat against the baseline"
	@echo ""
	@echo "$(GREEN)Quality gates:$(NC)"
	@echo "  check              Full pre-merge gate (tidy + lint + test + coverage)"
	@echo "  check-tidy         Fail if go mod tidy produces uncommitted changes"
	@echo "  check-coverage     Run tests and report coverage"
	@echo "  check-vuln         Run govulncheck"
	@echo ""
	@echo "$(GREEN)Building:$(NC)"
	@echo "  build              Compile-check all packages"
	@echo "  clean              Remove build artifacts and caches"
	@echo ""
	@echo "$(YELLOW)Flags:$(NC)   FLAGS=\"-run TestFoo\"          extra flags for test commands"
	@echo "          TEST_TIMEOUT=30m              per-package go test deadline"
	@echo "          TEST_COUNT=1                  iteration count for plain tests"
	@echo "          TEST_RACE_COUNT=3             iteration count for test-race"
	@echo "          TEST_CPU=8                    -cpu=N for parallel scheduling"
	@echo ""
	@echo "$(RED)Naming:$(NC)  test-* runs tests; check-* enforces a quality gate"

.DEFAULT_GOAL := help

# ─── Setup ───────────────────────────────────────────────────────

bootstrap:
	@echo "$(BLUE)Installing development tools...$(NC)"
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/daixiang0/gci@latest
	$(GO) install github.com/segmentio/golines@latest
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/palantir/go-license@latest
	$(GO) install golang.org/x/perf/cmd/benchstat@latest
	@command -v markdownlint-cli2 >/dev/null 2>&1 || { \
		command -v npm >/dev/null 2>&1 && npm install -g markdownlint-cli2 || \
		echo "$(YELLOW)Install markdownlint-cli2: brew install markdownlint-cli2 (or npm install -g markdownlint-cli2)$(NC)"; \
	}
	@echo "$(GREEN)Done. Run 'pre-commit install --hook-type pre-commit --hook-type pre-push --hook-type commit-msg'$(NC)"

install:
	@echo "$(BLUE)Installing dependencies...$(NC)"
	$(GO) mod download && $(GO) mod verify
	@echo "$(GREEN)Done$(NC)"

# ─── Formatting ──────────────────────────────────────────────────

fmt: license
	@echo "$(BLUE)Formatting Go...$(NC)"
	gofumpt -l -w -extra .
	gci write --section standard --section default --section "prefix(go.thesmos.sh/core)" --custom-order --skip-generated .
	@echo "$(BLUE)Formatting Markdown...$(NC)"
	markdownlint-cli2 --fix "**/*.md" "#vendor" "#dist" "#node_modules" 2>/dev/null || true
	@echo "$(GREEN)Done$(NC)"

license:
	@echo "$(BLUE)Applying license headers...$(NC)"
	@if [ -n "$(GO_FILES)" ]; then go-license --config=.go-license.yml $(GO_FILES); fi
	@echo "$(GREEN)Done$(NC)"

# ─── Linting ─────────────────────────────────────────────────────

vet:
	$(GO) vet ./...

lint: fmt vet lint-md
	@echo "$(BLUE)Running golangci-lint...$(NC)"
	golangci-lint run --timeout=5m ./...
	@echo "$(BLUE)Verifying license headers...$(NC)"
	@if [ -n "$(GO_FILES)" ]; then go-license --config=.go-license.yml --verify $(GO_FILES); fi
	@echo "$(GREEN)Lint passed$(NC)"

lint-md:
	@echo "$(BLUE)Linting Markdown...$(NC)"
	markdownlint-cli2 "**/*.md" "#vendor" "#dist" "#node_modules"

# ─── Testing ─────────────────────────────────────────────────────

test:
	@echo "$(BLUE)Running tests (timeout=$(TEST_TIMEOUT))...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -coverprofile=$(COVERAGE_DIR)/core.out -covermode=atomic -cpu=$(TEST_CPU) -count=$(TEST_COUNT) -timeout=$(TEST_TIMEOUT) $(FLAGS) ./...
	@echo "$(GREEN)Tests passed$(NC)"

test-race:
	@echo "$(BLUE)Running tests with race detector (count=$(TEST_RACE_COUNT), timeout=$(TEST_TIMEOUT))...$(NC)"
	$(GO) test -race -count=$(TEST_RACE_COUNT) -timeout=$(TEST_TIMEOUT) $(FLAGS) ./...
	@echo "$(GREEN)No races detected$(NC)"

test-bench:
	@echo "$(BLUE)Running benchmarks (timeout=$(TEST_TIMEOUT))...$(NC)"
	$(GO) test -bench=. -run=^$$ -benchmem -timeout=$(TEST_TIMEOUT) $(FLAGS) ./...

# Save the current bench output as the new baseline. Overwrites
# .bench/baseline.txt — commit it explicitly when the
# regression-detection target should treat the new numbers as
# authoritative.
bench-baseline:
	@echo "$(BLUE)Running benchmarks → $(BENCH_DIR)/baseline.txt (timeout=$(TEST_TIMEOUT))...$(NC)"
	@mkdir -p $(BENCH_DIR)
	$(GO) test -bench=. -run=^$$ -benchmem -timeout=$(TEST_TIMEOUT) $(FLAGS) ./... > $(BENCH_DIR)/baseline.txt
	@echo "$(GREEN)Saved $(BENCH_DIR)/baseline.txt$(NC)"

# Run benchmarks and benchstat against .bench/baseline.txt. Used
# as an advisory regression gate before opening a PR; the output
# flags any sub-benchmark that moved by more than benchstat's
# default significance threshold.
bench-compare:
	@if [ ! -f $(BENCH_DIR)/baseline.txt ]; then \
		echo "$(RED)No $(BENCH_DIR)/baseline.txt — run 'make bench-baseline' first$(NC)"; \
		exit 1; \
	fi
	@command -v benchstat >/dev/null 2>&1 || { \
		echo "$(RED)benchstat not on PATH — run 'make bootstrap'$(NC)"; \
		exit 1; \
	}
	@echo "$(BLUE)Running benchmarks → $(BENCH_DIR)/current.txt (timeout=$(TEST_TIMEOUT))...$(NC)"
	@mkdir -p $(BENCH_DIR)
	@$(GO) test -bench=. -run=^$$ -benchmem -timeout=$(TEST_TIMEOUT) $(FLAGS) ./... > $(BENCH_DIR)/current.txt
	@echo "$(BLUE)benchstat $(BENCH_DIR)/baseline.txt $(BENCH_DIR)/current.txt$(NC)"
	@benchstat $(BENCH_DIR)/baseline.txt $(BENCH_DIR)/current.txt

test-coverage: test
	@echo "$(BLUE)Generating coverage report...$(NC)"
	@if [ -f $(COVERAGE_DIR)/core.out ]; then \
		$(GO) tool cover -html=$(COVERAGE_DIR)/core.out -o $(COVERAGE_DIR)/core.html; \
		echo "$(GREEN)Report: $(COVERAGE_DIR)/core.html$(NC)"; \
	fi

# ─── Quality gates ───────────────────────────────────────────────

check-coverage:
	@printf "$(BLUE)Running tests …$(NC) "
	@logf=$$(mktemp); \
	if $(MAKE) --no-print-directory -s test >$$logf 2>&1; then \
		printf "$(GREEN)ok$(NC)\n\n"; \
		rm -f $$logf; \
	else \
		printf "$(RED)FAILED$(NC)\n\n"; \
		cat $$logf; \
		rm -f $$logf; \
		exit 1; \
	fi

check-vuln:
	govulncheck ./...

# ─── Building ────────────────────────────────────────────────────

build:
	$(GO) build ./...

# ─── Cleanup ─────────────────────────────────────────────────────

clean:
	@echo "$(BLUE)Cleaning...$(NC)"
	rm -rf $(COVERAGE_DIR) dist/ $(BENCH_DIR)/current.txt
	$(GO) clean -cache -testcache
	@echo "$(GREEN)Clean$(NC)"

# ─── Module hygiene ─────────────────────────────────────────────

tidy:
	$(GO) mod tidy

check-tidy: tidy
	@if ! git diff --quiet -- go.mod go.sum; then \
		echo "$(RED)go mod tidy produced changes. Run 'make tidy' and commit.$(NC)"; \
		git diff --stat -- go.mod go.sum; \
		exit 1; \
	fi

# ─── CI gate ─────────────────────────────────────────────────────

check: check-tidy lint test check-coverage
	@echo "$(GREEN)All checks passed$(NC)"
