# Organization transfer preparation

The repository remains `777genius/agent-notifications`. The planned destination is
`universal-agent-plugins/agent-notifications`; transfer requires a separate owner command.
Canonical download URLs, Go module paths, plugin names, Apple team/bundle identity,
Pages/DNS and provider bindings are unchanged.

Release/signing guards allow exactly these two repository slugs while retaining
`777genius` as both actor and triggering actor. Existing event, ref, source SHA,
run and attempt checks still apply. No other organization member gains signing
or publication authority from the new owner name.

New sealed manifests record the actual `GITHUB_REPOSITORY`. Historical seals keep
their original bytes, repository, SHA, run and attempt. Under the planned new
repository, release tooling can read an old seal and directs API requests to the
current repository. A new seal cannot be used from the old repository context;
other repositories are rejected.

Before transfer, apply the same guard compatibility to the executable
`release/macos-signing` branch through its normal protected delivery path after
its active qualification/release has finished. A main-branch change alone does
not update that branch. Do not merge main wholesale over an active release.

Remaining transfer work includes organization eligibility, GitHub Apps and runner
bindings, signing/environment policies, Pages domain verification, canonical URL
updates and post-transfer qualification. The old
`777genius.github.io/agent-notifications/install.sh` Pages entrypoint is an
unresolved compatibility gap: repository transfer does not preserve that URL.
No DNS, Pages, GitHub App, provider or repository transfer action is performed by
this preparation.
