# Notification landing

Static Nuxt 3 / Vue landing adapted from Universal Agent Plugins at commit afc94dfeb649d3e44be4c04cd12a8347ab982e1d. `components/PageBackground.vue` is copied from that project; the CSS palette, glow and panel treatment are adapted. Upstream Apache-2.0 license and notice are retained in public/. Registry/catalog/server/i18n/SDK features and unused dependencies were removed.

Node 22.12+; exact dependencies and package-lock.json. Stable versions verified against npm registry on 2026-09-09. Nuxt stays on the source project's supported major 3 rather than mixing a major migration into the adaptation.

```sh
npm ci
npm run typecheck
npm test
npm run generate
npx playwright install chromium
npm run test:browser
npm run preview
```

Production preview is http://127.0.0.1:4173/agent-notifications/. Bowser provides OS detection; mobile/unknown requires explicit target selection. It never decides runtime binary architecture. Install/update command generation lives in data/install.ts; configure uses documented product-specific instructions, not bootstrap flags.

The site does not install software or grant trust. Browser tests validate generated commands and navigation only. Notification previews are illustrations. Codex is presented as a supported product; keep platform-specific limitations factual and current.

GitHub Actions tests static production output, combines it with existing gh-pages contents and publishes through deploy-pages. Existing paths remain in the artifact; gh-pages baseline is preserved. Old Pages baseline: c1f24598233b0d55543c12a3d276175be26d3593. Rollback: switch Pages source back to the preserved gh-pages branch through repository Pages settings, or redeploy a previously qualified Pages artifact. Deployments on main are serialized.
