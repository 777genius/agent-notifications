import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
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

// A regression here would silently omit a selected host or leak one host's flags to another.
test("all seven selections produce one loader command with host-scoped consent", () => {
  const cases = [
    { selected: ["claude"], legacy: "claude", openCode: false },
    { selected: ["codex"], legacy: "codex", openCode: false },
    { selected: ["opencode"], legacy: null, openCode: true },
    { selected: ["claude", "codex"], legacy: "both", openCode: false },
    { selected: ["claude", "opencode"], legacy: "claude", openCode: true },
    { selected: ["codex", "opencode"], legacy: "codex", openCode: true },
    { selected: ["claude", "codex", "opencode"], legacy: "both", openCode: true },
  ] as const;
  for (const { selected, legacy, openCode } of cases) {
    for (const target of ["macos", "linux", "windows"] as const)
      for (const intent of ["install", "update"] as const)
        for (const agentNotify of [true, false])
          for (const channels of [
            { desktop: true, webhook: false },
            { desktop: false, webhook: true },
            { desktop: true, webhook: true },
            { desktop: false, webhook: false },
          ]) {
            if (openCode && !channels.desktop && !channels.webhook) {
              assert.equal(command(selected, target, intent, agentNotify, channels), null);
              continue;
            }
            const product = openCode && legacy
              ? `--products ${selected.join(",")}`
              : `--product ${openCode ? "opencode" : legacy}`;
            const pipeline = "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- " + product
              + (legacy && !agentNotify ? " --skip-agent-notify" : "")
              + (openCode && channels.desktop ? " --desktop" : "")
              + (openCode && channels.webhook ? " --webhook" : "");
            const expected = openCode && legacy ? `(set -o pipefail; ${pipeline})` : pipeline;
            const actual = command(selected, target, intent, agentNotify, channels);
            assert.equal(actual, expected);
            assert.equal(actual?.split("\n").length, 1);
          }
    assert.equal(command(selected, "linux", "configure"), null);
    assert.equal(command(selected, "manual", "install"), null);
    assert.equal(command(selected, "unknown", "install"), null);
  }
  assert.equal(command([], "linux", "install"), null);
});


// Selection order must not change the public dispatch order or duplicate a host.
test("mixed loader uses a canonical product list", () => {
  assert.equal(
    command(["opencode", "claude", "opencode", "codex"], "linux", "install"),
    "(set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --products claude,codex,opencode --desktop)",
  );
});

// The copied shell command must report a failed curl even if Bash accepts its input.
test("mixed single command propagates empty and partial download failures without changing caller pipefail", () => {
  const snippet = command(["claude", "opencode"], "linux", "install")!;
  for (const partial of [false, true]) {
    const result = spawnSync("bash", [], {
      input: `set +o pipefail
curl() { ${partial ? "printf '%s' 'printf partial-input'" : ":"}; return 22; }
${snippet}
install_status=$?
if [[ -o pipefail ]]; then printf 'caller pipefail changed'; exit 1; fi
exit "$install_status"`,
      encoding: "utf8",
    });
    assert.equal(result.status, 22, result.stderr);
    assert.equal(result.stdout, partial ? "partial-input" : "");
  }
});
