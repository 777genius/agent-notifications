import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import dc from 'node:diagnostics_channel';
import { createProcessRegistry } from './process-registry.mjs';
import { createPreparedDelivery } from './prepared-delivery.mjs';
import { createObserver } from 'universal-agent-plugins-opencode-events/v1';
import { createNativeV1 } from './native-v1.mjs';
import { parseJSON } from './protocol.mjs';
const policy=Object.freeze({profileID:'independent-fixture-only',calibrationID:'fixture-calibration',rawKind:'linux-boottime',nativeReadBoundNS:103000000n,comparisonBoundNS:430000000n,translationBoundNS:224000000n});
const boot='11111111-2222-3333-4444-555555555555',domain='linux-time:4:4026531834';
const pause=()=>new Promise(setImmediate);
const fixture=`#!${process.execPath}
import fs from 'node:fs';
if(process.argv[2]==='opencode-clock') process.stdout.write(fs.readFileSync('clock.json'));
else {let input='';process.stdin.on('data',b=>input+=b);process.stdin.on('end',()=>{
 if(input)fs.writeFileSync('event-frame',input);setTimeout(()=>process.stdout.write('{"status":"suppressed"}'),200);
});}
`;
async function setup(run,generation='v2',onDiagnostic){
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'TEST-prepared-'));
 const executable=path.join(root,'fixture.mjs'),children=[];
 const observer=({process:child})=>{const r={child,closed:false};children.push(r);child.once('close',()=>r.closed=true);};
 await fs.writeFile(executable,fixture,{mode:0o700});
 const registry=createProcessRegistry({executable,privateCwd:root,controlRoot:root});
 let tick=1000000000000n,wallJump=0n,fail=false,postGap=0n,active=true;
 const record=()=>{
  if(fail)throw Error('PRIVATE_CLOCK_DETAIL');
  const wall=1700000000000000000n+tick-1000000000000n+wallJump;
  return Object.freeze({boot,domain,rawKind:'linux-boottime',loNS:tick,hiNS:tick+10000000n,wallNS:wall,offsetLoNS:wall-tick-12000000n,offsetHiNS:wall-tick+2000000n});
 };
 const sourceFactory=()=>({sample:record,dispose(){}});
 let invalidations=0, invalidationCallback;
 const delivery=createPreparedDelivery({registry,origin:'a'.repeat(64),policy:{...policy,generation},sourceFactory,isOwned:()=>active,
  onDiagnostic:onDiagnostic ? reason=>onDiagnostic(reason,delivery,invalidations) : undefined,
  onInvalidate:()=>{invalidations++;invalidationCallback?.();}});
 const writeClock=()=>fs.writeFile(path.join(root,'clock.json'),JSON.stringify({protocol:1,boot,clockDomain:domain,clockKind:'linux-boottime',monoLoNs:String(tick+1000000n),monoHiNs:String(tick+2000000n),wallUnixNs:String(record().wallNS),uncertaintyNs:'4000000'}));
 await writeClock();dc.subscribe('child_process',observer);
 const advance=(n)=>{tick+=n;};
 const fact=()=>Object.freeze({version:1,kind:'question_asked',sessionID:'s',turnID:'evt_run',requestID:'request',rootSession:true,
  provenance:Object.freeze({generation:'v2',observationID:'original-request',nativeTime:Number(record().wallNS/1000000n),timeBasis:'envelope_created',nativeEventID:'evt_form'})});
 const handoff=()=>{delivery.beginIngress({type:'question.asked'});const born=delivery.clock.now();const controller=new AbortController();return {signal:controller.signal,
  ingressMonotonicMs:born,clockID:delivery.clock.id,metadataDeadline:born+2000,isCurrent:()=>!controller.signal.aborted,controller};};
 try {await run({registry,delivery,root,children,advance,writeClock,fact,handoff,fail:()=>fail=true,jump:()=>wallJump=10000000000n,invalidations:()=>invalidations,
  onInvalidation:(callback)=>invalidationCallback=callback,revoke:()=>active=false, postGap:(n)=>{postGap=n;}, gapObserver:()=>{tick+=postGap;}});}
 finally{delivery.dispose();await registry.dispose();dc.unsubscribe('child_process',observer);for(const c of children)if(!c.closed)c.child.kill('SIGKILL');
  for(let i=0;i<300 && children.some(c=>!c.closed);i++)await new Promise(r=>setTimeout(r,10));
  assert.ok(children.every(c=>c.closed));await fs.rm(root,{recursive:true,force:true});}
}
// Red if an anchor is stamped after ingress, event nativeTime changes, or emit
// resolves before an actual owned child close. All clocks here are synthetic.
test('preparation preserves original anchor/ingress/native birth; emit is synchronous through spawn',async()=>{
 await setup(async({delivery,advance,writeClock,fact,handoff,children,root})=>{
  assert.equal(await delivery.activate(),true);advance(60000000n);
  const event=fact(), h=handoff();await writeClock();assert.equal(await delivery.beforeEmit(event,h),true);
  const before=children.length;let settled=false;const result=delivery.emit(event,h).then(()=>settled=true);
  assert.equal(children.length,before+1);assert.equal(settled,false);assert.equal(children.at(-1).closed,false);
  await result;assert.equal(children.at(-1).closed,true);
  const frame=parseJSON(await fs.readFile(path.join(root,'event-frame')));
  assert.equal(frame.event.provenance.nativeTime,event.provenance.nativeTime);
  assert.equal(frame.provenance.anchor.monoLoNS,'1000001000000');
  assert.equal(frame.provenance.ingressTickNS,'1000060000000');assert.equal(frame.provenance.spawnTickNS,'1000060000000');
  assert.equal(frame.provenance.calibration.nativeLoNS,'1000001000000');
 });
});
// Red if the last async checkpoint can leave an expired/disposed/lost-source job
// with a live child, or if a clock exception merely rejects and keeps the epoch.
for(const mode of ['pause','wall','exception','dispose','scope'])test(`prepared ${mode} invalidates without IPC`,async()=>{
 await setup(async({delivery,advance,writeClock,fact,handoff,children,jump,fail,revoke,root,invalidations})=>{
  await delivery.activate();advance(60000000n);const event=fact(),h=handoff();await writeClock();
  assert.equal(await delivery.beforeEmit(event,h),true);const before=children.length;
  if(mode==='pause')advance(31000000000n);if(mode==='wall')jump();if(mode==='exception')fail();if(mode==='dispose')delivery.dispose();if(mode==='scope')revoke();
  await delivery.emit(event,h);assert.equal(children.length,before);assert.equal(delivery.ready(),false);assert.ok(invalidations()>0);
  await assert.rejects(fs.access(path.join(root,'event-frame')));
 });
});
// Red if a real returned child receives any frame after a pause INSIDE spawn,
// or if the post-spawn sample is replaced by a pre-spawn helper timestamp.
test('post-spawn upper bracket overrun aborts the actual child with no frame',async()=>{
 await setup(async({delivery,advance,writeClock,fact,handoff,children,postGap,gapObserver,root})=>{
  await delivery.activate();advance(60000000n);const event=fact(),h=handoff();await writeClock();assert.equal(await delivery.beforeEmit(event,h),true);
  postGap(120000000n);dc.subscribe('child_process',gapObserver);
  try{await delivery.emit(event,h);}finally{dc.unsubscribe('child_process',gapObserver);}
  assert.equal(children.at(-1).closed,true);assert.equal(delivery.ready(),false);await assert.rejects(fs.access(path.join(root,'event-frame')));
 });
});
// Red if old native births can attach to a new epoch, or a prepared old event
// can revive after reactivation with a different source epoch.
test('old source birth and retired epoch cannot reopen after fresh activation',async()=>{
 await setup(async({delivery,advance,writeClock,fact,handoff,children})=>{
  await delivery.activate();const old=fact();advance(60000000n);const h=handoff();await writeClock();
  assert.equal(await delivery.beforeEmit(old,h),false);assert.equal(delivery.ready(),false);
  assert.equal(await delivery.activate(),true);advance(60000000n);const fresh=fact(),h2=handoff();await writeClock();
  assert.equal(await delivery.beforeEmit(fresh,h2),true);delivery.invalidate('reader');
  await writeClock();assert.equal(await delivery.activate(),true);const n=children.length;
  await delivery.emit(fresh,h2);assert.equal(children.length,n);
 });
});
// Red if the E2 emit bridge detaches the registry Promise from the actual SDK
// handoff, freeing a job/callback while its REAL Node child remains live.
test('clock exception synchronously retires actual strict SDK work and holds its owned child through close',async()=>{
 await setup(async({delivery,registry,advance,writeClock,children,root,fail,onInvalidation})=>{
  await delivery.activate();advance(60000000n);await writeClock();
  const directory='/TEST-SDK-child-close';
  const user={id:'user',sessionID:'s',role:'user',time:{created:1700000000060}};
  const answer={id:'assistant',sessionID:'s',role:'assistant',parentID:'user',path:{cwd:directory},time:{created:1700000000060}};
  const client={session:{get:async()=>({id:'s',directory}),messages:async()=>structuredClone([user,answer])}};
  const view=createNativeV1(client,directory,delivery.invalidate);
  const observer=createObserver({client,location:directory,callbackAuthority:'qualified_native_sync',runtimeEligibility:()=> 'supported',clock:delivery.clock,
   beforeEmit:async(e,h)=>await delivery.beforeEmit(e,h)&&await view.finalize(e,h),emit:delivery.emit});
  onInvalidation(()=>observer.dispose());
  const observe=(type,properties)=>{const event={type,properties};delivery.beginIngress(event);view.ingest(event);return observer.observe(event);};
  try{
   await observe('message.updated',{info:user});await observe('message.updated',{info:answer});
   let settled=false;const work=observe('question.asked',{id:'request',sessionID:'s',tool:{messageID:'assistant',callID:'call'},questions:[{}]}).then(()=>settled=true);
   const end=Date.now()+2000;
   while(true){try{await fs.access(path.join(root,'event-frame'));break;}catch{assert.ok(Date.now()<end);await pause();}}
   assert.equal(children.at(-1).closed,false);assert.equal(registry.status().occupied,1);assert.equal(settled,false);
   fail();assert.throws(()=>observe('question.replied',{sessionID:'s',requestID:'request'}));
   assert.equal(delivery.ready(),false);assert.equal(children.at(-1).closed,false);assert.equal(settled,false);
   await work;assert.equal(children.at(-1).closed,true);assert.equal(registry.status().occupied,0);
  }finally{observer.dispose();view.dispose();}
 },'v1');
});
// Red if the 2s transport budget is incorrectly used as a 224ms source/native
// calibration grant. Actual helper close remains required even for refusal.
test('completed helper inside 2s is refused when the independent calibration bracket exceeds 224ms',async()=>{
 await setup(async({delivery,advance,writeClock,fact,handoff,children,postGap,gapObserver})=>{
  await delivery.activate();advance(60000000n);const event=fact(),h=handoff();await writeClock();
  postGap(300000000n);dc.subscribe('child_process',gapObserver);
  try{assert.equal(await delivery.beforeEmit(event,h),false);}finally{dc.unsubscribe('child_process',gapObserver);}
  assert.equal(children.at(-1).closed,true);assert.equal(delivery.ready(),false);
 });
});

// Red on R3 if a preparation exception calls diagnostics while its epoch is live.
// Observe inside the callback, assert outside: callback exceptions are deliberately contained.
test('preparation exception retires authority before a throwing diagnostic callback',async()=>{
 const observed=[];
 await setup(async({delivery,advance,writeClock,fact,handoff,children})=>{
  assert.equal(await delivery.activate(),true);
  const old=fact();advance(60000000n);const h=handoff();await writeClock();
  assert.equal(await delivery.beforeEmit(old,h),false);
  assert.equal(delivery.ready(),false);
  assert.ok(children.every(child=>child.closed));
 },'v2',(reason,delivery,invalidations)=>{
  observed.push({reason,ready:delivery.ready(),invalidations});
  throw Error('PRIVATE_DIAGNOSTIC_CALLBACK');
 });
 assert.equal(observed.length,1);
 assert.equal(observed[0].reason,'prep.catch.birth');
 assert.equal(observed[0].ready,false);
 assert.ok(observed[0].invalidations>0);
});
