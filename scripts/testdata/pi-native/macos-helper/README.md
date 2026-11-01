# Signed native helper test input

This directory retains the exact signed and notarized helper archive used by
Pi's installed-product qualification. It is a test fixture; ordinary release
shipping continues to build the helper from `swift-notifier` sources.

`../macos-helper.json` binds the archive, executable, external managed-runtime
attestation and the original signing run, artifact and custody records by hash.
The records are captured from the successful owner-only
`notifier-signing-smoke.yml` run before its Actions artifact expires. The retained
archive and records make later CI independent of Actions retention.

Acquisition still verifies the signing source's `swift-notifier` tree against
the candidate tree, the exact original owner/workflow/run/attempt/artifact
identity, all signed leaf hashes, and the actual installed archive's Developer
ID signature and notarization. Stored records alone grant no desktop, sound,
focus or native qualification.

To update this fixture, run the existing restricted signing workflow on the
reviewed helper source, retain its unmodified archive and custody plus its
successful run/artifact API records, independently compare those records with
the live producer run, then update the one pin. A source change without a
matching authenticated fixture remains unqualified.
