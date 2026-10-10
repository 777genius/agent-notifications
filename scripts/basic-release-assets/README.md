# Basic release seal and resume

The manual `basic-release.yml` operator accepts stable `vMAJOR.MINOR.PATCH` tags.
Before dispatch, prepare all five source versions and the changelog, explicitly
exclude that tag from `release.yml` in both candidate C and operator main O, and
complete independent review plus current-C source CI. The existing signing and
five-platform artifact checks remain mandatory. This helper does not authorize
publication, Latest, macOS/Codex E2E, installed business or full native claims.

A fresh dispatch takes `candidate_sha`, `release_tag`, and `signing_run`. It
builds each platform once, verifies signed helper/portable ZIP custody, then
uploads an immutable Actions artifact named
`basic-release-sealed-<run ID>-<attempt>` **before** any remote tag or draft
mutation. The seal contains 29 public assets, their hashes/sizes, C, producer O,
version, run/attempt, and hashed qualification/signing/helper receipts. Retention
is 14 days; expiry requires a new reviewed build, never a reconstructed seal.

If tag/draft/upload fails, dispatch with the same three inputs plus both
`resume_run` and `resume_attempt` naming the original sealed attempt. This skips
all builds, signing imports and artifact qualification jobs. It downloads only
that attempt's immutable seal and revalidates all bytes and receipts. A later
main controller may resume it only when the authenticated original producer O
is its ancestor. The operator retains both producer O and current controller O,
manifest digest and origin/ancestry receipts in
`basic-draft-operation-<current run ID>-<current attempt>`. A compare response
that omits commits (including the API's default 250-commit limit) fails closed.
Rerunning the original workflow without the explicit resume inputs is a fresh
attempt, not a supported substitute for resume.

Tag, draft and uploads are separate stages. Each requires tag == C and a draft
with the exact tag/target and no prerelease. Existing asset names must be unique,
expected, `uploaded`, and identical in size and SHA256. Matching assets are
skipped; missing assets are uploaded; any mismatch, unexpected name, published
release or ambiguous remote read stops the job. SHA256 uses GitHub's asset digest
when available, otherwise downloads the bytes once per immutable asset ID in
that stage. There is no clobber, deletion, rebuild, publish or Latest mutation.

After a failed write, the helper reads remote state before considering a retry.
A successful read proving the intended object is still absent permits at most
one additional write. A failed read stops immediately. An accepted write whose
response was lost is recognized and skipped. All stages revalidate the local
seal before their first write.

Run local transport/contract verification with Node 24 or later:

```sh
node --test scripts/basic-release-assets/assets.test.mts
```

These behavioral tests use a TEST-only fake `gh` executable from a private temp
directory outside the checkout. They use no GitHub token or network and perform
no real release, agent, VM, native registration or provider action. They cover
interruption after server-side upload acceptance and explicit resume without
duplicates, lost responses, bounded absence retry, digest fallback, corrupted
bytes/receipts, remote conflicts, and producer/controller provenance rejection.

A meaningful strict typecheck requires existing Node declarations, for example:

```sh
tsc7 --noEmit --strict --module nodenext --target es2023 \
  --allowImportingTsExtensions --types node --typeRoots /path/to/node_modules/@types \
  scripts/basic-release-assets.mts scripts/basic-release-assets/*.mts
```

No production dependencies are added. Node's TypeScript stripping is the runtime
mechanism and does not replace this typecheck. Workflow YAML/Bash syntax and fake
gh verification do not claim a live Actions/release test; that remains a future
explicit release operation.
