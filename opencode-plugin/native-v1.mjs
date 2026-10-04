const id = (x) => typeof x === 'string' && x.length > 0 && !/[\u0000-\u001f]/u.test(x) && Buffer.byteLength(x) <= 256;
const stamp = (x) => Number.isSafeInteger(x) && x > 0;
// Only captured native identity bindings. SDK owns lineage/control/tombstones.
export function createNativeV1(client, directory, fail) {
  const requests = new Map(), users = new Map();
  function ingest(event) {
    const p = event?.properties;
    if (event?.type === 'message.updated' && p?.info?.role === 'user') {
      const { sessionID, id: userID, time } = p.info;
      if (!id(sessionID) || !id(userID) || !stamp(time?.created)) { fail(); return; }
      const prior = users.get(sessionID), created = time.created;
      // Summary/diff republication is not a new native user birth. Retain the
      // admitted identities without eviction; ambiguity/capacity closes authority.
      if (prior?.seen.has(userID)) {
        if (prior.seen.get(userID) !== created) fail();
        return;
      }
      if (prior && created < prior.created) return;
      if (prior?.seen.size >= 512 || !prior && users.size >= 512) { fail(); return; }
      const seen = prior?.seen ?? new Map();
      seen.set(userID, created);
      users.set(sessionID, { id: userID, created, seen });
    }
    if (['question.asked', 'permission.asked'].includes(event?.type) && id(p?.id)) {
      if (requests.size >= 256 && !requests.has(p.id)) { fail(); return; }
      const next = { sessionID: p.sessionID, messageID: p.tool?.messageID ?? p.messageID,
        callID: p.tool?.callID, turnID: users.get(p.sessionID)?.id };
      const prior = requests.get(p.id);
      if (prior && Object.keys(next).some((key) => prior[key] !== next[key])) { fail(); return; }
      if (!prior) requests.set(p.id, Object.freeze(next));
    }
    if (['question.replied', 'question.rejected', 'permission.replied'].includes(event?.type)) requests.delete(p?.requestID);
    if (['session.deleted', 'session.compacted', 'session.error'].includes(event?.type) ||
        event?.type === 'session.status' && p?.status?.type === 'retry') {
      const sid = p?.sessionID ?? p?.info?.id;
      for (const [key, value] of requests) if (value.sessionID === sid) requests.delete(key);
    }
  }
  async function finalize(event, handoff) {
    if (!handoff.isCurrent() || handoff.signal.aborted || users.get(event.sessionID)?.id !== event.turnID) return false;
    // The pinned strict SDK has already finalized this root/current message.
    // Retain the live native user/control binding without a second HTTP snapshot.
    if (event.kind === 'turn_idle_verified') return event.version === 1 && event.rootSession === true &&
      event.requestID === undefined && id(event.sessionID) && id(event.turnID) && id(event.messageID) &&
      event.provenance?.generation === 'v1' && event.provenance.timeBasis === 'assistant_completed' &&
      id(event.provenance.observationID) && stamp(event.provenance.nativeTime);
    const request = event.requestID ? requests.get(event.requestID) : undefined;
    if (event.requestID && (!request || request.sessionID !== event.sessionID || request.turnID !== event.turnID ||
        !id(request.messageID) || !id(request.callID))) return false;
    const result = await client.session.get({ path: { id: event.sessionID }, signal: handoff.signal });
    if (!handoff.isCurrent() || handoff.signal.aborted) return false;
    const info = result?.data ?? result;
    if (info?.id !== event.sessionID || info.parentID !== undefined || info.directory !== directory) return false;
    const snapshot = await client.session.messages({ path: { id: event.sessionID }, query: { limit: 30 }, signal: handoff.signal });
    if (!handoff.isCurrent() || handoff.signal.aborted || users.get(event.sessionID)?.id !== event.turnID ||
        request && requests.get(event.requestID) !== request) return false;
    const rows = Array.isArray(snapshot) ? snapshot : snapshot?.data;
    if (!Array.isArray(rows) || rows.length > 30) return false;
    const messages = rows.map((row) => row?.info ?? row);
    if (!messages.every((row) => id(row?.id) && row.sessionID === event.sessionID)) return false;
    const answer = messages.findLast((row) => row.role === 'assistant' && row.summary !== true);
    const lastUser = messages.findLast((row) => row.role === 'user');
    if (lastUser && lastUser.id !== event.turnID || !answer || answer.parentID !== event.turnID || answer.path?.cwd !== directory ||
        messages.at(-1) !== answer || !stamp(answer.time?.created)) return false;
    if (event.kind === 'terminal_error') return answer.id === event.provenance.nativeMessageID &&
      answer.time.created === event.provenance.nativeTime && stamp(answer.time?.completed) && answer.error &&
      !/abort|cancel/i.test(answer.error.name ?? '');
    return request?.messageID === answer.id && answer.time.created === event.provenance.nativeTime;
  }
  return Object.freeze({ ingest, finalize, dispose() { users.clear(); requests.clear(); } });
}
