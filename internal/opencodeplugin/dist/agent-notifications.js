// Generated with the UAP observer pinned in package-lock.json.
// node_modules/universal-agent-plugins-opencode-events/index.js
var object = (x) => x !== null && typeof x === "object" && !Array.isArray(x);
var id = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
var typeOK = (x) => typeof x === "string" && /^[a-z][a-z0-9.-]{0,79}$/.test(x);
function createObserver(options) {
  if (typeof options?.emit !== "function" || typeof options.client?.session?.messages !== "function" || typeof options.client?.session?.get !== "function") {
    throw new TypeError("emit, messages and get required");
  }
  const limit = Math.max(2, Math.min(100, Number.isInteger(options.messageLimit) ? options.messageLimit : 30));
  const maxSessions = Math.max(1, Math.min(4096, Number.isInteger(options.dedupLimit) ? options.dedupLimit : 512));
  const timeoutMs = Math.max(100, Math.min(1e4, Number.isInteger(options.lookupTimeoutMs) ? options.lookupTimeoutMs : 2e3));
  const maxLookups = Math.max(1, Math.min(64, Number.isInteger(options.maxConcurrentLookups) ? options.maxConcurrentLookups : 16));
  const sessions = /* @__PURE__ */ new Map(), unknownSeen = /* @__PURE__ */ new Set();
  let activeLookups = 0;
  const diag = (reason) => {
    try {
      options.onDiagnostic?.(reason);
    } catch {
    }
  };
  const state = (sid) => {
    if (!sessions.has(sid)) {
      sessions.set(sid, {
        user: "",
        assistant: "",
        retry: false,
        retryAssistant: "",
        cancelled: false,
        questions: /* @__PURE__ */ new Set(),
        permissions: /* @__PURE__ */ new Set(),
        resolved: /* @__PURE__ */ new Set(),
        admitted: /* @__PURE__ */ new Set(),
        turnEpoch: 0,
        revision: 0,
        errorPending: 0,
        rootSession: void 0
      });
      if (sessions.size > maxSessions) sessions.delete(sessions.keys().next().value);
    }
    return sessions.get(sid);
  };
  async function emit(fact, key, session) {
    const admitted = session ? session.admitted : unknownSeen;
    if (admitted.has(key)) return;
    admitted.add(key);
    if (!session && admitted.size > maxSessions) admitted.delete(admitted.values().next().value);
    try {
      await options.emit({ version: 1, ...fact });
    } catch {
      diag("observed event callback failed");
    }
  }
  async function boundedLookup(call) {
    if (activeLookups >= maxLookups) {
      diag("messages lookup capacity exceeded");
      return;
    }
    const controller = new AbortController();
    let timer;
    activeLookups++;
    const request = Promise.resolve().then(() => call(controller.signal));
    request.then(() => {
      activeLookups--;
    }, () => {
      activeLookups--;
    });
    try {
      return await Promise.race([
        request,
        new Promise((_, reject) => {
          timer = setTimeout(() => {
            controller.abort();
            reject(new Error("lookup timeout"));
          }, timeoutMs);
        })
      ]);
    } catch {
      diag("lookup failed or timed out");
      return;
    } finally {
      clearTimeout(timer);
    }
  }
  async function rootStatus(sid) {
    const s = state(sid);
    if (typeof s.rootSession === "boolean") return s.rootSession;
    const result = await boundedLookup((signal) => options.client.session.get({ path: { id: sid }, signal }));
    const info = result?.data ?? result;
    if (!object(info) || info.id !== sid || info.parentID !== void 0 && !id(info.parentID)) {
      diag("invalid session ancestry");
      return;
    }
    s.rootSession = info.parentID === void 0;
    return s.rootSession;
  }
  async function latest(sid) {
    const result = await boundedLookup((signal) => options.client.session.messages({
      path: { id: sid },
      query: { limit },
      signal
    }));
    const rows = Array.isArray(result) ? result : result?.data;
    if (!Array.isArray(rows) || rows.length > limit) {
      diag("invalid messages lookup");
      return;
    }
    const messages = rows.map((x) => x?.info ?? x);
    if (!messages.every((m) => object(m) && id(m.id) && m.sessionID === sid && ["user", "assistant"].includes(m.role))) {
      diag("invalid or mismatched messages");
      return;
    }
    return messages;
  }
  function currentTurn(messages, uid) {
    const lastUser = messages.findLast((m) => m.role === "user");
    if (lastUser) return lastUser.id === uid;
    return messages.at(-1)?.role === "assistant" && messages.at(-1).parentID === uid;
  }
  async function idle(sid) {
    const s = state(sid), revision = s.revision, uid = s.user;
    if (!uid || !s.assistant || s.admitted.has("idle") || s.retry || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    const messages = await latest(sid);
    if (!messages || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.retry || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    if (!currentTurn(messages, uid)) return;
    const answer = messages.at(-1);
    if (!answer || answer.role !== "assistant" || answer.id !== s.assistant || answer.parentID !== uid || answer.finish !== "stop" || !Number.isFinite(answer.time?.completed) || answer.error != null) return;
    const rootSession = await rootStatus(sid);
    if (rootSession === void 0 || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.cancelled || s.errorPending) return;
    await emit({ kind: "turn_idle_verified", sessionID: sid, turnID: uid, messageID: answer.id, rootSession }, "idle", s);
  }
  async function observe(event) {
    if (!object(event) || !typeOK(event.type) || !object(event.properties)) {
      diag("invalid native event");
      return;
    }
    const { type, properties: p } = event;
    if (type === "message.updated") {
      const m = p.info;
      if (!object(m) || !id(m.id) || !id(m.sessionID) || !["user", "assistant"].includes(m.role)) {
        diag("invalid message.updated");
        return;
      }
      const s = state(m.sessionID);
      if (m.role === "user" && m.id !== s.user) {
        s.user = m.id;
        s.assistant = "";
        s.retry = false;
        s.retryAssistant = "";
        s.cancelled = false;
        s.questions.clear();
        s.permissions.clear();
        s.resolved.clear();
        s.admitted.clear();
        s.turnEpoch++;
        s.revision++;
      }
      if (m.role === "assistant" && m.parentID === s.user && m.finish === "stop" && Number.isFinite(m.time?.completed) && m.error == null && m.id !== s.retryAssistant) {
        s.assistant = m.id;
        s.retry = false;
        s.revision++;
      }
      if (m.role === "assistant" && m.parentID === s.user && m.error != null) {
        s.cancelled = true;
        s.revision++;
      }
      return;
    }
    if (type === "session.status") {
      if (!id(p.sessionID) || !object(p.status) || !["busy", "idle", "retry"].includes(p.status.type)) {
        diag("invalid session.status");
        return;
      }
      if (p.status.type === "retry") {
        const s = state(p.sessionID);
        s.retryAssistant = s.assistant;
        s.assistant = "";
        s.retry = true;
        s.revision++;
      }
      if (p.status.type === "idle") await idle(p.sessionID);
      return;
    }
    if (type === "session.idle") {
      if (!id(p.sessionID)) {
        diag("invalid session.idle");
        return;
      }
      await idle(p.sessionID);
      return;
    }
    if (type === "question.asked" || type === "permission.asked") {
      const requestMessageID = p.messageID ?? p.tool?.messageID;
      if (!id(p.sessionID) || !id(p.id) || requestMessageID !== void 0 && !id(requestMessageID)) {
        diag("invalid request");
        return;
      }
      if (type === "question.asked" && (!Array.isArray(p.questions) || p.questions.length === 0)) {
        diag("invalid question shape");
        return;
      }
      if (type === "permission.asked" && (typeof p.permission !== "string" || !p.permission)) {
        diag("invalid permission shape");
        return;
      }
      const s = state(p.sessionID), uid = s.user, epoch = s.turnEpoch;
      if (!uid || s.resolved.has(p.id)) {
        diag("unmatched or resolved request");
        return;
      }
      const pending = type === "question.asked" ? s.questions : s.permissions;
      pending.add(p.id);
      s.revision++;
      const messages = await latest(p.sessionID);
      if (sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || !pending.has(p.id) || s.resolved.has(p.id)) return;
      if (!messages || !currentTurn(messages, uid)) {
        pending.delete(p.id);
        diag("unmatched request turn");
        return;
      }
      if (requestMessageID && requestMessageID !== uid && !messages.some((m) => m.role === "assistant" && m.id === requestMessageID && m.parentID === uid)) {
        pending.delete(p.id);
        diag("unmatched request message");
        return;
      }
      const rootSession = await rootStatus(p.sessionID);
      if (rootSession === void 0 || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || !pending.has(p.id) || s.resolved.has(p.id)) return;
      s.assistant = "";
      s.revision++;
      await emit({ kind: type === "question.asked" ? "question_asked" : "permission_asked", sessionID: p.sessionID, turnID: uid, requestID: p.id, rootSession }, `${type}:${p.id}`, s);
      return;
    }
    if (type === "question.replied" || type === "question.rejected" || type === "permission.replied") {
      if (!id(p.sessionID) || !id(p.requestID)) {
        diag("invalid resolution");
        return;
      }
      const s = state(p.sessionID);
      const pending = type.startsWith("question.") ? s.questions : s.permissions;
      const current = pending.delete(p.requestID);
      s.resolved.add(p.requestID);
      if (s.resolved.size > maxSessions) s.resolved.delete(s.resolved.values().next().value);
      if (current) {
        s.assistant = "";
        s.revision++;
      }
      return;
    }
    if (type === "session.error") {
      if (!id(p.sessionID) || !object(p.error) || p.messageID !== void 0 && !id(p.messageID)) {
        diag("invalid session.error");
        return;
      }
      const s = state(p.sessionID), uid = s.user, epoch = s.turnEpoch;
      if (!uid || s.admitted.has("idle")) return;
      s.errorPending++;
      s.revision++;
      const messages = await latest(p.sessionID);
      s.errorPending--;
      if (!messages || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || s.admitted.has("idle") || !currentTurn(messages, uid)) return;
      if (p.messageID && !messages.some((m) => m.role === "assistant" && m.id === p.messageID && m.parentID === uid)) {
        diag("unmatched error message");
        return;
      }
      const rootSession = await rootStatus(p.sessionID);
      if (rootSession === void 0 || sessions.get(p.sessionID) !== s || s.turnEpoch !== epoch || s.user !== uid || s.admitted.has("idle")) return;
      s.cancelled = true;
      s.revision++;
      if (/abort|cancel/i.test(String(p.error.name ?? ""))) return;
      await emit({ kind: "terminal_error", sessionID: p.sessionID, turnID: uid, rootSession }, "error", s);
      return;
    }
    await emit(
      { kind: "unknown", nativeType: type, ...id(p.sessionID) ? { sessionID: p.sessionID } : {} },
      `unknown:${type}:${id(p.sessionID) ? p.sessionID : ""}`
    );
  }
  return { observe };
}

// ipc.mjs
import { spawn } from "node:child_process";
import path from "node:path";
var executable = "__AGENT_NOTIFICATIONS_EXECUTABLE__";
var controlRoot = "__AGENT_NOTIFICATIONS_CONTROL_ROOT__";
var maxWireBytes = 4096;
var maxReceiptBytes = 1024;
var processTimeoutMs = 25e3;
var commonEnvironment = ["AGENT_NOTIFICATIONS_CONFIG"];
var posixEnvironment = ["HOME", "XDG_CONFIG_HOME", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"];
var windowsEnvironment = ["SystemRoot", "WINDIR", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"];
function absoluteNativePath(value, platform) {
  if (typeof value !== "string" || value.includes("\0")) return false;
  if (platform !== "win32") return path.posix.isAbsolute(value);
  if (value.startsWith("\\\\?\\") || value.startsWith("\\\\.\\")) return false;
  const drive = /^[A-Za-z]:\\/.test(value);
  const unc = /^\\\\[^\\]+\\[^\\]+\\/.test(value);
  return (drive || unc) && path.win32.normalize(value) === value;
}
async function forward(event, spawnProcess = spawn, binary = executable, root = controlRoot, platform = process.platform) {
  const body = Buffer.from(JSON.stringify(event));
  if (body.length > maxWireBytes || !absoluteNativePath(binary, platform) || !absoluteNativePath(root, platform) || platform === "win32" && !binary.toLowerCase().endsWith(".exe")) return "invalid_plugin";
  return new Promise((resolve) => {
    let settled = false;
    let timer;
    const finish = (result) => {
      if (!settled) {
        settled = true;
        clearTimeout(timer);
        resolve(result);
      }
    };
    let child;
    try {
      child = spawnProcess(binary, ["opencode-event", "--protocol", "1"], {
        shell: false,
        stdio: ["pipe", "pipe", "pipe"],
        env: { ...Object.fromEntries([...commonEnvironment, ...platform === "win32" ? windowsEnvironment : posixEnvironment].filter((key) => process.env[key] !== void 0).map((key) => [key, process.env[key]])), AGENT_NOTIFICATIONS_CONTROL_ROOT: root }
      });
    } catch {
      finish("spawn_failed");
      return;
    }
    let stdout = Buffer.alloc(0), stderrBytes = 0;
    timer = setTimeout(() => {
      child.kill();
      finish("timeout");
    }, processTimeoutMs);
    child.on("error", () => {
      clearTimeout(timer);
      finish("spawn_failed");
    });
    child.stdout.on("data", (chunk) => {
      stdout = Buffer.concat([stdout, chunk]);
      if (stdout.length > maxReceiptBytes) {
        child.kill();
        finish("invalid_receipt");
      }
    });
    child.stderr.on("data", (chunk) => {
      stderrBytes += chunk.length;
      if (stderrBytes > maxReceiptBytes) {
        child.kill();
        finish("invalid_receipt");
      }
    });
    child.stdin.on("error", () => {
      child.kill();
      finish("write_failed");
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (settled) return;
      if (code !== 0) {
        finish("rejected");
        return;
      }
      try {
        const receipt = JSON.parse(stdout.toString("utf8"));
        finish(["submitted", "rejected", "suppressed", "unknown"].includes(receipt.status) ? receipt.status : "invalid_receipt");
      } catch {
        finish("invalid_receipt");
      }
    });
    child.stdin.end(body);
  });
}

// plugin.mjs
var AgentNotifications = async ({ client }) => {
  const observer = createObserver({
    client,
    emit: async (event) => {
      if (event.kind === "unknown" || event.rootSession !== true) return;
      const outcome = await forward(event);
      if (outcome !== "submitted" && outcome !== "suppressed") {
        console.error("Agent Notifications OpenCode delivery:", outcome);
      }
    },
    onDiagnostic: (reason) => console.error("Agent Notifications OpenCode observer:", reason)
  });
  return { event: async ({ event }) => observer.observe(event) };
};
export {
  AgentNotifications
};
