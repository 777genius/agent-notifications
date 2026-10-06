# Windows selected vendor package: metadata-only checkpoint

Scope: inspect the currently published official ARM64 package in a fresh disposable Windows CI TEST root. This supplies identity evidence for a later selected-client handoff investigation; it does not qualify installation, activation, notification callbacks, chat navigation, focus or the complete P5 stage.

Official inputs:
- [Windows enterprise deployment](https://learn.chatgpt.com/docs/enterprise/windows-deployment) documents the Store-signed ARM64 MSIX URL, package name OpenAI.Codex and deployment constraints.
- [Public commands](https://learn.chatgpt.com/docs/reference/commands) documents codex://threads/<thread-id> and codex://settings. A valid format alone does not establish a Windows-local test chat or a supported installed target.

Contract:
- PR/default mode compiles the SDK helper, typechecks both harnesses and checks the Windows PowerShell script syntax. It performs no vendor download.
- Only explicit inspect_metadata dispatch fetches the fixed official URL, with bounded HTTPS redirects, 120-second abort, streaming SHA256 and a 1GiB limit. Signtool verifies the actual downloaded package before SDK parsing; the hash is checked again after inspection.
- Public IAppxManifestPackageId supplies actual name, publisher, version, architecture, full name and family name. The package manifest projection supplies applications, protocols and dependencies. No inferred publisher/PFN/version is substituted for these bytes.
- The input remains in the owned TEST root. No package registration, provisioner, licensing change, target application launch, URI, notification Show, UI input, registry write or credential import occurs.
- Reports correlate native PID and random TEST nonce. XML is limited to 2MiB, DTDs and resolvers are disabled, strings/records and output are bounded. The complete package is excluded from uploaded artifacts.

Validation: an SDK compilation or syntax check is not actual metadata acceptance. After independent source review, one explicit inspection must preserve signature status, exact source SHA, image version, package hash/size, SDK identity and public manifest evidence. Before/after hashes bracket a diagnostic read and do not promise atomic production binding. This checkpoint cannot authorize installation or prove vendor navigation; those require their own accepted contract and fresh TEST evidence.
