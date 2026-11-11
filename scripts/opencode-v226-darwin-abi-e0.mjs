// Actual macOS ARM64/Bun 1.4.4 FFI observation, NOT a production admission.
// Uses the production ABI, clock conversion, and sampling implementation, but
// deliberately does not create a qualified clock cell or observer receipt.
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { isAbsolute } from 'node:path';
import { performance } from 'node:perf_hooks';
import { dlopen, ptr } from 'bun:ffi';
import { address, createNativeClock, darwinABI, darwinBoot, machNS, positiveNS } from '../opencode-plugin/native-clock-contract.mjs';
import { createDarwinClock } from '../opencode-plugin/darwin-clock.mjs';

const officialImage = '90e4d03597b03d2051b65b4d793467764e55bacd8e22bf4824479c7233ea0e15';
const grants = { nativeObserverQualified:false, nativeClockQualified:false, notificationDeliveryQualified:false };
let phase = 'entry';
let spanFailure;
const reasons = new Set(['platform_unverified','helper_unverified','image_unverified',
  'native_ffi_unavailable','native_clock_unavailable','go_clock_unavailable',
  'clock_tuple_mismatch','clock_containment_unverified','clock_span_unverified',
  'clock_tick_containment_unverified','clock_wall_containment_unverified',
  'clock_disposal_unverified','wrong_image_not_refused',
  'clock_pause_nonmonotonic']);
function requireValue(value, code) { if (!value) throw new Error(code); }
async function sha256(file) {
  const sum = createHash('sha256');
  for await (const block of createReadStream(file)) sum.update(block);
  return sum.digest('hex');
}
function ns(value) {
  requireValue(typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value), 'go_clock_unavailable');
  return BigInt(value);
}
function sample(clock) {
  const x = clock.sample();
  requireValue(Object.isFrozen(x) && x.domain === 'darwin-kernel' && x.rawKind === 'darwin-monotonic-raw' &&
    /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(x.boot) &&
    ['loNS','hiNS','wallNS'].every(k => typeof x[k] === 'bigint') &&
    x.hiNS > x.loNS && x.hiNS-x.loNS <= 100000000n && x.wallNS > 0n, 'native_clock_unavailable');
  return x;
}
async function main() {
  requireValue(process.platform === 'darwin' && process.arch === 'arm64' && globalThis.Bun?.version === '1.4.4', 'platform_unverified');
  const helper=process.argv[2];
  requireValue(typeof helper === 'string' && isAbsolute(helper), 'helper_unverified');
  phase = 'held_image';
  requireValue(await sha256(process.execPath) === officialImage, 'image_unverified');
  phase = 'ffi_load';
  let library, clock;
  const originalDateNow = Date.now;
  try {
    try { library=dlopen('/usr/lib/libSystem.B.dylib',darwinABI); }
    catch { throw new Error('native_ffi_unavailable'); }
    phase = 'ffi_timebase';
    const lib=library.symbols, aligned=x=>address(ptr(x),x.BYTES_PER_ELEMENT);
    const base=new Uint32Array(2);
    requireValue(lib.mach_timebase_info(aligned(base)) === 0 && base[0] > 0 && base[1] > 0, 'native_ffi_unavailable');
    const numer=base[0],denom=base[1];
    const bootKey=Buffer.from('kern.bootsessionuuid\0'), buffer=new Uint32Array(16), size=new BigUint64Array(1);
    function readBoot(){
      buffer.fill(0);size[0]=BigInt(buffer.byteLength);
      const status=lib.sysctlbyname(address(ptr(bootKey)),aligned(buffer),aligned(size),null,0n);
      return darwinBoot(new Uint8Array(buffer.buffer),size[0],status);
    }
    phase = 'clock_boot';
    const productionSource = process.argv.slice(3).includes('--production-source');
    const shiftJSWall = process.argv.slice(3).includes('--shift-js-wall');
    requireValue(!shiftJSWall || productionSource, 'native_clock_unavailable');
    let wrongImageRefused = false;
    if (productionSource) {
      // TEST only: a reported path to unrelated system bytes must not satisfy
      // the pinned candidate, even with the actual Bun 1.4.4 runtime present.
      const originalPath = process.execPath;
      try {
        process.execPath = '/usr/bin/true';
        const unexpected = await createDarwinClock();
        unexpected.dispose();
      } catch (error) { wrongImageRefused = error?.message === 'clock_unavailable'; }
      finally { process.execPath = originalPath; }
      requireValue(wrongImageRefused, 'wrong_image_not_refused');
    }
    clock=productionSource ? await createDarwinClock() : createNativeClock({
      imageSHA256:officialImage,
      readBoot,
      readCounter:()=>machNS(lib.mach_continuous_time(),numer,denom),
      readWall:()=>positiveNS(BigInt(Date.now())*1000000n),
      verify(){
        requireValue(lib.mach_timebase_info(aligned(base))===0 && base[0]===numer && base[1]===denom,'native_ffi_unavailable');
      },
      close(){} // The owning finally closes libSystem once after disposal.
    }, 'darwin-kernel');
    if (shiftJSWall) Date.now = () => originalDateNow() + 12000;
    let maxBracket=0n;
    const overlongSpans = [];
    // Extended real-FFI E0 scheduling sample, not independently qualified Q/R/T.
    for(let round=0;round<12;round++){
      const deadline = performance.now()+2000;
      let accepted = false;
      for (let attempt=0;attempt<3;attempt++) {
      phase = 'clock_sample';
      const before=sample(clock);
      phase = 'go_helper';
      const run=spawnSync(helper,['opencode-clock','--protocol','1'],{
        timeout:2000,maxBuffer:1024,env:{PATH:'/usr/bin:/bin'}});
      phase = 'clock_sample_after';
      const after=sample(clock);
      requireValue(!run.error && run.status===0 && run.stdout?.length>0 && run.stdout.length<=1024,'go_clock_unavailable');
      phase = 'clock_comparison';
      const raw=run.stdout.toString('utf8');
      let g;try{g=JSON.parse(raw);}catch{throw new Error('go_clock_unavailable');}
      requireValue(raw===JSON.stringify(g)+'\n' && g.protocol===1 &&
        Object.keys(g).join('|')==='protocol|boot|clockDomain|clockKind|monoLoNs|monoHiNs|wallUnixNs|uncertaintyNs',
        'go_clock_unavailable');
      requireValue(before.boot===g.boot && g.boot===after.boot && g.clockDomain==='darwin-kernel' &&
        g.clockKind==='darwin-monotonic-raw','clock_tuple_mismatch');
      const lo=ns(g.monoLoNs),hi=ns(g.monoHiNs),wall=ns(g.wallUnixNs),uncertainty=ns(g.uncertaintyNs);
      requireValue(hi>=lo && uncertainty===hi-lo+3000000n,'go_clock_unavailable');
      const width=after.hiNS-before.loNS;
      requireValue(before.loNS<=lo && hi<=after.hiNS,'clock_tick_containment_unverified');
      requireValue(before.wallNS-2000000n<=wall && wall<=after.wallNS+2000000n,'clock_wall_containment_unverified');
      if (width < 0n || width > 224000000n) {
        // A duration only, never a clock reading, boot identifier or path.
        spanFailure = { round, attempt, spanNS: String(width), limitNS: '224000000' };
        if (width > 224000000n) overlongSpans.push(spanFailure);
      }
      requireValue(width>=0n && performance.now()<deadline,'clock_span_unverified');
      if (width > 224000000n) continue;
      if(width>maxBracket)maxBracket=width;
      accepted = true;
      break;
      }
      requireValue(accepted, 'clock_span_unverified');
    }
    // Optional repeated native calls exercise the same pinned image, pointers,
    // sysctl, Mach counter and conversion after warm-up. This remains sampled
    // behavior: it does not claim a JIT tier, worst-case bound or qualification.
    const stressRounds = process.argv.slice(3).includes('--stress') ? 4096 : 0;
    if (stressRounds) {
      phase = 'clock_repeat_stress';
      let previous = sample(clock);
      for (let round = 0; round < stressRounds; round++) {
        const current = sample(clock);
        requireValue(current.boot === previous.boot && current.loNS >= previous.loNS &&
          current.wallNS >= previous.wallNS, 'clock_pause_nonmonotonic');
        previous = current;
      }
    }
    // Observe monotonic counter and wall reading across an actual timer pause.
    // A positive observation never registers a clock cell or delivery authority.
    phase = 'clock_pause_stress';
    const pauseBefore = sample(clock);
    await new Promise(resolve => setTimeout(resolve, 75));
    const pauseAfter = sample(clock);
    requireValue(pauseAfter.boot === pauseBefore.boot && pauseAfter.domain === pauseBefore.domain &&
      pauseAfter.loNS >= pauseBefore.loNS && pauseAfter.wallNS >= pauseBefore.wallNS,
      'clock_pause_nonmonotonic');
    phase = 'clock_disposal';
    clock.dispose();
    let denied=false;try{clock.sample();}catch{denied=true;}
    requireValue(denied,'clock_disposal_unverified');
    requireValue(await sha256(process.execPath)===officialImage,'image_unverified');
    return { status:'observed_ffi_e0_only',platform:'darwin',arch:'arm64',
      hostVersion:'2.2.6',embeddedBunVersion:'1.4.4',comparisonRounds:12,
      productionNativeClockObserved:productionSource,
      shiftedJSWallIgnored:shiftJSWall,
      wrongNativeImageRefused:wrongImageRefused,
      repeatedNativeSamples:stressRounds,
      delayedCounterAndWallNonregressionObserved:true,
      maxCounterSpanNS:String(maxBracket),overlongSpans,disposalRefused:true,...grants };
  } finally { Date.now = originalDateNow; try{clock?.dispose();}catch{} try{library?.close();}catch{} }
}
try { console.log(JSON.stringify(await main())); }
catch(error){
  const code = reasons.has(error?.message) ? error.message :
    error?.message === 'clock_unavailable' ? 'clock_unavailable' : 'unqualified';
  console.log(JSON.stringify({status:'unqualified',failureCode:code+'_'+phase,
    ...(code==='clock_span_unverified' && spanFailure ? { spanFailure } : {}),...grants}));
  process.exitCode=1;
}
