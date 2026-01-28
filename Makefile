.PHONY: build test test-e2e test-unit capture-visual compare-visual update-baseline clean install

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

# Binary names
RENDER_STATUS=bin/render-status
RENDER_TAB=bin/render-tab
TABBY_DAEMON=bin/tabby-daemon
SIDEBAR_RENDERER=bin/sidebar-renderer
PANE_HEADER=bin/pane-header
TABBAR=bin/tabbar

# Directories
BIN_DIR=bin
TEST_DIR=tests
E2E_DIR=$(TEST_DIR)/e2e
SCREENSHOT_DIR=$(TEST_DIR)/screenshots

# Default target
all: build

# Build all binaries
build: $(RENDER_STATUS) $(RENDER_TAB) $(TABBY_DAEMON) $(SIDEBAR_RENDERER) $(PANE_HEADER) $(TABBAR)

$(RENDER_STATUS): cmd/render-status/main.go pkg/**/*.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/render-status

$(RENDER_TAB): cmd/render-tab/main.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/render-tab

$(TABBY_DAEMON): cmd/tabby-daemon/*.go pkg/**/*.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/tabby-daemon

$(SIDEBAR_RENDERER): cmd/sidebar-renderer/main.go pkg/**/*.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/sidebar-renderer

$(PANE_HEADER): cmd/pane-header/main.go pkg/**/*.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/pane-header

$(TABBAR): cmd/tabbar/main.go pkg/**/*.go
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $@ ./cmd/tabbar

# Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Run all tests
test: test-unit test-e2e

# Run unit tests
test-unit:
	$(GOTEST) -v ./pkg/...

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

# Install to tmux plugins directory
install: build
	@echo "Installing to ~/.tmux/plugins/tmux-tabs/"
	@mkdir -p ~/.tmux/plugins/tmux-tabs/bin
	@mkdir -p ~/.tmux/plugins/tmux-tabs/scripts
	@cp $(RENDER_STATUS) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(RENDER_TAB) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(TABBY_DAEMON) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(SIDEBAR_RENDERER) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(PANE_HEADER) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(TABBAR) ~/.tmux/plugins/tmux-tabs/bin/
	@cp scripts/*.sh ~/.tmux/plugins/tmux-tabs/scripts/
	@cp tmux-tabs.tmux ~/.tmux/plugins/tmux-tabs/
	@cp config.yaml ~/.tmux/plugins/tmux-tabs/
	@chmod +x ~/.tmux/plugins/tmux-tabs/bin/*
	@chmod +x ~/.tmux/plugins/tmux-tabs/scripts/*
	@chmod +x ~/.tmux/plugins/tmux-tabs/tmux-tabs.tmux
	@echo "Installation complete. Reload tmux config with: tmux source ~/.tmux.conf"

# Sync development to install location
sync: build
	@cp $(RENDER_STATUS) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(RENDER_TAB) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(TABBY_DAEMON) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(SIDEBAR_RENDERER) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(PANE_HEADER) ~/.tmux/plugins/tmux-tabs/bin/
	@cp $(TABBAR) ~/.tmux/plugins/tmux-tabs/bin/
	@cp scripts/*.sh ~/.tmux/plugins/tmux-tabs/scripts/
	@cp tmux-tabs.tmux ~/.tmux/plugins/tmux-tabs/
	@cp config.yaml ~/.tmux/plugins/tmux-tabs/
	@echo "Synced to ~/.tmux/plugins/tmux-tabs/"

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
	@echo "tmux-tabs Makefile targets:"
	@echo ""
	@echo "  build          - Build all binaries"
	@echo "  test           - Run all tests (unit + E2E)"
	@echo "  test-unit      - Run Go unit tests"
	@echo "  test-e2e       - Run E2E integration tests"
	@echo "  capture-visual - Capture visual screenshots"
	@echo "  update-baseline- Update baseline screenshots"
	@echo "  install        - Install to ~/.tmux/plugins/tmux-tabs/"
	@echo "  sync           - Sync dev changes to install location"
	@echo "  clean          - Remove build artifacts"
	@echo "  deps           - Download Go dependencies"
	@echo ""
