# Agent Notifications — AI-agent discovery (2026-11-02)

## Goal
Help CLI/AI assistants discover, understand and safely install the current local notification plugin, while preserving every release, installer and privacy boundary.

## Inspected implementation
- Nuxt 3 prerendered landing (`landing/`), hosted on agent-notifications.com and deployed from GitHub Pages.
- Existing localized SEO/head, SoftwareApplication JSON-LD with an actual free offer, sitemap/robots, guided install commands and platform-specific install docs.
- Local install with `install.sh`, and an optional notification MCP integration for Claude/Codex. No public product-auth system, remote webhook receiver API or browser mutation tool.

## Scope
- Publish concise `llms.txt`, `agents.md` and `auth.md` on the public landing, with verified installation/update, platforms, notification MCP, docs, and safety boundaries.
- Normalize per-locale canonical and `og:url` against site origin; retain existing hreflang.
- Add generated-output / public route E2E checks in the existing landing CI (no mocked success; preserve website test suite).
- No public SaaS/MCP server, OAuth discovery metadata, WebMCP, install automation without consent, pricing plans or new backend.

## Acceptance
1. Build and test all currently supported landing locales. Every authored landing HTML page has one canonical URL and one matching OpenGraph URL.
2. The public static paths have HTTP 200, correct nonempty content and no accidental HTML fallback; no secrets or internal localhost URLs.
3. The existing install wizard and installer scripts remain unchanged. No false promise of arbitrary agent support or unsupported notification behaviors.
4. All required PR CI + security/reviews pass; merge only after current head and base are verified; check post-merge deployment separately and report honestly.

## Review
- Security: never tell an agent to execute downloaded installer without the user's approval and source review; never expose private webhook destinations/tokens.
- Product: distinguish the optional **local** MCP notification tool from a hosted product API; distinguish supported platforms/agents from unconditional compatibility.
- Reliability: static route output tests must not mistake locale redirects for actual pages.
