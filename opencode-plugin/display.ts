// Product-owned optional desktop context. The UAP observer remains content-free.
export interface Fact {
  version: number;
  kind: string;
  sessionID?: string;
  requestID?: string;
  rootSession?: boolean;
}
export interface Display {
  sessionID: string;
  requestID?: string;
  sessionTitle?: string;
  question?: string;
}
export interface SessionClient {
  session: { get(input: { path: { id: string }; signal: AbortSignal }): Promise<unknown> };
}
type Snapshot = { sessionID: string; requestID: string; question: string; active: boolean; observers: number };
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const bytes = (value: string) => new TextEncoder().encode(value).length;
const validID = (value: unknown): value is string => typeof value === 'string' &&
  value.length > 0 && bytes(value) <= 256 && !/[\u0000-\u001f\u007f]/u.test(value);

export function cleanText(value: unknown, limit: number): string {
  if (typeof value !== 'string' || bytes(value) > limit ||
      /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/u.test(value) ||
      /[\ud800-\udfff]/u.test(value)) return '';
  return value.trim().replace(/\s+/gu, ' ');
}

export function createDisplayContext(client: SessionClient, timeoutMs = 1000, maxLookups = 8) {
  const requests = new Map<string, Snapshot>();
  const turns = new Map<string, string>();
  const seenTurns = new Set<string>();
  let activeLookups = 0;
  const key = (sessionID: string, requestID: string) => JSON.stringify([sessionID, requestID]);
  // Capture only native question text before any observer/client await. Headers,
  // options, prompts, paths and error payloads never enter this snapshot.
  function capture(event: unknown): Snapshot | undefined {
    if (!object(event) || !object(event.properties)) return;
    const p = event.properties;
    if (event.type === 'message.updated' && object(p.info) && p.info.role === 'user' && validID(p.info.sessionID) && validID(p.info.id)) {
      const turnKey = key(p.info.sessionID, p.info.id);
      if (turns.get(p.info.sessionID) !== p.info.id && !seenTurns.has(turnKey)) {
        for (const [k, snapshot] of requests) {
          if (snapshot.sessionID === p.info.sessionID) { snapshot.active = false; requests.delete(k); }
        }
        turns.set(p.info.sessionID, p.info.id);
        if (turns.size > 512) turns.delete(turns.keys().next().value!);
        seenTurns.add(turnKey);
        if (seenTurns.size > 512) seenTurns.delete(seenTurns.values().next().value!);
      }
    }
    if (['question.replied', 'question.rejected'].includes(String(event.type)) && validID(p.sessionID) && validID(p.requestID)) {
      const k = key(p.sessionID, p.requestID), snapshot = requests.get(k);
      if (snapshot) { snapshot.active = false; requests.delete(k); }
    }
    if (event.type !== 'question.asked' || !validID(p.sessionID) || !validID(p.id)) return;
    const k = key(p.sessionID, p.id);
    const existing = requests.get(k);
    if (existing) {
      // The SDK may admit any concurrent observation. Keep the first immutable
      // text alive until all observations finish, including an awaited emit.
      existing.observers++;
      return existing;
    }
    if (requests.size >= 512) return;
    let question = '';
    if (Array.isArray(p.questions) && p.questions.length > 0 && p.questions.length <= 8) {
      const parts = p.questions.map((q: unknown) => object(q) ? cleanText(q.question, 1024) : '');
      const combined = parts.join(' · ');
      if (parts.every(Boolean) && bytes(combined) <= 2048) question = combined;
    }
    const snapshot = { sessionID: p.sessionID, requestID: p.id, question, active: true, observers: 1 };
    requests.set(k, snapshot);
    return snapshot;
  }
  function release(snapshot: Snapshot | undefined) {
    if (!snapshot || --snapshot.observers > 0) return;
    snapshot.active = false;
    const k = key(snapshot.sessionID, snapshot.requestID);
    if (requests.get(k) === snapshot) requests.delete(k);
  }
  async function title(sessionID: string): Promise<string> {
    if (activeLookups >= maxLookups) return '';
    activeLookups++;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const lookup = Promise.resolve().then(() => client.session.get({ path: { id: sessionID }, signal: controller.signal }));
    // Keep capacity occupied until the native request settles, even if it ignores abort.
    lookup.then(() => { activeLookups--; }, () => { activeLookups--; });
    try {
      const result = await Promise.race([lookup, new Promise<undefined>((resolve) => {
        timer = setTimeout(() => { controller.abort(); resolve(undefined); }, timeoutMs);
      })]);
      const info = object(result) && 'data' in result ? result.data : result;
      return object(info) && info.id === sessionID ? cleanText(info.title, 1024) : '';
    } catch { return ''; } finally { clearTimeout(timer); }
  }
  async function enrich(fact: Fact): Promise<Fact & { display?: Display }> {
    if (fact.rootSession !== true || !validID(fact.sessionID)) return fact;
    const snapshot = fact.kind === 'question_asked' && validID(fact.requestID)
      ? requests.get(key(fact.sessionID, fact.requestID)) : undefined;
    const sessionTitle = await title(fact.sessionID);
    const question = snapshot?.active ? snapshot.question : '';
    if (!sessionTitle && !question) return fact;
    return { ...fact, display: {
      sessionID: fact.sessionID,
      ...(fact.kind === 'question_asked' && fact.requestID ? { requestID: fact.requestID } : {}),
      ...(sessionTitle ? { sessionTitle } : {}), ...(question ? { question } : {}),
    } };
  }
  return { capture, release, enrich };
}
