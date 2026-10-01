// Generated with the UAP observer pinned in package-lock.json.
// node_modules/universal-agent-plugins-opencode-events/observer-v2.js
var object = (x) => x !== null && typeof x === "object" && !Array.isArray(x);
var id = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
var location = (x) => object(x) && typeof x.directory === "string" && x.directory.length > 0 && (x.workspaceID === void 0 || id(x.workspaceID));
var sameLocation = (a, b) => location(a) && location(b) && a.directory === b.directory && a.workspaceID === b.workspaceID;
var bounded = (value, fallback, min, max) => Math.max(min, Math.min(max, Number.isInteger(value) ? value : fallback));
var retainedAdd = (set, value, limit) => {
  set.add(value);
  if (set.size > limit) set.delete(set.values().next().value);
};
var supported = /* @__PURE__ */ new Set([
  "session.created",
  "session.moved",
  "session.deleted",
  "session.inbox.enqueued",
  "session.inbox.delivered",
  "session.inbox.cancelled",
  "session.inbox.delivery.changed",
  "session.execution.started",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "session.step.started",
  "session.step.ended",
  "session.step.failed",
  "session.retry.scheduled",
  "session.compaction.started",
  "session.compaction.ended",
  "session.compaction.failed",
  "form.created",
  "form.replied",
  "form.cancelled",
  "permission.asked",
  "permission.replied"
]);
function createV2Observer(options) {
  if (typeof options?.emit !== "function" || typeof options.client?.get !== "function" || typeof options.client?.context !== "function" || !location(options.location)) {
    throw new TypeError("emit, native get/context and location required");
  }
  const own = { directory: options.location.directory, workspaceID: options.location.workspaceID };
  const maxSessions = bounded(options.maxSessions, 512, 1, 512);
  const maxLookups = bounded(options.maxConcurrentLookups, 16, 1, 16);
  const timeout = bounded(options.lookupTimeoutMs, 2e3, 100, 1e4);
  const sessions = /* @__PURE__ */ new Map(), eventIDs = /* @__PURE__ */ new Set(), requests = /* @__PURE__ */ new Set();
  const diagnosticCodes = /* @__PURE__ */ new Set();
  let disposed = false, lifecycle = 0;
  const diag = (code) => {
    if (diagnosticCodes.has(code)) return;
    diagnosticCodes.add(code);
    try {
      options.onDiagnostic?.(code);
    } catch {
    }
  };
  function invalidate(s) {
    s.generation++;
    s.revision++;
    sessions.delete(s.sid);
  }
  function state(sid) {
    if (sessions.has(sid)) {
      const existing = sessions.get(sid);
      sessions.delete(sid);
      sessions.set(sid, existing);
      return existing;
    }
    if (sessions.size === maxSessions) {
      const records = [...sessions.values()];
      const victim = records.find((s2) => s2.ownership === "rejected" || s2.ownership === "child") ?? records.find((s2) => !(s2.started && !s2.result && !s2.interrupted) && !s2.verifying.size) ?? records[0];
      if (victim.started && !victim.result && !victim.interrupted) diag("session_capacity");
      invalidate(victim);
    }
    const s = {
      sid,
      generation: 0,
      ownership: "new",
      epoch: 0,
      revision: 0,
      started: false,
      user: "",
      assistant: "",
      final: "",
      retry: false,
      interrupted: false,
      compacting: false,
      compactedUser: "",
      blocked: false,
      admissionsOverflow: false,
      terminal: "",
      result: "",
      admissions: /* @__PURE__ */ new Map(),
      pending: /* @__PURE__ */ new Map(),
      resolved: /* @__PURE__ */ new Set(),
      admitted: /* @__PURE__ */ new Set(),
      liveAssistants: /* @__PURE__ */ new Set(),
      attempted: /* @__PURE__ */ new Set(),
      verifying: /* @__PURE__ */ new Set()
    };
    sessions.set(sid, s);
    return s;
  }
  const ownershipToken = (s) => ({ s, generation: s.generation, lifecycle });
  const ownedToken = (token) => !disposed && lifecycle === token.lifecycle && sessions.get(token.s.sid) === token.s && token.s.generation === token.generation;
  const semanticToken = (s) => ({ ...ownershipToken(s), epoch: s.epoch, revision: s.revision });
  const current = (token) => ownedToken(token) && token.s.epoch === token.epoch && token.s.revision === token.revision;
  const liveWork = (s) => s.started && Boolean(s.user) && !s.result && !s.interrupted && !s.blocked;
  function lookup(call) {
    if (disposed || requests.size >= maxLookups) {
      diag("lookup_capacity");
      return Promise.resolve(void 0);
    }
    const controller = new AbortController();
    let releaseWait, timer;
    const stopped = new Promise((resolve) => {
      releaseWait = resolve;
    });
    const record = { controller, stop: () => releaseWait(void 0), timer: void 0 };
    requests.add(record);
    const underlying = Promise.resolve().then(() => disposed ? void 0 : call(controller.signal));
    const result = underlying.then((value) => value, () => {
      diag("lookup_failed");
      return void 0;
    });
    result.then(() => {
      requests.delete(record);
      clearTimeout(timer);
    });
    timer = setTimeout(() => {
      controller.abort();
      diag("lookup_timeout");
      record.stop();
    }, timeout);
    record.timer = timer;
    return Promise.race([result, stopped]).finally(() => clearTimeout(timer));
  }
  function ensureOwnership(s) {
    if (s.ownership !== "new") return;
    s.ownership = "pending";
    const token = ownershipToken(s);
    void lookup((signal) => options.client.get({ sessionID: s.sid }, { signal })).then((info) => {
      if (!ownedToken(token)) return;
      if (info === void 0) {
        s.ownership = "unverified";
        diag("ownership_unverified");
        return;
      }
      if (!object(info) || info.id !== s.sid || info.parentID !== void 0 && !id(info.parentID) || !sameLocation(info.location, own)) {
        s.ownership = "rejected";
        diag("ownership_unverified");
        return;
      }
      s.ownership = info.parentID === void 0 ? "root" : "child";
      schedule(s);
    });
  }
  async function context(s) {
    const rows = await lookup((signal) => options.client.context({ sessionID: s.sid }, { signal }));
    if (!Array.isArray(rows) || rows.length > 4096) {
      diag("context_unverified");
      return;
    }
    const seen = /* @__PURE__ */ new Set(), metadata = [];
    for (const row of rows) {
      if (!object(row) || !id(row.id) || seen.has(row.id) || typeof row.type !== "string") {
        diag("context_unverified");
        return;
      }
      seen.add(row.id);
      metadata.push({
        id: row.id,
        type: row.type,
        finish: row.finish,
        completed: Number.isFinite(row.time?.completed),
        failed: row.error != null
      });
    }
    return metadata;
  }
  function associated(s, rows) {
    const lastUser = rows.findLast((row) => row.type === "user");
    return lastUser ? lastUser.id === s.user : s.compactedUser === s.user && Boolean(s.user);
  }
  function emit(s, key, fact) {
    if (disposed || s.ownership !== "root" || s.admitted.has(key)) return;
    if (s.admitted.size >= 2048) {
      s.blocked = true;
      diag("admission_capacity");
      return;
    }
    s.admitted.add(key);
    try {
      Promise.resolve(options.emit({ version: 1, sessionID: s.sid, turnID: s.user, rootSession: true, ...fact })).catch(() => diag("callback_failed"));
    } catch {
      diag("callback_failed");
    }
  }
  function schedule(s) {
    if (disposed || s.ownership !== "root" || !liveWork(s)) return;
    const candidates = [...s.pending.values()].filter((candidate) => !s.admitted.has(`request:${candidate.kind}:${candidate.id}`));
    if (s.terminal && !s.result) candidates.push({ kind: s.terminal, id: "", messageID: s.final });
    for (const candidate of candidates) {
      if (candidate.kind === "success" && (!s.final || s.retry || s.compacting || s.pending.size || s.admissions.size)) continue;
      const flight = `${s.epoch}:${candidate.kind}:${candidate.id}`;
      if (s.verifying.has(flight)) continue;
      const attempt = `${s.epoch}:${s.user}:${candidate.kind}:${candidate.id}:${candidate.messageID ?? ""}`;
      if (s.attempted.has(attempt)) continue;
      if (s.attempted.size >= 2048) {
        s.blocked = true;
        diag("verification_capacity");
        return;
      }
      s.attempted.add(attempt);
      const token = semanticToken(s);
      s.verifying.add(flight);
      void verify(s, candidate, token).catch(() => diag("verification_failed")).finally(() => {
        s.verifying.delete(flight);
        if (ownedToken(token) && !current(token)) {
          s.attempted.delete(attempt);
          schedule(s);
        }
      });
    }
  }
  async function verify(s, candidate, token) {
    const rows = await context(s);
    if (!current(token)) return;
    if (!rows || !liveWork(s) || !associated(s, rows)) return;
    if (candidate.kind === "success") {
      if (s.terminal !== "success" || s.result || s.retry || s.compacting || s.pending.size || s.admissions.size || !s.final) return;
      const index = rows.findIndex((row) => row.id === s.final && row.type === "assistant");
      const answer = rows[index];
      if (!answer || answer.finish !== "stop" || !answer.completed || answer.failed || rows.slice(index + 1).some((row) => row.type !== "idle")) return;
      s.result = "success";
      emit(s, `terminal:${s.epoch}`, { kind: "turn_idle_verified", messageID: s.final });
    } else if (candidate.kind === "failure") {
      if (s.terminal !== "failure" || s.result) return;
      s.result = "failure";
      emit(s, `terminal:${s.epoch}`, { kind: "terminal_error" });
    } else {
      if (s.pending.get(candidate.id) !== candidate || s.resolved.has(candidate.id)) return;
      if (candidate.messageID && (!s.liveAssistants.has(candidate.messageID) || !rows.some((row) => row.type === "assistant" && row.id === candidate.messageID))) return;
      emit(s, `request:${candidate.kind}:${candidate.id}`, { kind: candidate.kind, requestID: candidate.id });
    }
  }
  function resetFinal(s) {
    s.final = "";
    s.terminal = "";
  }
  function block(s) {
    s.blocked = true;
    resetFinal(s);
    s.revision++;
    diag("metadata_capacity");
  }
  function observe(event) {
    if (disposed) return;
    if (!object(event) || typeof event.type !== "string" || !object(event.data)) {
      diag("invalid_event");
      return;
    }
    const type = event.type, p = event.data;
    if (type === "location.shutdown") {
      if (sameLocation(event.location, own)) dispose();
      return;
    }
    if (!supported.has(type)) return;
    const sid = type === "form.created" ? p.form?.sessionID : p.sessionID;
    if (!id(sid) || sid === "global") {
      diag("invalid_session");
      return;
    }
    if (type === "session.moved" || type === "session.deleted") {
      const tracked = sessions.get(sid);
      if (tracked) invalidate(tracked);
      return;
    }
    if (event.location !== void 0 && !sameLocation(event.location, own)) return;
    if (event.id !== void 0) {
      if (!id(event.id)) {
        diag("invalid_event");
        return;
      }
      if (eventIDs.has(event.id)) return;
      retainedAdd(eventIDs, event.id, 2048);
    }
    const s = state(sid);
    if (s.ownership === "rejected" || s.ownership === "child") return;
    let changed = true;
    if (type === "session.execution.started") {
      if (s.ownership === "unverified") s.ownership = "new";
      s.epoch++;
      s.started = true;
      s.user = "";
      s.assistant = "";
      resetFinal(s);
      s.retry = false;
      s.interrupted = false;
      s.compacting = false;
      s.compactedUser = "";
      s.result = "";
      s.blocked = s.admissionsOverflow;
      s.admitted.clear();
      s.pending.clear();
      s.liveAssistants.clear();
      s.attempted.clear();
    } else if (type === "session.inbox.enqueued") {
      if (!id(p.inboxID) || !object(p.item) || typeof p.item.type !== "string") {
        diag("invalid_inbox");
        return;
      }
      if (s.admissions.size >= 64 && !s.admissions.has(p.inboxID)) {
        s.admissionsOverflow = true;
        block(s);
      } else s.admissions.set(p.inboxID, { type: p.item.type, delivery: p.item.delivery === "queue" ? "queue" : "steer" });
      resetFinal(s);
    } else if (type === "session.inbox.delivered") {
      if (!id(p.inboxID)) {
        diag("invalid_inbox");
        return;
      }
      const item = s.admissions.get(p.inboxID);
      s.admissions.delete(p.inboxID);
      if (!item) {
        s.blocked = true;
        diag("unmatched_delivery");
      } else if (item.type === "user" && s.started) {
        s.user = p.inboxID;
        s.assistant = "";
        s.liveAssistants.clear();
        s.compactedUser = "";
        s.retry = false;
      }
      resetFinal(s);
    } else if (type === "session.inbox.cancelled") {
      if (id(p.inboxID)) s.admissions.delete(p.inboxID);
    } else if (type === "session.inbox.delivery.changed") {
      const admission = s.admissions.get(p.inboxID);
      if (admission && ["steer", "queue"].includes(p.delivery)) admission.delivery = p.delivery;
    } else if (type === "session.step.started") {
      if (!id(p.assistantMessageID) || !liveWork(s)) {
        diag("unmatched_step");
        return;
      }
      s.assistant = p.assistantMessageID;
      retainedAdd(s.liveAssistants, p.assistantMessageID, 64);
      s.retry = false;
      resetFinal(s);
    } else if (type === "session.step.ended") {
      if (!id(p.assistantMessageID) || p.assistantMessageID !== s.assistant || !liveWork(s)) return;
      s.final = p.finish === "stop" && !s.retry && !s.compacting ? p.assistantMessageID : "";
    } else if (type === "session.step.failed" || type === "session.retry.scheduled") {
      resetFinal(s);
      s.retry = type === "session.retry.scheduled";
    } else if (type === "session.compaction.started") {
      s.compacting = true;
      resetFinal(s);
    } else if (type === "session.compaction.ended") {
      if (s.compacting && liveWork(s)) s.compactedUser = s.user;
      s.compacting = false;
      resetFinal(s);
    } else if (type === "session.compaction.failed") {
      s.compacting = false;
      resetFinal(s);
    } else if (type === "session.execution.interrupted") {
      s.interrupted = true;
      resetFinal(s);
      s.pending.clear();
    } else if (type === "session.execution.succeeded") {
      s.terminal = "success";
    } else if (type === "session.execution.failed") {
      if (!object(p.error)) {
        diag("invalid_failure");
        return;
      }
      const tag = p.error?._tag ?? p.error?.name ?? p.error?.type;
      if (typeof tag === "string" && /abort|cancel|interrupt/i.test(tag)) {
        s.interrupted = true;
        resetFinal(s);
      } else s.terminal = "failure";
    } else if (type === "form.replied" || type === "form.cancelled" || type === "permission.replied") {
      const rid = type === "permission.replied" ? p.requestID : p.id;
      if (!id(rid)) {
        diag("invalid_request");
        return;
      }
      retainedAdd(s.resolved, rid, 2048);
      s.pending.delete(rid);
      resetFinal(s);
    } else if (type === "form.created" || type === "permission.asked") {
      let rid, messageID, kind;
      if (type === "form.created") {
        const form = p.form;
        if (!object(form) || form.metadata?.kind !== "question" || !id(form.id) || !Array.isArray(form.fields) || form.fields.length === 0 || !id(form.metadata?.tool?.messageID) || !id(form.metadata?.tool?.id)) return;
        rid = form.id;
        messageID = form.metadata.tool.messageID;
        kind = "question_asked";
      } else {
        if (!id(p.id) || typeof p.action !== "string" || !p.action || !Array.isArray(p.resources) || p.source !== void 0 && (!object(p.source) || p.source.type !== "tool" || !id(p.source.messageID) || !id(p.source.id))) return;
        rid = p.id;
        messageID = p.source?.messageID;
        kind = "permission_asked";
      }
      if (!liveWork(s) || s.resolved.has(rid) || s.pending.has(rid)) return;
      if (s.pending.size >= 64) block(s);
      else s.pending.set(rid, { id: rid, messageID, kind });
      resetFinal(s);
    } else changed = false;
    if (s.admissionsOverflow && ["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"].includes(type)) {
      s.admissionsOverflow = false;
      s.admissions.clear();
    }
    if (changed) s.revision++;
    ensureOwnership(s);
    schedule(s);
  }
  function dispose() {
    if (disposed) return;
    disposed = true;
    lifecycle++;
    for (const record of requests) {
      clearTimeout(record.timer);
      record.controller.abort();
      record.stop();
    }
    sessions.clear();
    eventIDs.clear();
  }
  return { observe, dispose };
}

// node_modules/universal-agent-plugins-opencode-events/index.js
var object2 = (x) => x !== null && typeof x === "object" && !Array.isArray(x);
var id2 = (x) => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && new TextEncoder().encode(x).length <= 256;
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
        userCreated: void 0,
        seenUsers: /* @__PURE__ */ new Set(),
        assistant: "",
        failedAssistant: "",
        overflowPending: false,
        idleObserved: false,
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
    if (!object2(info) || info.id !== sid || info.parentID !== void 0 && !id2(info.parentID)) {
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
    if (!messages.every((m) => object2(m) && id2(m.id) && m.sessionID === sid && ["user", "assistant"].includes(m.role))) {
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
    s.idleObserved = true;
    const failed = Boolean(s.failedAssistant || s.overflowPending);
    if (!uid || !s.assistant && !failed || s.admitted.has("idle") || s.admitted.has("error") || s.retry && !failed || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    const messages = await latest(sid);
    if (!messages || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.retry && !failed || s.cancelled || s.errorPending || s.questions.size || s.permissions.size) return;
    if (!currentTurn(messages, uid)) return;
    const answer = messages.at(-1);
    if (s.failedAssistant || s.overflowPending) {
      if (!answer || answer.role !== "assistant" || answer.parentID !== uid || s.failedAssistant && answer.id !== s.failedAssistant || !object2(answer.error) || /abort|cancel/i.test(String(answer.error.name ?? ""))) return;
      if (s.overflowPending && !s.failedAssistant && answer.error.name !== "ContextOverflowError") return;
      const rootSession2 = await rootStatus(sid);
      if (rootSession2 === void 0 || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.cancelled) return;
      s.cancelled = true;
      s.revision++;
      await emit({ kind: "terminal_error", sessionID: sid, turnID: uid, rootSession: rootSession2 }, "error", s);
      return;
    }
    if (!answer || answer.role !== "assistant" || answer.id !== s.assistant || answer.parentID !== uid || answer.finish !== "stop" || !Number.isFinite(answer.time?.completed) || answer.error != null) return;
    const rootSession = await rootStatus(sid);
    if (rootSession === void 0 || sessions.get(sid) !== s || revision !== s.revision || s.user !== uid || s.cancelled || s.errorPending) return;
    await emit({ kind: "turn_idle_verified", sessionID: sid, turnID: uid, messageID: answer.id, rootSession }, "idle", s);
  }
  async function observe(event) {
    if (!object2(event) || !typeOK(event.type) || !object2(event.properties)) {
      diag("invalid native event");
      return;
    }
    const { type, properties: p } = event;
    if (type === "message.updated") {
      const m = p.info;
      if (!object2(m) || !id2(m.id) || !id2(m.sessionID) || !["user", "assistant"].includes(m.role)) {
        diag("invalid message.updated");
        return;
      }
      const s = state(m.sessionID);
      if (m.role === "user" && m.id !== s.user) {
        const created = m.time?.created;
        if (s.seenUsers.has(m.id) || Number.isFinite(created) && Number.isFinite(s.userCreated) && created < s.userCreated) return;
        s.seenUsers.add(m.id);
        if (s.seenUsers.size > 512) s.seenUsers.delete(s.seenUsers.values().next().value);
        s.userCreated = Number.isFinite(created) ? created : void 0;
        s.user = m.id;
        s.assistant = "";
        s.retry = false;
        s.retryAssistant = "";
        s.cancelled = false;
        s.failedAssistant = "";
        s.overflowPending = false;
        s.idleObserved = false;
        s.questions.clear();
        s.permissions.clear();
        s.resolved.clear();
        s.admitted.clear();
        s.turnEpoch++;
        s.revision++;
      }
      if (m.role === "assistant" && m.parentID === s.user && m.finish === "stop" && Number.isFinite(m.time?.completed) && m.error == null && m.id !== s.retryAssistant) {
        s.assistant = m.id;
        s.failedAssistant = "";
        s.overflowPending = false;
        s.retry = false;
        s.revision++;
      }
      if (m.role === "assistant" && m.parentID === s.user && m.error != null) {
        s.assistant = "";
        s.failedAssistant = m.id;
        s.revision++;
      }
      return;
    }
    if (type === "session.status") {
      if (!id2(p.sessionID) || !object2(p.status) || !["busy", "idle", "retry"].includes(p.status.type)) {
        diag("invalid session.status");
        return;
      }
      if (p.status.type === "busy") {
        const s = state(p.sessionID);
        s.idleObserved = false;
        s.revision++;
      }
      if (p.status.type === "retry") {
        const s = state(p.sessionID);
        s.retryAssistant = s.assistant;
        s.assistant = "";
        s.failedAssistant = "";
        s.overflowPending = false;
        s.idleObserved = false;
        s.retry = true;
        s.revision++;
      }
      if (p.status.type === "idle") await idle(p.sessionID);
      return;
    }
    if (type === "session.idle") {
      if (!id2(p.sessionID)) {
        diag("invalid session.idle");
        return;
      }
      await idle(p.sessionID);
      return;
    }
    if (type === "question.asked" || type === "permission.asked") {
      const requestMessageID = p.messageID ?? p.tool?.messageID;
      if (!id2(p.sessionID) || !id2(p.id) || requestMessageID !== void 0 && !id2(requestMessageID)) {
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
      if (!id2(p.sessionID) || !id2(p.requestID)) {
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
      if (!id2(p.sessionID) || !object2(p.error) || p.messageID !== void 0 && !id2(p.messageID)) {
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
      if (p.error.name === "ContextOverflowError") {
        s.overflowPending = true;
        s.revision++;
        if (s.idleObserved) await idle(p.sessionID);
        return;
      }
      s.cancelled = true;
      s.revision++;
      if (/abort|cancel/i.test(String(p.error.name ?? ""))) return;
      await emit({ kind: "terminal_error", sessionID: p.sessionID, turnID: uid, rootSession }, "error", s);
      return;
    }
    await emit(
      { kind: "unknown", nativeType: type, ...id2(p.sessionID) ? { sessionID: p.sessionID } : {} },
      `unknown:${type}:${id2(p.sessionID) ? p.sessionID : ""}`
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
        windowsHide: true,
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
function createBoundedForwarder({ limit = 16, spawnProcess = spawn, binary = executable, root = controlRoot, platform = process.platform } = {}) {
  if (!Number.isInteger(limit) || limit < 1 || limit > 64) throw new RangeError("invalid delivery limit");
  let active = 0;
  let disposed = false;
  return {
    async forward(event) {
      if (disposed) return "suppressed";
      if (active >= limit) return "capacity";
      return forward(event, (...args) => {
        const child = spawnProcess(...args);
        active++;
        child.once("close", () => {
          active--;
        });
        return child;
      }, binary, root, platform);
    },
    dispose() {
      disposed = true;
    }
  };
}

// plugin.mjs
var diagnostic = (reason) => console.error("Agent Notifications OpenCode observer:", reason);
var emitter = (deliver) => async (event) => {
  if (event.kind === "unknown" || event.rootSession !== true) return;
  const outcome = await deliver(event);
  if (outcome !== "submitted" && outcome !== "suppressed") {
    console.error("Agent Notifications OpenCode delivery:", outcome);
  }
};
async function server({ client }) {
  const observer = createObserver({
    client,
    emit: emitter(forward),
    onDiagnostic: diagnostic
  });
  return { event: async ({ event }) => observer.observe(event) };
}
function setup(ctx) {
  if (typeof ctx?.session?.get !== "function" || typeof ctx?.session?.context !== "function" || typeof ctx?.event?.subscribe !== "function") {
    diagnostic("invalid_host");
    return;
  }
  const controller = new AbortController();
  const delivery = createBoundedForwarder();
  let observer;
  try {
    observer = createV2Observer({ client: ctx.session, location: ctx.location, emit: emitter(delivery.forward), onDiagnostic: diagnostic });
  } catch {
    delivery.dispose();
    diagnostic("invalid_host");
    return;
  }
  let disposed = false;
  let iterator;
  const stop = () => {
    if (disposed) return;
    disposed = true;
    observer.dispose();
    delivery.dispose();
    controller.abort();
    try {
      void Promise.resolve(iterator?.return?.()).catch(() => {
      });
    } catch {
    }
  };
  void (async () => {
    try {
      iterator = ctx.event.subscribe({ signal: controller.signal })[Symbol.asyncIterator]();
      for (; ; ) {
        const next = await iterator.next();
        if (disposed || next.done) break;
        void Promise.resolve(observer.observe(next.value)).catch(() => {
          if (!disposed) diagnostic("observation_failed");
        });
      }
    } catch {
      if (!disposed) diagnostic("subscription_failed");
    } finally {
      stop();
    }
  })();
  return stop;
}
var plugin_default = { id: "agent-notifications", server, setup };
export {
  plugin_default as default
};
