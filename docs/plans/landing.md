# Notification landing and installation guide

## Plan
Adapt the existing Universal Agent Plugins Nuxt/Vue landing at afc94dfeb649d3e44be4c04cd12a8347ab982e1d. Preserve its dark cyan/magenta palette, grid/glow background, typography and panel treatment. Copy relevant actual source components/styles and retain attribution. Remove registry/catalog/discovery, SDK generators, server endpoints, i18n/stores and unused dependencies.

Deliver a static English landing with product hero and notification preview, honest features, focused installation wizard and FAQ. Wizard selects Claude/Codex/both, OS, install/update/configure use case. Bowser detects OS client-side; manual override always available. Unknown/mobile OS must not be silently treated as supported. Commands must use existing supported bootstrap flags, never invent CLI flags. Copy failure gives selectable text. Codex trust stays manual. Show Codex beta and unpublished >=1.42 prerequisite honestly.

## Critique and corrections before implementation
- Existing Pages is legacy gh-pages hosting: do not silently overwrite existing content. Inspect gh-pages and preserve existing paths or deploy only after collision audit. Build preview artifact before publishing website. Website deployment is user-authorized; product releases are not.
- Project Pages base /agent-notifications/ must work for assets, navigation and reload. Test production static output, not only dev server.
- Browser OS is a suggestion, not runtime authority. Keep architecture decisions in installer. Windows instructions explicitly Git Bash, not WSL or PowerShell curl pipe.
- Do not copy the source landing wholesale with signed registry build dependencies. Narrow copied shell/components, preserve licenses; audit absence of irrelevant branding/links.
- Release state must not depend on unreliable client GitHub API or claim draft publicly installable. Static explicit beta/prerequisite message and release link.
- Use exact stable package versions checked against registry, reuse upstream stack's supported major versions. Avoid adding component frameworks not used in UI.
- No commands executed on user projects/profiles. Browser E2E checks generated commands and UI only. Existing installer qualification remains separate evidence.

## Verification / acceptance
Build, typecheck, meaningful command/OS unit tests; Playwright production static page: agent x OS x intent, copy success/failure, OS override, mobile/unknown, keyboard, narrow viewport overflow, no console/hydration errors, reduced-motion and base-path asset correctness. Inspect desktop/mobile screenshots. CI reproducible frozen lockfile. Deployment retains existing content and rollback uses previous Pages commit/artifact. README links landing only once deployed and verified.

## Boundaries
No Go/runtime/installer/release version changes. Own landing/, dedicated Pages workflow and minimal README entry. Target under ~2000 changed authored LOC; generated lockfile explained separately. No analytics/backend/auth. Independent plan critique and implementation review before integration.

## Independent critique adopted
Configure is not a bootstrap flag. Claude settings command belongs in Claude chat; Codex-only users receive documented shared configuration-file guidance and never Claude slash commands. Install and update intentionally share the same supported command; aftercare differs. Fresh unknown/mobile visitors must choose a supported target OS before a copy command is offered. Generated command matrix has one source of truth, tested against documented actual --product contract. Avoid misleading standalone GUI preview as real captured delivery evidence.
Additional accepted review: beta/release prerequisite appears beside codex AND both commands. OS detection never overwrites manual selection; iPad desktop UA and Android/Linux handled as unsupported mobile. Preview explicitly illustrative. No exact-tab promise on Windows; iTerm2 targeting prerequisite documented. E2E expected commands are literal verified contract strings independent of generator. Capture old gh-pages SHA, serialize deploys, verify preserved path inventory, rollback via reverting deployment commit.

## Implemented and verified
Hosted worker produced first reusable wizard/background after ~3 minutes, then exhausted its selected identities; main completed the landing without spawning a replacement local coding agent. Builds/tests ran in the hosted sandbox. npm stable versions checked: Nuxt3.21.11, Vue3.5.42, Bowser2.14.1, Playwright1.63.0; Node22 types22.20.2. Typecheck, unit matrix and five production browser tests pass. Desktop1280px and mobile390px screenshots inspected.

Deployment review corrected legacy GITHUB_TOKEN-push trap: use official deploy-pages with Pages build_type=workflow. Rebuild legacy Jekyll routes into artifact before overlay; preserve original source and generated document bytes, except intentional new site route replacements. gh-pages itself remains unchanged and is rollback source. Product releases remain drafts and untouched.
