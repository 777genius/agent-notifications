// TEST-only public packed SDK consumer. No notification, helper or production grant.
import { createObserver } from 'universal-agent-plugins-opencode-events/v1';
import { createV2Observer } from 'universal-agent-plugins-opencode-events/v2';
import { createNativeV1 } from './candidate/opencode-plugin/native-v1.mjs';
import { createNativeV2, createRPCCheckpoint } from './candidate/opencode-plugin/native-v2.mjs';
import { readFileSync, realpathSync, statSync, openSync, fstatSync, closeSync,
  appendFileSync, writeFileSync, renameSync } from 'node:fs';
import { parseJSON } from './candidate/opencode-plugin/protocol.mjs';
import type { ObserverOptions } from 'universal-agent-plugins-opencode-events/v1';
import type { V2Context, V2CheckpointPort, V2NativePorts, V2Envelope } from 'universal-agent-plugins-opencode-events/v2';
import type { BigIntStats } from 'node:fs';
import { resolve, relative, isAbsolute, basename } from 'node:path';
import { createHash, createHmac } from 'node:crypto';

// Runtime context is supplied by the genuine host. Only structural public ports
// are described here; no replacement native event source exists in this helper.
type NativeEvent = { type?: string; [key: string]: unknown };
type V1Input = { client: ObserverOptions['client']; directory: string };
type Registration = { events: { emit(type: string, value: { nonce: string }): Promise<void> }; dispose(): Promise<void> };
type NativeContext = V2Context & {
  location: { directory: string; workspaceID?: string; project: { id: string } };
  rpc: { register(input: unknown, options: unknown): Promise<Registration> };
  session: { get(input: unknown): Promise<unknown>; context(input: unknown): Promise<unknown> };
};
type Authority = { version: string; os: 'linux' | 'windows'; arch: 'amd64' | 'arm64';
  official: { executableSHA256: string }; imageSHA256: string; parentPID: number;
  directory: string; mode: 'api-only' | 'one-completion'; apiOnlyReadiness: boolean };
const statKeys = ['dev','ino','size','mtimeNs','ctimeNs'] as const satisfies readonly (keyof BigIntStats)[];

const root = realpathSync(process.env.AN_TEST_ROOT || '/missing');
const own = parseJSON(readFileSync(resolve(root,'reader-authority-private.json')), 4096) as Authority;
const marker = parseJSON(readFileSync(resolve(root,'.owned-test-root.json')),4096);
if (marker.purpose !== 'TEST portable packaged SDK reader' || !/^TEST-/.test(basename(root))) throw Error('TEST root');
const project = realpathSync(resolve(root,'project')), key = readFileSync(resolve(root,'trace-key'));
const image = realpathSync(process.execPath), fd = openSync(image,'r'), imageStat = fstatSync(fd,{bigint:true});
const hash = createHash('sha256').update(readFileSync(fd)).digest('hex');
const inside = (path: string) => !relative(root,path).startsWith('..') && !isAbsolute(relative(root,path));
if (!inside(image) || !inside(project) || hash !== own.official.executableSHA256 || own.imageSHA256 !== hash ||
    process.platform !== ({linux:'linux',windows:'win32'}[own.os]) ||
    process.arch !== ({amd64:'x64',arm64:'arm64'}[own.arch]) || process.ppid !== own.parentPID ||
    (process.platform==='linux' && realpathSync('/proc/self/exe') !== image) || own.directory !== project || key.length !== 32 ||
    !statKeys.every(k=>typeof imageStat[k]==='bigint')) throw Error('image custody');
let valid = true, fdOpen = true, rows = 0, bytes = 0, nativeCalls = 0, sdkFacts = 0;
const h = (s: unknown) => 'h:'+createHmac('sha256',key).update(String(s)).digest('hex').slice(0,24);
const safe = new Set(['v1','v2','user','assistant','stop','turn_idle_verified','assistant_completed',
  'envelope_created','local-performance','subscription_ended','subscription_error','observer_disposed']);
function redact(value: unknown, name='', depth=0): unknown {
  if (depth > 14) throw Error('trace depth');
  if (value === null || typeof value === 'boolean' || typeof value === 'number') return value;
  if (typeof value === 'string') return safe.has(value) || name === 'type' && /^[a-z][a-z0-9_.-]{0,160}$/.test(value) ? value : h(value);
  if (Array.isArray(value)) { if (value.length > 96) throw Error('trace array'); return value.map(x=>redact(x,name,depth+1)); }
  if (value && typeof value === 'object') {
    if (Object.keys(value).length > 96) throw Error('trace object');
    return Object.fromEntries(Object.entries(value).map(([k,x])=>[k,redact(x,k,depth+1)]));
  }
  return '[unsupported]';
}
function record(kind: string,value: unknown={}) {
  const line = JSON.stringify({kind,value:redact(value)})+'\n';
  if (++rows > 512 || (bytes += Buffer.byteLength(line)) > 1024*1024) { valid=false; throw Error('trace bound'); }
  appendFileSync(resolve(root,'reader-private.jsonl'),line,{mode:0o600});
}
// Raw exception fields stay in this bounded private TEST file, never exported.
let exceptionRows=0, exceptionBytes=0;
function checkpointException(stage: string,error: unknown) {
  try {
    const text=(value: unknown,limit: number)=>typeof value==='string'?Buffer.from(value).subarray(0,limit).toString('utf8'):undefined;
    const detail=error instanceof Error?error:undefined;
    const row={stage,name:text(detail?.name,64),message:text(detail?.message,1536),stack:text(detail?.stack,2048)};
    let line=JSON.stringify(row)+'\n';
    while(Buffer.byteLength(line)>4096) {
      if(row.stack?.length)row.stack=row.stack.slice(0,Math.floor(row.stack.length/2));
      else row.message=row.message?.slice(0,Math.floor(row.message.length/2));
      line=JSON.stringify(row)+'\n';
    }
    const size=Buffer.byteLength(line);
    if(++exceptionRows>8 || (exceptionBytes+=size)>16384) { valid=false; return; }
    appendFileSync(resolve(root,'checkpoint-errors.private.jsonl'),line,{mode:0o600});
  } catch { valid=false; }
}
function witness() {
  if (!valid || !fdOpen) return false;
  try {
    const now=fstatSync(fd,{bigint:true}), named=statSync(image,{bigint:true});
    return statKeys.every(k=>now[k]===imageStat[k] && named[k]===imageStat[k]) &&
      (process.platform!=='linux' || realpathSync('/proc/self/exe')===image) && process.ppid===own.parentPID;
  } catch { return false; }
}
function fail(reason='scope_uncertain') { valid=false; record('uncertainty',{reason}); }
function publish(name: string,value: unknown) {
  if (!witness()) throw Error('authority lost');
  const target=resolve(root,name), tmp=target+'.tmp';
  writeFileSync(tmp,JSON.stringify(value)+'\n',{mode:0o600,flag:'wx'}); renameSync(tmp,target);
}
const slots = new WeakMap<object, unknown>();
function scopeOK(directory: string | undefined) { try { return witness() && typeof directory==='string' && realpathSync(directory)===project; } catch { return false; } }
function receipt(generation: string, extra: Record<string, unknown>={}) {
  return {schemaVersion:1,scope:'TEST packaged SDK reader',generation,version:own.version,
    ownedImageSHA256:hash,os:own.os,arch:own.arch,nativePID:process.pid,parentPID:process.ppid,actualExecPath:image,testAuthorityDerived:true,loadedMappedBytesQualified:false,productionQualified:false,
    installedQualified:false,timePolicyQualified:false,sourceEpochQualified:false,finalSpanQualified:false, fullNativeQualified:false,clockQualified:false,visibleDesktopQualified:false,platformLifetimeQualified:false,...extra};
}
function diagnostic(reason: string) { record('sdk-diagnostic',{reason}); }
async function boundedDrain(task: Promise<unknown>,deadline: number) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try { return await Promise.race([task,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('actual drain missing')),Math.max(0,deadline-performance.now()));})]); }
  finally { clearTimeout(timer); }
}
async function server(input: V1Input) {
  if (slots.has(input.client)) return slots.get(input.client);
  if (own.version==='2.0.21' || !scopeOK(input.directory) ||
      typeof input.client?.session?.messages!=='function' || typeof input.client?.session?.get!=='function') throw Error('V1 native context');
  const jobs=new Set<Promise<void>>(); let stopped=false, nativeCleanup=false, closing: Promise<void> | undefined;
  const view=createNativeV1(input.client,project,fail);
  let observer: ReturnType<typeof createObserver>;
  async function stop(native=false) {
    nativeCleanup ||= native;
    if (closing) return closing;
    const deadline=performance.now()+2000;
    stopped=true; observer.dispose(); view.dispose();
    closing=(async()=>{
      await boundedDrain(Promise.allSettled([...jobs]),deadline);
      if (jobs.size || performance.now()>deadline) throw Error('V1 drain');
      publish('reader-closed.json',receipt('v1',{actualSDKDispose:true,actualTrackedJobsSettled:true,
        nativeCallbackReader:true,nativeCleanupHookObserved:nativeCleanup,sdkFacts,nativeCalls,closureCause:own.mode==='api-only'?'consumer_after_native_session_deleted':'consumer_after_real_sdk_fact'}));
      record('reader-closed',{actualDrain:true}); closeSync(fd); fdOpen=false;
    })(); return closing;
  }
  observer=createObserver({client:input.client,location:project,callbackAuthority:'qualified_native_sync',
    runtimeEligibility:()=>scopeOK(input.directory)?'supported':'unverified',
    beforeEmit:(event,handoff)=>scopeOK(input.directory)&&view.finalize(event,handoff),onDiagnostic:diagnostic,
    emit(event,handoff) {
      if (stopped || !scopeOK(input.directory) || !handoff.isCurrent() || handoff.signal.aborted ||
          event.kind!=='turn_idle_verified' || event.rootSession!==true || ++sdkFacts!==1) { fail('SDK fact rejected'); return; }
      record('sdk-fact',{event,clockID:handoff.clockID,ingressMonotonicMs:handoff.ingressMonotonicMs,
        currentAtCallback:handoff.isCurrent(),nativeCallbackCount:nativeCalls});
      if(own.mode!=='one-completion') { fail('unplanned_completion_fact'); return; }
      queueMicrotask(()=>void stop().catch(()=>fail('consumer_dispose_failed')));
    }});
  const hooks={event({event}: { event: NativeEvent }) {
    if (stopped) { record('after-dispose-native',{type:event?.type}); return; }
    if (!scopeOK(input.directory)) { fail(); observer.dispose(); return; }
    nativeCalls++; record('native-v1',event); view.ingest(event);
    if (jobs.size>=256) { fail('job_capacity'); observer.dispose(); return; }
    const job=observer.observe(event); jobs.add(job);
    Promise.resolve(job).finally(()=>{jobs.delete(job);if(own.mode==='api-only'&&event?.type==='session.deleted')
      queueMicrotask(()=>void stop().catch(()=>fail('consumer_dispose_failed')));}).catch(()=>fail('callback_failure'));
    record('sdk-observe-invoked',{type:event?.type});
  },dispose:()=>stop(true)};
  slots.set(input.client,hooks); record('loader',{branch:'v1',scopeVerified:true});
  publish('reader-ready.json',receipt('v1',{nativeCallbackReady:true})); return hooks;
}
async function setup(ctx: NativeContext) {
  if (slots.has(ctx)) return slots.get(ctx);
  if (own.version!=='2.0.21' || ctx.app?.version!==own.version || !scopeOK(ctx.location?.directory) ||
      !ctx.location?.project?.id || typeof ctx.event?.subscribe!=='function' || typeof ctx.rpc?.register!=='function' ||
      typeof ctx.session?.get!=='function' || typeof ctx.session?.context!=='function') throw Error('V2 native context');
  let subscriptions=0, registrations=0, closures=0, disposedPorts=0, markerReads=0, cancelled=false, stopping=false, closing: Promise<void> | undefined;
  let firstController: AbortController | undefined, observer: ReturnType<typeof createV2Observer>;
  const scope=Object.freeze({directory:project,workspaceID:ctx.location.workspaceID,
    project:Object.freeze({id:ctx.location.project.id})}), view=createNativeV2(ctx,scope,()=>{},()=>fail());
  const sameScope=()=>scopeOK(ctx.location?.directory) && ctx.location.project.id===scope.project.id &&
    ctx.location.workspaceID===scope.workspaceID;
  const context: V2Context={app:ctx.app,event:{subscribe({signal}) {
    if (++subscriptions>2) { fail('unexpected_reader_replacement'); throw Error('reader limit'); }
    const number=subscriptions, ctrl=new AbortController(); if(number===1) firstController=ctrl;
    const abort=()=>ctrl.abort(); signal.addEventListener('abort',abort,{once:true}); if(signal.aborted) abort();
    const stream=ctx.event.subscribe({signal:ctrl.signal}), iterator=stream[Symbol.asyncIterator](); let closed=false, returned: Promise<IteratorResult<unknown>> | undefined;
    function closeObserved(cause: string) {
      if(closed)return; closed=true; closures++; signal.removeEventListener('abort',abort); view.reset();
      record('native-reader-close',{number,cause,actualSignalAborted:ctrl.signal.aborted});
    }
    function actualClose(cause: string) {
      returned ??= (async()=>{
        try {
          if(typeof iterator.return !== 'function')throw Error('native return absent');
          const result=await iterator.return();
          if(result?.done!==true)throw Error('native return did not finish');
          closeObserved(cause); return result;
        } catch(error) { fail('native_reader_close_failed'); throw error; }
      })();
      return returned;
    }
    record('native-reader-open',{number});
    const bridge: AsyncIterableIterator<unknown> = {[Symbol.asyncIterator](){return this;},async next() {
      try { const result=await iterator.next();
        if(result.done)await actualClose('actual_done');
        else { nativeCalls++; record('native-v2',result.value); }
        return result;
      } catch(error) { await actualClose('actual_error'); throw error; }
    },async return() { return await actualClose('actual_return'); }};
    return bridge;
  }}};
  const realPort=createRPCCheckpoint(ctx,async()=>sameScope());
  const checkpoint={async register(signal: AbortSignal,namespace: string) {
    let port; try { port=await realPort.register(signal,namespace); }
    catch(error) { checkpointException('native_registration',error); throw error; }
    if(!port)return;
    const number=++registrations, emitted=new Set<string>(); record('rpc-register',{number,type:port.type});
    return {type:port.type,emit:(nonce: string)=>{
      try { emitted.add(nonce); } catch(error) { checkpointException('consumer_nonce_tracking',error); throw error; }
      record('checkpoint-stage',{stage:'native_emit_started'});
      let pending; try { pending=port.emit(nonce); }
      catch(error) { checkpointException('native_emit',error); throw error; }
      return Promise.resolve(pending).then(value=>{record('checkpoint-stage',{stage:'native_emit_returned'});return value;},
        error=>{checkpointException('native_emit',error);throw error;});
    },read(envelope: V2Envelope) {
      const nonce=port.read(envelope); if(nonce===undefined)return nonce;
      if(!emitted.delete(nonce)) { fail('unexpected_checkpoint_nonce'); return undefined; }
      markerReads++; record('rpc-marker-consumed',{number,markerReads,envelope});
      if(number===1 && !cancelled) {
        cancelled=true; queueMicrotask(()=>{record('requested-native-abort',{number});if(!firstController)throw Error('first controller missing');firstController.abort();});
      } else if(number===2 && markerReads===2) {
        publish('reader-ready.json',receipt('v2',{realMarkerConsumed:true,subscriptions,registrations,
          firstReaderClosureObserved:closures===1,actualAbortRequested:cancelled}));
        if(own.apiOnlyReadiness===true)queueMicrotask(()=>void stop().catch(()=>fail('consumer_dispose_failed')));
      }
      return nonce;
    },async dispose() { await port.dispose(); disposedPorts++; record('rpc-disposed',{number}); }};
  }};
  async function stop(native=false) {
    if(closing)return closing; const deadline=performance.now()+2000; stopping=true; observer.dispose();
    closing=(async()=>{
      await boundedDrain(observer.done(),deadline);
      if(performance.now()>deadline || subscriptions!==2 || closures!==2 || disposedPorts!==2) throw Error('V2 actual closure');
      publish('reader-closed.json',receipt('v2',{actualSDKDispose:true,actualSDKDone:true,subscriptions,
        actualNativeReaderClosures:closures,registrations,actualRegistrationDisposals:disposedPorts,
        markerReads,actualAbortRequested:cancelled,nativeCleanupHookObserved:native,sdkFacts,nativeCalls,
        closureCause:own.apiOnlyReadiness===true?'consumer_after_actual_readiness_markers':'consumer_after_real_sdk_fact'}));
      record('reader-closed',{actualDrain:true}); closeSync(fd); fdOpen=false;
    })(); return closing;
  }
  observer=createV2Observer({context,location:project,native:view.native as V2NativePorts,checkpoint:checkpoint as V2CheckpointPort,
    runtimeEligibility:version=>version===own.version&&sameScope()?'supported':'unverified',onDiagnostic:diagnostic,
    beforeEmit:(event,handoff)=>sameScope()&&view.finalize(event,handoff),
    emit(event,handoff) {
      const projected=view.project(event);
      if(stopping || !sameScope() || !handoff.isCurrent() || handoff.signal.aborted ||
          !projected || event.kind!=='turn_idle_verified' || event.rootSession!==true || ++sdkFacts!==1) { fail('SDK fact rejected'); return; }
      record('sdk-fact',{event,projected,clockID:handoff.clockID,ingressMonotonicMs:handoff.ingressMonotonicMs,
        currentAtCallback:handoff.isCurrent(),nativeCallbackCount:nativeCalls});
      queueMicrotask(()=>void stop().catch(()=>fail('consumer_dispose_failed')));
    }});
  const cleanup=()=>stop(true); slots.set(ctx,cleanup);
  record('loader',{branch:'v2',scopeVerified:true}); observer.start(); return cleanup;
}
export default {id:'an-test-portable-public-packaged-reader',server,setup};
