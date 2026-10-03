import { createHash } from 'node:crypto';

const invalid = () => { throw new TypeError('invalid_private_protocol'); };
export const int64Max = 9223372036854775807n;
export function ns(value) {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,18})$/.test(value)) invalid();
  const result = BigInt(value);
  if (result > int64Max) invalid();
  return result;
}
export function closed(value, keys, required = keys) {
  if (!value || Object.getPrototypeOf(value) !== Object.prototype ||
      Reflect.ownKeys(value).some((key) => !keys.includes(key)) ||
      required.some((key) => !Object.hasOwn(value, key))) invalid();
  return value;
}
const identity = (x, limit = 256) => typeof x === 'string' && x.length > 0 &&
  !/[\u0000-\u001f\u007f]/u.test(x) && Buffer.byteLength(x) <= limit;
export const bootOK = (x) => typeof x === 'string' &&
  /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(x) && x !== '00000000-0000-0000-0000-000000000000';
export const rawKindOK = (kind) => ['linux-boottime', 'darwin-monotonic-raw', 'windows-interrupt-precise'].includes(kind);
export function domainOK(x, kind = 'linux-boottime') {
  if (kind === 'darwin-monotonic-raw') return x === 'darwin-kernel';
  if (kind === 'windows-interrupt-precise') return x === 'windows-kernel';
  if (kind !== 'linux-boottime' || typeof x !== 'string' || !/^linux-time:[1-9][0-9]*:[1-9][0-9]*$/.test(x)) return false;
  return x.split(':').slice(1).every(part => part.length <= 20 && BigInt(part) <= 18446744073709551615n);
}

// JSON.parse alone loses duplicate fields. Bound the grammar before decoding.
export function parseJSON(bytes, max = 4096) {
  if (!Buffer.isBuffer(bytes) || bytes.length > max) invalid();
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  const token = /\s*("(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null|[{}\[\],:])/y;
  let at = 0, entries = 0;
  function next() { token.lastIndex = at; const m = token.exec(text); if (!m) invalid(); at = token.lastIndex; return m[1]; }
  function value(t, depth) {
    if (depth > 8 || ++entries > 96) invalid();
    if (t === '{') {
      const keys = new Set(); let k = next();
      if (k === '}') return;
      for (;;) {
        if (!k.startsWith('"')) invalid();
        const key = JSON.parse(k); if (keys.has(key)) invalid(); keys.add(key);
        if (next() !== ':') invalid(); value(next(), depth + 1);
        k = next(); if (k === '}') return; if (k !== ',') invalid(); k = next();
      }
    }
    if (t === '[') {
      let v = next(); if (v === ']') return;
      for (;;) { value(v, depth + 1); v = next(); if (v === ']') return; if (v !== ',') invalid(); v = next(); }
    }
    if (['}', ']', ',', ':'].includes(t) || !t.startsWith('"') && !['true', 'false', 'null'].includes(t) &&
        (!/^-?(0|[1-9][0-9]*)$/.test(t) || !Number.isSafeInteger(Number(t)))) invalid();
  }
  value(next(), 0);
  if (text.slice(at).trim()) invalid();
  return JSON.parse(text);
}

export function profileReceipt(output) {
  const receipt = closed(parseJSON(output, 1024), ['protocol', 'semantic', 'generation', 'resourceClosure']);
  if (receipt.protocol !== 1 || receipt.resourceClosure !== 'reaped_or_not_started' ||
      !((receipt.semantic === 'unverified' && receipt.generation === 'none') ||
        (receipt.semantic === 'eligible' && ['v1', 'v2'].includes(receipt.generation)))) invalid();
  return Object.freeze(receipt);
}
export function clockReceipt(output) {
  const r = closed(parseJSON(output, 1024), ['protocol', 'boot', 'clockDomain', 'clockKind',
    'monoLoNs', 'monoHiNs', 'wallUnixNs', 'uncertaintyNs']);
  const lo = ns(r.monoLoNs), hi = ns(r.monoHiNs);
  if (r.protocol !== 1 || !bootOK(r.boot) || !domainOK(r.clockDomain, r.clockKind) ||
      hi < lo || hi - lo > 100000000n || ns(r.wallUnixNs) === 0n || ns(r.uncertaintyNs) !== hi - lo + 3000000n) invalid();
  return Object.freeze({ boot: r.boot, domain: r.clockDomain, rawKind: r.clockKind,
    monoLoNS: r.monoLoNs, monoHiNS: r.monoHiNs, wallNS: r.wallUnixNs, readUncertaintyNS: r.uncertaintyNs });
}
export function fence(anchor, policy) {
  const hash = createHash('sha256');
  for (const string of ['AN/OpenCode/clock-policy/v1', anchor.boot, anchor.domain, anchor.rawKind, policy.profileID]) {
    const data = Buffer.from(string), length = Buffer.alloc(4); length.writeUInt32BE(data.length); hash.update(length).update(data);
  }
  for (const bound of [policy.nativeReadBoundNS, policy.comparisonBoundNS]) {
    const data = Buffer.alloc(8); data.writeBigInt64BE(bound); hash.update(data);
  }
  return hash.digest('hex');
}
export function validateFrame(frame, policy) {
  const p = closed(frame, ['protocol', 'origin', 'event', 'provenance']);
  if (p.protocol !== 1 || typeof p.origin !== 'string' || !/^[a-f0-9]{64}$/.test(p.origin)) invalid();
  const e = closed(p.event, ['version', 'kind', 'sessionID', 'turnID', 'rootSession', 'provenance', 'messageID', 'requestID', 'nativeType'],
    ['version', 'kind', 'sessionID', 'turnID', 'rootSession', 'provenance']);
  if (e.version !== 1 || e.rootSession !== true || !identity(e.sessionID) || !identity(e.turnID) ||
      !['turn_idle_verified', 'question_asked', 'permission_asked', 'terminal_error'].includes(e.kind) ||
      e.nativeType !== undefined || (e.messageID !== undefined && !identity(e.messageID)) ||
      (e.requestID !== undefined && !identity(e.requestID)) ||
      (e.kind === 'turn_idle_verified' && (!identity(e.messageID) || e.requestID !== undefined)) ||
      (e.kind.endsWith('_asked') && (!identity(e.requestID) || e.messageID !== undefined)) ||
      (e.kind === 'terminal_error' && (e.messageID !== undefined || e.requestID !== undefined))) invalid();
  const native = closed(e.provenance, ['generation', 'observationID', 'nativeTime', 'timeBasis', 'nativeEventID', 'nativeMessageID'],
    ['generation', 'observationID', 'nativeTime', 'timeBasis']);
  if (!identity(native.observationID) || !Number.isSafeInteger(native.nativeTime) || native.nativeTime <= 0 ||
      BigInt(native.nativeTime) * 1000000n > int64Max ||
      !['v1', 'v2'].includes(native.generation) || native.generation !== policy.generation ||
      (native.nativeEventID !== undefined && !identity(native.nativeEventID)) ||
      (native.nativeMessageID !== undefined && !identity(native.nativeMessageID)) ||
      (native.generation === 'v2' && (!identity(native.nativeEventID) || native.timeBasis !== 'envelope_created')) ||
      (native.generation === 'v1' && native.timeBasis !== (e.kind === 'turn_idle_verified' ? 'assistant_completed' : 'assistant_created_lower_bound')) ||
      (native.generation === 'v1' && e.kind === 'terminal_error' && !identity(native.nativeMessageID))) invalid();
  const v = closed(p.provenance, ['sourceEpoch', 'epochStartedTickNS', 'policyID', 'fence', 'anchor',
    'ingressTickNS', 'spawnTickNS', 'deadlineTickNS', 'calibration']);
  if (!identity(v.sourceEpoch, 128) || !/^[\x20-\x7e]+$/.test(v.sourceEpoch) ||
      v.policyID !== policy.profileID || !rawKindOK(policy.rawKind) ||
      policy.nativeReadBoundNS < 3000000n || policy.nativeReadBoundNS > 103000000n ||
      policy.comparisonBoundNS < 2n * policy.nativeReadBoundNS || policy.comparisonBoundNS > 2000000000n ||
      policy.translationBoundNS < 0n || 2n * policy.nativeReadBoundNS + policy.translationBoundNS > policy.comparisonBoundNS) invalid();
  const a = closed(v.anchor, ['boot', 'domain', 'rawKind', 'monoLoNS', 'monoHiNS', 'wallNS', 'readUncertaintyNS']);
  const lo = ns(a.monoLoNS), hi = ns(a.monoHiNS);
  if (!bootOK(a.boot) || !domainOK(a.domain, a.rawKind) || a.rawKind !== policy.rawKind || hi < lo || hi - lo > 100000000n ||
      ns(a.wallNS) === 0n || ns(a.readUncertaintyNS) !== hi - lo + 3000000n || ns(a.readUncertaintyNS) > policy.nativeReadBoundNS ||
      v.fence !== fence(a, policy)) invalid();
  const start = ns(v.epochStartedTickNS), ingress = ns(v.ingressTickNS), spawn = ns(v.spawnTickNS), deadline = ns(v.deadlineTickNS);
  if (start > lo || lo > ingress || ingress > spawn || spawn >= deadline ||
      spawn - ingress > 30000000000n || deadline - spawn > 20000000000n) invalid();
  const c = closed(v.calibration, ['calibrationID', 'sourceEpoch', 'sourceLoNS', 'sourceHiNS', 'nativeLoNS', 'nativeHiNS', 'errorNS']);
  if (c.calibrationID !== policy.calibrationID || c.sourceEpoch !== v.sourceEpoch ||
      ns(c.sourceLoNS) >= ns(c.sourceHiNS) || ns(c.sourceHiNS) - ns(c.sourceLoNS) > policy.translationBoundNS ||
      ns(c.sourceLoNS) > hi || lo >= ns(c.sourceHiNS) || c.nativeLoNS !== a.monoLoNS || c.nativeHiNS !== a.monoHiNS ||
      ns(c.errorNS) !== policy.translationBoundNS) invalid();
  return frame;
}
export function encodeFrame(frame, policy) {
  validateFrame(frame, policy);
  const output = Buffer.from(JSON.stringify(frame));
  if (output.length > 4096) invalid();
  return output;
}
