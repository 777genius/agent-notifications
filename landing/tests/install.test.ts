import { test } from "node:test";
import assert from "node:assert/strict";
import { command, detectTarget } from "../data/install.ts";
test("one-line setup contract for each product and supported target", () => {
  for (const product of ["claude", "codex", "both"] as const)
    for (const target of ["macos", "linux", "windows"] as const) {
      const expected =
        "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product " +
        product;
      assert.equal(command(product, target, "install"), expected);
      assert.equal(command(product, target, "update"), expected);
      assert.equal(
        command(product, target, "install", false),
        expected + " --skip-agent-notify",
      );
      assert.equal(command(product, target, "configure"), null);
    }
  assert.equal(command("claude", "unknown", "install"), null);
  assert.equal(command("both", "manual", "update"), null);
});
test("Bowser OS suggestions and mobile exclusions", () => {
  assert.equal(
    detectTarget(
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
    ),
    "windows",
  );
  assert.equal(
    detectTarget(
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15",
    ),
    "macos",
  );
  assert.equal(
    detectTarget(
      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
    ),
    "linux",
  );
  assert.equal(
    detectTarget(
      "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/130.0.0.0 Mobile Safari/537.36",
    ),
    "unknown",
  );
  assert.equal(
    detectTarget(
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15",
      5,
    ),
    "unknown",
  );
  assert.equal(detectTarget("unknown"), "unknown");
});

test("OpenCode command requires explicit selected channels and omits MCP flags", () => {
  for (const target of ["macos", "linux", "windows"] as const) {
    const prefix = "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product opencode";
    assert.equal(command("opencode", target, "install"), prefix + " --desktop");
    assert.equal(command("opencode", target, "update", false, { desktop: false, webhook: true }), prefix + " --webhook");
    assert.equal(command("opencode", target, "install", true, { desktop: true, webhook: true }), prefix + " --desktop --webhook");
    assert.equal(command("opencode", target, "install", true, { desktop: false, webhook: false }), null);
    assert.equal(command("opencode", target, "configure"), null);
  }
  assert.equal(command("opencode", "manual", "install"), null);
  assert.equal(command("opencode", "unknown", "install"), null);
});
