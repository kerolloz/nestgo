#!/usr/bin/env -S just --justfile

set shell := ["bash", "-cu"]

# Initialize the project (tidy Go modules)
init:
	cd packages/ttsgo && go mod tidy
	cd packages/nestgo && go mod tidy

# Build both binaries for the current platform (used by CI + local dev)
build:
	mkdir -p bin
	cd packages/ttsgo && go build -ldflags="-s -w" -o ../../bin/ttsgo ./cmd/ttsgo
	cd packages/nestgo && go build -ldflags="-s -w" -o ../../bin/nestgo .

# Run the Go test suites for both packages
test:
	cd packages/ttsgo && go test ./pkg/...
	cd packages/nestgo && go test ./...

# Verify emitted decorator metadata still matches tsc (NestJS DI depends on it)
verify-decorators:
	./scripts/verify-decorator-emit.sh

# Clean build artifacts.
# Only the release-staged binaries under packages/*/bin are removed — the
# package bin/ directories also hold the committed launcher scripts, so they
# must not be deleted wholesale.
clean:
	rm -rf bin
	rm -f packages/*/bin/nestgo packages/*/bin/nestgo.exe
	rm -f packages/*/bin/ttsgo packages/*/bin/ttsgo.exe
