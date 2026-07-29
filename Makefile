.PHONY: help all generate build build-cli check test coverage coverage-html coverage-clean coverage-check cover fmt vet lint lint-ci vuln install clean

help: ## This help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

# Ignore a parent go.work (e.g. otfabric/go.work) so this module builds standalone.
export GOWORK := off

APP_NAME    = sunspecctl
APP_SRC     = ./cmd/sunspecctl
ARCHS       = linux/amd64 linux/arm64 linux/arm/v7 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
RELEASE_DIR = release
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TAG        ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS     = -ldflags "-s -w -X main.version=$(VERSION) -X main.tag=$(TAG) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"

# Library packages for coverage gates (exclude CLI, codegen, and test helpers).
TEST_PKGS     := . ./registry
COVERAGE_MIN  := 75

all: check build ## Run all checks and build library + CLI

generate: ## Regenerate registry from SunSpec JSON models
	@echo "Generating registry/models_gen.go"
	@go run ./internal/gen

sync: ## Sync SunSpec JSON models from upstream and regenerate
	@echo "Syncing SunSpec models"
	@./sync-models.sh
	@$(MAKE) generate

check: fmt vet lint lint-ci vuln test coverage-check ## Run all checks (format, vet, lint, test, coverage)

test: ## Run unit and integration tests with race detector
	@echo "Running tests (race detector)"
	@go test -count=1 -race ./...

coverage: ## Run library tests with coverage profile and text summary
	@echo "Running coverage on $(TEST_PKGS)"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic $(TEST_PKGS)
	@go tool cover -func=coverage.out | tee coverage.txt

coverage-html: coverage ## Generate HTML coverage report (coverage.html)
	@echo "Generating HTML coverage report"
	@go tool cover -html=coverage.out -o coverage.html

coverage-check: ## Fail if library coverage is below $(COVERAGE_MIN)%
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

fmt: ## Format Go code with gofmt
	@echo "Running gofmt"
	@gofmt -w .

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
