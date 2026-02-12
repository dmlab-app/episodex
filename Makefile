.PHONY: build run lint test clean dev help

# Variables
BINARY_NAME=episodex
BUILD_DIR=./bin
CMD_DIR=./cmd/server
GO=go
GOLANGCI_LINT=golangci-lint

# Default target
.DEFAULT_GOAL := help

## help: Show this help message
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## build: Build the application binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)/main.go
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## run: Run the application
run:
	@echo "Running $(BINARY_NAME)..."
	@$(GO) run $(CMD_DIR)/main.go

## dev: Run with auto-reload (requires air)
dev:
	@which air > /dev/null || (echo "air not found. Install: go install github.com/cosmtrek/air@latest" && exit 1)
	@air

## lint: Run linters
lint:
	@echo "Running linters..."
	@which $(GOLANGCI_LINT) > /dev/null || (echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/" && exit 1)
	@$(GOLANGCI_LINT) run --config .golangci.yml ./...

## test: Run tests
test:
	@echo "Running tests..."
	@$(GO) test -v -race -coverprofile=coverage.out ./...
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## test-short: Run tests without race detector
test-short:
	@echo "Running short tests..."
	@$(GO) test -v ./...

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	@rm -rf ./data/*.db
	@echo "Clean complete"

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	@$(GO) mod download
	@$(GO) mod tidy
	@echo "Dependencies updated"

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	@docker build -t episodex:latest .
	@echo "Docker image built"

## docker-up: Start services with docker-compose
docker-up:
	@echo "Starting services..."
	@docker-compose up -d
	@echo "Services started"

## docker-down: Stop services
docker-down:
	@echo "Stopping services..."
	@docker-compose down
	@echo "Services stopped"

## docker-logs: View container logs
docker-logs:
	@docker-compose logs -f

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@$(GO) fmt ./...
	@goimports -w .
	@echo "Formatting complete"

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@$(GO) vet ./...

## install-tools: Install development tools
install-tools:
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install golang.org/x/tools/cmd/goimports@latest
	@go install github.com/cosmtrek/air@latest
	@echo "Tools installed"
