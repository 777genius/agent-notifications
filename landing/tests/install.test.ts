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
test("all seven selections route each installer and keep consent scoped to its host", () => {
  const prefix = "curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- --product ";
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
            const expected = [];
            if (legacy) expected.push(prefix + legacy + (agentNotify ? "" : " --skip-agent-notify"));
            if (openCode) expected.push(prefix + "opencode" + (channels.desktop ? " --desktop" : "") + (channels.webhook ? " --webhook" : ""));
            const expectedSnippet = expected.length === 1
              ? expected[0]
              : "(\nset -o pipefail\n" + expected.join(" &&\n") + "\n)";
            assert.equal(command(selected, target, intent, agentNotify, channels), expectedSnippet);
          }
    assert.equal(command(selected, "linux", "configure"), null);
    assert.equal(command(selected, "manual", "install"), null);
    assert.equal(command(selected, "unknown", "install"), null);
  }
  assert.equal(command([], "linux", "install"), null);
});

test("mixed copied block is valid Bash and stops after a failed first installer", () => {
  const snippet = command(["claude", "codex", "opencode"], "linux", "install")!;
  const syntax = spawnSync("bash", ["-n"], { input: snippet, encoding: "utf8" });
  assert.equal(syntax.status, 0, syntax.stderr);
  // Replace both external commands with shell functions: no download or installation runs.
  const result = spawnSync("bash", [], {
    input: `curl() { printf 'mock installer'; }
bash() {
  case "$*" in
    *"--product both"*) printf 'legacy failed'; return 73 ;;
    *) printf 'unexpected OpenCode invocation' ;;
  esac
}
${snippet}`,
    encoding: "utf8",
  });
  assert.equal(result.status, 73, result.stderr);
  assert.equal(result.stdout, "legacy failed");
});

test("mixed block preserves download failures and skips the second installer", () => {
  const snippet = command(["claude", "opencode"], "linux", "install")!;
  for (const { response, expectedOutput } of [
    { response: "", expectedOutput: "" },
    { response: 'printf "partial installer ran"', expectedOutput: "partial installer ran" },
  ]) {
    // Real Bash consumes an empty or partial mocked download that finishes with curl failure.
    const result = spawnSync("bash", [], {
      input: `curl() { printf 'fetch attempted\n' >&2; printf '%s' '${response}'; return 22; }
${snippet}`,
      encoding: "utf8",
    });
    assert.equal(result.status, 22, result.stderr);
    assert.equal(result.stdout, expectedOutput);
    assert.equal(result.stderr, "fetch attempted\n");
  }
});

test("mixed block propagates the second installer failure without changing caller pipefail", () => {
  const snippet = command(["claude", "opencode"], "linux", "install")!;
  // Feed only a controlled shell script to the real bash -s invocation.
  const result = spawnSync("bash", [], {
    input: `set +o pipefail
curl() {
  printf '%s\n' 'case "$*" in *"--product opencode"*) printf "opencode failed"; exit 74 ;; *) printf "legacy installed\n" ;; esac'
}
${snippet}
install_status=$?
if [[ -o pipefail ]]; then printf 'caller pipefail changed'; exit 1; fi
exit "$install_status"`,
    encoding: "utf8",
  });
  assert.equal(result.status, 74, result.stderr);
  assert.equal(result.stdout, "legacy installed\nopencode failed");
});
