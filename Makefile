.PHONY: build build-all build-darwin-arm64 build-linux-amd64 build-windows-amd64 \
        test bench smoke demo-smoke tidy clean verify-cross-compile

GO ?= go
BIN_DIR := bin
DIST_DIR := dist
PKG := ./cmd/eideticd

# Inject Version from the most recent git tag (or `dev` for unreleased builds).
# Visible via `eideticd -version`. Single source of truth for the binary's
# self-identification (closes the 2026-05-13 spike finding #1).
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/eideticd $(PKG)

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/eideticd-darwin-arm64 $(PKG)

build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/eideticd-linux-amd64 $(PKG)

build-windows-amd64:
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/eideticd-windows-amd64.exe $(PKG)

build-all: build-darwin-arm64 build-linux-amd64 build-windows-amd64
	@./scripts/verify-cross-compile.sh

verify-cross-compile:
	@./scripts/verify-cross-compile.sh

test:
	$(GO) test ./...

smoke: build
	@./scripts/smoke.sh

# Spec § 8 acceptance #3 — write→capture→read end-to-end against real binary.
# Sister to `smoke` (daemon-up + JSON-shape only); this exercises the fsnotify
# capture path: write JSONL to watched dir → poll /engrams → assert marker.
demo-smoke: build
	@./scripts/demo-smoke.sh

bench:
	$(GO) test -bench=. -benchtime=10s ./bench

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
