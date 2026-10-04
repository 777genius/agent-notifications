import test from 'node:test';
import assert from 'node:assert/strict';
import { createObserver } from 'universal-agent-plugins-opencode-events/v1';
import { createV2Observer } from 'universal-agent-plugins-opencode-events/v2';
import { createNativeV1 } from './native-v1.mjs';
import { createNativeV2, createRPCCheckpoint } from './native-v2.mjs';
const pause=()=>new Promise(setImmediate);
async function until(check){const end=Date.now()+2500;while(!check()){assert.ok(Date.now()<end,'observable phase did not arrive');await pause();}}
const directory='/TEST-native-observer';
function v1({parentID, beforeEmit, onLookup, getDirectory=directory}={}){
 const facts=[];let rows=[],observer;
 let view;
 const client={session:{get:async({path})=>{onLookup?.('get');return {data:{id:path.id,parentID,directory:getDirectory}};},
  messages:async()=>{onLookup?.('messages');return {data:structuredClone(rows)};}}};
 observer=createObserver({client,location:directory,runtimeEligibility:()=> 'supported',callbackAuthority:'qualified_native_sync',beforeEmit:async(event,handoff)=>{
   if(beforeEmit && await beforeEmit(event,handoff,view)!==true)return false;return view.finalize(event,handoff);},
  emit:(fact)=>facts.push(fact)});
 view=createNativeV1(client,directory,()=>observer.dispose());
 const observe=(type,properties)=>{const event={type,properties};view.ingest(event);return observer.observe(event);};
 const user=(id='u',created=1000)=>({id,sessionID:'s',role:'user',time:{created}});
 const assistant=(id='a',parentID='u',extra={})=>({id,sessionID:'s',role:'assistant',parentID,path:{cwd:directory},time:{created:1100},...extra});
 const set=(next)=>rows=next;
 const birth=async()=>{set([user(),assistant()]);await observe('message.updated',{info:rows[0]});await observe('message.updated',{info:rows[1]});};
 const ask=(id='q',type='question.asked')=>observe(type,{id,sessionID:'s',tool:{messageID:'a',callID:'call'},questions:[{header:'PRIVATE_SENTINEL'}],permission:'read'});
 return{facts,observe,birth,ask,user,assistant,set,dispose:()=>observer.dispose()};
}
// Actual strict SDK accepts the native root client with NO pending namespaces;
// synchronous close-before-ask authority must not be reopened by hydration.
test('native V1 future-live attention retains native lower bound and close-before-ask stays closed',async()=>{
 const h=v1();try{
  await h.birth();await h.ask();await h.ask('permission','permission.asked');
  assert.deepEqual(h.facts.map(e=>e.kind),['question_asked','permission_asked']);
  assert.ok(h.facts.every(e=>e.provenance.nativeTime===1100 && e.provenance.timeBasis==='assistant_created_lower_bound'));
  await h.observe('question.replied',{sessionID:'s',requestID:'late'});await h.ask('late');
  assert.equal(h.facts.length,2);assert.ok(!JSON.stringify(h.facts).includes('PRIVATE_SENTINEL'));
 }finally{h.dispose();}
});
// Red if an async preparation or all callback slots can hide native close ingress.
test('native V1 close controls synchronously invalidate a held preparation',async()=>{
 let release,reached=false;const held=new Promise(r=>release=r);
 const h=v1({beforeEmit:()=>{reached=true;return held;}});
 try{
  await h.birth();const work=h.ask();await until(()=>reached);
  const close=h.observe('question.replied',{sessionID:'s',requestID:'q'});
  release(true);await Promise.all([work,close]);assert.deepEqual(h.facts,[]);
 }finally{release?.(false);h.dispose();}
});
// Red if lookup/job saturation queues close controls or late snapshots reopen tokens.
test('native V1 lookup saturation still processes close/tombstone control before late snapshots',async()=>{
 let release;const held=new Promise(r=>release=r),facts=[];
 const u={id:'u',sessionID:'s',role:'user',time:{created:1000}},a={id:'a',sessionID:'s',role:'assistant',parentID:'u',path:{cwd:directory},time:{created:1100}};
 const observer=createObserver({client:{session:{get:async()=>({id:'s',directory}),messages:()=>held}},location:directory,
  runtimeEligibility:()=> 'supported',callbackAuthority:'qualified_native_sync',emit:e=>facts.push(e)});
 try{
  await observer.observe({type:'message.updated',properties:{info:u}});await observer.observe({type:'message.updated',properties:{info:a}});
  const jobs=[];for(let i=0;i<256;i++)jobs.push(observer.observe({type:'question.asked',properties:{id:`q${i}`,sessionID:'s',tool:{messageID:'a',callID:'call'},questions:[{}]}}));
  for(let i=0;i<256;i++)void observer.observe({type:'question.replied',properties:{sessionID:'s',requestID:`q${i}`}});
  release([u,a]);await Promise.all(jobs);assert.deepEqual(facts,[]);
 }finally{release?.([u,a]);observer.dispose();}
});
// Red if ancestry, final native message, permanent outcome or ordinary assistant
// provenance is substituted by idle/error/retry/compaction alone.
test('native V1 root completion and final permanent error; child/scope/retry/abort/summary are silent',async()=>{
 for(const mode of ['root','child','scope','error','retry','abort','summary']){
  const h=v1({parentID:mode==='child'?'parent':undefined,getDirectory:mode==='scope'?'/OTHER':directory});
  try{
   await h.birth();let a=h.assistant();
   if(mode==='retry') { const prior={...a,finish:'stop',time:{created:1100,completed:1200}};
    await h.observe('message.updated',{info:prior});await h.observe('session.status',{sessionID:'s',status:{type:'retry'}}); }
   if(mode==='summary')a={...a,summary:true};
   if(['error','abort'].includes(mode))a={...a,time:{created:1100,completed:1200},error:{name:mode==='abort'?'MessageAbortedError':'APIError',message:'PRIVATE_SENTINEL'}};
   else a={...a,finish:'stop',time:{created:1100,completed:1200}};
   h.set([h.user(),a]);await h.observe('message.updated',{info:a});
   if(['error','abort'].includes(mode))await h.observe('session.error',{sessionID:'s',error:a.error});
   await h.observe('session.idle',{sessionID:'s'});
   assert.equal(h.facts.length,['root','error'].includes(mode)?1:0,mode);
   if(mode==='error'){assert.equal(h.facts[0].kind,'terminal_error');assert.equal(h.facts[0].provenance.nativeMessageID,'a');}
   if(mode==='root'){assert.equal(h.facts[0].provenance.nativeTime,1200);assert.equal(h.facts[0].provenance.timeBasis,'assistant_completed');}
  }finally{h.dispose();}
 }
});
// AUTO's new live continuation may precede the compaction control publication.
test('native V1 manual summary is silent; future AUTO lineage can complete',async()=>{
 const h=v1();try{
  await h.birth();const summary=h.assistant('summary','u',{summary:true,time:{created:1150}});
  h.set([h.user(),summary]);await h.observe('message.updated',{info:summary});await h.observe('session.idle',{sessionID:'s'});
  assert.equal(h.facts.length,0);
  const user=h.user('auto-user',1200),answer=h.assistant('auto-assistant','auto-user',{finish:'stop',time:{created:1300,completed:1400}});
  h.set([user,answer]);await h.observe('message.updated',{info:user});await h.observe('session.compacted',{sessionID:'s'});
  await h.observe('message.updated',{info:answer});await h.observe('session.idle',{sessionID:'s'});
  assert.equal(h.facts.length,1);assert.equal(h.facts[0].turnID,'auto-user');
 }finally{h.dispose();}
});

// Native summary work may republish U1 while U2's real SDK callback is held.
// Both the completion and the later question must keep the captured U2 binding.
for(const kind of ['completion','question'])test(`native V1 late old-user summary retains U2 ${kind} across held preparation`,async()=>{
  let release,reached=false;const held=new Promise(r=>release=r);
  const h=v1({beforeEmit:()=>{reached=true;return held;}});
  try{
   await h.birth();const user=h.user('u2',1200),answer=h.assistant('a','u2',
    {time:{created:1300,...(kind==='completion'?{completed:1400}:{})},...(kind==='completion'?{finish:'stop'}:{})});
   h.set([h.user(),user,answer]);await h.observe('message.updated',{info:user});await h.observe('message.updated',{info:answer});
   if(kind==='question')await h.observe('message.updated',{info:h.user()});
   const work=kind==='completion'?h.observe('session.idle',{sessionID:'s'}):h.ask();await until(()=>reached);
   if(kind==='completion')await h.observe('message.updated',{info:h.user()});
   release(true);await work;
   assert.equal(h.facts.length,1,kind);assert.equal(h.facts[0].turnID,'u2');
   assert.equal(h.facts[0].kind,kind==='completion'?'turn_idle_verified':'question_asked');
   assert.equal(h.facts[0].provenance.nativeTime,kind==='completion'?1400:1300);
   assert.equal(h.facts[0].provenance.timeBasis,kind==='completion'?'assistant_completed':'assistant_created_lower_bound');
   await h.observe('session.idle',{sessionID:'s'});assert.equal(h.facts.length,1);
  }finally{release?.(false);h.dispose();}
});
// A real newer turn cancels held work; another session cannot replace its user.
test('native V1 newer-user cancellation and unrelated-session isolation survive held preparation',async()=>{
 for(const foreign of [false,true]){
  let release,reached=false;const held=new Promise(r=>release=r);
  const h=v1({beforeEmit:()=>{reached=true;return held;}});
  try{
   await h.birth();const work=h.ask();await until(()=>reached);
   await h.observe('message.updated',{info:{...h.user('u2',1200),sessionID:foreign?'other':'s'}});
   release(true);await work;assert.equal(h.facts.length,foreign?1:0);
   if(foreign)assert.equal(h.facts[0].turnID,'u');
  }finally{release?.(false);h.dispose();}
 }
});
// Equal native milliseconds do not make distinct user IDs the same turn.
for(const kind of ['completion','question'])test(`native V1 equal-time distinct users cancel held U1 and retain U2 ${kind}`,async()=>{
 let release,reached=false;const held=new Promise(r=>release=r);
 const h=v1({beforeEmit:(event)=>{if(event.turnID==='u'){reached=true;return held;}return true;}});
 try{
  await h.birth();
  if(kind==='completion'){
   const answer=h.assistant('a','u',{finish:'stop',time:{created:1100,completed:1200}});
   h.set([h.user(),answer]);await h.observe('message.updated',{info:answer});
  }
  const work=kind==='completion'?h.observe('session.idle',{sessionID:'s'}):h.ask('q-u1');await until(()=>reached);
  const user=h.user('u2',1000),answer=h.assistant('a','u2',
   {time:{created:1300,...(kind==='completion'?{completed:1400}:{})},...(kind==='completion'?{finish:'stop'}:{})});
  h.set([h.user(),user,answer]);await h.observe('message.updated',{info:user});await h.observe('message.updated',{info:answer});
  release(true);await work;assert.deepEqual(h.facts,[]);
  await h.observe('message.updated',{info:h.user()});
  const current=()=>kind==='completion'?h.observe('session.idle',{sessionID:'s'}):h.ask('q-u2');await current();
  assert.equal(h.facts.length,1);assert.equal(h.facts[0].turnID,'u2');
  assert.equal(h.facts[0].kind,kind==='completion'?'turn_idle_verified':'question_asked');
  assert.equal(h.facts[0].provenance.nativeTime,kind==='completion'?1400:1300);
  await h.observe('message.updated',{info:h.user()});await current();assert.equal(h.facts.length,1);
 }finally{release?.(false);h.dispose();}
});
// Invalid native birth identities/order must synchronously close held authority.
test('native V1 malformed or changed-seen user birth invalidates held SDK work',async()=>{
 for(const bad of [{id:'bad\n'}, {sessionID:'bad\n'}, {time:{created:'1200'}},
  {time:{created:NaN}}, {time:{created:1200}}]){
  let release,reached=false;const held=new Promise(r=>release=r);
  const h=v1({beforeEmit:()=>{reached=true;return held;}});
  try{
   await h.birth();const work=h.ask();await until(()=>reached);
   await h.observe('message.updated',{info:{...h.user(),...bad}});
   release(true);await work;assert.deepEqual(h.facts,[]);
  }finally{release?.(false);h.dispose();}
 }
});
// No identity eviction can reopen old users: either bound closes the existing
// authority, even when overflowing traffic belongs to a different session.
test('native V1 session and seen-user capacity invalidate held work without eviction',async()=>{
 for(const bound of ['sessions','users']){
  let release,reached=false;const held=new Promise(r=>release=r);
  const h=v1({beforeEmit:()=>{reached=true;return held;}});
  try{
   await h.birth();
   const count=bound==='sessions'?511:512;
   for(let i=0;i<count;i++)await h.observe('message.updated',{info:{...h.user(`foreign-${i}`,1200+i),sessionID:bound==='sessions'?`foreign-${i}`:'foreign'}});
   const work=h.ask();await until(()=>reached);
   await h.observe('message.updated',{info:{...h.user('overflow',2000),sessionID:bound==='sessions'?'overflow':'foreign'}});
   release(true);await work;assert.deepEqual(h.facts,[]);
  }finally{release?.(false);h.dispose();}
 }
});

// Red if malformed nonces reach native emit, if a trailing newline passes the
// reader's regexp, or if malformed/extended data can cross the native port.
// This exercises the real port adapter; native schema conversion acceptance
// remains ROOT's actual API-only E2E proof, not a simulated converter here.
test('native V2 RPC checkpoint enforces ASCII lowerhex32 at both port boundaries',async()=>{
 const emitted=[],result=Object.freeze({native:'emit-result'});let disposed=0;
 const context={rpc:{register:async()=>({events:{emit:async(name,data)=>{
  emitted.push({name,data});return result;
 }},dispose:async()=>disposed++})}};
 const port=await createRPCCheckpoint(context,async()=>true).register(new AbortController().signal,'owned');
 const nonce='0123456789abcdef0123456789abcdef';
 const invalid=[undefined,null,0,true,{},[],new String(nonce),'',nonce.slice(1),nonce+'0',
  nonce.toUpperCase(),'g'+nonce.slice(1),nonce+'\n','\n'+nonce.slice(1),
  'é'+nonce.slice(1),'０'+nonce.slice(1)];
 for(const value of invalid){
  await assert.rejects(async()=>port.emit(value),{name:'TypeError'});
  assert.equal(emitted.length,0,'invalid nonce must not call native emit');
  assert.equal(port.read({data:{nonce:value}}),undefined);
 }
 for(const data of [undefined,null,0,true,nonce,{},[],Object.assign([],{nonce}),
  {nonce,extra:true},Object.assign(Object.create({nonce}),{extra:true}),
  Object.defineProperty({nonce},'extra',{value:true}),{nonce,[Symbol('extra')]:true}]){
  assert.equal(port.read({data}),undefined);
 }
 assert.equal(port.read(null),undefined);assert.equal(port.read(undefined),undefined);
 assert.equal(await port.emit(nonce),result);
 assert.deepEqual(emitted,[{name:'checkpoint',data:{nonce}}]);
 assert.equal(port.read({type:port.type,data:emitted[0].data}),nonce);
 assert.equal(port.type,'rpc.agent-notifications-owned.checkpoint');
 await port.dispose();assert.equal(disposed,1);
});

function v2({parentID,fork,wrongScope=false,wrongProject=false,workspaceID,beforeEmit,holdInitial=false}={}){
 const location={directory,project:{id:'project'},...(workspaceID?{workspaceID}:{})},facts=[],sdkFacts=[],diagnostics=[],order=[],queue=[],waiting=[];
 let rows=[],sequence=0,serial=0,disposed=false,markers=0,hold=holdInitial,heldMarkers=[],subscribes=0;
 const pump=()=>{while(waiting.length && queue.length)waiting.shift()(queue.shift());};
 const push=(e)=>{queue.push({done:false,value:e});pump();};
 const context={app:{version:'2.0.21'},location,
  session:{get:async(input)=>{assert.deepEqual(Object.keys(input),['sessionID']);order.push('session');return {id:input.sessionID,parentID,fork,projectID:wrongProject?'other-project':'project',
   location:{directory:wrongScope?'/OTHER':directory}};},
   context:async(input)=>{assert.deepEqual(Object.keys(input),['sessionID']);order.push('context');return structuredClone(rows);}},
  permission:{get:async(input)=>{assert.deepEqual(Object.keys(input).sort(),['requestID','sessionID']);order.push('permission');return {id:input.requestID,sessionID:input.sessionID,source:{type:'tool',messageID:'msg_assistant',id:'call'}};},list:async()=>[]},
  rpc:{register:async(definition,methods)=>{
   assert.deepEqual(definition.methods,{});assert.deepEqual(methods,{});assert.equal(definition.events.checkpoint.schema.additionalProperties,false);
   order.push('register');const type=`rpc.${definition.id}.checkpoint`;
   return {events:{emit:async(name,data)=>{assert.equal(name,'checkpoint');markers++;order.push('marker');
    const e={id:`evt_marker_${markers}`,created:1700000000000,type,data,location};if(hold)heldMarkers.push(e);else push(e);}},dispose:async()=>order.push('rpc-dispose')};
  }},event:{subscribe({signal}){
   subscribes++;disposed=false;assert.ok(signal instanceof AbortSignal);
   const abort=()=>{disposed=true;view.reset();while(waiting.length)waiting.shift()({done:true});};signal.addEventListener('abort',abort,{once:true});
   return {[Symbol.asyncIterator](){return this;},next(){return disposed?Promise.resolve({done:true}):new Promise(r=>{waiting.push(r);pump();});},
    return(){signal.removeEventListener('abort',abort);abort();return Promise.resolve({done:true});}};
  }}};
 const view=createNativeV2(context,location,()=>{},()=>{throw Error('unexpected_native_capacity');});
 const observer=createV2Observer({context,location:directory,native:view.native,runtimeEligibility:()=> 'supported',
  onDiagnostic:reason=>diagnostics.push(reason),checkpoint:createRPCCheckpoint(context,async()=>true),beforeEmit:async(e,h)=>{order.push('before');const fact=view.project(e);
   return Boolean(fact && (beforeEmit?await beforeEmit(fact,h):true) && await view.finalize(e,h));},
  emit:(e)=>{order.push('emit');sdkFacts.push(e);const fact=view.project(e);if(fact)facts.push(fact);}});
 const event=(type,data={},seq,loc)=>{serial++;const e={id:`evt_${serial}`,created:1700000000000+serial,type,data:{sessionID:'s',...data}};
  if(type.startsWith('session.')){sequence=seq??sequence+1;e.durable={aggregateID:'s',seq:sequence,version:1};}
  if(loc)e.location=loc;push(e);return e;};
 // Explicit live-only envelope: no durable field and no durable sequence advance.
 const nondurable=(type,data={},loc)=>{const e={id:`evt_${++serial}`,created:1700000000000+serial,type,data:{sessionID:'s',...data},...(loc?{location:loc}:{})};push(e);return e;};
 const birth=()=>{
  if(sequence===0)event('session.created',{location},1);event('session.inbox.enqueued',{inboxID:'inbox-user',item:{type:'user'}});
  const start=event('session.execution.started');event('session.inbox.delivered',{inboxID:'inbox-user'});
  event('session.step.started',{assistantMessageID:'msg_assistant',type:'assistant'});
  rows=[{id:'inbox-user',type:'user'},{id:'msg_assistant',type:'assistant',time:{created:1700000000005},content:[{type:'tool',id:'call',name:'question'}]}];return start;
 };
 const question=(rid='form')=>event('form.created',{form:{id:rid,sessionID:'s',fields:[{type:'select'}],metadata:{kind:'question',tool:{messageID:'msg_assistant',id:'call'}}}});
 const terminal=(failure=false)=>{
  event(failure?'session.step.failed':'session.step.ended',{assistantMessageID:'msg_assistant',type:'assistant'});
  const end=event(failure?'session.execution.failed':'session.execution.succeeded',failure?{error:{_tag:'APIError'}}:{});
  rows=[{id:'inbox-user',type:'user'},{id:'msg_assistant',type:'assistant',finish:failure?undefined:'stop',time:{created:1700000000005,completed:end.created},...(failure?{error:{_tag:'APIError'}}:{})},
   {id:`msg_${end.id.slice(4)}`,type:'idle'}];return end;
 };
 observer.start();
 return {facts,sdkFacts,diagnostics,order,birth,question,terminal,event,nondurable,observer,markers:()=>markers,subscribes:()=>subscribes,
  setRows:v=>rows=v,end(){queue.push({done:true});pump();},unhold(){hold=false;for(const e of heldMarkers)push(e);heldMarkers=[];},
  async stop(){observer.dispose();await observer.done();}};
}
// Red if marker emit-return is readiness, or if preparation moves after the
// SAME-reader final marker, or native source IDs are replaced by desired IDs.
test('native V2 marker consumption, sparse seq14->16 and original run/terminal IDs',async()=>{
 const h=v2({holdInitial:true});try{
  await until(()=>h.markers()===1);const start=h.birth();h.question('too-early');
  for(let i=0;i<10;i++)await pause();assert.deepEqual(h.facts,[]);
  h.unhold();for(let i=0;i<10;i++)await pause();h.question('future-live');await until(()=>h.facts.length===1);
  assert.equal(h.facts[0].requestID,'future-live');assert.equal(h.facts[0].turnID,start.id);
  const before=h.order.lastIndexOf('before'),marker=h.order.lastIndexOf('marker'),emit=h.order.lastIndexOf('emit');
  assert.ok(before<marker && marker<emit);assert.equal(h.markers(),2);
  h.event('form.replied',{id:'future-live'});h.event('session.compaction.started',{},14);h.event('session.compaction.ended',{},16);
  h.event('session.step.started',{assistantMessageID:'msg_assistant',type:'assistant'});const end=h.terminal();
  await until(()=>h.facts.length===2);assert.equal(h.subscribes(),1);assert.notEqual(end.id,start.id);
  assert.equal(h.facts[1].turnID,start.id);assert.equal(h.facts[1].provenance.nativeEventID,end.id);
  assert.equal(h.facts[1].provenance.nativeTime,end.created);
 }finally{await h.stop();}
});
// Red if a close event during all asynchronous preparation can spawn a question.
test('native V2 close during beforeEmit suppresses the final marker/effect',async()=>{
 let release,reached=false;const held=new Promise(r=>release=r);
 const h=v2({beforeEmit:()=>{reached=true;return held;}});
 try{
  await until(()=>h.markers()===1);h.birth();h.question();await until(()=>reached);
  h.event('form.cancelled',{id:'form'});for(let i=0;i<10;i++)await pause();release(true);
  for(let i=0;i<10;i++)await pause();assert.deepEqual(h.facts,[]);assert.equal(h.markers(),1);
 }finally{release?.(false);await h.stop();}
});
// Red if metadata copies run IDs instead of proving them, if fork is ancestry,
// if a permission read unwraps HTTP {data}, or if error terminal ID is invented.
test('native V2 direct metadata proves root/fork/permission/error and rejects child/wrong scope',async()=>{
 for(const mode of ['root','fork','child','scope','permission','error']){
  const h=v2({parentID:mode==='child'?'parent':undefined,fork:mode==='fork'?{sessionID:'lineage'}:undefined,wrongScope:mode==='scope',workspaceID:'workspace'});
  try{
   await until(()=>h.markers()===1);h.birth();let end;
   if(mode==='permission')h.event('permission.asked',{id:'permission',action:'read',resources:[],source:{type:'tool',messageID:'msg_assistant',id:'call'}});
   else end=h.terminal(mode==='error');
   if(['child','scope'].includes(mode)){for(let i=0;i<30;i++)await pause();assert.equal(h.facts.length,0,mode);}
   else {await until(()=>h.facts.length===1);assert.equal(h.facts[0].rootSession,true);
    if(mode==='error'){assert.equal(h.facts[0].kind,'terminal_error');assert.equal(h.facts[0].provenance.nativeEventID,end.id);}
    if(mode==='permission')assert.equal(h.facts[0].requestID,'permission');}
  }finally{await h.stop();}
 }
});
// Red if retry/interruption/manual compaction/assistant mismatch or a replayed
// durable sequence can manufacture a terminal fact or revive a live request.
test('native V2 negative discriminators and contradictory reorder stay silent',async()=>{
 for(const mode of ['retry','interrupt','manual','mismatch','reorder']){
  const h=v2();try{
   await until(()=>h.markers()===1);h.birth();
   if(mode==='retry')h.event('session.retry.scheduled',{assistantMessageID:'msg_assistant'});
   if(mode==='interrupt')h.event('session.execution.interrupted');
   if(mode==='manual')h.event('session.step.started',{type:'compaction',inputID:'manual-input'});
   if(mode==='reorder')h.event('session.step.ended',{assistantMessageID:'msg_assistant'},2);
   if(mode==='mismatch'){h.terminal(true);h.setRows([{id:'inbox-user',type:'user'},{id:'msg_other',type:'assistant',error:{_tag:'APIError'},time:{created:1700000000005,completed:1700000000020}},{id:'msg_7',type:'idle'}]);}
   else h.event('session.execution.succeeded');
   for(let i=0;i<30;i++)await pause();assert.equal(h.facts.length,0,mode);
  }finally{await h.stop();}
 }
});
// Red if preparation's last await makes an obsolete native assistant snapshot
// usable. The product rereads real public metadata AFTER preparation, before
// the SDK's sole final marker; there is no extra metadata await in final emit.
test('native V2 final metadata reread follows preparation and precedes the sole marker',async()=>{
 let release,reached=false;const held=new Promise(r=>release=r);
 const h=v2({beforeEmit:()=>{reached=true;return held;}});
 try{
  await until(()=>h.markers()===1);h.birth();h.question();await until(()=>reached);
  h.setRows([{id:'inbox-user',type:'user'},{id:'msg_assistant',type:'compaction',time:{created:1700000000005}}]);
  release(true);for(let i=0;i<20;i++)await pause();
  assert.deepEqual(h.facts,[]);assert.equal(h.markers(),1);
  assert.ok(h.order.lastIndexOf('context')>h.order.lastIndexOf('before'));
 }finally{release?.(false);await h.stop();}
});
// Red if a dead reader's held preparation revives after replacement, or if
// hydration/request traffic without new live execution lineage becomes a birth.
test('native V2 same-SDK reader replacement cancels held work and only future live lineage reactivates',async()=>{
 let release,reached=false,first=true;const held=new Promise(r=>release=r);
 const h=v2({beforeEmit:()=>{if(first){first=false;reached=true;return held;}return true;}});
 try{
  await until(()=>h.markers()===1);h.birth();h.question('old-form');await until(()=>reached);h.end();
  await until(()=>h.subscribes()===2);await until(()=>h.markers()===2);release(true);
  for(let i=0;i<10;i++)await pause();assert.deepEqual(h.facts,[]);
  h.question('hydrated-old-run');for(let i=0;i<10;i++)await pause();assert.deepEqual(h.facts,[]);
  const run=h.birth();h.question('fresh-form');await until(()=>h.facts.length===1);
  assert.equal(h.facts[0].requestID,'fresh-form');assert.equal(h.facts[0].turnID,run.id);
 }finally{release?.(false);await h.stop();}
});
// Red if unrelated global forms or a foreign RPC namespace poison an eligible
// native root. Irrelevant event shape is classified before business validation.
test('native V2 unrelated global form and sparse irrelevant session traffic do not cancel live work',async()=>{
 const h=v2();try{
  await until(()=>h.markers()===1);h.birth();
  h.event('form.created',{form:{id:'global',sessionID:'global',metadata:{kind:'mcp'}}});
  h.event('session.title.updated',{title:'PRIVATE_SENTINEL'},14);
  h.question();await until(()=>h.facts.length===1);assert.equal(h.subscribes(),1);
  assert.ok(!JSON.stringify(h.facts).includes('PRIVATE_SENTINEL'));
 }finally{await h.stop();}
});
// Red if replacement count resets on brief success or arbitrary reader replay
// can bypass the qualified factory's lifetime bounded reconnect policy.
test('packed native SDK exhausts exactly three lifetime reader replacements',async()=>{
 let subscriptions=0,disposedRPC=0;
 const observer=createV2Observer({location:directory,runtimeEligibility:()=> 'supported',
  context:{app:{version:'2.0.21'},event:{subscribe(){subscriptions++;return (async function*(){})()}}},
  native:{correlate(){},session:async()=>{},assistant:async()=>{}},
  checkpoint:{register:async()=>({type:'rpc.test.checkpoint',emit:async()=>{},read:()=>undefined,dispose:()=>disposedRPC++})},emit(){throw Error('must_not_emit');}});
 observer.start();await observer.done();assert.equal(subscriptions,4);assert.ok(disposedRPC<=4);observer.dispose();
});
// Supplied source correction: context is the whole closed public message union,
// projectID is top-level Session.Info, and synthetic delivery is not a user.
test('native V2 legitimate control rows and synthetic inbox do not corrupt root/user binding',async()=>{
 const h=v2();try{
  await until(()=>h.markers()===1);const run=h.birth();
  h.event('session.inbox.enqueued',{inboxID:'synthetic',item:{type:'synthetic'}});h.event('session.inbox.delivered',{inboxID:'synthetic'});
  const end=h.terminal();h.setRows([{id:'inbox-user',type:'user'},
   {id:'msg_assistant',type:'assistant',finish:'stop',time:{created:1700000000005,completed:end.created}},
   ...['agent-switched','model-switched','location-switched','synthetic','system','skill','shell'].map((type,i)=>({id:`control-${i}`,type})),
   {id:`msg_${end.id.slice(4)}`,type:'idle'}]);
  await until(()=>h.facts.length===1);assert.equal(h.facts[0].turnID,run.id);
 }finally{await h.stop();}
 const wrong=v2({wrongProject:true});try{
  await until(()=>wrong.markers()===1);wrong.birth();wrong.terminal();for(let i=0;i<30;i++)await pause();assert.deepEqual(wrong.facts,[]);
 }finally{await wrong.stop();}
});
// Red if a free-form metadata.kind/IDs alone attest the actual question call.
test('native V2 question requires one actual ordinary assistant content tool id/name',async()=>{
 for(const content of [[],[{type:'tool',id:'call',name:'read'}],[{type:'tool',id:'wrong-call',name:'question'}],
  [{type:'tool',id:'call',name:'question'},{type:'tool',id:'call',name:'question'}]]){
  const h=v2();try{
   await until(()=>h.markers()===1);h.birth();h.setRows([{id:'inbox-user',type:'user'},
    {id:'msg_assistant',type:'assistant',time:{created:1700000000005},content}]);h.question();
   for(let i=0;i<30;i++)await pause();assert.deepEqual(h.facts,[]);assert.equal(h.markers(),1);
  }finally{await h.stop();}
 }
});
// Direct final-boundary regression: source cache deletion can precede reply
// publication. A held root read must finish BEFORE the final pending snapshot.
test('native V2 pending permission snapshot is the last awaited read after root metadata',async()=>{
 let release,reached=false,readPending=false,pending=true;const held=new Promise(r=>release=r);
 const context={location:{directory,project:{id:'project'}},session:{get(){reached=true;return held;},context:async()=>[]},
  permission:{get:async()=>{readPending=true;if(!pending)throw Error('pending_absent');return {id:'request',sessionID:'s',source:{type:'tool',messageID:'msg_assistant',id:'call'}};}}};
 const view=createNativeV2(context,context.location,()=>{},()=>{throw Error('capacity');});
 for(const e of [
  {id:'evt_inbox',type:'session.inbox.enqueued',data:{sessionID:'s',inboxID:'user',item:{type:'user'}}},
  {id:'evt_run',type:'session.execution.started',data:{sessionID:'s'}},
  {id:'evt_delivery',type:'session.inbox.delivered',data:{sessionID:'s',inboxID:'user'}},
  {id:'evt_step',type:'session.step.started',data:{sessionID:'s',assistantMessageID:'msg_assistant',type:'assistant'}}])view.native.correlate(e);
 const result=view.native.currentPermission({sessionID:'s',turnID:'evt_run',userID:'user',location:directory,requestID:'request'},new AbortController().signal);
 await until(()=>reached);assert.equal(readPending,false);pending=false;
 release({id:'s',projectID:'project',location:{directory}});await assert.rejects(result,/pending_absent/);assert.equal(readPending,true);
});

// Independent closed schema cases from OpenCode 8a8bd622 session-event.ts.
const liveOnly = [
 ['session.text.delta',{assistantMessageID:'msg_assistant',ordinal:0,delta:'fragment'}],
 ['session.usage.updated',{cost:0,tokens:{input:1,output:1,reasoning:0,cache:{read:0,write:0}}}],
 ['session.reasoning.delta',{assistantMessageID:'msg_assistant',ordinal:0,delta:'fragment'}],
 ['session.tool.input.delta',{assistantMessageID:'msg_assistant',id:'call',delta:'fragment'}],
 ['session.tool.progress',{assistantMessageID:'msg_assistant',id:'call',metadata:{progress:1}}],
 ['session.compaction.delta',{text:'fragment'}],
];
const sequenceDefect=reason=>/sequence|correlation_unverified|identity_contradiction/.test(reason);
test('native V2 nondurable text delta and usage preserve one public SDK completion and original identity',async t=>{
 const h=v2();try{
  await until(()=>h.markers()===1);const run=h.birth();
  h.event('session.text.started',{assistantMessageID:'msg_assistant',ordinal:0});
  const delta=h.nondurable(...liveOnly[0]);
  h.event('session.text.ended',{assistantMessageID:'msg_assistant',ordinal:0,text:'fragment'});
  h.event('session.step.streamed',{assistantMessageID:'msg_assistant',type:'assistant'});
  const usage=h.nondurable(...liveOnly[1]);const end=h.terminal();
  assert.equal(Object.hasOwn(delta,'durable'),false);assert.equal(Object.hasOwn(usage,'durable'),false);
  await until(()=>h.sdkFacts.length>0 || h.diagnostics.some(sequenceDefect));
  for(let i=0;i<30;i++)await pause();
  t.diagnostic(JSON.stringify({publicSDKFacts:h.sdkFacts,projectedFacts:h.facts,diagnostics:h.diagnostics,
   original:{run:run.id,user:'inbox-user',assistant:'msg_assistant',terminal:end.id},durableTerminalSeq:end.durable.seq}));
  assert.equal(h.sdkFacts.length,1,'live fragments must not block the genuine terminal');
  assert.equal(h.facts.length,1);assert.equal(h.sdkFacts[0].turnID,'inbox-user');
  assert.equal(h.facts[0].turnID,run.id);assert.equal(h.sdkFacts[0].sessionID,'s');
  assert.equal(h.sdkFacts[0].kind,'turn_idle_verified');assert.equal(h.sdkFacts[0].rootSession,true);
  assert.equal(h.sdkFacts[0].messageID,'msg_assistant');
  assert.equal(h.sdkFacts[0].provenance.nativeEventID,end.id);
  assert.equal(h.sdkFacts[0].provenance.nativeTime,end.created);
  assert.equal(end.durable.seq,10,'ephemeral events must not consume durable sequence');
  assert.deepEqual(h.diagnostics.filter(sequenceDefect),[]);
 }finally{await h.stop();assert.equal(h.order.filter(x=>x==='rpc-dispose').length,1);assert.ok(h.diagnostics.includes('observer_disposed'));}
});
// Every exact kind is irrelevant before ingress and identity scope mutation;
// 513 irrelevant sessions also cannot consume the adapter's 512 binding slots.
test('native V2 six live-only kinds leave ingress, scope, question binding and capacity unchanged',async()=>{
 const location={directory,workspaceID:'workspace',project:{id:'project'}},ingress=[];let uncertain=0;
 const rows=[{id:'user',type:'user'},{id:'msg_assistant',type:'assistant',content:[{type:'tool',id:'call',name:'question'}]}];
 const context={session:{get:async()=>({id:'s',projectID:'project',location:{directory}}),context:async()=>rows}};
 const view=createNativeV2(context,location,e=>ingress.push(e),()=>uncertain++);
 const send=(type,data={},extra={})=>view.native.correlate({id:'evt_'+ingress.length,created:1700000000000,type,data:{sessionID:'s',...data},...extra});
 send('session.execution.started',{}, {id:'evt_run',location});
 send('session.inbox.enqueued',{inboxID:'user',item:{type:'user'}});send('session.inbox.delivered',{inboxID:'user'});
 send('session.step.started',{assistantMessageID:'msg_assistant',type:'assistant'});
 send('form.created',{form:{id:'form',sessionID:'s',fields:[{}],metadata:{kind:'question',tool:{messageID:'msg_assistant',id:'call'}}}});
 const run={sessionID:'s',turnID:'evt_run',userID:'user',location:directory,requestID:'form',messageID:'msg_assistant',callID:'call'};
 const signal=new AbortController().signal;
 for(const [type,data] of liveOnly){
  const before=ingress.length;
  assert.equal(send(type,data,{location:{directory:'/OTHER',workspaceID:'other'}}),undefined,type);
  assert.equal(ingress.length,before,type);
  assert.equal((await view.native.questionSource(run,signal))?.requestID,'form',type);
  for(let i=0;i<513;i++)assert.equal(send(type,{...data,sessionID:`ephemeral-${i}`}),undefined,type);
  assert.equal(ingress.length,before,type);assert.equal(uncertain,0,type);
 }
});
test('native V2 every live-only kind permits completion but durable compaction controls remain silent',async()=>{
 for(const [type,data] of liveOnly){
  const h=v2();try{
   await until(()=>h.markers()===1);h.birth();h.nondurable(type,data);h.terminal();
   await until(()=>h.facts.length===1 || h.diagnostics.some(sequenceDefect));
   assert.equal(h.facts.length,1,type);assert.deepEqual(h.diagnostics.filter(sequenceDefect),[],type);
  }finally{await h.stop();}
 }
 for(const control of ['session.compaction.started','session.compaction.ended','session.compaction.failed']){
  const h=v2();try{
   await until(()=>h.markers()===1);h.birth();
   h.event('session.compaction.started',{reason:'manual',recent:'',inputID:'manual'});
   h.event('session.step.started',{type:'compaction',inputID:'manual'});
   h.nondurable(...liveOnly[5]);
   h.event(control.endsWith('failed')?'session.step.failed':'session.step.ended',{type:'compaction',inputID:'manual'});
   if(control!=='session.compaction.started')h.event(control,control.endsWith('failed')?{reason:'manual',error:{_tag:'APIError'}}:
    {reason:'manual',model:{},providerState:{},providerContext:[],text:'summary',recent:'',cost:0,tokens:liveOnly[1][1].tokens});
   const end=h.event('session.execution.succeeded');
   h.setRows([{id:'inbox-user',type:'user'},{id:'summary',type:'compaction'}, {id:`msg_${end.id.slice(4)}`,type:'idle'}]);
   for(let i=0;i<30;i++)await pause();assert.deepEqual(h.facts,[],control);
  }finally{await h.stop();}
 }
});
test('native V2 semantic and unknown nondurable events retain fail-closed native order',async()=>{
 for(const mode of ['missing','invalid','aggregate','version','nonincreasing','conflict','unknown','viewed']){
  const h=v2();try{
   await until(()=>h.markers()===1);h.birth();
   const type=mode==='unknown'?'session.future.delta':mode==='viewed'?'session.viewed':'session.text.ended';
   const e=h.nondurable(type,{assistantMessageID:'msg_assistant',ordinal:0,text:'fragment'});
   if(['invalid','aggregate','version','nonincreasing','conflict'].includes(mode))e.durable={aggregateID:mode==='aggregate'?'foreign':'s',seq:mode==='invalid'?'6':mode==='nonincreasing'?5:6,version:mode==='version'?-1:1};
   if(mode==='conflict'){const other=h.event('session.text.ended',{assistantMessageID:'msg_assistant',ordinal:0,text:'other'},7);other.id=e.id;}
   h.terminal();await until(()=>h.diagnostics.some(sequenceDefect));
   for(let i=0;i<30;i++)await pause();assert.deepEqual(h.facts,[],mode);
  }finally{await h.stop();}
 }
});

test('native V2 resolved question and permission stay silent after live-only traffic',async()=>{
 for(const kind of ['question','permission']){
  let release,reached=false;const held=new Promise(r=>release=r);
  const h=v2({beforeEmit:()=>{reached=true;return held;}});try{
   await until(()=>h.markers()===1);h.birth();
   const ask=()=>kind==='question'?h.question('resolved'):h.event('permission.asked',{id:'resolved',action:'read',resources:[],source:{type:'tool',messageID:'msg_assistant',id:'call'}});
   ask();await until(()=>reached);
   h.event(kind==='question'?'form.replied':'permission.replied',kind==='question'?{id:'resolved'}:{requestID:'resolved'});
   for(const pair of liveOnly)h.nondurable(...pair);
   release(true);for(let i=0;i<30;i++)await pause();ask();
   for(let i=0;i<30;i++)await pause();assert.deepEqual(h.facts,[],kind);
  }finally{release?.(false);await h.stop();}
 }
});

// Red if native completion repeats HTTP after the actual strict SDK snapshot,
// or its own binding admits a substituted root/turn/provenance or expired handoff.
test('native V1 SDK-finalized completion keeps live bindings without later HTTP',async()=>{
 let finalized=false,laterReads=0;
 const h=v1({onLookup:()=>{if(finalized){laterReads++;throw Error('PRIVATE_CLOSED_CLIENT');}},
  beforeEmit:async(event,handoff,view)=>{
   finalized=true;
   for(const changed of [{rootSession:false},{turnID:'foreign'},
    {provenance:{...event.provenance,generation:'v2'}},
    {provenance:{...event.provenance,nativeTime:0}}])
    assert.equal(await view.finalize({...event,...changed},handoff),false);
   assert.equal(await view.finalize(event,{...handoff,isCurrent:()=>false}),false);
   const controller=new AbortController();controller.abort();
   assert.equal(await view.finalize(event,{...handoff,signal:controller.signal}),false);
   return true;
  }});
 try{
  await h.birth();const answer=h.assistant('a','u',{finish:'stop',time:{created:1100,completed:1200}});
  h.set([h.user(),answer]);await h.observe('message.updated',{info:answer});
  await h.observe('session.idle',{sessionID:'s'});
  assert.equal(h.facts.length,1);assert.equal(h.facts[0].kind,'turn_idle_verified');
  assert.equal(laterReads,0);
 }finally{h.dispose();}
});
// Red if completion simplification also removes attention/error's later snapshot.
for(const kind of ['question_asked','terminal_error'])test(`native V1 ${kind} retains later get/messages`,async()=>{
 let finalized=false;const later=[];
 const h=v1({onLookup:name=>{if(finalized)later.push(name);},beforeEmit:()=>{finalized=true;return true;}});
 try{
  await h.birth();
  if(kind==='question_asked')await h.ask();
  else{
   const answer=h.assistant('a','u',{time:{created:1100,completed:1200},error:{name:'APIError'}});
   h.set([h.user(),answer]);await h.observe('message.updated',{info:answer});
   await h.observe('session.error',{sessionID:'s',error:answer.error});
   await h.observe('session.idle',{sessionID:'s'});
  }
  assert.equal(h.facts.length,1);assert.equal(h.facts[0].kind,kind);
  assert.deepEqual(later,['get','messages']);
 }finally{h.dispose();}
});
