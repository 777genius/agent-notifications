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
  const labels = { claude: "Claude Code", codex: "Codex CLI", opencode: "OpenCode", gemini: "Gemini CLI" };
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
          `curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product ${product}`,
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
    /--product both$/,
  );
  await agentNotify.uncheck();
  await expect(page.getByLabel("Install command")).toHaveValue(
    /--skip-agent-notify$/,
  );
  await agentNotify.check();
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.getByRole("button", { name: "Copy command" }).click();
  await expect(page.getByRole("status")).toContainText("Copied");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
    "--product both",
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
  await page.goto("http://127.0.0.1:4173/agent-notifications/");
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
test("keyboard navigation, base-path reload and desktop screenshot", async ({
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
  for (const asset of await page
    .locator("script[src]")
    .evaluateAll((nodes) => nodes.map((n) => (n as HTMLScriptElement).src)))
    expect(asset).toContain("/agent-notifications/");
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
    /--product both$/,
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
    /\/agent-notifications\/zh\/?\?source=i18n#features$/,
  );
  await expect(page.getByRole("heading", { level: 1 })).toContainText(
    "保持专注",
  );
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
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
    /\/agent-notifications\/?\?source=i18n#features$/,
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
  await expect(page).toHaveURL(/\/agent-notifications\/?$/);
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
      .locator("main > *")
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
  await page.goto("http://127.0.0.1:4173/agent-notifications/");
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
  const labels = { claude: "Claude Code", codex: "Codex CLI", opencode: "OpenCode", gemini: "Gemini CLI" };
  const prefix = "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product ";
  const cases = [
    { selected: ["claude"], expected: prefix + "claude" },
    { selected: ["claude", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,opencode --desktop)" },
    { selected: ["claude", "codex", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --desktop)" },
    { selected: ["codex", "opencode"], expected: "(set -o pipefail; " + prefix.replace("--product ", "--products ") + "codex,opencode --desktop)" },
    { selected: ["opencode"], expected: prefix + "opencode --desktop" },
    { selected: ["codex"], expected: prefix + "codex" },
    { selected: ["claude", "codex"], expected: prefix + "both" },
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
  const info = page.getByRole("group", { name: "Selected agent capabilities" });
  await expect(info.getByRole("heading")).toHaveCount(3);
  await expect(info).toContainText("Completion and permission alerts");
  await expect(info).toContainText("Silent completion, question, permission and error alerts for root sessions");
  await expect(page.getByText(/Tested with OpenCode 1.18.33/)).not.toBeVisible();
  await info.locator("article").filter({ has: page.getByRole("heading", { name: "OpenCode", exact: true }) })
    .getByText("Compatibility details", { exact: true }).click();
  await expect(page.getByText(/Tested with OpenCode 1.18.33/)).toBeVisible();
  const channels = page.getByRole("group", { name: "Observer notification channels" });
  await expect(channels).toContainText("configure webhook URLs separately");
  await expect(channels.getByRole("checkbox")).toHaveCount(2);
  const controls = await channels.locator("label").evaluateAll((nodes) => nodes.map((node) => {
    const rect = node.getBoundingClientRect();
    return { x: rect.x, right: rect.right, y: rect.y, bottom: rect.bottom };
  }));
  expect(controls[1].x >= controls[0].right + 8 || controls[1].y >= controls[0].bottom + 8).toBe(true);
  const agentNotify = page.getByRole("checkbox", { name: /Let agents send/ });
  await agentNotify.uncheck();
  await expect(page.getByLabel("Install command", { exact: true })).toHaveValue("(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --skip-agent-notify --desktop)");
  const desktop = page.getByRole("checkbox", { name: "Allow desktop notifications", exact: true });
  const webhook = page.getByRole("checkbox", { name: "Allow webhook notifications", exact: true });
  await webhook.check();
  await desktop.uncheck();
  await expect(page.getByLabel("Install command", { exact: true })).toHaveValue("(set -o pipefail; " + prefix.replace("--product ", "--products ") + "claude,codex,opencode --skip-agent-notify --webhook)");
  await webhook.uncheck();
  await expect(page.getByLabel("Install command", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
  await desktop.check();
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
  await expect(feature).toContainText("Claude Code and Codex CLI only");
  await expect(feature).toContainText("terminal and OS");
});

test("Gemini selection explains pending release, shared consent and configuration without offering a released command", async ({ page }) => {
  await page.goto("");
  await chooseOS(page, "linux");
  for (const selected of [["gemini"], ["gemini", "opencode"], ["claude", "codex", "opencode", "gemini"]] as const) {
    await chooseAgents(page, selected);
    const info = page.getByRole("group", { name: "Selected agent capabilities" });
    const gemini = info.locator("article").filter({ has: page.getByRole("heading", { name: "Gemini CLI", exact: true }) });
    await expect(gemini).toContainText("A completed turn does not imply success or a final answer");
    await expect(page.getByLabel("Install command", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
    await expect(page.locator(".setup-panel[role=status]")).toContainText("Public release 1.46.0 does not include Gemini");
    const channels = page.getByRole("group", { name: "Observer notification channels" });
    await expect(channels).toContainText("consent saved separately");
    await channels.getByRole("checkbox", { name: "Allow desktop notifications" }).uncheck();
    await channels.getByRole("checkbox", { name: "Allow webhook notifications" }).uncheck();
    await expect(page.getByText("Choose at least one notification channel.", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
    await channels.getByRole("checkbox", { name: "Allow desktop notifications" }).check();
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
  await page.goto("");
  await chooseAgents(page, ["claude", "codex", "gemini"]);
  await chooseOS(page, "manual");
  for (const intent of ["Install", "Update"]) {
    await page.getByRole("button", { name: intent, exact: true }).click();
    const manual = page.locator(".setup-panel.instructions");
    await expect(manual.locator('a[href$="docs/INSTALLATION.md#manual-install"]')).toBeVisible();
    await expect(manual.locator('a[href$="docs/CODEX.md#manual-codex-registration"]')).toBeVisible();
    await expect(manual.getByRole("link", { name: "Gemini candidate setup and limits" })).toBeVisible();
    await expect(manual).toContainText("Public release 1.46.0 does not include Gemini");
    await expect(page.getByLabel(intent + " command", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Copy command" })).toHaveCount(0);
  }
});
