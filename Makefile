.PHONY: build build-all build-darwin-arm64 build-linux-amd64 build-windows-amd64 \
        test bench tidy clean verify-cross-compile

GO ?= go
BIN_DIR := bin
DIST_DIR := dist
PKG := ./cmd/eideticd

build:
	$(GO) build -o $(BIN_DIR)/eideticd $(PKG)

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(GO) build -o $(DIST_DIR)/eideticd-darwin-arm64 $(PKG)

build-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build -o $(DIST_DIR)/eideticd-linux-amd64 $(PKG)

build-windows-amd64:
	GOOS=windows GOARCH=amd64 $(GO) build -o $(DIST_DIR)/eideticd-windows-amd64.exe $(PKG)

build-all: build-darwin-arm64 build-linux-amd64 build-windows-amd64
	@./scripts/verify-cross-compile.sh

verify-cross-compile:
	@./scripts/verify-cross-compile.sh

test:
	$(GO) test ./...

bench:
	$(GO) test -bench=. -benchtime=10s ./bench

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
