import { readFile } from "node:fs/promises";
import { test } from "node:test";
import assert from "node:assert/strict";
import { isLocaleCode, supportedLocales } from "../data/i18n.ts";

type Messages = Record<string, unknown>;

async function loadMessages(file: string): Promise<Messages> {
  return JSON.parse(
    await readFile(new URL(`../locales/${file}`, import.meta.url), "utf8"),
  ) as Messages;
}

function flatten(value: unknown, prefix = ""): Map<string, string> {
  const entries = new Map<string, string>();
  assert.ok(value && typeof value === "object" && !Array.isArray(value));
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof child === "string") {
      assert.ok(child.trim(), `${path} must not be empty`);
      entries.set(path, child);
    } else {
      for (const [nestedPath, text] of flatten(child, path))
        entries.set(nestedPath, text);
    }
  }
  return entries;
}

function placeholders(value: string): string[] {
  return [...value.matchAll(/\{([^{}]+)\}/g)].map((match) => match[1]!).sort();
}

test("every locale has the same non-empty keys and placeholders", async () => {
  const [reference, ...translations] = await Promise.all(
    supportedLocales.map(async (locale) => ({
      locale: locale.code,
      messages: flatten(await loadMessages(locale.file)),
    })),
  );
  assert.ok(reference);

  for (const translation of translations) {
    assert.deepEqual(
      [...translation.messages.keys()].sort(),
      [...reference.messages.keys()].sort(),
      `${translation.locale} keys must match ${reference.locale}`,
    );
    for (const [key, source] of reference.messages)
      assert.deepEqual(
        placeholders(translation.messages.get(key)!),
        placeholders(source),
        `${translation.locale}:${key} placeholders must match`,
      );
  }
});

test("locale manifest accepts only published locale codes", () => {
  assert.equal(isLocaleCode("en"), true);
  assert.equal(isLocaleCode("zh"), true);
  assert.equal(isLocaleCode("zh-CN"), false);
  assert.equal(isLocaleCode("ru"), true);
  assert.equal(isLocaleCode(null), false);
});

test("crawler files publish every localized route", async () => {
  const [robots, sitemap] = await Promise.all([
    readFile(new URL("../public/robots.txt", import.meta.url), "utf8"),
    readFile(new URL("../public/sitemap.xml", import.meta.url), "utf8"),
  ]);
  assert.match(robots, /Sitemap: https:\/\/777genius\.github\.io\/agent-notifications\/sitemap\.xml/);
  for (const path of ["agent-notifications/", ...supportedLocales.filter((locale) => locale.code !== "en").map((locale) => `agent-notifications/${locale.code}/`)])
    assert.match(sitemap, new RegExp(`<loc>https://777genius\\.github\\.io/${path}</loc>`));
  assert.match(sitemap, /hreflang="x-default"/);
  for (const locale of supportedLocales)
    assert.match(sitemap, new RegExp(`hreflang="${locale.code === "en" ? "en" : locale.language}"`));
});
