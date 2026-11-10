// Actual macOS ARM64/Bun 1.4.4 FFI observation, NOT a production admission.
// Uses the production ABI, clock conversion, and sampling implementation, but
// deliberately does not create a qualified clock cell or observer receipt.
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { isAbsolute } from 'node:path';
import { dlopen, ptr } from 'bun:ffi';
import { address, createNativeClock, darwinABI, darwinBoot, machNS, positiveNS } from '../opencode-plugin/native-clock-contract.mjs';

const officialImage = '90e4d03597b03d2051b65b4d793467764e55bacd8e22bf4824479c7233ea0e15';
const grants = { nativeObserverQualified:false, nativeClockQualified:false, notificationDeliveryQualified:false };
let phase = 'entry';
const reasons = new Set(['platform_unverified','helper_unverified','image_unverified',
  'native_ffi_unavailable','native_clock_unavailable','go_clock_unavailable',
  'clock_tuple_mismatch','clock_containment_unverified','clock_disposal_unverified']);
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
    clock=createNativeClock({
      imageSHA256:officialImage,
      readBoot,
      readCounter:()=>machNS(lib.mach_continuous_time(),numer,denom),
      readWall:()=>positiveNS(BigInt(Date.now())*1000000n),
      verify(){
        requireValue(lib.mach_timebase_info(aligned(base))===0 && base[0]===numer && base[1]===denom,'native_ffi_unavailable');
      },
      close(){} // The owning finally closes libSystem once after disposal.
    }, 'darwin-kernel');
    let maxBracket=0n;
    for(let round=0;round<3;round++){
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
      requireValue(width>=0n && width<=224000000n && before.loNS<=lo && hi<=after.hiNS &&
        before.wallNS-2000000n<=wall && wall<=after.wallNS+2000000n,'clock_containment_unverified');
      if(width>maxBracket)maxBracket=width;
    }
    phase = 'clock_disposal';
    clock.dispose();
    let denied=false;try{clock.sample();}catch{denied=true;}
    requireValue(denied,'clock_disposal_unverified');
    requireValue(await sha256(process.execPath)===officialImage,'image_unverified');
    return { status:'observed_ffi_e0_only',platform:'darwin',arch:'arm64',
      hostVersion:'2.2.6',embeddedBunVersion:'1.4.4',comparisonRounds:3,
      maxCounterSpanNS:String(maxBracket),disposalRefused:true,...grants };
  } finally { try{clock?.dispose();}catch{} try{library?.close();}catch{} }
}
try { console.log(JSON.stringify(await main())); }
catch(error){
  const code = reasons.has(error?.message) ? error.message :
    error?.message === 'clock_unavailable' ? 'clock_unavailable' : 'unqualified';
  console.log(JSON.stringify({status:'unqualified',failureCode:code+'_'+phase,...grants}));
  process.exitCode=1;
}
