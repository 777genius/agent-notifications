# Contributing

Thank you for your interest in contributing to Agent Notifications!

## Contributor License Agreement

You keep copyright in what you write.

GitHub's Terms of Service already license a pull request under the repository license, which is GPL-3.0-or-later. That is not enough if the project later needs Apache-2.0 or a commercial license that includes your work.

This CLA keeps your copyright and grants the project owner a transferable license to sublicense that work, including commercially, while still keeping the GPL-3.0-or-later grant that applied when you submitted it. Read [.github/CLA.md](.github/CLA.md).

Sign before we merge: add your legal name, GitHub login, email, and date to the **CLA signatures** table in the same PR or in a first-time CLA-only PR. Use the GitHub account that will author the commits. Every human listed in `Co-authored-by` trailers must also be on the table. If a company owns the work, the row must name that entity.

Maintainers: do not merge a human contribution until every human author is on the CLA signatures table. Bots such as Dependabot do not sign.

## Prerequisites

- **Go 1.21+** (tested with 1.25)
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
