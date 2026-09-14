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
  for (const [name, product] of [
    ["Claude Code", "claude"],
    ["Codex CLI · beta", "codex"],
    ["Both agents", "both"],
  ]) {
    await page.getByRole("button", { name, exact: true }).click();
    for (const os of ["macos", "linux", "windows"]) {
      await chooseOS(page, os);
      for (const intent of ["Install", "Update"]) {
        if (
          await page.getByRole("button", { name: intent, exact: true }).count()
        )
          await page.getByRole("button", { name: intent, exact: true }).click();
        await expect(page.getByLabel(intent + " command")).toHaveValue(
          "curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh |\nbash -s -- --product " +
            product,
        );
      }
      if (os === "windows")
        await expect(
          page.getByText("Run in Git Bash on Windows.", { exact: true }),
        ).toBeVisible();
    }
    if (product !== "claude")
      await expect(
        page.getByText("Codex CLI beta — release prerequisite.", {
          exact: true,
        }),
      ).toBeVisible();
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
  await page.getByRole("button", { name: "Both agents", exact: true }).click();
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
  await page.getByRole("button", { name: "Codex CLI · beta", exact: true }).click();
  await chooseOS(page, "windows");
  await expect(page.getByLabel("Install command")).toHaveValue(/--product codex$/);
  await chooseLanguage(page, /Current language/, "简体中文");
  await expect(page).toHaveURL(/\/agent-notifications\/zh\/?\?source=i18n#features$/);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("保持专注");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page).toHaveTitle("Agent Notifications - 专注工作，及时获知进展");
  await expect(page.locator('meta[name="description"]')).toHaveAttribute(
    "content",
    /桌面通知/,
  );
  await expect(page.locator('meta[property="og:type"]')).toHaveAttribute("content", "website");
  expect(
    await page.locator('script[type="application/ld+json"]').textContent(),
  ).toContain("SoftwareApplication");
  await expect(page.getByLabel("安装命令")).toHaveValue(/--product codex$/);
  await expect(page.getByText("请在 Windows 的 Git Bash 中运行。", { exact: true })).toBeVisible();
  expect((await page.context().cookies()).find((cookie) => cookie.name === "agent_notifications_locale")?.value).toBe("zh");

  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("保持专注");
  await chooseLanguage(page, /当前语言/, "English");
  await expect(page).toHaveURL(/\/agent-notifications\/?\?source=i18n#features$/);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Stay in flow");
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
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Stay in flow");
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
  await expect(page.getByText("No languages found", { exact: true })).toBeVisible();
  await search.press("Escape");
  await expect(search).toHaveCount(0);
  await trigger.click();
  await page.locator("main").click({ position: { x: 5, y: 5 } });
  await expect(page.getByRole("searchbox", { name: "Search languages" })).toHaveCount(0);
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
  for (let i = 0; i < 7; i++) {
    for (const title of await cards.locator("h3").allTextContents())
      seen.add(title);
    await page.clock.fastForward(3400);
    await page.clock.runFor(600);
  }
  expect([...seen].sort()).toEqual(
    [
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
  await page.getByRole("button", { name: "Both agents", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "Claude installation help ↗" }),
  ).toBeVisible();
  await context.close();
});
