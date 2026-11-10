# PR7 portable locator and entrypoint slice

The projected package invokes its notification binary with exactly
`portable-launch --locator agent-notify-<64 lowercase SHA256 hex>.json`.
Only PLUGIN_DATA is used to select identity; PLUGIN_ROOT is passed through but
never interpreted as a client identifier. HOME, PATH, cwd and MCP clientInfo
cannot select the integration. Each launcher keeps a lightweight stdio MCP adapter, so caller metadata stays
specific to each chat and each call. Adapters share one local backend per exact
registered binding and installed-primary SHA256. Separate providers/profiles and
runtime epochs use separate services. No chat-count limit of eight exists.

Private endpoint state lives below `PLUGIN_DATA/.shared-mcp`. A starter validates
the complete installed snapshot under the existing setup lease before executing
`portable-shared-daemon`; the daemon holds a singleton lock and independently
validates its executable and expected SHA256. Metadata-only discovery never
authorizes execution. Legacy `portable-primary` remains compatible.

The backend listens only on an ephemeral IPv4 loopback port with pinned mutual
TLS 1.3. The random credential stays in private owner-only state, never in argv
or on the wire. No service registration, setup, repair, or permission action is
performed by this route. See `../sharedrpc` and `../sharedstate` for transport and
filesystem boundaries. Unsupported platforms fail closed.

## Plan and consumer contract

This is the first implementation slice of LAUNCHER-PROMPT.md, the PR7 plan, and
PORTABLE-CONTRACT.md (the successful UAP spike at
/tmp/pr7-portable-spike-ww36typd). Next comes the setup-owned UAP stager/activator
decorator, discovery handoff and consumer removal integration. Those are not
implemented here.

Binding.Registration is a pure API returning an exact existing-kernel consumer
key/record and locator bytes. Binding.Filename derives the immutable selector
from the complete canonical JSON, including explicit integration, UAP install,
binding and scope IDs, component ID, owner and all physical root identities.
Shared data therefore contains independent per-binding files. The next writer
must independently validate the committed UAP binding/data receipt, snapshot the
existing installation, then use the existing component transaction/CAS API to
register that exact consumer and publish the 0600 locator. This API alone does
not authorize a writer. Never write an arbitrary installation marker as authority.

Registration uses Consumer.Registration for the canonical binding document and
Consumer.Commands for the one primary path; it adds no duplicate ledger and no
kernel schema extension. The primary filename must select a fingerprinted,
regular executable in Ledger.RuntimeRoot. Setup must select the installed
platform binary, not the existing installer’s symlink alias. Locator bytes are
delivery metadata, not a second authoritative managed file: do not add their
removable PLUGIN_DATA path to Ledger.Files, since cache loss must not invalidate
the permanent installation.

Each elected service startup checks the current installed snapshot and acquires the existing
setup lease (disabled notification policy still permits MCP status). Current
owner, installation, runtime, consumer record, recovery, generation/policy floor
and fingerprints are checked. Per-request policy reads check the binding again;
native delivery retains the existing installed lease. Binding does not freeze
the component generation, so unrelated consumer updates do not stale survivors.
Consumer removal/replacement revokes the binding; an old snapshot cannot acquire
a lease after a concurrent managed change. Managed primary refresh may retain
the same path; an immutable binding change requires new projected argv/locator.
The next writer must revoke through the component kernel before deleting the
locator or forwarding UAP removal.

## Security and runtime boundaries

The reader walks absolute clean physical directories with no-follow openat,
then opens one exact file nonblocking/no-follow. It checks owner, mode, type,
link count and a 16 KiB size/read bound. JSON rejects duplicate keys, aliases,
unknown fields, trailing values and invalid identifiers. The data directory is
private; permanent roots reject group/other writes. Parent symlinks are rejected,
including common macOS aliases: setup must provide physical paths.

A five-second context bounds lock acquisition; regular filesystem syscalls,
including the existing kernel's fingerprint reads, have no hard wall-time bound.
The launch lease covers Start and is released before the primary acquires its
own lease, avoiding a child-start deadlock. The backend is detached from any one chat and exits after ten idle minutes.
Closing a bridge does not terminate peers. Calls reconnect before transmitting
content if the backend has exited; a transmitted notification is never silently
resent. Shutdown drains handlers before removing its own endpoint record.
Binding removal or primary refresh invalidates the old service, including replay
requests; policy snapshots also verify the same primary epoch before delivery. Security assumes the existing kernel's
trusted same-user setup boundary: this does not sandbox a malicious process
already able to rewrite private owner state outside the component lock.

MCP uses explicit permanent ControlRoot/GlobalConfig and the same service,
journal and spool defaults. Package removal after start does not relocate state.
No new Linux native capability is introduced: the Linux subprocess bridge uses
an injected fixed boot clock and a native factory that panics if invoked, with
actual runtime/MCP composition and read-only status calls. Production retains
its existing unsupported-platform behavior where the native clock is absent.

## Qualification

Focused tests cover both integrations in shared data, exact argv, minimal
two-variable child environments, spaces, removal of package/data, revocation,
lease exclusion, canceled acquisition, strict JSON and unsafe filesystem
objects, owner/floor/recovery/primary failures, MCP stdout purity and signal
cancellation. The production composition bridge copies the test executable into
fresh package and permanent-runtime roots; it never invokes a client or native
backend. No installed activation, UAP decorator, discovery collision/handoff,
real native retention or full E2E qualification is claimed. Darwin execution
and unsupported-platform behavior beyond the existing contract remain separate
platform gates.

## Shared-service qualification

`TestPortableSharedDaemonLifecycle` runs real stdio bridge and backend processes
in a fresh temporary installation. It checks 32 simultaneous clients, exactly one
backend, per-call contexts, restart through an already initialized bridge, closing
all bridges, private state removal, protocol-clean stdout and process cleanup.
`AGENT_NOTIFY_SHARED_STRESS=1` adds 64 and 128 clients. These tests use an injected
clock and prohibit native delivery; desktop display/click confirmation remains a
separate installed-client check. RPC tests exercise real service/journal replay
with an injected OS boundary and uncertain transport outcomes without resending.
