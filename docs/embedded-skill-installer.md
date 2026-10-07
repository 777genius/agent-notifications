# Embedded skill delivery

The checksum-verified shell installer executable embeds `skills/agent-notifications/SKILL.md` through the Go `skills` package. This is the only authored source; ordinary release `go build` compiles it into the executable. No runtime source cache or additional download is used.

A validated managed sender `--entry` installs or refreshes the embedded bytes at `<runtime>/skills/agent-notifications/SKILL.md` with mode 0600 in the existing runtime Commit transaction. Here `<runtime>` is the physical parent of the target bin directory. Missing files become owned assets; existing files require the ledger's exact current regular-file identity, checked under the component lock. Untracked identical bytes, edited files and symlinks are refused. Windows hook preparation is composed into the same transaction. An unchanged
ledger-owned legacy `skills/agent-notify/SKILL.md` is removed in that transaction
when the renamed source is installed. Edited, unowned or symlinked legacy files
conflict without changing either skill. Existing client projection state remains
readable and explicit reconfiguration moves its owned legacy destination to
`skills/agent-notifications/SKILL.md` atomically.

No-entry installs and utility refreshes do not inject a skill. Native admission, signature verification, writer floors, activation and the stage allowlist are unchanged. Independent consumer removal preserves the shared source; final consumer removal uses the kernel's ownership cleanup. Explicit client setup may project this exact ledger-owned source through `clientsetup.SkillProjection`; delivery alone does not configure a client or enable notifications.

Focused adapter tests use inert staged binaries and private temporary roots, exercise the production producer and projection consumer, and never invoke the network shell installer, native helpers, applications or user projects. Full CLI activation and release qualification remain separate work.
