# Contributing

Thank you for your interest in contributing to Agent Notifications!

## Prerequisites

- **Go 1.25.0+** (CI covers the minimum Go 1.25 and Go 1.26; automatic toolchain switching is disabled)
- **Make** (for build commands)
- **Claude Code** (tested on v2.0.15)

## Getting Started

### 1. Clone and build

```bash
git clone https://github.com/777genius/agent-notifications
cd agent-notifications
make build
```

### 2. Install as local plugin

```bash
# Add as local marketplace
/plugin marketplace add .

# Install plugin
/plugin install claude-notifications-go@claude-notifications-go

# Restart Claude Code for hooks to take effect

# Download binary and configure settings
/claude-notifications-go:init
/claude-notifications-go:settings
```

`/claude-notifications-go:init` will use your locally built binary from `bin/` if it exists, otherwise it downloads from GitHub Releases.

For repeatable local install/update testing without touching your real Claude setup, use:

```bash
scripts/dev-local-plugin.sh install
scripts/dev-local-plugin.sh bootstrap
scripts/dev-local-plugin.sh status
```

This uses an isolated Claude config dir under `~/.claude-dev/claude-notifications-go` by default.

For the full local-development workflow, including real-`claude` E2E tests and switching your real Claude environment between local and remote sources, see **[docs/LOCAL_DEVELOPMENT.md](docs/LOCAL_DEVELOPMENT.md)**.

## Local Testing Workflows

Use the smallest workflow that matches the change:

- `scripts/dev-local-plugin.sh` for safe install/update/bootstrap testing in an isolated Claude config
- `scripts/e2e-real-claude.sh` for real-`claude` smoke/manual validation
- `scripts/dev-real-plugin.sh` only when you must validate inside your real `~/.claude` setup

Start with:

```bash
scripts/e2e-real-claude.sh status
```

Then follow **[docs/LOCAL_DEVELOPMENT.md](docs/LOCAL_DEVELOPMENT.md)** for the detailed workflow, platform support, and recommended validation matrix.

## Project Structure

See [Architecture](docs/ARCHITECTURE.md) for a detailed overview. Key directories:

| Directory | Description |
|-----------|-------------|
| `cmd/` | CLI entry points (`claude-notifications`, `sound-preview`, `list-devices`, `list-sounds`) |
| `internal/` | Core logic (analyzer, hooks, notifier, webhook, config, audio, etc.) |
| `pkg/jsonl/` | JSONL streaming parser |
| `commands/` | Plugin skill definitions (`.md` files) |
| `sounds/` | Built-in notification sounds (MP3) |

## Make Targets

```bash
make help              # Show all available targets
make build             # Build binaries (development mode with debug symbols)
make build-all         # Build optimized binaries for all platforms
make test              # Run tests with coverage
make test-race         # Run tests with race detection
make test-coverage     # Generate HTML coverage report
make lint              # Run go vet + go fmt
make clean             # Clean build artifacts
make rebuild-and-commit  # Rebuild optimized binaries for all platforms
```

## Testing

### Run all tests

```bash
make test
```

### Run specific packages

```bash
go test ./internal/analyzer -v
go test ./internal/hooks -v
go test ./internal/config -v
go test ./internal/dedup -v -race
```

### Integration tests

```bash
go test ./test -v
```

### Real-Claude smoke tests

See **[docs/LOCAL_DEVELOPMENT.md](docs/LOCAL_DEVELOPMENT.md)** for supported platforms, command examples, and manual click-to-focus validation notes.

### Run a single test

```bash
go test -run TestStateMachine ./internal/analyzer -v
```

### Coverage

```bash
make test-coverage
open coverage.html
```

## CI/CD

GitHub Actions run on every push:

- **ci-ubuntu.yml** — Tests on Ubuntu
- **ci-macos.yml** — Tests on macOS
- **ci-windows.yml** — Tests on Windows
- **release.yml** — Builds and publishes release binaries

All three platform CIs must pass before merging.

## Submitting Changes

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/my-feature`
3. Make your changes
4. Run tests: `make test-race`
5. Run linter: `make lint`
6. Commit with a descriptive message following [Conventional Commits](https://www.conventionalcommits.org/):
   - `feat:` — new features
   - `fix:` — bug fixes
   - `docs:` — documentation changes
   - `test:` — adding/updating tests
   - `chore:` — maintenance tasks
7. Open a Pull Request against `main`

## Releasing

See **[Release Checklist](docs/RELEASE.md)** for the full step-by-step guide.

## Code Style

- Standard Go formatting (`go fmt`)
- Use `go vet` for static analysis
- Keep functions focused and small
- Add tests for new functionality
- Use structured logging via `internal/logging` package

## Reporting Issues

Found a bug or have a feature request? [Open an issue](https://github.com/777genius/agent-notifications/issues).
