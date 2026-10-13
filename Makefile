.PHONY: help all generate sync models-check build check test soak coverage coverage-html coverage-clean coverage-check cover fmt fmt-check vet lint lint-ci vuln clean

help: ## This help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

# Ignore a parent go.work (e.g. otfabric/go.work) so this module builds standalone.
export GOWORK := off

# CI formats and lints with the Go version from go.mod, not with the toolchain installed here.
# gofmt's output changes between Go releases (comment alignment, for one), so formatting is
# done with that version, and `fmt-check` also requires the local gofmt to agree: a file that
# two versions format differently fails CI on one side or the other.
GO_MOD_VERSION := $(shell sed -nE 's/^go ([0-9]+\.[0-9]+).*/\1/p' go.mod | head -n1)
GOFMT_CI = $(shell GOTOOLCHAIN=go$(GO_MOD_VERSION).0 go env GOROOT)/bin/gofmt

# Packages measured by the coverage gate: everything, as in CI.
TEST_PKGS     := ./...
COVERAGE_MIN  := 90

all: check build ## Run all checks and build the library

generate: ## Regenerate registry from SunSpec JSON models
	@echo "Generating registry/models_gen.go"
	@go run ./internal/gen

sync: ## Sync SunSpec JSON models from upstream and regenerate
	@echo "Syncing SunSpec models"
	@./sync-models.sh
	@$(MAKE) generate

models-check: ## Report SunSpec models that are new, changed or removed upstream (read-only)
	@./check-models.sh

check: fmt fmt-check vet lint lint-ci vuln test coverage-check ## Run all checks (format, vet, lint, test, coverage)

test: ## Run unit and integration tests with race detector
	@echo "Running tests (race detector)"
	@go test -count=1 -race ./...

# Duration the chaos and the stress tests each keep looping in soak mode. Override with
# SOAKTIME=... (e.g. SOAKTIME=30m). In soak mode the scripted chaos scenarios also sweep
# every frame instead of a sample. A chaos or stress test logs its seed when it fails;
# replay it with SUNSPEC_CHAOS_SEED=<seed>.
SOAKTIME ?= 2m
soak: ## Run the chaos and stress tests under the race detector for SOAKTIME each (default 2m)
	@echo "Soaking chaos and stress tests (SOAKTIME=$(SOAKTIME))"
	@SUNSPEC_CHAOS_DURATION=$(SOAKTIME) SUNSPEC_STRESS_DURATION=$(SOAKTIME) \
		go test -race -count=1 -timeout=0 -run 'Chaos|Stress' .

coverage: ## Run tests with coverage profile and text summary
	@echo "Running coverage on $(TEST_PKGS)"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic $(TEST_PKGS)
	@go tool cover -func=coverage.out | tee coverage.txt

coverage-html: coverage ## Generate HTML coverage report (coverage.html)
	@echo "Generating HTML coverage report"
	@go tool cover -html=coverage.out -o coverage.html

coverage-check: ## Fail if total coverage is below $(COVERAGE_MIN)%
	@echo "Running coverage check (minimum $(COVERAGE_MIN)%) on $(TEST_PKGS)"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic $(TEST_PKGS)
	@go tool cover -func=coverage.out | tee coverage.txt
	@go tool cover -func=coverage.out | grep 'total:' | awk -v min=$(COVERAGE_MIN) '{gsub(/%/,""); p=$$NF+0; if (p < min) { printf "Coverage %.1f%% is below %d%%\n", p, min; exit 1 } else { printf "Coverage %.1f%% (>= %d%%)\n", p, min } }'

coverage-clean: ## Remove coverage artifacts
	@echo "Removing coverage artifacts"
	@rm -f coverage.out coverage.txt coverage.html

cover: coverage-html ## Open coverage report in browser
	@echo "Opening coverage report in browser"
	@go tool cover -html=coverage.out

fmt: ## Format Go code with the gofmt of the Go version in go.mod (what CI uses)
	@echo "Running gofmt (go$(GO_MOD_VERSION))"
	@$(GOFMT_CI) -w .

fmt-check: ## Fail if the go.mod-version gofmt or the local gofmt would change any file
	@echo "Checking formatting (gofmt go$(GO_MOD_VERSION) and local $$(go env GOVERSION))"
	@ci="$$($(GOFMT_CI) -l .)"; loc="$$(gofmt -l .)"; \
	if [ -n "$$ci$$loc" ]; then \
		[ -z "$$ci" ] || { echo "Not formatted for gofmt go$(GO_MOD_VERSION) (CI):"; echo "$$ci"; }; \
		[ -z "$$loc" ] || { echo "Not formatted for the local gofmt:"; echo "$$loc"; }; \
		echo "Files must be stable under both. Run 'make fmt'; if the two versions still disagree,"; \
		echo "restructure the code they format differently (e.g. move trailing comments onto their own lines)."; \
		exit 1; \
	fi

vet: ## Run go vet on all packages
	@echo "Running go vet"
	@go vet ./...

lint: ## Run staticcheck
	@echo "Running staticcheck"
	@staticcheck ./...

lint-ci: ## Run golangci-lint (uses .golangci.yml)
	@echo "Running golangci-lint"
	@golangci-lint run ./...

vuln: ## Run govulncheck
	@echo "Running govulncheck"
	@govulncheck ./...

build: generate ## Regenerate the registry and build all packages
	@echo "Building library"
	@go build ./...

clean: coverage-clean ## Clean build artifacts and generated code
	@echo "Cleaning build artifacts"
	@rm -f registry/models_gen.go
