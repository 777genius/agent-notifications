# Architecture Documentation

## Overview

This is a professional Go rewrite of the claude-notifications plugin. The architecture follows clean architecture principles with clear separation of concerns and testability.

## Directory Structure

```
notification_plugin_go/
├── cmd/
│   └── claude-notifications/     # CLI entry point
│       └── main.go                # Main executable
├── internal/                      # Private application code
│   ├── config/                    # Configuration management
│   │   └── config.go              # Config loading, validation, defaults
│   ├── logging/                   # Structured logging
│   │   └── logging.go             # Logger implementation
│   ├── platform/                  # Cross-platform utilities
│   │   └── platform.go            # OS detection, temp dirs, file operations
│   ├── analyzer/                  # Status analysis
│   │   └── analyzer.go            # JSONL parsing, state machine
│   ├── state/                     # Session state management
│   │   └── state.go               # Per-session state, cooldown
│   ├── dedup/                     # Deduplication
│   │   └── dedup.go               # Two-phase lock mechanism
│   ├── notifier/                  # Desktop notifications
│   │   └── notifier.go            # Cross-platform notifications via beeep
│   ├── webhook/                   # Webhook integrations
│   │   └── webhook.go             # Slack, Discord, Telegram, Custom
│   ├── summary/                   # Message generation
│   │   └── summary.go             # Markdown cleanup, summarization
│   └── hooks/                     # Hook orchestration
│       └── hooks.go               # Main hook handler logic
├── pkg/                           # Public libraries
│   └── jsonl/                     # JSONL parser
│       └── jsonl.go               # Streaming JSONL parser
├── config/                        # Packaged defaults / explicit historical recovery source
│   └── config.json                # Template only; never a runtime fallback or write target
├── hooks/                         # Claude hooks
│   └── hooks.json                 # Hook definitions
├── .claude-plugin/                # Plugin metadata
│   ├── plugin.json                # Plugin manifest
│   └── marketplace.json           # Marketplace definition
└── bin/                           # Compiled binaries
    └── claude-notifications       # Main executable
```

## Shared configuration contract

The config Store owns selection and raw patch writes; see [OS paths and recovery](CONFIGURATION.md#manual-configuration). Selection is E → existing L → N, independent of adapter and resources. Hooks read without migration or writes; invalid selected config is an error, never bundle fallback. Only genuinely missing automatic config permits verified in-memory defaults after historical checks.

`config path` and safe `config inspect --json` share runtime selection. The wizard submits JSON Pointer edits with the inspect revision to `config edit --stdin --expect-revision TOKEN`; conflicts require an explicit user decision. Raw untouched fields and templates survive; the expanded/defaulted runtime Config must never be serialized back. `config init` is create-only, and `--from FILE` is explicit missing-target recovery before cache updates. Resource/permission/venv/state locations and legacy binary/plugin identifiers stay unchanged. Native OS transactional qualification is a release gate, not proven by these docs.

## Core Components

### Application and adapter boundaries

Agent event protocols stay independent. `internal/hooks` adapts Claude, Codex and
Qoder through the existing `EventSource` contract. The handler owns resource
cleanup and the shared notification sequence; `claude_policy.go`,
`codex_policy.go` and `qoder_policy.go` own classification. Claude transcript and
team behavior live alongside its policy. Persistence receives the selected
`eventKeys.stateKey`, not presentation metadata. In particular, Qoder's hashed
state must never select a Claude session file, and Qoder events cannot mutate
Claude team state.

The newer observer adapters retain their individual admission, clocks, claims
and installation leases. `internal/notification.DeliveryPort` is their shared
desktop boundary. A receipt describes effects already observed: a later panic
cannot turn a submitted desktop notification into a wholly suppressed event.
An uncertain effect is not permission to retry it.

`internal/desktopinstall` owns the platform mechanics already shared by
OpenCode, Gemini, Cursor and Copilot in VS Code: fixed native identities,
Windows shortcut staging/readiness and private macOS spool preparation. It
depends on installation and portable identity contracts, never on the notifier
or a product installer. Product registration, consent and retained native
leases remain in their product adapters. Existing OpenCode entry points delegate
to this package for compatibility; moving mechanics does not expand supported
platforms or establish physical click-to-focus qualification.

`internal/agentnotify/setupwizard` owns the installation application flow:

| Responsibility | Source |
| --- | --- |
| Public request/result contracts | `contracts.go` |
| Admission, recovery coordination and operation dispatch | `wizard.go` |
| Read-only preflight and source binding | `plan.go` |
| Bounded update/add phases with preserved opt-outs | `install_or_update.go` |
| Install and removal lifecycle | `install.go`, `uninstall.go` |
| Recorded intent and recovery | `intent.go`, `recovery.go` |
| Installed facts and binding identity | `inspect.go`, `bindings.go` |
| Concrete UAP and discovery composition | `composition.go` |
| Unit selection and caller-visible outcomes | `selection.go`, `result.go`, `failures.go` |

The CLI still parses arguments, obtains terminal confirmation and renders
results. It calls this application flow rather than choosing its own update/add
algorithm. Cursor/Local consent composition retains its existing owner and
ordering; it is not generalized into a second setup controller.

`internal/agentnotify/portablesetup` connects UAP materialization to the managed
runtime. UAP Apply and host finalization are separate stages. `ResultError`
retains the exact installer result after partial mutation or a later failure;
finalization failures keep successful activation/removal facts while the
overall operation remains incomplete. The existing recovery path remains
authoritative. Neither extraction changes the durable `installruntime`
transaction kernel or stored schemas.

The audit, scoped decisions and verification record are in the
[boundary refactor plan](plans/notification-boundaries-refactor-2026-11-04.md).

### 1. Platform Layer (`internal/platform`)

**Purpose**: Abstract OS-specific operations for cross-platform compatibility.

**Key Functions**:
- `TempDir()` - Get temporary directory (removes trailing slash on macOS)
- `FileMTime(path)` - Get file modification time (handles BSD/GNU stat differences)
- `AtomicCreateFile(path)` - Atomically create file with O_EXCL
- `FileAge(path)` - Calculate file age in seconds
- `CleanupOldFiles(dir, pattern, maxAge)` - Remove stale files

**Cross-Platform Considerations**:
- macOS: `$TMPDIR` has trailing slash → stripped
- Linux: GNU stat format (`-c %Y`)
- macOS: BSD stat format (`-f %m`)
- Windows: Git Bash/WSL compatibility

### 2. Config Layer (`internal/config`)

**Purpose**: Load, validate, and provide default configuration.

**Features**:
- JSON-based configuration
- Environment variable expansion (`${AGENT_NOTIFICATIONS_ROOT}`)
- Sensible defaults for all settings
- Validation for webhook presets, formats, required fields

**Configuration Structure**:
```go
type Config struct {
    Notifications NotificationsConfig
    Statuses      map[string]StatusInfo
}
```

### 3. JSONL Parser (`pkg/jsonl`)

**Purpose**: Parse Claude transcript files efficiently.

**Design**:
- Streaming parser (doesn't load entire file into memory)
- Tolerant to malformed lines
- Extracts tools, messages, text content
- Supports temporal window queries (last N messages)

**Key Functions**:
- `ParseFile(path)` - Parse entire JSONL file
- `GetLastAssistantMessages(messages, count)` - Get recent messages
- `ExtractTools(messages)` - Extract all tool uses with positions
- `FindToolPosition(tools, name)` - Find tool by name

### 4. Analyzer (`internal/analyzer`)

**Purpose**: Determine task status using state machine logic.

**State Machine**:
0. Text contains "Session limit reached" → `session_limit_reached` (priority check)
1. Last tool = `ExitPlanMode` → `plan_ready`
2. Last tool = `AskUserQuestion` → `question`
3. `ExitPlanMode` exists + tools after → `task_complete`
4. At least one `Read`/`Grep`/`Glob` used + no ACTIVE tools + response >200 chars → `review_complete`
5. Last tool in ACTIVE_TOOLS → `task_complete`
6. Any tool usage (fallback) → `task_complete`
7. No tools + `notifyOnTextResponse` enabled → `task_complete`

**Tool Categories**:
- **ACTIVE**: Write, Edit, Bash, NotebookEdit, SlashCommand, KillShell
- **QUESTION**: AskUserQuestion
- **PLANNING**: ExitPlanMode, TodoWrite
- **PASSIVE**: Read, Grep, Glob, WebFetch, WebSearch, Search, Fetch, Task

### 5. State Manager (`internal/state`)

**Purpose**: Manage per-session state and cooldown.

**State File Format**:
```json
{
  "session_id": "abc-123",
  "last_interactive_tool": "ExitPlanMode",
  "last_ts": 1234567890,
  "last_task_complete_ts": 1234567890,
  "cwd": "/path/to/project"
}
```

**Features**:
- Per-session state files in `$TMPDIR`
- Cooldown for question notifications after task completion
- Automatic cleanup of old state files

### 6. Dedup Manager (`internal/dedup`)

**Purpose**: Prevent duplicate notifications (workaround for Claude bug #9602).

**Two-Phase Lock Algorithm**:

**Phase 1 (Early Check)**:
```
IF lock file exists AND age < 2s:
    EXIT (duplicate)
```

**Phase 2 (Atomic Acquisition)**:
```
TRY create lock file with O_EXCL
IF created:
    PROCEED
ELSE IF lock age < 2s:
    EXIT (duplicate)
ELSE:
    REPLACE stale lock
    PROCEED
```

**Design Trade-offs**:
- ✅ Guarantees at least 1 notification
- ⚠️ Small risk (~1-2%) of 2 notifications (acceptable vs 0 notifications)
- Lock created AFTER validation checks (prevents 0 notifications on early exit)

### 7. Notifier (`internal/notifier`)

**Purpose**: Send cross-platform desktop notifications.

**Implementation**:
- Uses `github.com/gen2brain/beeep` for notifications
- Supports macOS, Linux, Windows
- Custom sound playback uses `malgo` (miniaudio) cross-platform
- Audio formats are decoded in-process via `gopxl/beep` (MP3, WAV, FLAC, OGG/Vorbis) and `go-audio/aiff` (AIFF)

### 8. Webhook Sender (`internal/webhook`)

**Purpose**: Send notifications to external services.

**Supported Presets**:

**Slack**:
```json
{
  "text": "✅ Task Completed: Created factorial function"
}
```

**Discord**:
```json
{
  "content": "✅ Task Completed: ...",
  "username": "Claude"
}
```

**Telegram**:
```json
{
  "chat_id": "123456789",
  "text": "✅ Task Completed: ..."
}
```

**Custom (JSON)**:
```json
{
  "status": "task_complete",
  "message": "...",
  "timestamp": "2025-10-18T12:34:56Z",
  "session_id": "abc-123",
  "source": "claude-notifications"
}
```

**Custom (Text)**:
```
[task_complete] Created factorial function
```

**Features**:
- 10s timeout per request
- Custom headers support
- HTTP status code validation (2xx only)
- Async sending (non-blocking)

### 9. Summary Generator (`internal/summary`)

**Purpose**: Generate concise notification messages.

**Features**:
- Markdown cleanup (removes headers, bullets, backticks)
- Whitespace normalization
- 200 character limit
- Fallback to default messages

### 10. Hook Handler (`internal/hooks`)

**Purpose**: Orchestrate all components for hook events.

**Hook Event Flow**:

**PreToolUse**:
```
1. Parse hook data (tool_name)
2. Early duplicate check
3. Determine status (ExitPlanMode → plan_ready, AskUserQuestion → question)
4. Acquire lock
5. Update session state
6. Send notifications
```

**Stop/SubagentStop**:
```
1. Parse hook data
2. Early duplicate check
3. Analyze transcript → determine status
4. Acquire lock
5. Update session state
6. Send notifications
7. Cleanup session state
```

**Notification**:
```
1. Parse hook data
2. Early duplicate check
3. Status = question (always)
4. Check cooldown
5. Acquire lock
6. Send notifications
```

## Data Flow

```
┌─────────────────┐
│  Claude    │
│  Hook Event     │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  main.go        │
│  (CLI)          │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Hook Handler   │
│  - Load config  │
│  - Parse input  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Dedup Manager  │
│  Phase 1 Check  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Analyzer       │
│  - Parse JSONL  │
│  - State mach.  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Dedup Manager  │
│  Phase 2 Lock   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  State Manager  │
│  - Update state │
│  - Cooldown     │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Summary Gen.   │
│  - Clean text   │
│  - Fallback     │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Notifier       │
│  - Desktop      │
│  - Sound        │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│  Webhook        │
│  - HTTP POST    │
│  - Async        │
└─────────────────┘
```

## Key Design Decisions

### 1. Why Two-Phase Deduplication?

**Problem**: Claude bug causes hooks to fire 2-4x for single events.

**Solution**:
- Phase 1: Fast early exit (no lock creation)
- Phase 2: Atomic lock acquisition before sending

**Rationale**: Lock created AFTER validation prevents "0 notifications" when processes exit early.

### 2. Why Temporal Window (15 messages)?

**Problem**: Old `ExitPlanMode` in history causes false "plan_ready" status.

**Solution**: Only analyze last 15 assistant messages.

**Rationale**: Recent context is more relevant; old plans have been executed.

### 3. Why beeep Library?

**Problem**: Platform-specific notification APIs are complex.

**Solution**: Use `github.com/gen2brain/beeep` for unified API.

**Benefits**:
- Single API for macOS, Linux, Windows
- Well-maintained, popular library
- Handles platform quirks automatically

### 4. Why Separate State Files?

**Problem**: Cooldown and session state need to persist between hook invocations.

**Solution**: Per-session JSON files in `$TMPDIR`.

**Benefits**:
- Fast read/write
- Automatic cleanup on session end
- No external dependencies (no database)

## Testing Strategy

### Unit Tests
- Config loading and validation
- JSONL parsing
- State machine logic
- Deduplication (with race detection)
- Cooldown calculations

### Integration Tests
- End-to-end hook handling
- Mock stdin/stdout
- Fixture-based transcript analysis

### Cross-Platform Tests
- CI matrix: macOS, Linux, Windows
- Platform-specific code paths

## Performance Considerations

### Memory
- Streaming JSONL parser (no full file in memory)
- 15-message window (bounded memory)
- Efficient string operations

### Speed
- Fast early exit on duplicates (<1ms)
- Minimal file I/O (state files only)
- Async webhook sending (non-blocking)

### Concurrency
- Goroutine-safe logging
- Atomic file operations
- No global mutable state

## Future Improvements

1. **Metrics**: Prometheus metrics for hook execution times, duplicate rates
2. **Caching**: Cache transcript analysis for session
3. **Plugins**: Extensible notifier/webhook system
4. **Config Hot Reload**: Watch config file for changes
5. **Testing**: Increase test coverage to 90%+
