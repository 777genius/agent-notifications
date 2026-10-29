import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { copyFileSync, constants, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { test } from "node:test";

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(predicate, timeout = 4000) {
  const until = Date.now() + timeout;
  while (!predicate()) { if (Date.now() >= until) throw new Error("fixture did not settle"); await sleep(10); }
}
const rows = filename => existsSync(filename) ? readFileSync(filename, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse) : [];
async function fixture(t, { delay = 100, hang = false, ignoreTerm = false } = {}) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "TEST-pi-observer-")));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const part of ["active/com.universal-agent-plugins.pi", "data", "runtime/bin", "project"]) mkdirSync(join(root, part), { recursive: true, mode: 0o700 });
  const executable = join(root, "runtime/bin/helper" + (process.platform === "win32" ? ".exe" : ""));
  copyFileSync(process.execPath, executable, constants.COPYFILE_FICLONE);
  const primaryHash = createHash("sha256").update(readFileSync(executable)).digest("hex");
  const source = join(root, "active/com.universal-agent-plugins.pi/index.ts");
  copyFileSync(new URL("../portable-package/com.universal-agent-plugins.pi/index.ts", import.meta.url), source);
  writeFileSync(join(root, "active/package.json"), '{"type":"module"}');
  writeFileSync(join(root, "project/package.json"), '{"type":"module"}');
  const descriptor = { schema_version: 1, bootstrap_version: 1, active_path: join(root, "active"), package_root: join(root, "active"), data_root: join(root, "data"), canonical_tree_digest: "sha256:" + "2".repeat(64) };
  const revision = { projection_digest: "sha256:" + "1".repeat(64), canonical_digest: descriptor.canonical_tree_digest, bootstrap_version: 1, binding_id: "binding", runtime_root: join(root, "runtime"), primary_sha256: primaryHash };
  const locator = "agent-notify-" + "a".repeat(64) + ".json";
  const runtime = { schema_version: 1, locator, executable, primary_sha256: primaryHash, loaded_revision: revision };
  writeFileSync(join(root, "data/pi-runtime.json"), JSON.stringify(runtime));
  writeFileSync(join(root, "data", locator), JSON.stringify({ version: 1, integration: "pi", bindingID: "binding", dataRoot: descriptor.data_root, runtimeRoot: revision.runtime_root, primary: "bin/" + executable.split(/[\\/]/).at(-1) }));
  const invoked = join(root, "invoked.jsonl"), delivered = join(root, "delivered.jsonl");
  writeFileSync(join(root, "project/pi-event"), `import { appendFileSync } from 'node:fs';
    ${ignoreTerm ? "process.on('SIGTERM', () => {});" : ""}
    let raw=''; process.stdin.setEncoding('utf8'); process.stdin.on('data', b => raw += b);
    process.stdin.on('end', () => { const frame=JSON.parse(raw); appendFileSync(${JSON.stringify(invoked)}, JSON.stringify({frame, argv:process.argv.slice(2), parent:process.ppid, terminal:process.env.TEST_PI_TERMINAL})+'\\n');
      ${hang ? "setInterval(() => {}, 1000);" : `setTimeout(() => appendFileSync(${JSON.stringify(delivered)}, JSON.stringify(frame)+'\\n'), ${delay});`}
    });`);
  const handlers = new Map();
  const pi = { on(event, handler) { assert.equal(handlers.has(event), false); handlers.set(event, handler); } };
  const extension = await import(pathToFileURL(source));
  extension.default(pi, descriptor);
  const context = mode => ({ mode, cwd: join(root, "project"), sessionManager: { getSessionId: () => "native-session" } });
  const emit = (name, event, mode = "json") => handlers.get(name)?.({ type: name, ...event }, context(mode));
  const settle = (outcome = "completed", mode = "json") => { emit("agent_start", {}, mode); emit("agent_before_settle", { outcome }, mode); return emit("agent_settled", { aborted: false }, mode); };
  return { root, descriptor, runtime, handlers, invoked, delivered, emit, settle };
}

test("native settlement carries immutable revision and direct parent context", async t => {
  const f = await fixture(t);
  process.env.TEST_PI_TERMINAL = "origin-terminal-nonce";
  t.after(() => { delete process.env.TEST_PI_TERMINAL; });
  assert.equal(f.settle(), undefined);
  const draining = f.emit("session_shutdown", { reason: "quit" });
  assert.ok(draining instanceof Promise);
  await draining;
  const [invocation] = rows(f.invoked), [frame] = rows(f.delivered);
  assert.equal(invocation.parent, process.pid);
  assert.equal(invocation.terminal, "origin-terminal-nonce");
  assert.deepEqual(invocation.argv, ["--binding", join(f.descriptor.data_root, f.runtime.locator)]);
  assert.equal(frame.event, "agent_settled");
  assert.deepEqual(frame.settled, { aborted: false, boundary_outcome: "completed" });
  assert.deepEqual(frame.loaded_revision, f.runtime.loaded_revision);
  assert.equal(frame.pi_pid, process.pid);
  assert.equal(frame.session_id, "native-session");
});

test("error exit meaning, repeated starts, abort and missing outcome", async t => {
  const f = await fixture(t);
  f.emit("agent_start", {}); f.emit("agent_before_settle", { outcome: "error" }); f.emit("agent_start", {});
  assert.equal(f.emit("agent_settled", { aborted: false }), undefined);
  f.emit("agent_start", {}); f.emit("agent_settled", { aborted: true });
  f.emit("agent_start", {}); f.emit("agent_settled", { aborted: false });
  await f.emit("session_shutdown", { reason: "quit" });
  const frames = rows(f.delivered);
  assert.equal(frames.length, 1);
  assert.equal(frames[0].settled.boundary_outcome, "error");
  assert.equal(f.handlers.has("agent_end"), false);
});

test("two-child admission drops excess work without queued resend", async t => {
  const f = await fixture(t, { delay: 250 });
  f.settle(); f.settle(); f.settle();
  await f.emit("session_shutdown", { reason: "quit" });
  assert.equal(rows(f.delivered).length, 2);
  assert.equal(rows(f.invoked).length, 2);
});

test("idle nested prompt is private and cancels when the outer prompt ends", async t => {
  const f = await fixture(t, { delay: 1200 });
  assert.equal(f.emit("ui_prompt_start", { kind: "confirm", title: "PRIVATE_SENTINEL" }, "rpc"), undefined);
  await waitFor(() => rows(f.invoked).length === 1);
  f.emit("ui_prompt_start", { kind: "input", title: "OTHER_PRIVATE" }, "rpc");
  f.emit("ui_prompt_end", { kind: "input" }, "rpc");
  f.emit("ui_prompt_end", { kind: "confirm" }, "rpc");
  assert.equal(f.emit("session_shutdown", { reason: "quit" }, "rpc"), undefined);
  await sleep(350);
  assert.equal(rows(f.delivered).length, 0);
  assert.equal(rows(f.invoked).length, 1);
  assert.equal(JSON.stringify(rows(f.invoked)).includes("PRIVATE"), false);
});

test("TUI quit and reload cancel terminal work instead of draining", async t => {
  for (const [mode, reason] of [["tui", "quit"], ["json", "reload"]]) {
    const f = await fixture(t, { delay: 1500 });
    f.settle("completed", mode);
    await waitFor(() => rows(f.invoked).length === 1);
    assert.equal(f.emit("session_shutdown", { reason }, mode), undefined);
    await sleep(350);
    assert.equal(rows(f.delivered).length, 0);
  }
});

test("loaded code cannot refresh its revision by rereading a new locator", async t => {
  const f = await fixture(t);
  const replacement = structuredClone(f.runtime);
  replacement.loaded_revision.projection_digest = "sha256:" + "9".repeat(64);
  writeFileSync(join(f.root, "data/pi-runtime.json"), JSON.stringify(replacement));
  f.settle(); await f.emit("session_shutdown", { reason: "quit" });
  assert.equal(rows(f.delivered)[0].loaded_revision.projection_digest, f.runtime.loaded_revision.projection_digest);
});

test("normal non-TUI quit does not reset a hung child's original lifetime", { timeout: 27000 }, async t => {
  const f = await fixture(t, { hang: true, ignoreTerm: true });
  const born = Date.now(); f.settle();
  await waitFor(() => rows(f.invoked).length === 1);
  await sleep(200);
  await f.emit("session_shutdown", { reason: "quit" });
  const elapsed = Date.now() - born;
  assert.ok(elapsed >= 20_500 && elapsed < 21_500, `original child lifetime: ${elapsed}ms`);
  assert.equal(rows(f.delivered).length, 0);
  assert.equal(rows(f.invoked).length, 1);
});
