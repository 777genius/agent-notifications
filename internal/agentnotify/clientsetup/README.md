# Managed MCP registration core

`Apply(ctx, Request)` registers, updates or removes one Codex TOML or Claude JSON
MCP entry using the existing installer kernel and pure registration editor. It
is callable installer code; no CLI wiring, executable invocation, settings
installation, delivery opt-in, native probing or client activation occurs here.

Call with a bounded context (deadline required), explicit physical absolute clean
control/runtime/config/command paths, `Mode: clientsetup.Managed`, the provider,
and the observed nonzero `ExpectedGeneration`. The control directory and config
parent must already exist. Canonicalize trusted fixture/home aliases before the
call; this package does not follow configuration symlinks or relax kernel path
checks. The config itself may be absent and is created with mode 0600 through
kernel CAS. Existing regular-file permissions are retained. Empty existing files,
malformed documents, and documents over 1 MiB are rejected before commit.

The existing runtime must have the `existing-installer` owner and a matching
registered root. The command is trusted installer selection, verified against
ledger fingerprints: a regular executable or a bounded chain of ledger-owned
stable aliases resolving inside that runtime to a ledger-owned executable.
No executable or shell is run to establish trust. A path containing spaces is
one command value, not shell text. Arguments are fixed to `mcp-server
--integration codex` or `mcp-server --integration claude` as three separate args.

A SHA-256 consumer key binds provider and physical config path. Version 1 state
at `<control>/clientsetup-<sha256-of-consumer-key>.json` binds installation ID,
mode, runtime, config path, consumer ID and the previous pure-editor Ownership.
It is a kernel-ledger-owned 0600 file, updated with the client config in the same
recoverable transaction. The config is listed in `ConfigPaths`, so ordinary
unrelated edits are permitted between transactions. State is limited to 16 KiB,
strict JSON depth/entry bounds, exact schema keys and transport identity.
Missing, untracked, mismatched or malformed expected ownership fails closed.
The config cannot declare its own ownership. An unowned existing server is a
conflict even when its transport is identical. Edited owned command/args conflict.

All validation is repeated in kernel `Prepare` under component/config locks and
expected-generation fencing. No-op preserves config/state bytes and generation;
existing kernel lock files may still be opened. Changed documents use the pure
editor's semantic serialization: TOML comments and TOML/JSON formatting may change.
Same-server enabled=false, disabled_tools, timeouts, env and unknown fields remain;
unrelated configuration remains. Removal deletes only this server/consumer and
its state. Other consumers, hook files and native eligibility remain intact.
Final-consumer removal delegates asset cleanup/policy revocation to the kernel;
retained native callback data remains. No native cleanup is reimplemented here.

`ErrRecovery` means pending kernel work must be reconciled by the trusted
installer using `installruntime.Commit` recovery (or its `RollbackPending` option),
then a fresh snapshot/generation must be supplied. Do not delete the marker or
ownership state to repair a conflict. Kernel recovery checks per-file identities
and refuses to overwrite later foreign edits; this is recoverability, not a
promise of simultaneous external visibility across multiple files. The public
core exposes no fault injection; tests use a package-private seam into the actual
kernel's transaction/promotion/ledger boundaries. A new pending transaction that
appears after preflight is handled by the kernel's existing recovery semantics;
generation must be reread if recovery changes it.

`Mode` is the explicit ownership seam for future portable integration. Other
modes/owners and known pure-editor duplicate-command collisions fail with an
explicit conflict. This does not discover unknown plugin registrations or prove
that portable/global activation cannot overlap. Registration success is neither
client activation nor OS permission readiness. Claude session-target capability
remains separate; no chat/store/auth discovery occurs.

The bounded descriptor-relative reader supports Linux and Darwin. It supplies
only bytes/identity; kernel APIs supply private-directory validation, fingerprint
CAS, locking, writing and recovery. Other platforms fail closed pending a
qualified bounded no-follow reader. Linux tests run as nonroot with isolated
physical temp roots, including permission denial, both formats, lifecycle,
restrictions, collisions, ownership corruption, interrupted create/removal,
redo/rollback, concurrent foreign edits, native retention and final cleanup.
Darwin execution and installed client E2E are separate, unclaimed qualifications.

Codex callers may set `SkillProjection: &clientsetup.SkillProjection{SourcePath:
managedSource, DestinationPath: selectedDestination}`. The source must be exactly
`<RuntimeRoot>/skills/agent-notify/SKILL.md`, a ledger-owned regular file matching
its fingerprint, at most 64 KiB. No plugin cache, environment, cwd or client
configuration is searched for a source or destination. The destination is an
explicit absolute clean physical user path ending in `agent-notify/SKILL.md`,
outside runtime/control and distinct from the MCP config. The selected physical
skills root and `agent-notify` parent **must already exist**, prepared by the
explicit installer caller. This API creates no directories, including on reads.
Symlinks in either file path or its parents are refused. New skill files are 0600.

A projection extends per-consumer ownership to schema 2 with exact source,
destination and fingerprint. Exact schema 1 remains readable as no-skill state
and retains its original encoding until a projection is explicitly requested;
there is no adoption of an existing destination, even with identical bytes.
Schema 1 readers reject schema 2. Both versions require the exact complete JSON
shape; aliases, unknown fields, omitted fields and malformed ownership conflict.
The skill is excluded from generic ledger files and is declared with the config
locks. Relocation additionally locks and verifies the former owned destination.
Preflight determines this narrow lock set, and Prepare verifies it again under
the component lock and expected-generation fence.

Omitting `SkillProjection` preserves the existing skill bytes and ownership,
even after a managed source update. Explicit projection refreshes from that
updated source. Identical projection is a byte/state/generation no-op. Explicit
relocation removes the unchanged old copy and creates the absent new destination
in the same recoverable transaction. `Remove` removes the recorded unchanged
skill with MCP registration and state, including final-consumer cleanup. Missing,
modified, permission-changed or symlink replacements conflict; foreign data is
preserved. Recovery uses the same kernel CAS checks as config publication.

These tests qualify the bounded consumer API, not full installer wiring, shell
installer delivery, installed skill discovery, model use or client activation.
