# Makefile for sael.
#
# Targets are grouped so that `make help` is the documentation: every target
# carrying a `##` comment shows up there, and nothing else does. A stale help
# listing is worse than none, so update the comment with the target.
#
# Tools are pinned and installed into ./bin rather than $GOPATH/bin, so a clone
# gets reproducible checks without touching the rest of the machine. A linter
# that changes its mind overnight should be a commit, not a surprise on someone
# else's branch.

# Recipes here are multi-line blocks, which rely on .ONESHELL. That needs GNU
# Make 3.82 or newer, and macOS still ships 3.81 at /usr/bin/make, where a block
# is fed to the shell one line at a time and dies with a baffling "syntax error:
# unexpected end of file". Fail with something actionable instead.
MAKE_MAJOR := $(word 1,$(subst ., ,$(MAKE_VERSION)))
ifeq ($(shell [ "$(MAKE_MAJOR)" -ge 4 ] 2>/dev/null && echo yes),)
$(error GNU Make 4 or newer is required, but this is $(MAKE_VERSION). On macOS: `brew install make`, or put /usr/local/bin ahead of /usr/bin on PATH)
endif

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
.ONESHELL:

GO      ?= go
GOFLAGS ?=
PKG     := ./...
COVERAGE   := coverage.out
COVER_HTML := coverage.html
BIN_DIR    := bin

TOOLS_DIR   := $(CURDIR)/bin
STATICCHECK := $(TOOLS_DIR)/staticcheck
GOLANGCI    := $(TOOLS_DIR)/golangci-lint

# Pinned deliberately. Bump these in a commit that also fixes whatever the new
# version reports, so the change is reviewable rather than arriving by surprise.
STATICCHECK_VERSION := v0.8.1
GOLANGCI_VERSION    := v2.13.2

export PATH := $(TOOLS_DIR):$(PATH)

##@ General

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Compile every package.
	$(GO) build $(GOFLAGS) $(PKG)

.PHONY: fmt
fmt: ## Format the code in place.
	$(GO) fmt $(PKG)

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet $(GOFLAGS) $(PKG)

.PHONY: tidy
tidy: ## Sync go.mod and go.sum with the imports.
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod or go.sum is not tidy.
	@$(GO) mod tidy
	if ! git diff --quiet -- go.mod go.sum; then
		echo "go.mod or go.sum is not tidy; run 'make tidy' and commit the result" >&2
		git --no-pager diff -- go.mod go.sum >&2
		exit 1
	fi

##@ Testing

.PHONY: test
test: ## Run the offline test suite.
	$(GO) test $(GOFLAGS) -short -count=1 $(PKG)

.PHONY: test-race
test-race: ## Run the offline suite under the race detector.
	$(GO) test $(GOFLAGS) -race -short -count=1 $(PKG)

.PHONY: test-integration
test-integration: ## Run the live tests. Needs TYPESAFE_API_KEY and TYPESAFE_BASE_URL.
	if [ -z "$${TYPESAFE_API_KEY:-}" ]; then
		echo "TYPESAFE_API_KEY is not set; point TYPESAFE_BASE_URL at any host that speaks the API" >&2
		exit 1
	fi
	$(GO) test $(GOFLAGS) -run Integration -count=1 -v ./systemone/

.PHONY: cover
cover: ## Write coverage.out and coverage.html.
	$(GO) test $(GOFLAGS) -short -count=1 -coverprofile=$(COVERAGE) -covermode=atomic $(PKG)
	$(GO) tool cover -html=$(COVERAGE) -o $(COVER_HTML)
	$(GO) tool cover -func=$(COVERAGE) | tail -1
	echo "wrote $(COVERAGE) and $(COVER_HTML)"

##@ Linting

.PHONY: staticcheck
staticcheck: $(STATICCHECK) ## Run staticcheck.
	$(STATICCHECK) $(PKG)

.PHONY: lint
lint: $(GOLANGCI) ## Run golangci-lint.
	$(GOLANGCI) run

.PHONY: fmt-check
fmt-check: ## Fail if any tracked Go file is not gofmt-ed.
	@files=$$(gofmt -l $$(git ls-files '*.go'))
	if [ -n "$$files" ]; then
		echo "these files are not gofmt-ed (run 'make fmt'):" >&2
		echo "$$files" >&2
		exit 1
	fi

##@ Aggregate

.PHONY: check
check: fmt-check tidy-check vet staticcheck lint test-race ## Everything CI runs except the live tests.

.PHONY: ci
ci: check ## Alias for check, for CI scripts to call.

.PHONY: clean
clean: ## Remove build and coverage output, keeping the pinned tools.
	rm -rf $(COVERAGE) $(COVER_HTML)

.PHONY: clean-tools
clean-tools: ## Remove the pinned tools in ./bin, so the next check reinstalls them.
	rm -rf $(BIN_DIR)

##@ Tools

$(STATICCHECK):
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

$(GOLANGCI):
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
