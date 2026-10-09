.PHONY: help all generate sync models-check build build-cli build-all release-all check test coverage coverage-html coverage-clean coverage-check cover fmt fmt-check vet lint lint-ci vuln install clean

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

APP_NAME    = sunspecctl
APP_SRC     = ./cmd/sunspecctl
ARCHS       = linux/amd64 linux/arm64 linux/arm/v7 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
RELEASE_DIR = release
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TAG        ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS     = -ldflags "-s -w -X main.version=$(VERSION) -X main.tag=$(TAG) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"

# Packages measured by the coverage gate: everything, as in CI.
TEST_PKGS     := ./...
COVERAGE_MIN  := 90

all: check build ## Run all checks and build library + CLI

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

build: generate ## Build the library and CLI
	@echo "Building library"
	@go build ./...
	@mkdir -p bin
	@echo "Building $(APP_NAME) $(VERSION)"
	@go build $(LDFLAGS) -o bin/$(APP_NAME) $(APP_SRC)

build-cli: ## Build CLI only (skip generate)
	@mkdir -p bin
	@echo "Building $(APP_NAME) $(VERSION)"
	@go build $(LDFLAGS) -o bin/$(APP_NAME) $(APP_SRC)

build-all: generate ## Build CLI for all architectures
	@mkdir -p $(RELEASE_DIR)
	@for arch in $(ARCHS); do \
		os=$${arch%%/*}; \
		rest=$${arch#*/}; \
		cpu=$${rest%%/*}; \
		variant=$${rest#*/}; \
		if [ "$$os" = "windows" ]; then \
			echo "Building $(APP_NAME)-$$os-$$cpu.exe..."; \
			GOOS=$$os GOARCH=$$cpu go build $(LDFLAGS) -o $(RELEASE_DIR)/$(APP_NAME)-$$os-$$cpu.exe $(APP_SRC); \
		elif [ "$$cpu" = "arm" ] && [ "$$variant" = "v7" ]; then \
			echo "Building $(APP_NAME)-$$os-armv7..."; \
			GOOS=$$os GOARCH=$$cpu GOARM=7 go build $(LDFLAGS) -o $(RELEASE_DIR)/$(APP_NAME)-$$os-armv7 $(APP_SRC); \
		else \
			echo "Building $(APP_NAME)-$$os-$$cpu..."; \
			GOOS=$$os GOARCH=$$cpu go build $(LDFLAGS) -o $(RELEASE_DIR)/$(APP_NAME)-$$os-$$cpu $(APP_SRC); \
		fi \
	done

release-all: build-all ## Package CLI binaries into tar.gz (Unix) and zip (Windows)
	@for arch in $(ARCHS); do \
		os=$${arch%%/*}; \
		rest=$${arch#*/}; \
		cpu=$${rest%%/*}; \
		variant=$${rest#*/}; \
		if [ "$$os" = "windows" ]; then \
			bin=$(APP_NAME)-$$os-$$cpu.exe; \
			echo "Packaging $(APP_NAME)-$$os-$$cpu.zip..."; \
			zip -j $(RELEASE_DIR)/$(APP_NAME)-$$os-$$cpu.zip $(RELEASE_DIR)/$$bin; \
		elif [ "$$cpu" = "arm" ] && [ "$$variant" = "v7" ]; then \
			bin=$(APP_NAME)-$$os-armv7; \
			echo "Packaging $$bin.tar.gz..."; \
			tar czf $(RELEASE_DIR)/$$bin.tar.gz -C $(RELEASE_DIR) $$bin; \
		else \
			bin=$(APP_NAME)-$$os-$$cpu; \
			echo "Packaging $$bin.tar.gz..."; \
			tar czf $(RELEASE_DIR)/$$bin.tar.gz -C $(RELEASE_DIR) $$bin; \
		fi; \
	done

install: build ## Install sunspecctl to /usr/local/bin
	@echo "Installing $(APP_NAME) to /usr/local/bin"
	@sudo install -m 0755 bin/$(APP_NAME) /usr/local/bin/$(APP_NAME)

clean: coverage-clean ## Clean build artifacts and generated code
	@echo "Cleaning build artifacts"
	@rm -rf bin
	@rm -rf $(RELEASE_DIR)
	@rm -f registry/models_gen.go
