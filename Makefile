.PHONY: all build build-launcher build-launcher-frontend clean help deps update-deps test harness-test integration-test benchmark check fmt

# Build variables
BINARY_NAME=gogoclaw
BUILD_DIR=build
CMD_DIR=cmd/$(BINARY_NAME)
EXT=

ifeq ($(OS),Windows_NT)
	POWERSHELL?=pwsh -NoProfile -Command
	WINDOWS_GOARCH_RAW:=$(strip $(shell go env GOARCH 2>/dev/null))
endif

# Version
ifeq ($(OS),Windows_NT)
	VERSION_RAW:=$(strip $(shell git describe --tags --always --dirty 2>/dev/null))
	GIT_COMMIT_RAW:=$(strip $(shell git rev-parse --short=8 HEAD 2>/dev/null))
	BUILD_TIME_RAW:=$(strip $(shell $(POWERSHELL) "Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'"))
	GO_VERSION_RAW:=$(strip $(shell go env GOVERSION 2>/dev/null))
else
	VERSION_RAW:=$(strip $(shell git describe --tags --always --dirty 2>/dev/null))
	GIT_COMMIT_RAW:=$(strip $(shell git rev-parse --short=8 HEAD 2>/dev/null))
	BUILD_TIME_RAW:=$(strip $(shell date +%FT%T%z))
	GO_VERSION_RAW:=$(strip $(shell go env GOVERSION 2>/dev/null))
endif
VERSION?=$(if $(VERSION_RAW),$(VERSION_RAW),dev)
GIT_COMMIT=$(if $(GIT_COMMIT_RAW),$(GIT_COMMIT_RAW),dev)
BUILD_TIME=$(if $(BUILD_TIME_RAW),$(BUILD_TIME_RAW),dev)
GO_VERSION=$(if $(GO_VERSION_RAW),$(firstword $(GO_VERSION_RAW)),unknown)
CONFIG_PKG=github.com/biaobiaobiao108/gogoclaw/pkg/config
LDFLAGS=-X $(CONFIG_PKG).Version=$(VERSION) -X $(CONFIG_PKG).GitCommit=$(GIT_COMMIT) -X $(CONFIG_PKG).BuildTime=$(BUILD_TIME) -X $(CONFIG_PKG).GoVersion=$(GO_VERSION) -s -w

# Go variables
GO?=go
WEB_GO?=$(GO)
CGO_ENABLED?=0
GO_BUILD_TAGS?=goolm,stdjson
GOFLAGS?=-v -tags $(GO_BUILD_TAGS)
GOCACHE?=$(CURDIR)/.cache/go-build
GOMODCACHE?=$(CURDIR)/.cache/go-mod
GOTOOLCHAIN?=local
export CGO_ENABLED
export GOCACHE
export GOMODCACHE
export GOTOOLCHAIN
LNCMD=ln -sf

# OS detection
ifeq ($(OS),Windows_NT)
	UNAME_S=Windows
	ifeq ($(WINDOWS_GOARCH_RAW),amd64)
		UNAME_M=x86_64
	else ifeq ($(WINDOWS_GOARCH_RAW),arm64)
		UNAME_M=arm64
	else
		UNAME_M=$(if $(WINDOWS_GOARCH_RAW),$(WINDOWS_GOARCH_RAW),x86_64)
	endif
else
	UNAME_S?=$(shell uname -s)
	UNAME_M?=$(shell uname -m)
endif

# Platform-specific settings
ifeq ($(UNAME_S),Linux)
	PLATFORM=linux
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),aarch64)
		ARCH=arm64
	else
		ARCH=$(UNAME_M)
	endif
else ifeq ($(UNAME_S),Darwin)
	PLATFORM=darwin
	ifeq ($(UNAME_M),x86_64)
		ARCH?=amd64
	else ifeq ($(UNAME_M),arm64)
		ARCH?=arm64
	else
		ARCH?=$(UNAME_M)
	endif
else
	PLATFORM=$(UNAME_S)
	ifeq ($(UNAME_M),x86_64)
		ARCH?=amd64
	else
	    ARCH?=$(UNAME_M)
	endif
	# Detect Windows (Git Bash / MSYS2)
    IS_WINDOWS:=$(if $(findstring MINGW,$(UNAME_S)),yes,$(if $(findstring MSYS,$(UNAME_S)),yes,$(if $(findstring CYGWIN,$(UNAME_S)),yes,no)))
	ifeq ($(IS_WINDOWS),yes)
	    EXT=.exe
	    LNCMD=cp
	else ifeq ($(UNAME_S),windows) # failsafe for force windows build in other OS using UNAME_S=windows
		EXT=.exe
	endif

endif

ifeq ($(OS),Windows_NT)
	PLATFORM=windows
	ifeq ($(UNAME_M),x86_64)
		ARCH?=amd64
	else ifeq ($(UNAME_M),arm64)
		ARCH?=arm64
	else
		ARCH?=$(UNAME_M)
	endif
	EXT=.exe
endif

ifneq ($(strip $(GOOS)),)
	PLATFORM:=$(GOOS)
endif

ifneq ($(strip $(GOARCH)),)
	ARCH:=$(GOARCH)
endif

ifeq ($(PLATFORM),windows)
	EXT=.exe
endif

ifeq ($(filter $(ARCH),amd64 arm64),)
$(error Unsupported architecture "$(ARCH)"; only amd64 and arm64 are supported)
endif

BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)-$(ARCH)

# Default target
all: build

## generate: Run generate
generate:
	@echo "Run generate..."
ifeq ($(OS),Windows_NT)
	@$(POWERSHELL) "if (Test-Path -LiteralPath './$(CMD_DIR)/workspace') { Remove-Item -LiteralPath './$(CMD_DIR)/workspace' -Recurse -Force }"
	@$(GO) generate ./...
else
	@rm -r ./$(CMD_DIR)/workspace 2>/dev/null || true
	@GOOS=$$($(GO) env GOHOSTOS) GOARCH=$$($(GO) env GOHOSTARCH) $(GO) generate ./...
endif
	@echo "Run generate complete"

## build: Build the gogoclaw binary for current platform
build: generate
	@echo "Building $(BINARY_NAME)$(EXT) for $(PLATFORM)/$(ARCH)..."
ifeq ($(OS),Windows_NT)
	@$(POWERSHELL) "New-Item -ItemType Directory -Force -Path '$(BUILD_DIR)' | Out-Null"
	@$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_PATH)$(EXT) ./$(CMD_DIR)
	@$(POWERSHELL) "Copy-Item -LiteralPath '$(BINARY_PATH)$(EXT)' -Destination '$(BUILD_DIR)/$(BINARY_NAME)$(EXT)' -Force"
else
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(PLATFORM) GOARCH=$(ARCH) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_PATH)$(EXT) ./$(CMD_DIR)
	@echo "Build complete: $(BINARY_PATH)$(EXT)"
	@$(LNCMD) $(BINARY_NAME)-$(PLATFORM)-$(ARCH)$(EXT) $(BUILD_DIR)/$(BINARY_NAME)$(EXT)
endif
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(EXT)"

## build-launcher: Build the gogoclaw-launcher (web console) binary
build-launcher:
	@echo "Building gogoclaw-launcher for $(PLATFORM)/$(ARCH)..."
ifeq ($(OS),Windows_NT)
	@$(POWERSHELL) "New-Item -ItemType Directory -Force -Path '$(BUILD_DIR)' | Out-Null"
	@$(MAKE) -C web build PLATFORM="$(PLATFORM)" ARCH="$(ARCH)" EXT="$(EXT)" OUTPUT="$(CURDIR)/$(BUILD_DIR)/gogoclaw-launcher-$(PLATFORM)-$(ARCH)$(EXT)" GO_BUILD_TAGS="$(GO_BUILD_TAGS)"
	@$(POWERSHELL) "Copy-Item -LiteralPath '$(BUILD_DIR)/gogoclaw-launcher-$(PLATFORM)-$(ARCH)$(EXT)' -Destination '$(BUILD_DIR)/gogoclaw-launcher$(EXT)' -Force"
	@$(POWERSHELL) "Copy-Item -LiteralPath '$(CURDIR)/scripts/windows/Start-GoGoClaw.cmd' -Destination '$(CURDIR)/$(BUILD_DIR)/Start-GoGoClaw.cmd' -Force"
else
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(PLATFORM) GOARCH=$(ARCH) $(MAKE) -C web build \
		OUTPUT="$(CURDIR)/$(BUILD_DIR)/gogoclaw-launcher-$(PLATFORM)-$(ARCH)$(EXT)" \
		WEB_GO='$(WEB_GO)' \
		GO_BUILD_TAGS='$(GO_BUILD_TAGS)' \
		LDFLAGS='$(LDFLAGS)'
	@$(LNCMD) gogoclaw-launcher-$(PLATFORM)-$(ARCH)$(EXT) $(BUILD_DIR)/gogoclaw-launcher$(EXT)
endif
	@echo "Build complete: $(BUILD_DIR)/gogoclaw-launcher$(EXT)"

build-launcher-frontend:
	@$(MAKE) -C web build-frontend

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
ifeq ($(OS),Windows_NT)
	@$(POWERSHELL) "if (Test-Path -LiteralPath '$(BUILD_DIR)') { Remove-Item -LiteralPath '$(BUILD_DIR)' -Recurse -Force }"
else
	@rm -rf $(BUILD_DIR)
endif
	@echo "Clean complete"

## test: Test Go code
test: generate
	@$(GO) test $(GOFLAGS) $$($(GO) list $(GOFLAGS) ./... | grep -v github.com/biaobiaobiao108/gogoclaw/web/)
	@cd web && make test

## harness-test: Run trajectory-level evaluation and fault-injection scenarios
harness-test:
	@CGO_ENABLED=1 $(GO) test -race $(GOFLAGS) ./pkg/harness/...
	@$(GO) test $(GOFLAGS) ./cmd/gogoclaw-harness

## integration-test: Run Docker-backed integration test suites
integration-test:
	@bash ./scripts/run-integration-tests.sh

## benchmark: Run repeatable Go benchmarks and save the environment and results
benchmark:
	@bash ./scripts/run-benchmarks.sh

## deps: Download dependencies
deps:
	@$(GO) mod download
	@$(GO) mod verify

## update-deps: Update dependencies
update-deps:
	@$(GO) get -u ./...
	@$(GO) mod tidy

## fmt: Format Go code with gofmt and frontend with bun fmt
fmt:
	@echo "Formatting Go code..."
	@$(GO) fmt ./...
	@echo "Formatting frontend code..."
	@cd web/frontend && bun run fmt

## check: Verify dependencies, tests, and development builds
check: deps test build build-launcher

## run: Build and run gogoclaw
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

## docker-build: Build the production container image
docker-build:
	docker build -f docker/Dockerfile -t gogoclaw:dev .

## docker-onboard: Create configuration in the local development volume
docker-onboard:
	docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --profile gateway run --rm --entrypoint gogoclaw gogoclaw-gateway onboard

## docker-test: Test MCP tools in Docker container
docker-test:
	@echo "Testing MCP tools in Docker..."
	@chmod +x scripts/test-docker-mcp.sh
	@./scripts/test-docker-mcp.sh

## docker-run: Build and run the local development gateway container
docker-run:
	docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --profile gateway up --build

## docker-run-agent: Run a one-shot development agent container
docker-run-agent:
	docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --profile agent run --rm gogoclaw-agent

## docker-clean: Clean Docker images and volumes
docker-clean:
	docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --profile gateway --profile launcher --profile agent down -v
	docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --profile gateway --profile launcher --profile agent down --rmi local

## mem: Build membench, download LOCOMO data (if needed), run benchmark, and show results
mem:
	@echo "Building membench..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -o $(BUILD_DIR)/membench ./cmd/membench
	@echo "Build complete: $(BUILD_DIR)/membench"
	@if [ ! -f $(BUILD_DIR)/memdata/locomo10.json ]; then \
		echo "Downloading LOCOMO dataset..."; \
		mkdir -p $(BUILD_DIR)/memdata; \
		curl -sfL "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json" \
			-o $(BUILD_DIR)/memdata/locomo10.json && [ -s $(BUILD_DIR)/memdata/locomo10.json ] || { echo "Error: LOCOMO download failed"; exit 1; }; \
		echo "Download complete"; \
	else \
		echo "LOCOMO dataset already exists, skipping download"; \
	fi
	@echo "Running benchmark..."
	@rm -rf $(BUILD_DIR)/memout
	@$(BUILD_DIR)/membench run --data $(BUILD_DIR)/memdata --out $(BUILD_DIR)/memout --budget 4000

## help: Show this help message
help:
	@echo "gogoclaw Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sort | awk -F': ' '{printf "  %-16s %s\n", substr($$1, 4), $$2}'
	@echo ""
	@echo "Examples:"
	@echo "  make build              # Build for current platform"
	@echo "  make build-launcher     # Build the web control plane for current platform"
	@echo "  make docker-build       # Build the production container image"
	@echo "  make docker-test        # Test MCP tools in Docker"
	@echo ""
	@echo "Environment Variables:"
	@echo "  VERSION                 # Version string (default: git describe)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  Platform: $(PLATFORM)/$(ARCH)"
	@echo "  Binary: $(BINARY_PATH)"
