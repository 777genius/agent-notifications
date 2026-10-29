// TEMPORARY DIAGNOSTIC ONLY. Remove with the timing instrumentation.
import assert from "node:assert/strict";
import { copyFileSync, mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { test } from "node:test";

test("temporary trace forwards only bounded fixed codes into the private TEST witness", async t => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "TEST-pi-windows-desktop-")));
  const previous = process.env.TEST_PI_WITNESS;
  t.after(() => { if (previous === undefined) delete process.env.TEST_PI_WITNESS; else process.env.TEST_PI_WITNESS = previous; rmSync(root, { recursive: true, force: true }); });
  mkdirSync(join(root, "case-diagnostic"));
  const filename = join(root, "case-diagnostic/witness.jsonl");
  writeFileSync(filename, "");
  process.env.TEST_PI_WITNESS = filename;
  writeFileSync(join(root, "package.json"), '{"type":"module"}');
  const source = join(root, "index.ts");
  copyFileSync(new URL("../portable-package/com.universal-agent-plugins.pi/index.ts", import.meta.url), source);
  const { recordTestDiagnostic } = await import(pathToFileURL(source));
  const valid = { type: "TEST_pi_native_trace", phase: "consume_returned", channel: "", elapsed_ms: 15001, remaining_ms: 0,
    context: "deadline_exceeded", receipt: { status: "submitted", reason: "hook_handoff", desktop: "submitted", webhook: "expired" } };
  const sender = { type: "TEST_pi_native_trace", phase: "winrt_ready", channel: "desktop", elapsed_ms: 4000,
    remaining_ms: 11000, context: "active" };
  const invalid = ["PRIVATE_RAW_URL_HEADERS_CONFIG", { ...valid, phase: "PRIVATE_PHASE" }, { ...valid, headers: "PRIVATE_HEADER" },
    { ...valid, elapsed_ms: "PRIVATE_TIME" }, { ...valid, remaining_ms: 15001 }, { ...valid, phase: [valid.phase] },
    { ...valid, channel: ["desktop"] }, { ...valid, context: [valid.context] },
    { ...valid, receipt: { ...valid.receipt, reason: "PRIVATE_REASON" } },
    { ...valid, receipt: { ...valid.receipt, user_text: "PRIVATE_TEXT" } }];
  recordTestDiagnostic(invalid.map(value => typeof value === "string" ? value : JSON.stringify(value)).concat(JSON.stringify(valid), JSON.stringify(sender)).join("\n"));
  assert.deepEqual(readFileSync(filename, "utf8").trim().split("\n").map(JSON.parse),
    [valid, sender].map(trace => ({ type: "TEST_pi_native_trace", trace })));
  const before = readFileSync(filename, "utf8");
  recordTestDiagnostic("PRIVATE_OVERSIZED".repeat(3000) + "\n" + JSON.stringify(valid));
  assert.equal(readFileSync(filename, "utf8"), before);
});
