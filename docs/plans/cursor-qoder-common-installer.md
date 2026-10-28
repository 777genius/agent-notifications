# Cursor and Qoder common installer delivery plan

Scope: Cursor CLI and Qoder only. No Pi work, no VS Code changes, and no legacy Qoder installation migration or adoption.

## Reviewed design

Two independent architecture and scope critics reviewed the current host and UAP implementations before implementation. Their findings changed the design in these ways:

- Keep Cursor's qualified single-target UAP wizard and physical authority. The common installer orchestrates it within a mixed, immutable confirmed selection instead of expanding Cursor's internal group contract.
- Bind every Qoder UAP native CLI operation to the explicit canonical selected profile. A registry addition alone would lose custom-profile authority.
- Give UAP sole ownership of Qoder package delivery and native activation/removal. Keep shared runtime and notification consent in Notifications, with a receipt referencing the exact UAP installation/binding and verified native package snapshot.
- Update the managed event reader, inspect and removal together with installation so a successful install cannot leave delivery silently disabled.
- Preserve explicit channel opt-outs and complete saved per-product choices on update. Refuse foreign/edited native state and retired custom Qoder footprints.

## Delivery sequence

1. Add and verify the minimal profile-bound Qoder adapter change in UAP; open its PR after the first reviewable commit, review, fix, and merge it.
2. Pin Notifications to the merged immutable UAP revision.
3. Replace the Qoder custom registry writer with UAP lifecycle composition and the matching runtime/consent receipt.
4. Extend the public loader, its published mirror, bootstrap, product discovery/capability protocol and immutable child admission for Cursor in mixed selections. Preserve one understandable common selection, consent and result.
5. Update README, installation/Cursor/Qoder guides and the existing landing's actual multiselect/command generation.
6. Exercise focused UAP/host ownership and consent tests, public combined installer E2E, retained Cursor physical gates, landing tests/build and independent changed-code reviews. Fix legitimate findings and merge after required CI passes.

## Acceptance evidence

- Explicit nondefault Qoder profile is used for list, collision checks, install, verify, update and uninstall; default profile remains untouched.
- UAP and runtime ledgers never both own the native Qoder package/settings. Consent stays off until activation and the host receipt succeed; removal revokes consent before native effects.
- Cursor retains selected vendor executable versus owned observer executable separation, original profile/hook-file authority, projection acknowledgement and platform qualification.
- Combined public entry cannot omit a selected client or mark an unexecuted client complete. Omitted update channels preserve both-off; explicit false flags disable selected channels.
- Cancellation before approval creates no persistent state. Partial completed effects are reported honestly after later failures.
- No migration/adoption framework, Pi changes, VS Code dedicated changes or local dependency replacements are left behind.

Fixture-backed installer tests, retained native qualification and actual native desktop evidence will be reported separately. This work does not publish a release or broaden notification/navigation claims.
