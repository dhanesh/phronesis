# phronesis — distribution Makefile.
#
# Satisfies: U1 (<=7 core targets), U2 (help output auto-extracted from
# docstrings), RT-5 (tool versions in go.mod), RT-6 (help auto-extract),
# RT-8 (CHANNEL= env parametrizes release).
#
# Design rules (see .manifold/dist-packaging.md for derivation):
#   1. Every user-facing target has a `## ` docstring on the SAME line as the
#      target so `make help` can surface it without drifting. DO NOT put the
#      doc on the preceding line.
#   2. Tool versions are pinned.
#      - staticcheck: via the Go 1.24 `tool` directive in go.mod (`go tool staticcheck`)
#      - goreleaser:  pinned by version in `.github/workflows/release.yml` via
#        goreleaser/goreleaser-action. Released tool; not carried in go.mod to
#        keep the module's Go floor at 1.24.5.
#      Never `go install <url>@latest` (unpinned).
#   3. Production builds go through `build` which sets `-tags=prod`. Never
#      bypass — a binary without the tag emits a startup warning (RT-9).

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:
.DEFAULT_GOAL := help

# Build metadata (resolved at make time so sub-targets share them).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse HEAD 2>/dev/null || echo none)
# RT-2 + TN1: commit time, not wall-clock. Same tag on two machines produces
# the same timestamp, so the resulting binary is byte-identical.
BUILD_TIME ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildTime=$(BUILD_TIME)

# RT-8: CHANNEL scopes `make release` to one distribution channel. Empty = all.
# Accepted values passed through to goreleaser via GORELEASER_FILTER.
CHANNEL ?=

WEBFS_DIST := internal/webfs/dist
FRONTEND_DIST := frontend/dist

.PHONY: help build test lint release docker clean test-flake-monitor

help: ## Show this help (auto-extracted from target docstrings)
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
	     /^[a-zA-Z_-]+:.*?## / { printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2 }' \
	     $(MAKEFILE_LIST)

build: ## Build production binary (embeds frontend, deterministic ldflags)
	cd frontend && npm ci && npm run build
	@if [ ! -d "$(FRONTEND_DIST)" ] || [ -z "$$(ls -A $(FRONTEND_DIST) 2>/dev/null)" ]; then \
		echo "error: $(FRONTEND_DIST) is empty after npm run build" >&2; exit 1; \
	fi
	rm -rf $(WEBFS_DIST)
	mkdir -p $(WEBFS_DIST)
	cp -R $(FRONTEND_DIST)/. $(WEBFS_DIST)/
	SOURCE_DATE_EPOCH=$$(git log -1 --format=%ct 2>/dev/null || echo 0) \
	    go build -trimpath -tags=prod -ldflags="$(LDFLAGS)" -o phronesis ./cmd/phronesis

test: ## Run backend test suite (dev stub frontend — no npm build needed)
	go test -race -timeout=90s ./...

test-flake-monitor: ## Unit-test the flake-rate computation script (scripts/compute-flake-rate.js)
	bash tests/flake-monitor.test.sh

lint: ## Run gofmt + go vet + staticcheck (all three gate CI)
	@UNFMT="$$(gofmt -l . 2>/dev/null | grep -v '^frontend/' || true)"; \
	if [ -n "$$UNFMT" ]; then \
		echo "error: gofmt violations; run 'gofmt -w <file>' on:" >&2; \
		echo "$$UNFMT" >&2; \
		exit 1; \
	fi
	go vet ./...
	go tool staticcheck ./...

release: ## Publish to all channels (set CHANNEL=homebrew|docker|nfpm|github to scope)
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "error: goreleaser not on PATH. In CI use goreleaser/goreleaser-action@v6 (pinned via .github/workflows/release.yml). Locally install via: brew install goreleaser" >&2; \
		exit 1; \
	fi
	@if [ -n "$(CHANNEL)" ]; then \
		case '$(CHANNEL)' in \
			github)   SKIP='docker,homebrew,nfpm' ;; \
			docker)   SKIP='archive,homebrew,nfpm' ;; \
			homebrew) SKIP='archive,docker,nfpm' ;; \
			nfpm)     SKIP='archive,docker,homebrew' ;; \
			*) echo "error: invalid CHANNEL=$(CHANNEL); expected one of: github|docker|homebrew|nfpm" >&2; exit 1 ;; \
		esac; \
		echo "→ releasing channel: $(CHANNEL)"; \
		goreleaser release --clean --skip="$$SKIP"; \
	else \
		echo "→ releasing all channels"; \
		goreleaser release --clean; \
	fi

docker: ## Build local multi-arch container image (goreleaser snapshot, no publish)
	@if ! command -v goreleaser >/dev/null 2>&1; then \
		echo "error: goreleaser not on PATH. Install: brew install goreleaser" >&2; exit 1; \
	fi
	goreleaser release --snapshot --clean --skip=archive,homebrew,nfpm

clean: ## Remove build outputs (binary, frontend/dist, webfs/dist, goreleaser dist)
	rm -rf phronesis dist $(WEBFS_DIST) $(FRONTEND_DIST)

# Review response I2: internal/webfs/dist/ is populated by `make build`
# and gitignored. It should never be present when committing. This is
# not a user-facing target (no ## docstring) so `make help` stays at 7
# entries. Pre-commit hook or release playbook can invoke it as:
#   make -s _check-clean || { echo "run: make clean"; exit 1; }
.PHONY: _check-clean
_check-clean:
	@if [ -d "$(WEBFS_DIST)" ] && [ -n "$$(ls -A $(WEBFS_DIST) 2>/dev/null)" ]; then \
		echo "warn: $(WEBFS_DIST) has content; run 'make clean' before commit to avoid accidental force-adds" >&2; \
		exit 1; \
	fi
