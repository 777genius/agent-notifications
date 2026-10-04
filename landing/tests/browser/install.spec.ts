import { test, expect, type Page } from "@playwright/test";
async function chooseOS(page: Page, value: string) {
  const labels: Record<string, string> = {
    macos: "macOS",
    linux: "Linux",
    windows: "Windows · Git Bash",
    manual: "Manual instructions",
  };
  if (
    !(await page
      .getByRole("combobox", { name: "Target operating system" })
      .isVisible())
  )
    await page.getByRole("button", { name: "Change", exact: true }).click();
  await page.getByRole("combobox", { name: "Target operating system" }).click();
  await page.getByRole("option", { name: labels[value], exact: true }).click();
}
async function chooseAgents(page: Page, selected: readonly ("claude" | "codex" | "opencode" | "gemini")[]) {
  const labels = { claude: "Claude", codex: "Codex CLI", opencode: "OpenCode", gemini: "Gemini CLI" };
  // Select desired cards first so switching hosts never needs an empty selection.
  for (const wanted of [true, false])
    for (const value of ["claude", "codex", "opencode", "gemini"] as const) {
      const card = page.getByRole("button", { name: labels[value], exact: true });
      if (selected.includes(value) === wanted &&
          (await card.getAttribute("aria-pressed")) !== String(wanted))
        await card.click();
    }
}
async function chooseProduct(page: Page, product: "claude" | "codex" | "both") {
  await chooseAgents(page, product === "both" ? ["claude", "codex"] : [product]);
}
async function chooseLanguage(page: Page, current: RegExp, language: string) {
  await page.getByRole("button", { name: current }).click();
  await page.getByRole("option", { name: language, exact: true }).click();
}
test("production command matrix, aftercare, clipboard and configuration", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("");
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "Stay in flow",
  );
  for (const product of ["claude", "codex", "both"] as const) {
    await chooseProduct(page, product);
    for (const os of ["macos", "linux", "windows"]) {
      await chooseOS(page, os);
      for (const intent of ["Install", "Update"]) {
        if (
          await page.getByRole("button", { name: intent, exact: true }).count()
        )
          await page.getByRole("button", { name: intent, exact: true }).click();
        const value = await page.getByLabel(intent + " command").inputValue();
        expect(value).toBe(
          product === "both"
            ? "(set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --products claude,codex)"
            : `curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product ${product}`,
        );
      }
      if (os === "windows")
        await expect(
          page.getByText("Run in Git Bash on Windows.", { exact: true }),
        ).toBeVisible();
    }
    await page.getByRole("button", { name: "Configure", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Copy command" }),
    ).toHaveCount(0);
    if (product === "codex")
      await expect(
        page.getByText("/claude-notifications-go:settings", { exact: true }),
      ).toHaveCount(0);
  }
  await page.getByRole("button", { name: "Install", exact: true }).click();
  await chooseOS(page, "linux");
  const agentNotify = page.getByRole("checkbox", {
    name: /Let agents send you notifications when they need your attention/,
  });
  await expect(agentNotify).toBeChecked();
  await expect(page.getByLabel("Install command")).toHaveValue(
    /--products claude,codex\)$/,
  );
  await agentNotify.uncheck();
  await expect(page.getByLabel("Install command")).toHaveValue(
    /--skip-agent-notify\)$/,
  );
  await agentNotify.check();
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.getByRole("button", { name: "Copy command" }).click();
  await expect(page.getByRole("status")).toContainText("Copied");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    "--products claude,codex",
  );
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async () => {
          throw new Error("denied");
        },
      },
    });
  });
  await page.getByRole("button", { name: "Copy command" }).click();
  await expect(page.getByRole("status")).toContainText("Copy unavailable");
  await expect(page.getByLabel("Install command")).toBeFocused();
  expect(errors).toEqual([]);
});
test("unknown target, manual route and mobile layout", async ({ browser }) => {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    userAgent: "unknown",
    reducedMotion: "reduce",
  });
  const page = await context.newPage();
  await page.goto("http://127.0.0.1:4173/");
  await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(
    0,
  );
  await chooseOS(page, "manual");
  await expect(
    page.getByRole("link", { name: "Manual Claude installation", exact: true }),
  ).toBeVisible();
  await chooseOS(page, "windows");
  await expect(
    page.getByRole("button", { name: "Copy command" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await page.screenshot({ path: "test-results/mobile.png", fullPage: true });
  await context.close();
});
test("keyboard navigation, root-path reload and desktop screenshot", async ({
  page,
}) => {
  await page.goto("");
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("link", { name: "Skip to content" }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
    "href",
    "https://agent-notifications.com/",
  );
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
    "content",
    "https://agent-notifications.com/agent-notifications-logo.png",
  );
  for (const asset of await page
    .locator("script[src]")
    .evaluateAll((nodes) => nodes.map((n) => (n as HTMLScriptElement).src)))
    expect(new URL(asset).pathname).toMatch(/^\/_nuxt\//);
  await page.screenshot({ path: "test-results/desktop.png", fullPage: true });
});
test("pending clipboard completion cannot claim a different command was copied", async ({
  page,
}) => {
  await page.addInitScript(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: () =>
          new Promise<void>((resolve) => {
            (window as any).finishCopy = resolve;
          }),
      },
    }),
  );
  await page.goto("");
  await chooseOS(page, "linux");
  await page.getByRole("button", { name: "Copy command" }).click();
  await chooseProduct(page, "both");
  await page.evaluate(() => (window as any).finishCopy());
  await expect(page.getByRole("status")).not.toContainText("Copied");
  await expect(page.getByLabel("Install command")).toHaveValue(
    /--products claude,codex\)$/,
  );
});
test("assets load, hydration is clean and reduced motion disables background animation", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error" || /hydration/i.test(msg.text()))
      errors.push(msg.text());
  });
  page.on("response", (r) => {
    if (r.status() >= 400 && r.url().includes("127.0.0.1"))
      errors.push(r.url());
  });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("");
  await page.waitForLoadState("networkidle");
  const geminiLogo = page.locator(".supported-agents").getByRole("img", { name: "Gemini CLI", exact: true });
  await expect(geminiLogo).toBeVisible();
  await expect.poll(() => geminiLogo.evaluate((image) =>
    image instanceof HTMLImageElement && image.complete && image.naturalWidth > 0 && image.naturalHeight > 0,
  )).toBe(true);
  expect(
    await page
      .locator(".page-bg__orb")
      .evaluateAll((nodes) =>
        nodes.every((n) => getComputedStyle(n).animationName === "none"),
      ),
  ).toBe(true);
  expect(
    await page.locator("h1").evaluate((n) => getComputedStyle(n).fontSize),
  ).not.toBe("32px");
  await expect(page.locator(".notification-card").first()).toContainText(
    /main · (checkout-service|patient-portal|payments-api|mobile-app|analytics-pipeline|customer-dashboard|design-system)/,
  );
  expect(errors).toEqual([]);
});
test("background follows the pointer and scroll, sections reveal and motion preference resets them", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await page.goto("");
  const orb = page.locator(".page-bg__orb--1");
  const feature = page.locator(".feature").first();
  const translation = () => orb.evaluate((node) => {
    // CSS serializes a zero Y component as a single value.
    const [x = "0", y = "0"] = getComputedStyle(node).translate.split(" ");
    return [Number.parseFloat(x), Number.parseFloat(y)];
  });
  await expect(feature).toHaveCSS("opacity", "0");
  const viewport = page.viewportSize()!;
  await page.mouse.move(20, 200);
  await expect.poll(async () => (await translation())[0]).toBeLessThan(-40);
  await page.mouse.move(viewport.width - 20, 200);
  await expect.poll(async () => (await translation())[0]).toBeGreaterThan(40);

  await page.mouse.move(viewport.width / 2, viewport.height / 2);
  await expect.poll(async () => Math.abs((await translation())[1])).toBeLessThan(1);
  await page.evaluate(() => window.scrollTo({ top: 700, behavior: "instant" }));
  await expect.poll(async () => (await translation())[1]).toBeGreaterThan(60);
  await feature.scrollIntoViewIfNeeded();
  await expect(feature).toHaveCSS("opacity", "1");
  await expect(feature).toHaveCSS("translate", "none");

  // A live accessibility preference change stops motion and reveals remaining content.
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(orb).toHaveCSS("translate", "none");
  await expect(orb).toHaveCSS("animation-name", "none");
  await expect(page.locator(".page-bg__grid")).toHaveCSS("background-position", /^0px 0px(?:, 0px 0px)*$/);
  await expect(page.locator(".closing")).toHaveCSS("opacity", "1");
  await page.mouse.move(20, 200);
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
  await expect(page.locator(".page-bg__grid")).toHaveCSS("background-position", /^0px 0px(?:, 0px 0px)*$/);
});

test("language switch localizes content, URL, metadata and persists the choice", async ({
  page,
}) => {
  await page.goto("?source=i18n#features");
  await chooseProduct(page, "codex");
  await chooseOS(page, "windows");
  await expect(page.getByLabel("Install command")).toHaveValue(
    /--product codex$/,
  );
  await chooseLanguage(page, /Current language/, "简体中文");
  await expect(page).toHaveURL(
    /\/zh\/?\?source=i18n#features$/,
  );
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "保持专注",
  );
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
    "href",
    /^https:\/\/agent-notifications\.com\/zh\/?$/,
  );
  await expect(page).toHaveTitle(
    "Agent Notifications - 专注工作，及时获知进展",
  );
  await expect(page.locator('meta[name="description"]')).toHaveAttribute(
    "content",
    /桌面通知/,
  );
  await expect(page.locator('meta[property="og:type"]')).toHaveAttribute(
    "content",
    "website",
  );
  expect(
    await page.locator('script[type="application/ld+json"]').textContent(),
  ).toContain("SoftwareApplication");
  await expect(page.getByLabel("安装命令")).toHaveValue(/--product codex$/);
  await expect(
    page.getByText("请在 Windows 的 Git Bash 中运行。", { exact: true }),
  ).toBeVisible();
  expect(
    (await page.context().cookies()).find(
      (cookie) => cookie.name === "agent_notifications_locale",
    )?.value,
  ).toBe("zh");

  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "保持专注",
  );
  await chooseLanguage(page, /当前语言/, "English");
  await expect(page).toHaveURL(
    /\/\?source=i18n#features$/,
  );
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "Stay in flow",
  );
});
test("failed locale payload keeps the working language and reports the error", async ({
  page,
}) => {
  await page.route("**/_i18n/**/zh/messages.json", (route) => route.abort());
  await page.goto("");
  await chooseLanguage(page, /Current language/, "简体中文");
  await expect(page.getByRole("alert")).toContainText(
    "Unable to change language",
  );
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "Stay in flow",
  );
  await expect(page).toHaveURL(/^http:\/\/127\.0\.0\.1:4173\/$/);
});
test("language menu is searchable, keyboard accessible and closes outside", async ({
  page,
}) => {
  await page.goto("");
  const trigger = page.getByRole("button", { name: /Current language/ });
  await trigger.press("ArrowDown");
  const search = page.getByRole("searchbox", { name: "Search languages" });
  await expect(search).toBeFocused();
  await search.fill("zh-CN");
  await expect(page.getByRole("option", { name: "简体中文" })).toBeVisible();
  await expect(page.getByRole("option", { name: "English" })).toHaveCount(0);
  await search.fill("missing");
  await expect(
    page.getByText("No languages found", { exact: true }),
  ).toBeVisible();
  await search.press("Escape");
  await expect(search).toHaveCount(0);
  await trigger.click();
  await page.locator("main").click({ position: { x: 5, y: 5 } });
  await expect(
    page.getByRole("searchbox", { name: "Search languages" }),
  ).toHaveCount(0);
});
test("notification sequence covers statuses and agents, pause and reduced motion", async ({
  page,
}) => {
  await page.clock.install();
  await page.goto("");
  await page.getByRole("button", { name: "Pause", exact: true }).click();
  const cards = page.locator(".notification-card");
  const initial = await cards.allTextContents();
  await page.clock.fastForward(7000);
  expect(await cards.allTextContents()).toEqual(initial);
  await page.getByRole("button", { name: "Resume", exact: true }).click();
  const seen = new Set<string>();
  for (let i = 0; i < 11; i++) {
    for (const title of await cards.locator("h3").allTextContents())
      seen.add(title);
    await page.clock.fastForward(3400);
    await page.clock.runFor(600);
  }
  expect([...seen].sort()).toEqual(
    [
      "OpenCode",
      "❓ Question",
      "📋 Plan",
      "✅ Completed",
      "🔍 Review",
      "🔐 Permission Request",
      "⏱️ Session Limit Reached",
      "🔴 API Error: 401",
    ].sort(),
  );
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeVisible();
  const staticCards = await cards.allTextContents();
  await page.clock.fastForward(10000);
  expect(await cards.allTextContents()).toEqual(staticCards);
  await page.getByRole("button", { name: "Next", exact: true }).click();
  expect(await cards.allTextContents()).not.toEqual(staticCards);
});
test("installation order, sticky header and custom select keyboard behavior", async ({
  page,
}) => {
  await page.goto("");
  expect(
    await page
      .locator('main > :not([aria-hidden="true"])')
      .evaluateAll((nodes) =>
        nodes.map((n) => n.id || n.className).slice(0, 3),
      ),
  ).toEqual(["hero-wrap", "compatibility", "install"]);
  for (const title of await page.locator("h1,h2,h3").allTextContents())
    expect(title).not.toContain(".");
  await chooseOS(page, "linux");
  const select = page.getByRole("combobox", {
    name: "Target operating system",
  });
  await select.focus();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("option", { name: "Linux", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Home");
  await expect(
    page.getByRole("option", { name: "Choose target OS", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(
    page.getByRole("option", { name: "macOS", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(select).toContainText("macOS");
  await expect(select).toBeFocused();
  await page.evaluate(() =>
    window.scrollTo({ top: 1200, behavior: "instant" }),
  );
  expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(500);
  expect(
    await page
      .locator(".header")
      .evaluate((n) => Math.abs(n.getBoundingClientRect().top)),
  ).toBeLessThan(1);
  for (const logo of await page
    .locator(".agent-logo")
    .evaluateAll((nodes) =>
      nodes.map((n) => (n as HTMLImageElement).naturalWidth),
    ))
    expect(logo).toBeGreaterThan(0);
  for (const logo of await page
    .locator(".brand-icon, .notification-brand-logo")
    .evaluateAll((nodes) =>
      nodes.map((n) => (n as HTMLImageElement).naturalWidth),
    ))
    expect(logo).toBeGreaterThan(0);
});

test("hero headline remains a single unclipped line at narrow widths", async ({
  page,
}) => {
  for (const width of [320, 390, 768, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("");
    const box = await page.locator("h1 em").boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
    expect(box!.height).toBeLessThan(76);
  }
});
test("guided reference layout, detected OS and mode focus", async ({
  browser,
}) => {
  const context = await browser.newContext({
    viewport: { width: 1088, height: 900 },
    userAgent:
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
    reducedMotion: "reduce",
  });
  const page = await context.newPage();
  await page.goto("http://127.0.0.1:4173/");
  await expect(page.locator(".os-summary")).toContainText("macOS");
  await expect(page.locator(".os-summary")).toContainText(
    "Detected automatically",
  );
  await expect(
    page.getByRole("button", { name: "Back", exact: true }),
  ).toHaveCount(0);
  await page
    .locator("#install")
    .screenshot({ path: "test-results/installation-reference-desktop.png" });
  await page.getByRole("button", { name: "Configure", exact: true }).click();
  await expect(page.locator("#install-title")).toBeFocused();
  await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "Install", exact: true }).click();
  await chooseOS(page, "windows");
  await expect(page.locator(".os-summary")).toContainText("Selected");
  await expect(page.locator(".os-summary")).not.toContainText(
    "Detected automatically",
  );
  await expect(
    page.getByRole("heading", { name: "Run in Git Bash", exact: true }),
  ).toBeVisible();
  await chooseProduct(page, "both");
  await expect(
    page.getByRole("link", { name: "Claude installation help ↗" }),
  ).toBeVisible();
  await context.close();
});

test("all agents toggle independently, copied commands and configuration cover the selection", async ({ page }) => {
  await page.goto("");
  await chooseOS(page, "macos");
  const labels = { claude: "Claude", codex: "Codex CLI", opencode: "OpenCode", gemini: "Gemini CLI" };
  const prefix = "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product ";
  const cases = [
    { selected: ["claude"], expected: prefix + "claude" },
    { selected: ["claude", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,opencode --desktop)" },
    { selected: ["claude", "codex", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --desktop)" },
    { selected: ["codex", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "codex,opencode --desktop)" },
    { selected: ["opencode"], expected: prefix + "opencode --desktop" },
    { selected: ["codex"], expected: prefix + "codex" },
    { selected: ["claude", "codex"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex)" },
  ] as const;
  for (const { selected, expected } of cases) {
    await chooseAgents(page, selected);
    for (const value of ["claude", "codex", "opencode", "gemini"] as const)
      await expect(page.getByRole("button", { name: labels[value], exact: true }))
        .toHaveAttribute("aria-pressed", String((selected as readonly string[]).includes(value)));
    await expect(page.getByLabel("Install command", { exact: true })).toHaveValue(expected);
    if (selected.length === 1) {
      await page.getByRole("button", { name: labels[selected[0]], exact: true }).click();
      await expect(page.getByLabel("Install command", { exact: true })).toHaveValue(expected);
    }
  }
  await chooseAgents(page, ["claude", "codex", "opencode"]);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.getByRole("button", { name: "Copy command" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --desktop)");
  const table = page.getByRole("table", { name: "Compare agent features" });
  await expect(table.getByRole("columnheader")).toHaveText(["Feature", "Claude", "Codex CLI", "OpenCode", /Gemini CLI\s*Unreleased/]);
  await expect(table.getByRole("row", { name: /^Completed/ }).getByRole("cell")).toHaveText(["✓Supported", "✓Supported", "✓Supported", "✓Supported"]);
  await expect(table.getByRole("row", { name: /^Review/ }).getByRole("cell")).toHaveText(["✓Supported", "✕Not supported", "✕Not supported", "✕Not supported"]);
  await expect(table.getByRole("row", { name: /^Sounds/ }).getByRole("cell")).toHaveText(["✓Supported", "✓Supported", "✕Not supported", "✕Not supported"]);
  await expect(table.getByRole("row", { name: /^Question/ }).getByRole("cell")).toHaveText(["✓Supported", "✓*Supported with limitations", "✓Supported", "✕Not supported"]);
  await expect(table.getByRole("row", { name: /^Errors/ }).getByRole("cell")).toHaveText(["✓Supported", "✓*Supported with limitations", "✓Supported", "✕Not supported"]);
  const compatibility = page.locator(".agent-support details");
  const qualification = compatibility.getByText("Codex: Windows hook delivery and the question tool hook are not yet qualified in live sessions.", { exact: true });
  await expect(qualification).not.toBeVisible();
  await expect(compatibility.getByText(/^\* Codex questions depend/)).not.toBeVisible();
  await expect(compatibility.getByText(/^OpenCode: silent completion/)).not.toBeVisible();
  await expect(page.getByText(/Tested with OpenCode 1.18.33/)).not.toBeVisible();
  await page.locator(".agent-support summary").click();
  await expect(qualification).toBeVisible();
  await expect(compatibility.getByText(/^\* Codex questions depend/)).toBeVisible();
  await expect(compatibility.getByText(/^OpenCode: silent completion/)).toBeVisible();
  await expect(page.getByText(/Tested with OpenCode 1.18.33/)).toBeVisible();
  await expect(page.getByRole("link", { name: "What is OpenCode V2? ↗" })).toHaveAttribute("href", "https://opencode.ai/v2/docs");
  await expect(page.getByRole("group", { name: "OpenCode notification channels" })).toHaveCount(0);
  const agentNotify = page.getByRole("checkbox", { name: /Let agents send/ });
  await agentNotify.uncheck();
  await expect(page.getByLabel("Install command", { exact: true })).toHaveValue("(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --skip-agent-notify --desktop)");
  await page.getByRole("button", { name: "Configure", exact: true }).click();
  const configuration = page.locator(".configuration");
  await expect(configuration.getByText("/claude-notifications-go:settings", { exact: true })).toBeVisible();
  await expect(configuration.getByText("config path", { exact: true })).toBeVisible();
  await expect(configuration.getByText(/Use config path for shared settings/)).toBeVisible();
  await page.getByRole("button", { name: "Install", exact: true }).click();
  await expect(page.getByText(/Restart OpenCode to load the global plugin/)).toBeVisible();
  await chooseAgents(page, ["opencode"]);
  await expect(agentNotify).toHaveCount(0);
  await expect(page.getByLabel("Install command", { exact: true })).toHaveValue(prefix + "opencode --desktop");
});

test("first feature explains supported click-to-focus and its agent scope", async ({ page }) => {
  await page.goto("");
  const feature = page.locator("#features article").first();
  await expect(feature.getByRole("heading", { name: "Return with one click" })).toBeVisible();
  await expect(feature).toContainText("CLICK TO FOCUS");
  await expect(feature).toContainText("terminal, editor or tab where supported");
  await expect(feature).toContainText("Claude and Codex CLI only");
  await expect(feature).toContainText("terminal and OS");
});

// Revealed sections must still be reachable through a direct URL and navigation.
test("installation deep link and navigation expose the comparison and four agent logos", async ({ page }) => {
  await page.goto("#install");
  await expect(page).toHaveURL(/#install$/);
  await expect(page.locator("#install")).toBeInViewport();
  await expect(page.getByRole("table", { name: "Compare agent features" })).toBeVisible();
  await expect(page.locator(".supported-agents").getByRole("img")).toHaveCount(4);
  const nav = page.getByRole("navigation");
  await nav.getByRole("link", { name: "Features", exact: true }).click();
  await expect(page).toHaveURL(/#features$/);
  await nav.getByRole("link", { name: "Install", exact: true }).click();
  await expect(page).toHaveURL(/#install$/);
  await expect(page.locator("#install")).toBeInViewport();
});

test("Gemini selection explains pending release and configuration without offering a released command", async ({ page }) => {
  await page.goto("");
  await chooseOS(page, "linux");
  const table = page.getByRole("table", { name: "Compare agent features" });
  await expect(table.getByRole("columnheader").last()).toContainText("Unreleased");
  for (const [feature, supported] of [
    [/^Completed/, true], [/^Permission Request/, true], [/^Webhooks/, true],
    [/^Question/, false], [/^Errors/, false], [/^Sounds/, false],
    [/^Click-to-focus/, false], [/^Plan/, false], [/^Review/, false], [/^Session Limit/, false],
  ] as const) {
    await expect(table.getByRole("row", { name: feature }).getByRole("cell").last())
      .toHaveText(supported ? "✓Supported" : "✕Not supported");
  }
  const details = page.locator(".agent-support details");
  await expect(details.getByText(/A completed turn does not imply success or a final answer/)).not.toBeVisible();
  await page.locator(".agent-support summary").click();
  await expect(details).toContainText("A completed turn does not imply success or a final answer");
  await expect(details).toContainText("silent turn-completed and tool-permission alerts");
  for (const selected of [["gemini"], ["gemini", "opencode"], ["claude", "codex", "opencode", "gemini"]] as const) {
    await chooseAgents(page, selected);
    await expect(page.getByLabel("Install command", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
    await expect(page.locator(".setup-panel[role=status]")).toContainText("Public release 1.46.1 does not include Gemini");
    await expect(page.getByRole("group", { name: "Observer notification channels" })).toHaveCount(0);
    await page.getByRole("button", { name: "Configure", exact: true }).click();
    const config = page.locator(".configuration");
    await expect(config.getByRole("link", { name: "Gemini candidate setup and limits" })).toHaveAttribute("href", /docs\/gemini-notifications.md$/);
    await expect(config).toContainText("no MCP tool or skill is installed");
    await expect(config).toContainText("webhook-only; built-in settings are not changed");
    if (selected.length === 1)
      await expect(page.getByRole("checkbox", { name: /Let agents send/ })).toHaveCount(0);
    await page.getByRole("button", { name: "Install", exact: true }).click();
  }
  await chooseAgents(page, ["claude"]);
  await expect(page.getByLabel("Install command", { exact: true })).toHaveValue(/--product claude$/);
});

// Regression: selecting Gemini must preserve the other selected agents' manual links.
test("mixed Gemini selection keeps Claude and Codex manual instructions", async ({ page }) => {
  for (const intent of ["Install", "Update"]) {
    await page.goto("");
    await chooseOS(page, "macos");
    await chooseAgents(page, ["claude", "codex"]);
    // Update is a real action for the published agents. Select it before adding
    // the unreleased Gemini candidate, which suppresses the public command.
    if (intent === "Update")
      await page.getByRole("button", { name: "Update", exact: true }).click();
    await chooseAgents(page, ["claude", "codex", "gemini"]);
    await chooseOS(page, "manual");
    const manual = page.locator(".setup-panel.instructions");
    await expect(manual.locator('a[href$="docs/INSTALLATION.md#manual-install"]')).toBeVisible();
    await expect(manual.locator('a[href$="docs/CODEX.md#manual-codex-registration"]')).toBeVisible();
    await expect(manual.getByRole("link", { name: "Gemini candidate setup and limits" })).toBeVisible();
    await expect(manual).toContainText("Public release 1.46.1 does not include Gemini");
    await expect(page.getByLabel(intent + " command", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
  }
});

// A regression here would clip feature comparisons or restore channel setup friction.
test("agent comparison stays readable on mobile and keeps all agents visible", async ({ page }) => {
  for (const width of [320, 390, 1088]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("");
    await chooseOS(page, "linux");
    await chooseAgents(page, ["opencode"]);
    const table = page.getByRole("table", { name: "Compare agent features" });
    await expect(table.getByRole("columnheader")).toHaveCount(5);
    await expect(table.getByRole("row")).toHaveCount(11);
    await expect(page.getByRole("checkbox")).toHaveCount(0);
    await expect(page.getByLabel("Install command", { exact: true })).toHaveValue(/--product opencode --desktop$/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    for (const logo of await table.locator("img").evaluateAll((nodes) => nodes.map((node) => (node as HTMLImageElement).naturalWidth)))
      expect(logo).toBeGreaterThan(0);
    await page.locator(".agent-support").screenshot({ path: `test-results/agent-support-${width}.png` });
  }
});
