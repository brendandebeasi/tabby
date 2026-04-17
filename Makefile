.PHONY: build test test-e2e test-unit test-race test-cover vet ci capture-visual compare-visual update-baseline clean install

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

# Directories
BIN_DIR=bin
TEST_DIR=tests
E2E_DIR=$(TEST_DIR)/e2e
SCREENSHOT_DIR=$(TEST_DIR)/screenshots

# Default target
all: build

# Build all binaries from cmd/
build:
	@mkdir -p $(BIN_DIR)
	@for d in cmd/*/; do \
		name=$$(basename $$d); \
		$(GOBUILD) -o $(BIN_DIR)/$$name ./$$d || exit 1; \
	done
	@chmod +x $(BIN_DIR)/*

# Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Run all tests
test: test-unit test-e2e

# Run unit tests
test-unit:
	$(GOTEST) -v ./pkg/...

# Run all Go unit tests with race detector
test-race:
	$(GOTEST) -race ./...

# Run tests with coverage profile and per-function report
test-cover:
	$(GOTEST) -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -func=coverage.out

# Run static analysis
vet:
	$(GOCMD) vet ./...

# Full CI check: vet + race tests + coverage
ci: vet test-race test-cover

# Run E2E tests
test-e2e: build
	@$(E2E_DIR)/run_e2e.sh

# Capture visual screenshots
capture-visual: build
	@$(E2E_DIR)/capture_visual.sh

# Compare visual screenshots with baseline
compare-visual: build
	@$(E2E_DIR)/capture_visual.sh

# Update baseline screenshots
update-baseline: build
	@$(E2E_DIR)/capture_visual.sh --update-baseline

# Plugin install directory (override with: make install PLUGIN_DIR=~/custom/path)
PLUGIN_DIR ?= $(HOME)/.tmux/plugins/tabby

# Install to tmux plugins directory
install: build
	@echo "Installing to $(PLUGIN_DIR)/"
	@mkdir -p $(PLUGIN_DIR)/bin
	@mkdir -p $(PLUGIN_DIR)/scripts
	@mkdir -p ~/.config/tabby
	@cp $(BIN_DIR)/* $(PLUGIN_DIR)/bin/
	@cp scripts/*.sh $(PLUGIN_DIR)/scripts/
	@cp tabby.tmux $(PLUGIN_DIR)/
	@test -f ~/.config/tabby/config.yaml || cp config.yaml ~/.config/tabby/config.yaml
	@chmod +x $(PLUGIN_DIR)/bin/*
	@chmod +x $(PLUGIN_DIR)/scripts/*
	@chmod +x $(PLUGIN_DIR)/tabby.tmux
	@echo "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf"

# Sync development to install location
sync: build
	@cp $(BIN_DIR)/* $(PLUGIN_DIR)/bin/
	@cp scripts/*.sh $(PLUGIN_DIR)/scripts/
	@cp tabby.tmux $(PLUGIN_DIR)/
	@test -f ~/.config/tabby/config.yaml || cp config.yaml ~/.config/tabby/config.yaml
	@echo "Synced to $(PLUGIN_DIR)/ (config -> ~/.config/tabby/)"

# Clean build artifacts
clean:
	@rm -rf $(BIN_DIR)
	@rm -f $(SCREENSHOT_DIR)/current/*
	@rm -f $(SCREENSHOT_DIR)/diffs/*

# Clean everything including baseline
clean-all: clean
	@rm -f $(SCREENSHOT_DIR)/baseline/*

# Run in development mode (rebuild on change)
dev: build
	@echo "Development mode - rebuild with 'make build'"
	@echo "Sync changes with 'make sync'"

# Show help
help:
	@echo "Tabby Makefile targets:"
	@echo ""
	@echo "  build          - Build all binaries"
	@echo "  test           - Run all tests (unit + E2E)"
	@echo "  test-unit      - Run Go unit tests"
	@echo "  test-e2e       - Run E2E integration tests"
	@echo "  capture-visual - Capture visual screenshots"
	@echo "  update-baseline- Update baseline screenshots"
	@echo "  install        - Install binaries + config (~/.config/tabby/config.yaml)"
	@echo "  sync           - Sync dev changes to install location"
	@echo "  clean          - Remove build artifacts"
	@echo "  deps           - Download Go dependencies"
	@echo ""
