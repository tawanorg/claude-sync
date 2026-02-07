.PHONY: build install clean test fmt lint

BINARY_NAME=claude-sync
VERSION?=0.1.0
BUILD_DIR=bin
GO=go

# Build flags
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

# Default target
all: build

# Build the binary
build:
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/claude-sync

# Install to GOPATH/bin
install:
	$(GO) install $(LDFLAGS) ./cmd/claude-sync

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Run tests
test:
	$(GO) test -v ./...

# Format code
fmt:
	$(GO) fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Build for multiple platforms
build-all: build-darwin build-linux

build-darwin:
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/claude-sync
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/claude-sync

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/claude-sync
	GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/claude-sync

# Development: build and run
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Download dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy
