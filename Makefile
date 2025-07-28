# Makefile for grove (meta CLI)

BINARY_NAME=grove
INSTALL_PATH=/usr/local/bin

.PHONY: all build install uninstall test clean fmt vet lint run

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) .

install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	@sudo cp $(BINARY_NAME) $(INSTALL_PATH)/
	@echo "Installed successfully!"

uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Uninstalled successfully!"

test:
	@echo "Running tests..."
	@go test -v ./...

clean:
	@echo "Cleaning..."
	@go clean
	@rm -f $(BINARY_NAME)
	@rm -f coverage.out

fmt:
	@echo "Formatting code..."
	@go fmt ./...

vet:
	@echo "Running go vet..."
	@go vet ./...

lint:
	@echo "Running linter..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# Run the CLI
run: build
	@./$(BINARY_NAME) $(ARGS)

# Run all checks
check: fmt vet lint test

# Development build with race detector
dev:
	@echo "Building $(BINARY_NAME) with race detector..."
	@go build -race -o $(BINARY_NAME) .

# Cross-compilation targets
build-all:
	@echo "Building for multiple platforms..."
	@GOOS=darwin GOARCH=amd64 go build -o $(BINARY_NAME)-darwin-amd64 .
	@GOOS=darwin GOARCH=arm64 go build -o $(BINARY_NAME)-darwin-arm64 .
	@GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 .
	@GOOS=linux GOARCH=arm64 go build -o $(BINARY_NAME)-linux-arm64 .
	@GOOS=windows GOARCH=amd64 go build -o $(BINARY_NAME)-windows-amd64.exe .

# Show available targets
help:
	@echo "Available targets:"
	@echo "  make build       - Build the binary"
	@echo "  make install     - Build and install to $(INSTALL_PATH)"
	@echo "  make uninstall   - Remove from $(INSTALL_PATH)"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make fmt         - Format code"
	@echo "  make vet         - Run go vet"
	@echo "  make lint        - Run linter"
	@echo "  make run ARGS=.. - Run the CLI with arguments"
	@echo "  make check       - Run all checks"
	@echo "  make dev         - Build with race detector"
	@echo "  make build-all   - Build for multiple platforms"
	@echo "  make help        - Show this help"