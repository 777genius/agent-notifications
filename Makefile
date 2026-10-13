.PHONY: build test test-race lint clean install help build-notifier \
	dev-local-install dev-local-update dev-local-bootstrap dev-local-status dev-local-reset \
	dev-real-local dev-real-remote dev-real-toggle dev-real-status \
	e2e-status e2e-smoke e2e-smoke-installed e2e-manual e2e-manual-installed \
	linux-focus-debug preview-installer release-preflight release-tools-check release-workflow-check codex-release-smoke release-prepare release-wait release-verify

# Binary names
BINARY=claude-notifications
SOUND_PREVIEW=sound-preview
LIST_SOUNDS=list-sounds
BINARY_PATH=bin/$(BINARY)
SOUND_PREVIEW_PATH=bin/$(SOUND_PREVIEW)
LIST_SOUNDS_PATH=bin/$(LIST_SOUNDS)

# Build flags
# Development build: includes debug symbols for debugging
# Production build: optimized for size and deployment
RELEASE_FLAGS=-ldflags="-s -w" -trimpath

# Build targets
build: ## Build the binaries (development mode with debug symbols)
	@echo "Building $(BINARY), $(SOUND_PREVIEW) and $(LIST_SOUNDS) (development mode)..."
	@go build -o $(BINARY_PATH) ./cmd/claude-notifications
	@go build -o $(SOUND_PREVIEW_PATH) ./cmd/sound-preview
	@go build -o $(LIST_SOUNDS_PATH) ./cmd/list-sounds
	@echo "Build complete! Binaries in bin/"

build-all: ## Build optimized binaries for all platforms
	@echo "Building optimized release binaries for all platforms..."
	@mkdir -p dist
	@echo "Building claude-notifications..."
	@GOOS=darwin GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(BINARY)-darwin-amd64 ./cmd/claude-notifications
	@GOOS=darwin GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(BINARY)-darwin-arm64 ./cmd/claude-notifications
	@GOOS=linux GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(BINARY)-linux-amd64 ./cmd/claude-notifications
	@GOOS=linux GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(BINARY)-linux-arm64 ./cmd/claude-notifications
	@GOOS=windows GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(BINARY)-windows-amd64.exe ./cmd/claude-notifications
	@echo "Building claude-notifications focus-handler (GUI subsystem, no console flash on toast click)..."
	@GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H=windowsgui" -trimpath -o dist/$(BINARY)-windows-amd64-focus.exe ./cmd/claude-notifications
	@echo "Building sound-preview..."
	@GOOS=darwin GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(SOUND_PREVIEW)-darwin-amd64 ./cmd/sound-preview
	@GOOS=darwin GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(SOUND_PREVIEW)-darwin-arm64 ./cmd/sound-preview
	@GOOS=linux GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(SOUND_PREVIEW)-linux-amd64 ./cmd/sound-preview
	@GOOS=linux GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(SOUND_PREVIEW)-linux-arm64 ./cmd/sound-preview
	@GOOS=windows GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(SOUND_PREVIEW)-windows-amd64.exe ./cmd/sound-preview
	@echo "Building list-sounds..."
	@GOOS=darwin GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(LIST_SOUNDS)-darwin-amd64 ./cmd/list-sounds
	@GOOS=darwin GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(LIST_SOUNDS)-darwin-arm64 ./cmd/list-sounds
	@GOOS=linux GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(LIST_SOUNDS)-linux-amd64 ./cmd/list-sounds
	@GOOS=linux GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/$(LIST_SOUNDS)-linux-arm64 ./cmd/list-sounds
	@GOOS=windows GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/$(LIST_SOUNDS)-windows-amd64.exe ./cmd/list-sounds
	@echo "Build complete! Optimized binaries in dist/"

# Test targets
test: ## Run tests
	@echo "Running tests..."
	@go test -v -cover ./...

test-race: ## Run tests with race detection
	@echo "Running tests with race detection..."
	@go test -v -race -cover ./...

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	@go test -v -coverprofile=coverage.txt -covermode=atomic ./...
	@go tool cover -html=coverage.txt -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Linting
lint: ## Run linter
	@echo "Running linter..."
	@go vet ./...
	@go fmt ./...

# Installation
install: build ## Install binary to /usr/local/bin
	@echo "Installing $(BINARY) to /usr/local/bin..."
	@cp $(BINARY_PATH) /usr/local/bin/$(BINARY)
	@echo "Installation complete!"

# Release operator checks (install locked tool dependencies once with npm ci).
release-preflight: ## Check published metadata before release preparation (RELEASE_PREFLIGHT_ARGS optional)
	@node scripts/release-preflight.mts $(if $(strip $(RELEASE_PREFLIGHT_ARGS)),$(RELEASE_PREFLIGHT_ARGS),--mode snapshot)

release-workflow-check: ## Validate release workflow expressions with pinned actionlint
	@go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/basic-release.yml .github/workflows/landing.yml .github/workflows/release-tools.yml .github/workflows/ci-ubuntu.yml .github/workflows/ci-windows.yml .github/workflows/install-recovery-e2e.yml

release-tools-check: release-workflow-check ## Typecheck and test release tooling without agents, native registration or GitHub effects
	@npm --prefix scripts/codex-offline-release-smoke run typecheck
	@scripts/codex-offline-release-smoke/node_modules/.bin/tsc --noEmit -p scripts/release-tools-tsconfig.json
	@npm --prefix scripts/codex-offline-release-smoke test
	@node --test scripts/release-progress.test.mts scripts/release-preflight.test.mts scripts/basic-release-assets/assets.test.mts scripts/release-wait.test.mts scripts/release-public-verify.test.mts
	@python3 -B tools/ci_release_scope_test.py
	@bash scripts/codex-release-gate_test.sh
	@python3 scripts/stage-released-loader-test.py

codex-release-smoke: ## Run one explicit isolated Codex E2E phase (SMOKE_PHASE, SMOKE_INPUTS required)
	@test -n "$(SMOKE_PHASE)" && test -n "$(SMOKE_INPUTS)" || { echo 'Set SMOKE_PHASE and SMOKE_INPUTS (see scripts/codex-offline-release-smoke/README.md)' >&2; exit 2; }
	@node scripts/codex-offline-release-smoke/run-phase.mts "$(SMOKE_PHASE)" "$(SMOKE_INPUTS)"

# Preparation and read-only fan-in: never publish, submit a turn or apply setup.
release-prepare: ## Preflight frozen C and prepared P together (RELEASE_VERSION, CANDIDATE_SHA, CANDIDATE_ROOT, PROMOTION_ROOT)
	@test -n "$(RELEASE_VERSION)" && test -n "$(CANDIDATE_SHA)" && test -n "$(CANDIDATE_ROOT)" && test -n "$(PROMOTION_ROOT)" || { echo 'Set RELEASE_VERSION, CANDIDATE_SHA, CANDIDATE_ROOT and PROMOTION_ROOT' >&2; exit 2; }
	@node scripts/release-preflight.mts --mode candidate --version "$(RELEASE_VERSION)" --source-sha "$(CANDIDATE_SHA)" --root "$(CANDIDATE_ROOT)"
	@node scripts/release-preflight.mts --mode prepared-promotion --version "$(RELEASE_VERSION)" --source-sha "$(CANDIDATE_SHA)" --root "$(PROMOTION_ROOT)"

release-wait: ## Join C/P CI and signing concurrently (RELEASE_WAIT_INPUTS JSON; no effects/retries)
	@test -n "$(RELEASE_WAIT_INPUTS)" || { echo 'Set RELEASE_WAIT_INPUTS (see docs/RELEASE_SPEED.md)' >&2; exit 2; }
	@node scripts/release-wait.mts "$(RELEASE_WAIT_INPUTS)"

release-verify: ## Verify one public release/Latest/channels/Pages/loader (no installation)
	@test -n "$(SEALED_DIR)" && test -n "$(MAIN_SHA)" && test -n "$(PUBLIC_LOADER_URL)" && test -n "$(PAGES_RUN_ID)" || { echo 'Set SEALED_DIR, MAIN_SHA, PUBLIC_LOADER_URL and PAGES_RUN_ID' >&2; exit 2; }
	@node scripts/release-public-verify.mts "$(SEALED_DIR)" "$(MAIN_SHA)" "$(PUBLIC_LOADER_URL)" "$(PAGES_RUN_ID)"

# Local plugin workflows
preview-installer: ## Preview installer with real CLIs in isolated macOS TEST profiles (PREVIEW_ARGS optional)
	@bash scripts/preview-installer.sh $(PREVIEW_ARGS)

dev-local-install: ## Install plugin in isolated Claude config
	@bash scripts/dev-local-plugin.sh install

dev-local-update: ## Update plugin in isolated Claude config
	@bash scripts/dev-local-plugin.sh update

dev-local-bootstrap: ## Run bootstrap in isolated Claude config
	@bash scripts/dev-local-plugin.sh bootstrap

dev-local-status: ## Show isolated Claude config plugin status
	@bash scripts/dev-local-plugin.sh status

dev-local-reset: ## Reset isolated Claude config
	@bash scripts/dev-local-plugin.sh reset

dev-real-local: ## Point real Claude marketplace to local source
	@bash scripts/dev-real-plugin.sh local

dev-real-remote: ## Point real Claude marketplace back to remote source
	@bash scripts/dev-real-plugin.sh remote

dev-real-toggle: ## Toggle real Claude marketplace between local and remote
	@bash scripts/dev-real-plugin.sh toggle

dev-real-status: ## Show real Claude marketplace/plugin status
	@bash scripts/dev-real-plugin.sh status

e2e-status: ## Show real-Claude E2E support and target status
	@bash scripts/e2e-real-claude.sh status

e2e-smoke: ## Run real-Claude smoke test via --plugin-dir
	@bash scripts/e2e-real-claude.sh smoke-plugin-dir

e2e-smoke-installed: ## Run real-Claude smoke test against installed plugin
	@bash scripts/e2e-real-claude.sh smoke-installed

e2e-manual: ## Run manual click validation via --plugin-dir
	@bash scripts/e2e-real-claude.sh manual-click-plugin-dir

e2e-manual-installed: ## Run manual click validation against installed plugin
	@bash scripts/e2e-real-claude.sh manual-click-installed

linux-focus-debug: ## Collect Linux click-to-focus diagnostics report
	@bash scripts/linux-focus-debug.sh

# Cleanup
clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf bin/ dist/ coverage.* *.log
	@echo "Clean complete!"

# Rebuild and prepare for commit
rebuild-and-commit: build-all ## Rebuild optimized binaries and prepare for commit
	@echo "Moving optimized binaries to bin/..."
	@cp dist/* bin/ 2>/dev/null || true
	@rm -rf dist
	@echo "✓ Optimized binaries ready in bin/"
	@echo ""
	@echo "Platform binaries updated:"
	@ls -1 bin/claude-notifications-* 2>/dev/null || echo "  (none found)"
	@echo ""
	@echo "To commit: git add bin/claude-notifications-* && git commit -m 'chore: rebuild binaries'"

# Swift notifier (macOS only)
build-notifier: ## Build ClaudeNotifier .app bundle (macOS)
	@echo "Building ClaudeNotifier..."
	@cd swift-notifier && bash scripts/build-app.sh
	@echo "Done! Bundle at swift-notifier/ClaudeNotifier.app"

# Help
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
