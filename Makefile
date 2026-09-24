# Makefile for sael.
#
# Targets are grouped so that `make help` is the documentation: every target
# carrying a `##` comment shows up there, and nothing else does. A stale help
# listing is worse than none, so update the comment with the target.
#
# The repository holds three modules, sdk/, cli/, and gateway/, so that each can be tagged and
# depended on on its own. Every target below therefore runs once per module: a
# change to one module's go.mod is a change only that module has to answer for.
#
# The SDK and CLI modules are joined by a `replace` directive in cli/go.mod rather than by
# a committed go.work. A workspace would be convenient and is deliberately not
# used: it is incompatible with GOFLAGS=-mod=mod, a setting that predates Go 1.16
# and is still common on developer machines, and a checkout that only builds on
# machines with the right GOFLAGS is worse than one that needs a two-line
# replace. Anyone who wants a workspace can create one locally; it is ignored.
#
# Linters are pinned to an exact version and run with `go run pkg@version`
# rather than installed as binaries. The pin is the part that matters: a linter
# that changes its mind overnight should be a commit, not a surprise on someone
# else's branch. Running from source keeps the download in the shared module
# cache, instead of dropping ~75 MB of binary into every clone of every project,
# and it costs nothing after the first build.
#
# GoReleaser is the exception, and it is fetched as a released binary instead.
# `go run github.com/goreleaser/goreleaser/v2@...` compiles GoReleaser's whole
# dependency tree -- the Azure, AWS, Google Cloud and Docker SDKs, hundreds of
# megabytes of source -- and it will not build with the Go this module targets,
# so the go command silently downloads a second toolchain first. In practice that
# stalls for many minutes. The released binary is 25 MB, needs no compilation,
# and builds all three archives in under twenty seconds. It lands in a shared
# cache for the same reason the linters are not installed into ./bin: a checkout
# should not carry a copy of a tool that is identical in every other checkout.

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

# Ordered so that a failure names the SDK first: a broken SDK explains a broken
# CLI, and reading them in the other order sends you looking in the wrong place.
MODULES := sdk cli gateway

# Pinned deliberately. Bump these in a commit that also fixes whatever the new
# version reports, so the change is reviewable rather than arriving by surprise.
STATICCHECK_VERSION := v0.8.1
GOLANGCI_VERSION    := v2.13.2
GORELEASER_VERSION  := v2.18.2
ACTIONLINT_VERSION  := v1.7.7

STATICCHECK := $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
GOLANGCI    := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
# Validates the workflow files themselves. They are a deliverable that nothing
# else checks: an undefined matrix key or a typo in a run block is invisible until
# a push, and a push is the expensive place to find out.
ACTIONLINT  := $(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

# GoReleaser, fetched rather than compiled. See the note at the top for why.
#
# The cache honours XDG_CACHE_HOME so it lands somewhere a person expects, and it
# is shared: the same 25 MB serves every clone on the machine. Override
# GORELEASER to use one that is already on PATH.
GORELEASER_CACHE   := $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/sael/tools
GORELEASER         ?= $(GORELEASER_CACHE)/goreleaser-$(GORELEASER_VERSION)

##@ General

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Compile every package in every module.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(GO) build $(GOFLAGS) $(PKG))
	done

.PHONY: fmt
fmt: ## Format the code in place.
	for m in $(MODULES); do
		(cd $$m && $(GO) fmt $(PKG))
	done

.PHONY: vet
vet: ## Run go vet in every module.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(GO) vet $(GOFLAGS) $(PKG))
	done

.PHONY: tidy
tidy: ## Sync go.mod and go.sum in every module.
	for m in $(MODULES); do
		(cd $$m && $(GO) mod tidy)
	done

.PHONY: tidy-check
tidy-check: ## Fail if any module's go.mod or go.sum is not tidy.
	for m in $(MODULES); do
		(cd $$m && $(GO) mod tidy)
	done
	if ! git diff --quiet -- '*/go.mod' '*/go.sum'; then
		echo "a go.mod or go.sum is not tidy; run 'make tidy' and commit the result" >&2
		git --no-pager diff -- '*/go.mod' '*/go.sum' >&2
		exit 1
	fi

##@ Testing

.PHONY: test
test: ## Run the offline test suite in every module.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(GO) test $(GOFLAGS) -short -count=1 $(PKG))
	done

.PHONY: test-race
test-race: ## Run the offline suite under the race detector.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(GO) test $(GOFLAGS) -race -short -count=1 $(PKG))
	done

.PHONY: test-integration
test-integration: ## Run the live SDK tests. Needs TYPESAFE_API_KEY and TYPESAFE_BASE_URL.
	if [ -z "$${TYPESAFE_API_KEY:-}" ]; then
		echo "TYPESAFE_API_KEY is not set; point TYPESAFE_BASE_URL at any host that speaks the API" >&2
		exit 1
	fi
	cd sdk && $(GO) test $(GOFLAGS) -run Integration -count=1 -v $(PKG)

.PHONY: cover
cover: ## Write coverage.out and coverage.html in each module.
	for m in $(MODULES); do
		echo "--- $$m"
		# Written inside the module so the profile paths line up with the module
		# root, which is what `go tool cover` needs to find the sources.
		(cd $$m && $(GO) test $(GOFLAGS) -short -count=1 -coverprofile=coverage.out -covermode=atomic $(PKG))
		(cd $$m && $(GO) tool cover -html=coverage.out -o coverage.html)
		echo -n "$$m total: "
		(cd $$m && $(GO) tool cover -func=coverage.out | tail -1)
	done

##@ Linting

.PHONY: staticcheck
staticcheck: ## Run staticcheck in every module.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(STATICCHECK) $(PKG))
	done

.PHONY: lint-workflows
lint-workflows: ## Check the GitHub Actions workflows.
	$(ACTIONLINT)

.PHONY: lint
lint: ## Run golangci-lint in every module.
	for m in $(MODULES); do
		echo "--- $$m"
		(cd $$m && $(GOLANGCI) run $(PKG))
	done

.PHONY: fmt-check
fmt-check: ## Fail if any tracked Go file is not gofmt-ed.
	@files=$$(gofmt -l $$(git ls-files '*.go'))
	if [ -n "$$files" ]; then
		echo "these files are not gofmt-ed (run 'make fmt'):" >&2
		echo "$$files" >&2
		exit 1
	fi

##@ Release

.PHONY: tools
tools: $(GORELEASER) ## Fetch the pinned release tool into the shared cache.

# GoReleaser's own asset names are not the ones you would guess: x86_64 rather
# than amd64, and a capitalised OS. A wrong guess is a 404, so the mapping is
# written out and an unlisted host fails loudly instead of quietly fetching the
# wrong architecture.
$(GORELEASER):
	os=$$(uname -s | tr '[:upper:]' '[:lower:]')
	arch=$$(uname -m)
	case "$$os/$$arch" in
		darwin/arm64)  asset=goreleaser_Darwin_arm64.tar.gz ;;
		darwin/x86_64) asset=goreleaser_Darwin_x86_64.tar.gz ;;
		linux/x86_64)  asset=goreleaser_Linux_x86_64.tar.gz ;;
		linux/aarch64) asset=goreleaser_Linux_arm64.tar.gz ;;
		*)
			echo "no pinned GoReleaser release for $$os/$$arch." >&2
			echo "Install one yourself and pass it: make release-snapshot GORELEASER=/path/to/goreleaser" >&2
			exit 1
			;;
	esac
	base="https://github.com/goreleaser/goreleaser/releases/download/$(GORELEASER_VERSION)"
	tmp=$$(mktemp -d)
	trap 'rm -rf "$$tmp"' EXIT
	echo "fetching GoReleaser $(GORELEASER_VERSION) ($$asset)"
	curl -fsSL -o "$$tmp/$$asset" "$$base/$$asset"
	curl -fsSL -o "$$tmp/checksums.txt" "$$base/checksums.txt"
	# Verified against the release's own checksums, the same way install.sh
	# verifies the binary it downloads. Both files come from the same origin, so
	# this catches a truncated or corrupted download. It does not defend against
	# that origin being compromised, and nothing here could.
	if command -v shasum >/dev/null 2>&1; then sum="shasum -a 256 -c -"; else sum="sha256sum -c -"; fi
	(cd "$$tmp" && grep " $$asset$$" checksums.txt | $$sum >/dev/null)
	tar -xzf "$$tmp/$$asset" -C "$$tmp" goreleaser
	mkdir -p "$(dir $(GORELEASER))"
	mv "$$tmp/goreleaser" "$(GORELEASER)"
	chmod 755 "$(GORELEASER)"
	echo "installed $(GORELEASER)"

.PHONY: release-check
release-check: $(GORELEASER) ## Validate the release configuration without publishing.
	$(GORELEASER) check

.PHONY: release-snapshot
release-snapshot: $(GORELEASER) ## Build the release archives into dist/ without publishing.
	$(GORELEASER) release --snapshot --clean

##@ Aggregate

.PHONY: check
check: fmt-check tidy-check vet staticcheck lint lint-workflows test-race ## Everything CI runs except the live tests.

.PHONY: ci
ci: check ## Alias for check, for CI scripts to call.

.PHONY: clean
clean: ## Remove coverage output.
	rm -f sdk/coverage.out sdk/coverage.html cli/coverage.out cli/coverage.html
	rm -rf dist
