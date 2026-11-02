import { expect, test } from "@playwright/test";
import { supportedLocales } from "../../data/i18n";

test("machine discovery routes have real public text and no HTML fallback", async ({ request }) => {
  const endpoints = [
    { path: "/llms.txt", type: "text/plain", markers: ["# Agent Notifications", "agents.md", "agent-notifications.com/install.sh"] },
    { path: "/auth.md", type: "text/markdown", markers: ["# Authentication", "No Agent Notifications website account", "no public REST"] },
    { path: "/agents.md", type: "text/markdown", markers: ["# Using Agent Notifications", "Ask permission", "Git Bash", "local"] },
  ] as const;

  for (const endpoint of endpoints) {
    const response = await request.get(endpoint.path, { maxRedirects: 0 });
    expect(response.status(), endpoint.path).toBe(200);
    expect(response.headers()["content-type"], endpoint.path).toContain(endpoint.type);
    const body = await response.text();
    expect(body).not.toContain("<!doctype html>");
    for (const marker of endpoint.markers) expect(body, endpoint.path).toContain(marker);
  }
});

test("published locale HTML has unique canonical and matching Open Graph URL", async ({ request }) => {
  for (const locale of supportedLocales.map(({ code }) => code === "en" ? "" : `${code}/`)) {
    const path = "/" + locale;
    const response = await request.get(path, { maxRedirects: 0 });
    expect(response.status(), path).toBe(200);
    const html = await response.text();
    const expected = `https://agent-notifications.com/${locale}`;
    const tags = [...html.matchAll(/<link\b[^>]*>/gi)].map((m) => m[0]);
    const canonical = tags.filter((tag) => /\brel="canonical"/.test(tag));
    expect(canonical, path).toHaveLength(1);
    expect(canonical[0]).toContain(`href="${expected}"`);
    const alternates = tags.filter((tag) => /\brel="alternate"/.test(tag) && /\bhreflang="/.test(tag));
    expect(alternates.some((tag) => tag.includes(`href="${expected}"`)), path).toBe(true);
    const ogTags = [...html.matchAll(/<meta\b[^>]*>/gi)].map((m) => m[0]).filter((tag) => /\bproperty="og:url"/.test(tag));
    expect(ogTags, path).toHaveLength(1);
    expect(ogTags[0]).toContain(`content="${expected}"`);
    expect(html).toContain('application/ld+json');
    expect(html).toContain('SoftwareApplication');
  }
});
