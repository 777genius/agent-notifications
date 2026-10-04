import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const protocol = await import(new URL('./protocol.mjs', import.meta.url).href);
const literal = readFileSync(new URL('./fixtures/private-frame.json', import.meta.url));
const policy = Object.freeze({ generation: 'v2', profileID: 'independent-fixture-only',
  calibrationID: 'fixture-calibration', rawKind: 'linux-boottime', nativeReadBoundNS: 103000000n,
  comparisonBoundNS: 430000000n, translationBoundNS: 224000000n });

// Red if display replaces SDK identity/time, enlarges the frame, or authorizes
// an invalid neutral fact. This replaces the old raw-forward oversized test.
test('private optional display preserves authority and falls back to exact neutral bytes', () => {
  const frame = protocol.parseJSON(literal);
  const neutral = protocol.encodeFrame(frame, policy);
  const display = { sessionID: frame.event.sessionID, sessionTitle: 'Native title' };
  const decorated = protocol.parseJSON(protocol.encodeFrame({ ...frame, display }, policy));
  assert.deepEqual(decorated.event, frame.event);
  assert.deepEqual(decorated.provenance, frame.provenance);
  assert.deepEqual(decorated.display, display);
  assert.equal(decorated.origin, frame.origin);
  const oversized = { ...display, question: '"'.repeat(4096) };
  assert.deepEqual(protocol.encodeFrame({ ...frame, display: oversized }, policy), neutral);
  assert.throws(() => protocol.encodeFrame({ ...frame, event: { ...frame.event, rootSession: false }, display }, policy));
  assert.throws(() => protocol.parseJSON(Buffer.from(JSON.stringify(decorated).replace('"display":', '"display":{},"display":'))));
});

// Red if a delayed optional lookup precedes an already-finished clock check or
// restamps ingress: expiration during lookup must prevent a new helper query.
// Independent synthetic clock/registry ports grant no installed qualification.
test('optional lookup expiration keeps original ingress and prevents helper admission', async () => {
  const { createPreparedDelivery } = await import(new URL('./prepared-delivery.mjs', import.meta.url).href);
  const boot = '11111111-2222-3333-4444-555555555555', domain = 'linux-time:4:4026531834';
  const base = 1000000000000n;
  let tick = base, calls = 0, reached = false;
  const sample = () => {
    const wallNS = 1700000000000000000n + tick - base;
    return { boot, domain, rawKind: 'linux-boottime', loNS: tick, hiNS: tick + 10000000n,
      wallNS, offsetLoNS: wallNS - tick - 12000000n, offsetHiNS: wallNS - tick + 2000000n };
  };
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const registry = { cancel() {}, async clock() {
    calls++;
    const lo = tick, wall = sample().wallNS;
    tick += 20000000n;
    return { status: 'ok', output: Buffer.from(JSON.stringify({ protocol: 1, boot, clockDomain: domain,
      clockKind: 'linux-boottime', monoLoNs: String(lo + 1000000n), monoHiNs: String(lo + 2000000n),
      wallUnixNs: String(wall), uncertaintyNs: '4000000' })) };
  }, event() { throw Error('expired display reached child admission'); } };
  const delivery = createPreparedDelivery({ registry, origin: 'a'.repeat(64), policy, isOwned: () => true,
    sourceFactory: () => ({ sample, dispose() {} }), enrich: async (event: unknown) => {
      reached = true; await held; return { event, display: { sessionID: 's', sessionTitle: 'Late title' } };
    } });
  try {
    assert.equal(await delivery.activate(), true);
    tick = base + 60000000n;
    delivery.beginIngress({ type: 'question.asked' });
    const ingress = delivery.clock.now(), controller = new AbortController();
    const handoff = { signal: controller.signal, ingressMonotonicMs: ingress, clockID: delivery.clock.id,
      metadataDeadline: ingress + 2000, isCurrent: () => !controller.signal.aborted };
    const event = protocol.parseJSON(literal).event;
    const work = delivery.beforeEmit(event, handoff);
    await Promise.resolve();
    tick += 2000000000n;
    release();
    assert.equal(await work, false);
    assert.equal(reached, true);
    assert.equal(calls, 1, 'expired lookup must not requery/re-anchor');
    assert.equal(handoff.ingressMonotonicMs, ingress);
  } finally { release(); delivery.dispose(); }
});
