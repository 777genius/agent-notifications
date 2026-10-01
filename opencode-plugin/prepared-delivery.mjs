import { randomBytes } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { createLinuxClock } from './linux-clock.mjs';
import { clockReceipt, encodeFrame, fence, ns } from './protocol.mjs';

const bad = () => { throw new TypeError('source_unverified'); };
const overlaps = (a, b, bound) => a.offsetLoNS <= b.offsetHiNS + bound && a.offsetHiNS >= b.offsetLoNS - bound;
export function anchorMatches(anchor, before, after, policy) {
  const lo = ns(anchor.monoLoNS), hi = ns(anchor.monoHiNS), wall = ns(anchor.wallNS);
  return anchor.boot === before.boot && anchor.boot === after.boot && anchor.domain === before.domain &&
    anchor.domain === after.domain && anchor.rawKind === policy.rawKind &&
    ns(anchor.readUncertaintyNS) <= policy.nativeReadBoundNS && before.loNS <= hi && lo < after.hiNS &&
    after.hiNS - before.loNS <= policy.translationBoundNS &&
    overlaps({ offsetLoNS: wall - hi - policy.nativeReadBoundNS,
      offsetHiNS: wall - lo + policy.nativeReadBoundNS }, after, policy.comparisonBoundNS);
}

// Ports are private composition dependencies. The plugin never accepts them
// from host configuration/environment; only the closed compiled ledger supplies policy.
export function createPreparedDelivery({ registry, origin, policy, isOwned, onInvalidate, sourceFactory = createLinuxClock }) {
  let epoch, source, anchor, activation, activating, last, ingress, disposed = false;
  const originals = new Map(), prepared = new WeakMap();
  function invalidate(reason = 'clock') {
    if (!epoch && !source) return;
    epoch = undefined; originals.clear(); anchor = undefined; ingress = undefined; last = undefined;
    try { source?.dispose(); } catch {} source = undefined;
    onInvalidate?.(reason);
    registry.cancel();
  }
  function sample() {
    try {
      if (disposed || !source || !isOwned()) bad();
      const value = source.sample();
      if (last && (value.boot !== last.boot || value.domain !== last.domain || value.loNS < last.loNS ||
          !overlaps(last, value, policy.comparisonBoundNS))) bad();
      if (activation && !overlaps(activation, value, policy.comparisonBoundNS)) bad();
      last = value;
      return value;
    } catch { invalidate(); bad(); }
  }
  const ms = (record) => Number(record.loNS / 1000000n);
  const clock = Object.freeze({ id: 'linux-proc-boottime', now() {
    if (ingress) { const original = ingress; ingress = undefined; last = original; return ms(original); }
    return ms(sample());
  } });
  async function activate() {
    if (disposed) return false;
    if (epoch) return true;
    if (activating) return activating;
    activating = (async () => {
      const pending = randomBytes(16).toString('hex');
      try {
        source = sourceFactory(); activation = undefined;
        const before = sample();
        const r = await registry.clock({ isCurrent: () => !disposed && source !== undefined && isOwned(),
          deadline: performance.now() + 2000 });
        const after = sample(), original = clockReceipt(r.output);
        if (r.status !== 'ok' || !anchorMatches(original, before, after, policy) || after.loNS - before.loNS > 2000000000n) bad();
        anchor = original; activation = before;
        epoch = Object.freeze({ id: pending, started: before.loNS, after: after.hiNS });
        return true;
      } catch { invalidate(); return false; }
    })().finally(() => { activating = undefined; });
    return activating;
  }
  function retain(record) {
    if (!epoch) bad();
    for (const [tick] of originals) if (record.loNS - tick > 30000000000n) originals.delete(tick);
    if (!originals.has(record.loNS)) {
      if (originals.size >= 256) { invalidate(); bad(); }
      originals.set(record.loNS, Object.freeze({ sample: record, epoch, anchor }));
    }
  }
  function beginIngress(event) {
    const record = sample(); ingress = record;
    if (['session.idle', 'session.error', 'question.asked', 'permission.asked'].includes(event?.type) ||
        event?.type === 'session.status' && event.properties?.status?.type === 'idle' ||
        event?.type === 'message.updated' && event.properties?.info?.error != null) retain(record);
  }
  function nativeIngress(event) {
    if (['form.created', 'permission.asked', 'session.execution.succeeded', 'session.execution.failed'].includes(event?.type))
      retain(last ?? sample());
  }
  const current = (record, handoff) => {
    if (disposed || epoch !== record.epoch) return false;
    if (!isOwned()) { invalidate(); return false; }
    return handoff.clockID === clock.id && !handoff.signal.aborted && handoff.isCurrent();
  };
  async function beforeEmit(event, handoff) {
    try {
      const tick = BigInt(handoff.ingressMonotonicMs) * 1000000n;
      const original = originals.get(tick);
      if (!original || !current(original, handoff) || event.rootSession !== true || !event.provenance) return false;
      const before = sample(), remaining = handoff.metadataDeadline - clock.now();
      if (remaining <= 0) return false;
      const response = await registry.clock({ signal: handoff.signal, isCurrent: () => current(original, handoff),
        deadline: performance.now() + Math.min(remaining, 2000) });
      if (!current(original, handoff) || response.status !== 'ok') return false;
      const after = sample(), calibrationCheck = clockReceipt(response.output);
      if (!anchorMatches(calibrationCheck, before, after, policy) ||
          !overlaps(original.sample, after, policy.comparisonBoundNS) || after.loNS - before.loNS > 2000000000n) bad();
      // Keep the genuine PRE-ACTIVATION anchor; the later helper is only a check.
      const birthNS = BigInt(event.provenance.nativeTime) * 1000000n;
      if (birthNS < original.sample.wallNS - 60000000000n || birthNS > original.sample.wallNS + 2000000000n ||
          birthNS < activation.wallNS + (original.epoch.after - activation.loNS)) bad();
      const provenance = Object.freeze({ sourceEpoch: original.epoch.id,
        epochStartedTickNS: String(original.epoch.started), policyID: policy.profileID,
        fence: fence(original.anchor, policy), anchor: original.anchor, ingressTickNS: String(tick),
        calibration: Object.freeze({ calibrationID: policy.calibrationID, sourceEpoch: original.epoch.id,
          sourceLoNS: String(activation.loNS), sourceHiNS: String(original.epoch.after),
          nativeLoNS: original.anchor.monoLoNS, nativeHiNS: original.anchor.monoHiNS,
          errorNS: String(policy.translationBoundNS) }) });
      encodeFrame({ protocol: 1, origin, event, provenance: { ...provenance,
        spawnTickNS: String(after.loNS), deadlineTickNS: String(after.loNS + 20000000000n) } }, policy);
      prepared.set(event, { original, provenance });
      return true;
    } catch { invalidate(); return false; }
  }
  // No async function here. Registry acquires a slot, samples immediately before
  // spawn, then samples immediately after spawn, before any frame reaches stdin.
  function emit(event, handoff) {
    const held = prepared.get(event); prepared.delete(event);
    if (!held || !current(held.original, handoff)) return Promise.resolve();
    return registry.event({ signal: handoff.signal, isCurrent: () => current(held.original, handoff),
      prepare() {
        if (!current(held.original, handoff)) { invalidate(); bad(); }
        const before = sample();
        if (before.loNS - held.original.sample.loNS > 30000000000n ||
            ms(before) >= handoff.metadataDeadline || !overlaps(held.original.sample, before, policy.comparisonBoundNS)) { invalidate(); bad(); }
        const spawnTick = before.loNS;
        const deadline = ns((spawnTick + 20000000000n).toString());
        return () => {
          const after = sample();
          if (epoch !== held.original.epoch || handoff.signal.aborted || after.hiNS - before.loNS > 110000000n ||
              after.hiNS - held.original.sample.loNS > 30000000000n || after.hiNS >= deadline ||
              ms(after) >= handoff.metadataDeadline) { invalidate(); bad(); }
          return encodeFrame({ protocol: 1, origin, event, provenance: { ...held.provenance,
            spawnTickNS: String(spawnTick), deadlineTickNS: String(deadline) } }, policy);
        };
      },
    });
  }
  return Object.freeze({ clock, activate, beginIngress, nativeIngress, beforeEmit, emit, invalidate,
    allowsBirth(nativeTime) {
      return epoch && Number.isSafeInteger(nativeTime) && nativeTime > 0 &&
        BigInt(nativeTime) * 1000000n >= activation.wallNS + (epoch.after - activation.loNS);
    },
    dispose() { disposed = true; invalidate(); }, ready: () => Boolean(epoch && !disposed) });
}
