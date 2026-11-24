// Observational E0 probe of the actual packaged clocks. Not a TimePolicy,
// native observer, or notification-delivery qualification.
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { isAbsolute } from 'node:path';

const expectedImage = '50503d24577fa4117a7c95ee85b2cd679606a87eacf20ca54ed5db5c9e1cf895';
const grants = { nativeObserverQualified: false, nativeClockQualified: false, notificationDeliveryQualified: false };
const allowed = new Set(['wrong_platform', 'wrong_bun', 'invalid_helper', 'host_image_mismatch',
  'clock_unavailable', 'clock_sample_invalid', 'go_clock_unavailable', 'clock_tuple_mismatch',
  'clock_containment_unverified', 'clock_disposal_failed', 'clock_stress_nonmonotonic']);
function requireTrue(ok, code) { if (!ok) throw new Error(code); }
const decimal = x => {
  requireTrue(typeof x === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(x), 'clock_sample_invalid');
  return BigInt(x);
};
async function digest(file) {
  const h = createHash('sha256');
  for await (const block of createReadStream(file)) h.update(block);
  return h.digest('hex');
}
function readSource(clock) {
  const s = clock.sample();
  requireTrue(Object.isFrozen(s) && s.rawKind === 'linux-boottime' &&
    typeof s.boot === 'string' && /^[-a-f0-9]{36}$/.test(s.boot) &&
    /^linux-time:[1-9][0-9]*:[1-9][0-9]*$/.test(s.domain) &&
    ['loNS','hiNS','wallNS','offsetLoNS','offsetHiNS'].every(k => typeof s[k] === 'bigint') &&
    s.loNS > 0n && s.hiNS >= s.loNS && s.hiNS-s.loNS <= 110000000n && s.wallNS > 0n &&
    s.offsetLoNS === s.wallNS-s.hiNS-2000000n && s.offsetHiNS === s.wallNS-s.loNS+2000000n,
    'clock_sample_invalid');
  return s;
}
async function main() {
  requireTrue(process.platform === 'linux' && process.arch === 'x64', 'wrong_platform');
  requireTrue(globalThis.Bun?.version === '1.4.4', 'wrong_bun');
  const helper = process.argv[2];
  requireTrue(typeof helper === 'string' && isAbsolute(helper), 'invalid_helper');
  requireTrue(await digest(process.execPath) === expectedImage, 'host_image_mismatch');
  const { createPlatformClock } = await import('../opencode-plugin/platform-clock.mjs');
  let clock;
  let maxSpan = 0n;
  try {
    clock = await createPlatformClock();
    // Multiple official-Bun/Go comparisons, including scheduling pressure.
    // This is still E0 observation, never a reviewed Q/R/T policy.
    for (let trial=0; trial<12; trial++) {
      const before = readSource(clock);
      const run = spawnSync(helper, ['opencode-clock','--protocol','1'],
        { timeout: 2000, maxBuffer: 1024, env: {PATH:'/usr/bin:/bin'} });
      const after = readSource(clock);
      requireTrue(!run.error && run.status === 0 && run.stdout?.length > 0 && run.stdout.length <= 1024,
        'go_clock_unavailable');
      const raw = run.stdout.toString('utf8');
      let g;
      try { g = JSON.parse(raw); } catch { throw new Error('go_clock_unavailable'); }
      const keys = ['protocol','boot','clockDomain','clockKind','monoLoNs','monoHiNs','wallUnixNs','uncertaintyNs'];
      requireTrue(Object.keys(g).join('|') === keys.join('|') &&
        g.protocol === 1 && raw === JSON.stringify(g)+'\n', 'go_clock_unavailable');
      requireTrue(before.boot === g.boot && g.boot === after.boot &&
        before.domain === g.clockDomain && g.clockDomain === after.domain &&
        g.clockKind === 'linux-boottime', 'clock_tuple_mismatch');
      const lo = decimal(g.monoLoNs), hi = decimal(g.monoHiNs), wall = decimal(g.wallUnixNs);
      requireTrue(decimal(g.uncertaintyNs) === hi-lo+3000000n && hi >= lo, 'go_clock_unavailable');
      const span = after.hiNS-before.loNS;
      requireTrue(before.loNS <= lo && hi <= after.hiNS && span >= 0n && span <= 224000000n &&
        before.wallNS-2000000n <= wall && wall <= after.wallNS+2000000n,
        'clock_containment_unverified');
      if (span > maxSpan) maxSpan = span;
    }
    // Probe counter/wall monotonicity across an actual timer delay; do not
    // infer a source-event Q bound or a clock trust grant from this pause.
    const pauseBefore = readSource(clock);
    await new Promise(resolve => setTimeout(resolve, 75));
    const pauseAfter = readSource(clock);
    requireTrue(pauseAfter.boot === pauseBefore.boot && pauseAfter.domain === pauseBefore.domain &&
      pauseAfter.loNS >= pauseBefore.loNS && pauseAfter.wallNS >= pauseBefore.wallNS,
      'clock_stress_nonmonotonic');
    clock.dispose();
    let refused = false;
    try { clock.sample(); } catch { refused = true; }
    requireTrue(refused, 'clock_disposal_failed');
    requireTrue(await digest(process.execPath) === expectedImage, 'host_image_mismatch');
    return { status:'observed_e0_only', stockHostVersion:'2.2.6', stockBunVersion:'1.4.4',
      originalGoClockComparisonRounds:12, delayedCounterAndWallNonregressionObserved:true,
      maxCounterSpanNS:String(maxSpan), disposalRefused:true, ...grants };
  } finally { try { clock?.dispose(); } catch {} }
}
try {
  console.log(JSON.stringify(await main()));
} catch (error) {
  const code = allowed.has(error?.message) ? error.message : 'unqualified';
  console.log(JSON.stringify({ status:'unqualified', failureCode:code, ...grants }));
  process.exitCode = 1;
}
