import { test } from "node:test";
import assert from "node:assert/strict";
import { command, detectTarget } from "../data/install.ts";
test("verified bootstrap contract for each product and supported target", () => {
  for (const product of ["claude", "codex", "both"] as const)
    for (const target of ["macos", "linux", "windows"] as const) {
      const install = command(product, target, "install");
      assert.equal(command(product, target, "update"), install);
      assert.match(install ?? "", /^\(\n  set -euo pipefail\n/);
      assert.match(install ?? "", /\/releases\/latest/);
      assert.match(install ?? "", /\/commits\/\$tag/);
      assert.match(install ?? "", /\$commit\/bin/);
      assert.match(install ?? "", /python3 -I -c/);
      assert.ok((install ?? "").includes('v+"\\n"'));
      assert.ok(!(install ?? "").includes('v+"\\\\n"'));
      assert.match(install ?? "", new RegExp(`--product ${product}\\n\\)$`));
      assert.doesNotMatch(install ?? "", /\/main\/bin\/bootstrap\.sh/);
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
