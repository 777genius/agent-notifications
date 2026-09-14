# Draft qualification — 2026-09-11

Worktree `/private/tmp/notification-agent-notify-e2e`, branch `feat/agent-notify-e2e`. This qualifies local exact-head bytes for the isolated E2E matrix. It is not a release artifact, not updater input, and not permission to publish.

## Environment

| Field | Value |
| --- | --- |
| OS | macOS darwin 24.6.0 arm64 |
| Go | go1.25.1 darwin/arm64 (`/opt/homebrew/bin/go`, `GOTOOLCHAIN=local`) |
| UAP module | `github.com/777genius/plugin-kit-ai/install/integrationctl v0.0.0-20260910100557-6af7f412cb4a` |
| UAP zip sum | `h1:6d6MndaSdF8mD0QIDrsJs/tIZMoUSiCfNfEu5OMYtP0=` |
| Test isolation | `HOME=/tmp/agent-notify-e2e-home`, `TMPDIR=/tmp/agent-notify-e2e-tmp` |

## Exact-head native helper

Path: `swift-notifier/.build/arm64-apple-macosx/release/terminal-notifier-modern`

| Field | Value |
| --- | --- |
| File | Mach-O 64-bit executable arm64 |
| SHA-256 | `c1ef7778579228396dc55f958b926c74e61f49197cd3745e9886ba29d53c9f2a` |
| Info.plist bundle ID | `com.claude.desktop.notifier` |
| Signature | adhoc, linker-signed; TeamIdentifier not set |
| CDHash sha256 | `a1c7e4be0e9454b39b65f51670e9dd6917a80a9e` |
| Isolated-flow resign | Default: `codesign --force --sign - --timestamp=none --identifier com.agentnotify.test.flow`. Production identifier `com.claude.desktop.notifier` only when `AGENT_NOTIFY_DARWIN_E2E=1`. |

`--capabilities-json`:

```json
{"actionKinds":["none","desktop_thread_v1"],"backend":"macos.usernotifications","receiptSupport":true,"protocolVersions":[1],"explicitFeatureEnabledByDefault":false,"schemaVersion":1}
```

Legacy argv `--help` remains a help path and must not be a successful send. Structured `--send-json` is the delivery path used by the isolated flow.

## Claimed vs unavailable

Claimed capabilities and honest unavailable rows are in `plan14-local-e2e-2026-09-11.md`. Publish of this helper, a Developer ID signed build, or updater distribution is out of scope until the owner explicitly permits a named version.
