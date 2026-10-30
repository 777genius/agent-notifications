// TEMPORARY MACOS DIAGNOSTIC ONLY. Remove with the instrumentation.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { test } from "node:test";

for (const prefix of ["TEST-pi-installed-macos-", "TEST-pi-windows-desktop-"])
test(`temporary diagnostic retains only closed codes in ${prefix}`, async t => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), prefix)));
  const previous = { TEST_PI_WITNESS: process.env.TEST_PI_WITNESS, TEST_PI_NATIVE_DIAGNOSTIC: process.env.TEST_PI_NATIVE_DIAGNOSTIC };
  t.after(() => { for (const [key, value] of Object.entries(previous)) { if (value === undefined) delete process.env[key]; else process.env[key] = value; } rmSync(root, { recursive: true, force: true }); });
  // F.prepare creates this intermediate parent with ordinary directory mode;
  // the enclosing TEST root is the explicit private boundary.
  mkdirSync(join(root, "case-diagnostic"));
  const filename = join(root, "case-diagnostic/witness.jsonl");
  writeFileSync(filename, "");
  if (process.platform !== "win32") {
    chmodSync(join(root, "case-diagnostic"), 0o755);
    chmodSync(filename, 0o644);
  }
  process.env.TEST_PI_WITNESS = filename;
  process.env.TEST_PI_NATIVE_DIAGNOSTIC = "1";
  writeFileSync(join(root, "package.json"), '{"type":"module"}');
  const source = join(root, "index.ts");
  copyFileSync(new URL("../portable-package/com.universal-agent-plugins.pi/index.ts", import.meta.url), source);
  const { recordTestDiagnostic } = await import(pathToFileURL(source));
  const valid = { type: "TEST_pi_native_delivery", phase: "native_receipt", code: "os_accepted", elapsed_ms: 15001, remaining_ms: 4999 };
  const invalid = ["PRIVATE_RAW_PAYLOAD", { ...valid, phase: "PRIVATE_PHASE" }, { ...valid, headers: "PRIVATE_HEADER" },
    { ...valid, code: [valid.code] }, { ...valid, phase: null }, { ...valid, elapsed_ms: "1" },
    { ...valid, remaining_ms: true }, { ...valid, remaining_ms: 20001 }];
  recordTestDiagnostic(invalid.map(value => typeof value === "string" ? value : JSON.stringify(value)).concat(JSON.stringify(valid)).join("\n"));
  assert.deepEqual(readFileSync(filename, "utf8").trim().split("\n").map(JSON.parse), [{ type: "TEST_pi_native_delivery", trace: valid }]);
  const before = readFileSync(filename, "utf8");
  recordTestDiagnostic("PRIVATE_OVERSIZED".repeat(3000) + "\n" + JSON.stringify(valid));
  assert.equal(readFileSync(filename, "utf8"), before);
  if (process.platform !== "win32") {
    chmodSync(join(root, "case-diagnostic"), 0o700);
    chmodSync(root, 0o755);
    recordTestDiagnostic(JSON.stringify(valid));
    assert.equal(readFileSync(filename, "utf8"), before);
    chmodSync(root, 0o700);
  }
  delete process.env.TEST_PI_NATIVE_DIAGNOSTIC;
  recordTestDiagnostic(JSON.stringify(valid));
  assert.equal(readFileSync(filename, "utf8"), before);
});
