const id = (x) => typeof x === 'string' && x.length > 0 && Buffer.byteLength(x) <= 256 && !/[\u0000-\u001f]/u.test(x);
const stamp = (x) => Number.isSafeInteger(x) && x > 0;
const cancelled = (error) => /abort|cancel|interrupt/i.test(error?._tag ?? error?.name ?? error?.type ?? '');
const scope = (x) => x && typeof x.directory === 'string' ? Object.freeze({ directory: x.directory, workspaceID: x.workspaceID }) : undefined;
const same = (a, b) => a && b && a.directory === b.directory && a.workspaceID === b.workspaceID;
const rowTypes = new Set(['user', 'assistant', 'compaction', 'idle', 'agent-switched', 'model-switched',
  'location-switched', 'synthetic', 'system', 'skill', 'shell']);
const followsAssistant = (value, row) => value.slice(value.indexOf(row) + 1).some((item) => ['user', 'assistant', 'compaction'].includes(item.type));

// Native identity view, not a reducer: all scheduling, sequence/tombstone and
// checkpoint semantics remain in the single packed SDK engine.
export function createNativeV2(context, ownedLocation, onIngress, onUncertainty) {
  const own = scope(ownedLocation), projectID = ownedLocation?.project?.id, bindings = new Map(), projected = new WeakMap();
  if (!own || !id(projectID)) throw new TypeError('scope_unverified');
  function reset() { bindings.clear(); }
  function state(sid) {
    if (!bindings.has(sid)) {
      if (bindings.size >= 512) { onUncertainty(); return; }
      bindings.set(sid, { forms: new Map(), requests: new Map(), inbox: new Map() });
    }
    return bindings.get(sid);
  }
  function correlate(event) {
    const { type, data: p } = event;
    if (type === 'location.shutdown') return { location: scope(event.location)?.directory };
    const sid = type === 'form.created' ? p.form?.sessionID : p.sessionID;
    if (!id(sid) || sid === 'global') return;
    if (type === 'form.created' && p.form?.metadata?.kind !== 'question') return;
    onIngress(event);
    const b = state(sid); if (!b) return { sessionID: sid };
    const nativeScope = scope(event.location) ?? scope(p.location);
    if (nativeScope) b.scope = nativeScope;
    if (['session.moved', 'session.deleted'].includes(type)) { b.run = undefined; b.forms.clear(); b.requests.clear(); }
    if (type === 'session.execution.started') {
      b.run = event.id; b.user = undefined; b.assistant = undefined; b.step = undefined;
      b.terminal = undefined; b.forms.clear(); b.requests.clear();
    }
    if (type === 'session.inbox.enqueued' && id(p.inboxID) && id(p.item?.type)) {
      if (b.inbox.size >= 64 && !b.inbox.has(p.inboxID)) { onUncertainty(); return { sessionID: sid }; }
      if (b.inbox.has(p.inboxID) && b.inbox.get(p.inboxID) !== p.item.type) { onUncertainty(); return { sessionID: sid }; }
      b.inbox.set(p.inboxID, p.item.type);
    }
    if (type === 'session.inbox.delivered') {
      const kind = b.inbox.get(p.inboxID); b.inbox.delete(p.inboxID);
      if (kind === 'user') b.user = p.inboxID;
    }
    if (type === 'session.inbox.cancelled') b.inbox.delete(p.inboxID);
    const compaction = type.startsWith('session.compaction.') || p.type === 'compaction';
    if (type === 'session.step.started') { b.assistant = compaction ? undefined : p.assistantMessageID; b.step = undefined; b.forms.clear(); b.requests.clear(); }
    if (['session.step.ended', 'session.step.failed'].includes(type)) b.step = Object.freeze({
      type, eventID: event.id, messageID: p.assistantMessageID, compaction });
    if (['session.execution.succeeded', 'session.execution.failed'].includes(type)) {
      b.terminal = Object.freeze({ id: event.id, type, created: event.created, run: b.run, assistant: b.assistant, step: b.step });
      b.forms.clear(); b.requests.clear();
    }
    if (['session.retry.scheduled', 'session.execution.interrupted', 'session.compaction.started'].includes(type) || compaction) { b.forms.clear(); b.requests.clear(); }
    let requestID, messageID, callID;
    if (type === 'form.created') {
      const form = p.form;
      requestID = form.id; messageID = form.metadata?.tool?.messageID; callID = form.metadata?.tool?.id;
      if (id(requestID) && id(messageID) && id(callID) && Array.isArray(form.fields) && form.fields.length > 0) {
        if (b.forms.size >= 64) { onUncertainty(); return { sessionID: sid }; }
        b.forms.set(requestID, Object.freeze({ requestID, messageID, callID, run: b.run,
          eventID: event.id, created: event.created, tool: 'question' }));
      }
    } else if (type === 'permission.asked') {
      requestID = p.id;
      if (p.source?.type === 'tool') { messageID = p.source.messageID; callID = p.source.id; }
    } else if (['form.replied', 'form.cancelled', 'permission.replied'].includes(type)) {
      requestID = type === 'permission.replied' ? p.requestID : p.id; b.forms.delete(requestID); b.requests.delete(requestID);
    } else messageID = p.assistantMessageID;
    if (['form.created', 'permission.asked'].includes(type) && id(requestID) && id(messageID) && id(callID)) {
      if (b.requests.size >= 64) { onUncertainty(); return { sessionID: sid }; }
      b.requests.set(requestID, Object.freeze({ eventID: event.id, created: event.created, run: b.run, messageID, callID }));
    }
    const durable = event.durable;
    return { sessionID: sid, ...(nativeScope ? { location: nativeScope.directory } : {}),
      ...(messageID !== undefined ? { messageID } : {}), ...(requestID !== undefined ? { requestID } : {}),
      ...(callID !== undefined ? { callID } : {}), ...(compaction ? { compaction: true } : {}),
      ...(durable ? { sequence: { aggregate: durable.aggregateID, seq: durable.seq, version: durable.version } } : {}) };
  }
  const binding = (run) => {
    const b = bindings.get(run.sessionID);
    return b && b.run === run.turnID && b.user === run.userID && b.assistant &&
      (!run.terminalEventID || b.terminal?.id === run.terminalEventID && b.terminal?.created === run.terminalCreated) ? b : undefined;
  };
  async function session(run, signal) {
    const b = binding(run); if (!b || signal.aborted) return;
    const info = await context.session.get({ sessionID: run.sessionID });
    if (signal.aborted || binding(run) !== b || info?.id !== run.sessionID ||
        (info.parentID !== undefined && !id(info.parentID))) return;
    const observed = b.scope;
    const native = scope(info.location);
    // Public get strips workspaceID. Only independently observed scope proves it.
    if (!native || native.directory !== own.directory ||
        info.projectID !== projectID ||
        (observed && !same(observed, own)) ||
        (own.workspaceID !== undefined && !same(observed, own)) ||
        (native.workspaceID !== undefined && native.workspaceID !== own.workspaceID)) return;
    return Object.freeze({ ...run, location: native.directory, rootSession: info.parentID === undefined });
  }
  async function rows(run, signal) {
    const b = binding(run); if (!b || signal.aborted) return;
    const value = await context.session.context({ sessionID: run.sessionID });
    if (signal.aborted || binding(run) !== b || !Array.isArray(value) || value.length > 4096) return;
    const seen = new Set();
    for (const row of value) {
      if (!id(row?.id) || seen.has(row.id) || !rowTypes.has(row.type) ||
          row.sessionID !== undefined && row.sessionID !== run.sessionID) return;
      seen.add(row.id);
    }
    if (value.findLast((row) => row.type === 'user')?.id !== b.user) return;
    return value;
  }
  async function assistant(run, signal) {
    const proof = await session(run, signal); if (!proof?.rootSession) return;
    const b = binding(run), value = await rows(run, signal);
    if (!value || signal.aborted || binding(run) !== b || b.assistant !== run.messageID) return;
    const terminal = b.terminal, step = terminal?.step;
    const row = value.find((item) => item.id === run.messageID);
    // Exact schema mapping, not a fabricated terminal or desired outcome.
    const terminalMessageID = terminal?.id.startsWith('evt_') ? `msg_${terminal.id.slice(4)}` : undefined;
    if (row?.type !== 'assistant' || !stamp(row.time?.completed) || !stamp(row.time?.created) ||
        followsAssistant(value, row) || step?.messageID !== row.id || step.compaction ||
        !terminalMessageID || value.findIndex((item) => item.type === 'idle' && item.id === terminalMessageID) <= value.indexOf(row)) return;
    let outcome;
    if (terminal.type === 'session.execution.succeeded' && step.type === 'session.step.ended' &&
        row.finish === 'stop' && row.error == null) outcome = 'success';
    if (terminal.type === 'session.execution.failed' && row.error && !cancelled(row.error)) outcome = 'error';
    if (!outcome) return;
    return Object.freeze({ ...proof, messageID: row.id, role: 'assistant', summary: false, final: true, outcome });
  }
  async function questionSource(run, signal) {
    const b = binding(run), form = b?.forms.get(run.requestID);
    if (!form || form.run !== run.turnID || form.messageID !== run.messageID || form.callID !== run.callID) return;
    const proof = await session(run, signal), value = await rows(run, signal);
    if (!proof?.rootSession || !value || signal.aborted || binding(run) !== b || b.forms.get(run.requestID) !== form ||
        !value.some((row) => row.type === 'assistant' && row.id === form.messageID && !followsAssistant(value, row))) return;
    const assistant = value.find((row) => row.id === form.messageID && row.type === 'assistant');
    if (!Array.isArray(assistant?.content) || assistant.content.length > 256) return;
    const tools = assistant.content.filter((part) => part?.type === 'tool' && part.id === form.callID);
    if (tools.length !== 1 || tools[0].name !== 'question') return;
    return Object.freeze({ ...proof, requestID: form.requestID, messageID: form.messageID, callID: form.callID,
      role: 'assistant', tool: tools[0].name, summary: false });
  }
  async function currentPermission(run, signal) {
    const b = binding(run); if (!b || signal.aborted) return;
    const proof = await session(run, signal);
    if (!proof?.rootSession || signal.aborted || binding(run) !== b) return;
    // The LAST awaited read is the real pending cache snapshot.
    const request = await context.permission.get({ sessionID: run.sessionID, requestID: run.requestID });
    if (!proof?.rootSession || signal.aborted || binding(run) !== b || request?.id !== run.requestID ||
        request.sessionID !== run.sessionID || request.source?.type !== 'tool' ||
        request.source.messageID !== b.assistant || !id(request.source.id)) return;
    return Object.freeze({ ...proof, requestID: request.id, messageID: request.source.messageID,
      callID: request.source.id, pending: true });
  }
  async function finalize(event, handoff) {
    const b = bindings.get(event.sessionID);
    if (!b || !project(event) || !handoff.isCurrent() || handoff.signal.aborted) return false;
    const run = Object.freeze({ sessionID: event.sessionID, turnID: b.run, userID: b.user,
      location: own.directory, terminalEventID: event.requestID ? undefined : b.terminal?.id,
      terminalCreated: event.requestID ? undefined : b.terminal?.created });
    const proof = await session(run, handoff.signal);
    if (!proof?.rootSession || !handoff.isCurrent() || handoff.signal.aborted) return false;
    if (!event.requestID) {
      const answer = await assistant({ ...run, messageID: b.assistant }, handoff.signal);
      return handoff.isCurrent() && answer?.final === true && answer.outcome ===
        (event.kind === 'terminal_error' ? 'error' : 'success');
    }
    const value = await rows(run, handoff.signal);
    return handoff.isCurrent() && !handoff.signal.aborted && binding(run) === b &&
      b.requests.has(event.requestID) && value?.some((row) => row.type === 'assistant' && row.id === b.assistant && !followsAssistant(value, row));
  }
  function project(event) {
    const b = bindings.get(event.sessionID);
    if (!b || !id(b.run) || event.turnID !== b.user || event.provenance?.generation !== 'v2') return;
    const source = event.requestID ? b.requests.get(event.requestID) : b.terminal;
    if (!source || source.run !== b.run || (source.eventID ?? source.id) !== event.provenance.nativeEventID ||
        source.created !== event.provenance.nativeTime) return;
    // Packed strict reader preserves the published user-turn projection. AN's
    // private Execution binds the INDEPENDENTLY observed execution.started.id.
    // Original native provenance and all SDK handoff coordinates stay intact.
    let value = projected.get(event);
    if (!value || value.turnID !== b.run) {
      value = Object.freeze({ ...event, turnID: b.run }); projected.set(event, value);
    }
    return value;
  }
  return Object.freeze({ native: Object.freeze({ correlate, session, assistant, questionSource, currentPermission }), reset, project, finalize });
}

export function createRPCCheckpoint(context, ready) {
  return Object.freeze({ async register(signal, namespace) {
    if (!await ready() || signal.aborted) return;
    const id = `agent-notifications-${namespace}`;
    const schema = { type: 'object', properties: { nonce: { type: 'string', pattern: '^[a-f0-9]{32}$' } },
      required: ['nonce'], additionalProperties: false };
    const registration = await context.rpc.register({ id, methods: {}, events: { checkpoint: { schema } } }, {});
    if (signal.aborted) { await registration.dispose(); return; }
    return Object.freeze({ type: `rpc.${id}.checkpoint`,
      emit: (nonce) => registration.events.emit('checkpoint', { nonce }),
      read: (envelope) => envelope.data && Object.keys(envelope.data).length === 1 &&
        typeof envelope.data.nonce === 'string' && /^[a-f0-9]{32}$/.test(envelope.data.nonce) ? envelope.data.nonce : undefined,
      dispose: () => registration.dispose() });
  } });
}
