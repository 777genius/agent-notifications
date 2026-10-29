import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { execFile, type ChildProcess } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { appendFileSync, closeSync, fstatSync, lstatSync, openSync, readSync, realpathSync } from "node:fs";
import { basename, dirname, isAbsolute, join, normalize, relative } from "node:path";

type Descriptor = Readonly<{
  schema_version: number;
  bootstrap_version: number;
  active_path: string;
  package_root: string;
  data_root: string;
  canonical_tree_digest: string;
}>;
type Revision = Readonly<{
  projection_digest: string;
  canonical_digest: string;
  bootstrap_version: number;
  binding_id: string;
  runtime_root: string;
  primary_sha256: string;
}>;
type Runtime = Readonly<{ executable: string; selector: string; loaded_revision: Revision }>;
type Mode = "tui" | "rpc" | "json" | "print";
type Outcome = "completed" | "aborted" | "error";
type Work = {
  id: string;
  terminal: boolean;
  mode: Mode;
  child: ChildProcess;
  done: Promise<void>;
  cancel: () => void;
};
const FRAME_LIMIT = 64 << 10;
const SOFT_LIFETIME = 15_500;
const KILL_GRACE = 250;
const HARD_LIFETIME = 16_000;
const text = (value: unknown, max = 4096): value is string => typeof value === "string" && value.length > 0 && value.length <= max && !/[\u0000-\u001f\u007f]/u.test(value);
const path = (value: unknown): value is string => text(value) && isAbsolute(value) && normalize(value) === value;
const digest = (value: unknown): value is string => typeof value === "string" && /^sha256:[a-f0-9]{64}$/u.test(value);
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const sameKeys = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).sort().join("\0") === keys.sort().join("\0");

// TEMPORARY DIAGNOSTIC ONLY: fixed native codes into the existing private TEST
// witness. Never forward arbitrary child stderr or ship this in a final build.
export function recordTestDiagnostic(stderr: unknown): void {
  try {
    const filename = process.env.TEST_PI_WITNESS;
    if (!path(filename) || basename(filename) !== "witness.jsonl" || !basename(dirname(filename)).startsWith("case-") ||
        !basename(dirname(dirname(filename))).startsWith("TEST-pi-windows-desktop-") || typeof stderr !== "string") return;
    const info = lstatSync(filename);
    if (!info.isFile() || info.isSymbolicLink() || info.size > FRAME_LIMIT || realpathSync(filename) !== filename) return;
    const phases = new Set(["admitted", "resolve_started", "resolve_ready", "resolve_refused", "consume_started", "channels_ready",
      "claim_started", "lease_started", "lease_ready", "invoke_started", "invoke_returned", "consume_returned",
      "native_started", "focus_ready", "toast_started", "toast_returned", "sound_started", "sound_returned"]);
    const effects = new Set(["suppressed", "expired", "cache_unavailable", "duplicate", "revoked", "unknown", "submitted"]);
    const reasons = new Set(["invalid_fact", "unsupported_fact", "expired", "not_registered", "invalid_config", "channels_disabled",
      "delivery_uncertain", "no_attempt", "hook_handoff", "consumer_unavailable"]);
    let output = "";
    for (const line of stderr.slice(0, 32768).split("\n").slice(0, 64)) {
      let row: unknown;
      try { row = JSON.parse(line); } catch { continue; }
      if (!object(row) || !sameKeys(row, ["type", "phase", "channel", "elapsed_ms", "remaining_ms", "context", ...(row.receipt === undefined ? [] : ["receipt"])]) ||
          row.type !== "TEST_pi_native_trace" || typeof row.phase !== "string" || !phases.has(row.phase) ||
          typeof row.channel !== "string" || !["", "desktop", "webhook"].includes(row.channel) ||
          typeof row.context !== "string" || !["active", "cancelled", "deadline_exceeded"].includes(row.context) ||
          !Number.isInteger(row.elapsed_ms) || Number(row.elapsed_ms) < 0 || Number(row.elapsed_ms) > 60000 ||
          !Number.isInteger(row.remaining_ms) || Number(row.remaining_ms) < 0 || Number(row.remaining_ms) > 15000) continue;
      if (row.receipt !== undefined) {
        if (!object(row.receipt) || !["suppressed", "unknown", "submitted"].includes(String(row.receipt.status)) ||
            !Object.entries(row.receipt).every(([key, value]) => typeof value === "string" &&
              (key === "status" || key === "reason" && reasons.has(value) || ["desktop", "webhook"].includes(key) && effects.has(value)))) continue;
      }
      output += JSON.stringify({ type: "TEST_pi_native_trace", trace: row }) + "\n";
    }
    if (output && info.size + Buffer.byteLength(output) <= FRAME_LIMIT) appendFileSync(filename, output);
  } catch { /* diagnostics never affect observation or delivery */ }
}

function readJSON(filename: string): unknown {
  const info = lstatSync(filename);
  if (!info.isFile() || info.isSymbolicLink() || info.size > FRAME_LIMIT || info.size < 2) throw new Error("invalid runtime metadata");
  const handle = openSync(filename, "r");
  try {
    const opened = fstatSync(handle);
    if (!opened.isFile() || opened.dev !== info.dev || opened.ino !== info.ino || opened.size !== info.size) throw new Error("runtime metadata changed");
    const bytes = Buffer.alloc(FRAME_LIMIT + 1);
    let used = 0;
    while (used < bytes.length) {
      const count = readSync(handle, bytes, used, bytes.length - used, null);
      if (count === 0) break;
      used += count;
    }
    if (used > FRAME_LIMIT || used !== opened.size) throw new Error("runtime metadata size changed");
    return JSON.parse(bytes.subarray(0, used).toString("utf8"));
  } finally { closeSync(handle); }
}

function hashExecutable(filename: string): string {
  const info = lstatSync(filename);
  if (!info.isFile() || info.isSymbolicLink() || info.size < 1 || info.size > 512 * 1024 * 1024) throw new Error("invalid runtime executable");
  const handle = openSync(filename, "r");
  try {
    const opened = fstatSync(handle);
    if (opened.dev !== info.dev || opened.ino !== info.ino || opened.size !== info.size) throw new Error("runtime executable changed");
    const buffer = Buffer.alloc(64 << 10);
    const hash = createHash("sha256");
    let used = 0;
    for (;;) {
      const count = readSync(handle, buffer, 0, buffer.length, null);
      if (count === 0) break;
      used += count;
      if (used > opened.size) throw new Error("runtime executable changed");
      hash.update(buffer.subarray(0, count));
    }
    if (used !== opened.size) throw new Error("runtime executable changed");
    return hash.digest("hex");
  } finally { closeSync(handle); }
}

// This is a derived locator, never delivery authority. The Go entrypoint checks
// the committed UAP binding, installed primary, revision and policy under lease.
function resolveRuntime(d: Descriptor): Runtime {
  if (!object(d) || d.schema_version !== 1 || d.bootstrap_version !== 1 || !path(d.active_path) || d.package_root !== d.active_path || !path(d.data_root) || !digest(d.canonical_tree_digest)) throw new Error("invalid Pi descriptor");
  if (realpathSync(d.active_path) !== d.active_path || realpathSync(d.data_root) !== d.data_root) throw new Error("unbound physical roots");
  const facts = readJSON(join(d.data_root, "pi-runtime.json"));
  if (!object(facts) || !sameKeys(facts, ["schema_version", "locator", "executable", "primary_sha256", "loaded_revision"]) || facts.schema_version !== 1 || typeof facts.locator !== "string" || !/^agent-notify-[a-f0-9]{64}\.json$/u.test(facts.locator) || !path(facts.executable) || !object(facts.loaded_revision)) throw new Error("invalid Pi runtime locator");
  const revision = facts.loaded_revision;
  if (!sameKeys(revision, ["projection_digest", "canonical_digest", "bootstrap_version", "binding_id", "runtime_root", "primary_sha256"]) || !digest(revision.projection_digest) || revision.canonical_digest !== d.canonical_tree_digest || revision.bootstrap_version !== d.bootstrap_version || !text(revision.binding_id, 256) || !path(revision.runtime_root) || typeof revision.primary_sha256 !== "string" || !/^[a-f0-9]{64}$/u.test(revision.primary_sha256) || facts.primary_sha256 !== revision.primary_sha256) throw new Error("invalid loaded Pi revision");
  const selector = join(d.data_root, facts.locator);
  const binding = readJSON(selector);
  if (!object(binding) || binding.version !== 1 || binding.integration !== "pi" || binding.bindingID !== revision.binding_id || binding.dataRoot !== d.data_root || binding.runtimeRoot !== revision.runtime_root || !text(binding.primary, 512)) throw new Error("invalid installed Pi binding");
  if (binding.primary.includes("\\") || binding.primary.includes(":") || binding.primary.split("/").some(part => part === ".." || part === "." || part === "")) throw new Error("invalid installed primary selector");
  const executable = join(revision.runtime_root, binding.primary);
  if (executable !== facts.executable || relative(revision.runtime_root, executable).startsWith("..") || realpathSync(executable) !== executable || hashExecutable(executable) !== revision.primary_sha256) throw new Error("installed primary mismatch");
  return Object.freeze({ executable, selector, loaded_revision: Object.freeze({ ...revision }) as Revision });
}

/** Native Pi factory. UAP's guarded bootstrap supplies the captured descriptor. */
export default function notifications(pi: ExtensionAPI, descriptor: Descriptor): void {
  let runtime: Runtime;
  try { runtime = resolveRuntime(descriptor); } catch { return; }
  const observer = randomUUID();
  const children = new Map<string, Work>();
  const prompts: { id: string; kind: string }[] = [];
  let active = false;
  let boundary: Outcome | undefined;
  let shuttingDown = false;

  const spawn = (ctx: ExtensionContext, event: "agent_settled" | "ui_prompt_start", detail: Record<string, unknown>, id: string, terminal: boolean): void => {
    if (shuttingDown || children.size >= 2) return;
    let frame: string;
    const mode = ctx.mode as Mode;
    try {
      const session = ctx.sessionManager.getSessionId();
      const cwd = ctx.cwd;
      if (!text(session, 1024) || !path(cwd) || !["tui", "rpc", "json", "print"].includes(mode)) return;
      frame = JSON.stringify({ schema_version: 1, event, observer_id: observer, observation_id: id, session_id: session, cwd, mode, pi_pid: process.pid, loaded_revision: runtime.loaded_revision, ...detail });
      if (Buffer.byteLength(frame) > FRAME_LIMIT) return;
      const born = Date.now();
      let finish!: () => void;
      const done = new Promise<void>(resolve => { finish = resolve; });
      let closed = false;
      let killTimer: ReturnType<typeof setTimeout> | undefined;
      let softTimer: ReturnType<typeof setTimeout>;
      let hardTimer: ReturnType<typeof setTimeout>;
      const child = execFile(runtime.executable, ["pi-event", "--binding", runtime.selector], { cwd, env: process.env, shell: false, windowsHide: true, maxBuffer: FRAME_LIMIT }, (_error, _stdout, stderr) => { recordTestDiagnostic(stderr); });
      const hardKill = () => {
        if (closed) return;
        try { child.kill("SIGKILL"); } catch { /* process may already have exited */ }
        child.stdin?.destroy(); child.stdout?.destroy(); child.stderr?.destroy();
      };
      const cancel = () => {
        if (closed || killTimer !== undefined) return;
        try { child.kill("SIGTERM"); } catch { /* same cancellation path still joins close */ }
        killTimer = setTimeout(hardKill, Math.max(0, Math.min(KILL_GRACE, born + HARD_LIFETIME - Date.now())));
      };
      softTimer = setTimeout(cancel, SOFT_LIFETIME);
      hardTimer = setTimeout(hardKill, HARD_LIFETIME);
      child.once("close", () => {
        closed = true;
        clearTimeout(softTimer); clearTimeout(hardTimer); if (killTimer !== undefined) clearTimeout(killTimer);
        children.delete(id); finish();
      });
      child.on("error", () => { /* execFile's callback and close settle the owned child */ });
      child.stdin?.on("error", () => { /* closed native pipe never changes Pi's outcome */ });
      children.set(id, { id, terminal, mode, child, done, cancel });
      child.stdin?.end(frame + "\n");
    } catch { /* malformed native context or failed spawn is a quiet observation */ }
  };

  pi.on("agent_start", () => { if (!active) boundary = undefined; active = true; });
  pi.on("agent_before_settle", event => { if (["completed", "aborted", "error"].includes(event.outcome)) boundary = event.outcome; });
  pi.on("agent_settled", (event, ctx) => {
    const outcome = boundary;
    active = false; boundary = undefined;
    if (event.aborted || outcome !== "completed" && outcome !== "error") return;
    spawn(ctx, "agent_settled", { settled: { aborted: false, boundary_outcome: outcome } }, randomUUID(), true);
  });
  pi.on("ui_prompt_start", (event, ctx) => {
    if (shuttingDown || prompts.length >= 8) return;
    const id = randomUUID();
    prompts.push({ id, kind: event.kind });
    if (prompts.length === 1) spawn(ctx, "ui_prompt_start", { prompt: { kind: event.kind } }, id, false);
  });
  pi.on("ui_prompt_end", event => {
    const prompt = prompts.pop();
    if (!prompt) return;
    if (prompt.kind !== event.kind) { prompts.length = 0; for (const work of children.values()) if (!work.terminal) work.cancel(); return; }
    if (prompts.length === 0) for (const work of children.values()) if (!work.terminal) work.cancel();
  });
  pi.on("session_shutdown", (event, ctx) => {
    shuttingDown = true; active = false; boundary = undefined; prompts.length = 0;
    const drain: Promise<void>[] = [];
    for (const work of children.values()) {
      if (event.reason === "quit" && ctx.mode !== "tui" && work.mode !== "tui" && work.terminal) drain.push(work.done);
      else work.cancel();
    }
    // Only normal non-TUI quit awaits already admitted terminal webhooks. All
    // original timers remain intact; settlement and TUI shutdown stay prompt.
    if (drain.length > 0) return Promise.all(drain).then(() => undefined);
  });
}
