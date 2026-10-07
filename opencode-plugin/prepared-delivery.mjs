import { randomBytes } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { createPlatformClock } from './platform-clock.mjs';
import { withWallOffset } from './native-clock-contract.mjs';
import { bootOK, domainOK, clockReceipt, encodeFrame, fence, ns } from './protocol.mjs';

const bad = () => { throw new TypeError('source_unverified'); };
const overlaps = (a, b, bound) => a.offsetLoNS <= b.offsetHiNS + bound && a.offsetHiNS >= b.offsetLoNS - bound;
export function anchorMatches(anchor, before, after, policy) {
  const lo = ns(anchor.monoLoNS), hi = ns(anchor.monoHiNS), wall = ns(anchor.wallNS);
  return bootOK(anchor.boot) && domainOK(anchor.domain, anchor.rawKind) &&
    before.rawKind === policy.rawKind && after.rawKind === policy.rawKind &&
    anchor.boot === before.boot && anchor.boot === after.boot && anchor.domain === before.domain &&
    anchor.domain === after.domain && anchor.rawKind === policy.rawKind &&
    ns(anchor.readUncertaintyNS) <= policy.nativeReadBoundNS && before.loNS <= hi && lo < after.hiNS &&
    after.hiNS - before.loNS <= policy.translationBoundNS &&
    overlaps({ offsetLoNS: wall - hi - policy.nativeReadBoundNS,
      offsetHiNS: wall - lo + policy.nativeReadBoundNS }, after, policy.comparisonBoundNS);
}

// Ports are private composition dependencies. The plugin never accepts them
// from host configuration/environment; only the closed compiled ledger supplies policy.
export function createPreparedDelivery({ registry, origin, policy, isOwned, onInvalidate, onDiagnostic, enrich, sourceFactory = createPlatformClock }) {
  let epoch, source, anchor, activation, pending, last, ingress, disposed = false;
  const originals = new Map(), prepared = new WeakMap();
  const refuse = reason => {
    try { onDiagnostic?.(reason); } catch {}
    return false;
  };
  function invalidate(reason = 'clock') {
    if (!epoch && !source && !pending) return;
    const attempt = pending; pending = undefined;
    epoch = undefined; originals.clear(); anchor = undefined; activation = undefined; ingress = undefined; last = undefined;
    attempt?.controller.abort();
    const retired = source; source = undefined;
    try { retired?.dispose(); } catch {}
    try { onInvalidate?.(reason); } catch {} finally { registry.cancel(); }
  }
  function sample() {
    try {
      if (disposed || !source || !isOwned()) bad();
      const raw = source.sample();
      if (raw.rawKind !== policy.rawKind) bad();
      const value = withWallOffset(raw, policy.rawKind === 'linux-boottime' ? 2000000n : policy.sourceWallBoundNS);
      // Proc offsets retain the independently fixed Q2ms recipe.
      if (policy.rawKind === 'linux-boottime' &&
          (raw.offsetLoNS !== value.offsetLoNS || raw.offsetHiNS !== value.offsetHiNS)) bad();
      if (last && (value.boot !== last.boot || value.domain !== last.domain || value.loNS < last.loNS ||
          !overlaps(last, value, policy.comparisonBoundNS))) bad();
      if (activation && !overlaps(activation, value, policy.comparisonBoundNS)) bad();
      last = value;
      return value;
    } catch { invalidate(); bad(); }
  }
  const ms = (record) => Number(record.loNS / 1000000n);
  const clock = Object.freeze({ id: policy.sourceKind ?? (policy.rawKind === 'linux-boottime' ? 'linux-proc-boottime' : policy.rawKind), now() {
    if (ingress) { const original = ingress; ingress = undefined; last = original; return ms(original); }
    return ms(sample());
  } });
  async function activate() {
    if (disposed) return false;
    if (epoch) { if (isOwned()) return true; invalidate(); return false; }
    if (pending) return pending.promise;
    const attempt = { controller: new AbortController(), deadline: performance.now() + 2000 };
    pending = attempt;
    const isCurrent = () => {
      try { return pending === attempt && !disposed && !attempt.controller.signal.aborted &&
        performance.now() < attempt.deadline && isOwned(); } catch { return false; }
    };
    // The same existing 2s preparation budget covers ABI setup AND helper close.
    const timer = setTimeout(() => { if (pending === attempt) invalidate(); }, 2000);
    attempt.promise = (async () => {
      const id = randomBytes(16).toString('hex');
      try {
        const acquired = await new Promise((resolve, reject) => {
          const signal = attempt.controller.signal;
          const abort = () => reject(new TypeError('source_unverified'));
          signal.addEventListener('abort', abort, { once: true });
          let preparing;
          try { preparing = sourceFactory({ signal }); } catch (error) { preparing = Promise.reject(error); }
          Promise.resolve(preparing).then(value => {
            signal.removeEventListener('abort', abort);
            if (!isCurrent()) { try { value?.dispose(); } catch {} reject(new TypeError('source_unverified')); }
            else resolve(value);
          }, error => { signal.removeEventListener('abort', abort); reject(error); });
        });
        if (!isCurrent()) { try { acquired?.dispose(); } catch {} bad(); }
        source = acquired; activation = undefined;
        if (policy.rawKind !== 'linux-boottime' &&
            !policy.images?.some(image => image.imageSHA256 === source.imageSHA256)) bad();
        const before = sample();
        const r = await registry.clock({ signal: attempt.controller.signal, isCurrent, deadline: attempt.deadline });
        if (!isCurrent() || performance.now() >= attempt.deadline || r.status !== 'ok') bad();
        const after = sample(), original = clockReceipt(r.output);
        if (!anchorMatches(original, before, after, policy) || after.loNS - before.loNS > 2000000000n) bad();
        anchor = original; activation = before;
        epoch = Object.freeze({ id, started: before.loNS, after: after.hiNS });
        return true;
      } catch { if (pending === attempt) invalidate(); return false; }
      finally { clearTimeout(timer); if (pending === attempt) pending = undefined; }
    })();
    return attempt.promise;
  }
  function retain(record) {
    if (!epoch) bad();
    for (const [tick] of originals) if (record.loNS - tick > 30000000000n) originals.delete(tick);
    // SDK handoffs carry integer milliseconds. Keep the first genuine sample
    // in that bucket and serialize its exact counter, without restamping it.
    const tick = record.loNS / 1000000n * 1000000n;
    if (!originals.has(tick)) {
      if (originals.size >= 256) { invalidate(); bad(); }
      originals.set(tick, Object.freeze({ sample: record, epoch, anchor }));
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
    let diagnosticStage = 'prep.catch.original';
    try {
      const completion = policy.generation === 'v1' && event.version === 1 &&
        event.kind === 'turn_idle_verified' && event.provenance?.generation === 'v1' &&
        event.provenance.timeBasis === 'assistant_completed';
      // Completion uses neutral display rather than another optional native GET.
      // Keep the SDK fact itself as the WeakMap key and native authority.
      let display;
      if (!completion && enrich) { try { display = (await enrich(event))?.display; } catch {} }
      const tick = BigInt(handoff.ingressMonotonicMs) * 1000000n;
      const original = originals.get(tick);
      if (!original) return refuse('prep.original');
      if (!current(original, handoff)) return refuse('prep.current');
      if (event.rootSession !== true || !event.provenance) return refuse('prep.native');
      diagnosticStage = 'prep.catch.sample';
      const before = sample(), remaining = handoff.metadataDeadline - clock.now();
      if (remaining <= 0) return refuse('prep.remaining');
      let after;
      if (completion) {
        // Activation supplies the serialized native/source calibration. Fresh
        // samples still enforce its offset, ownership and monotonic bounds.
        after = sample();
        if (!current(original, handoff)) return refuse('prep.current');
      } else {
        diagnosticStage = 'prep.catch.helper';
        const response = await registry.clock({ signal: handoff.signal, isCurrent: () => current(original, handoff),
          deadline: performance.now() + Math.min(remaining, 2000) });
        if (!current(original, handoff)) return refuse('prep.current_after_helper');
        if (response.status !== 'ok') return refuse('prep.helper');
        diagnosticStage = 'prep.catch.calibration';
        after = sample();
        const calibrationCheck = clockReceipt(response.output);
        if (!anchorMatches(calibrationCheck, before, after, policy)) bad();
      }
      if (!overlaps(original.sample, after, policy.comparisonBoundNS) || after.loNS - before.loNS > 2000000000n) bad();
      // Keep the genuine PRE-ACTIVATION anchor; no completion restamping.
      diagnosticStage = 'prep.catch.birth';
      const birthNS = BigInt(event.provenance.nativeTime) * 1000000n;
      if (birthNS < original.sample.wallNS - 60000000000n || birthNS > original.sample.wallNS + 2000000000n ||
          birthNS < activation.wallNS + (original.epoch.after - activation.loNS)) bad();
      const provenance = Object.freeze({ sourceEpoch: original.epoch.id,
        epochStartedTickNS: String(original.epoch.started), policyID: policy.profileID,
        fence: fence(original.anchor, policy), anchor: original.anchor, ingressTickNS: String(original.sample.loNS),
        calibration: Object.freeze({ calibrationID: policy.calibrationID, sourceEpoch: original.epoch.id,
          sourceLoNS: String(activation.loNS), sourceHiNS: String(original.epoch.after),
          nativeLoNS: original.anchor.monoLoNS, nativeHiNS: original.anchor.monoHiNS,
          errorNS: String(policy.translationBoundNS) }) });
      diagnosticStage = 'prep.catch.frame';
      encodeFrame({ protocol: 1, origin, event, ...(display ? { display } : {}), provenance: { ...provenance,
        spawnTickNS: String(after.loNS), deadlineTickNS: String(after.loNS + 20000000000n) } }, policy);
      prepared.set(event, { original, provenance, display });
      return true;
    } catch {
      invalidate();
      return refuse(diagnosticStage);
    }
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
          return encodeFrame({ protocol: 1, origin, event, ...(held.display ? { display: held.display } : {}), provenance: { ...held.provenance,
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
