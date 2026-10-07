import assert from 'node:assert/strict';
import { test } from 'node:test';
import dc from 'node:diagnostics_channel';

// PR294's dual-object boundary plus the retained legacy entry: neither branch
// may obtain a second loader authority from multiple native export discovery.
test('self-contained bundle has the exact dual definition and one shared V1 loader', async()=>{
 const exports=await import('../internal/opencodeplugin/dist/agent-notifications.js');
 assert.deepEqual(Object.keys(exports),['AgentNotifications','default']);
 assert.deepEqual(Object.keys(exports.default).sort(),['id','server','setup']);
 assert.equal(exports.default.id,'agent-notifications');
 assert.equal(typeof exports.default.server,'function');assert.equal(typeof exports.default.setup,'function');
 const client={session:{get(){throw Error('must_not_read');},messages(){throw Error('must_not_read');}}};
 const input={client,directory:'/TEST-unqualified'};
 assert.equal(await exports.AgentNotifications(input),await exports.default.server(input));
});
// Red if a public runtimeEligibility boolean, env version, content diagnostic or
// ctx.app.version can authorize an unbound/unqualified installed artifact.
test('unrendered/unqualified cells are silent even with fabricated public grants',async()=>{
 const {default:plugin}=await import('../internal/opencodeplugin/dist/agent-notifications.js');
 const output=[],children=[];
 const observer=({process:child})=>children.push(child);
 const previous=console.error;console.error=(...args)=>output.push(args);
 dc.subscribe('child_process',observer);
 try {
  const input={directory:'/TEST-unqualified',runtimeEligibility:true,client:{session:{get(){throw Error('PRIVATE_SENTINEL');},messages(){throw Error('PRIVATE_SENTINEL');}}}};
  const hooks=await plugin.server(input);
  assert.equal(hooks.event({event:{type:'session.error',properties:{sessionID:'PRIVATE_SENTINEL',error:{message:'PRIVATE_SENTINEL'}}}}),undefined);
  const context={app:{version:'2.0.21'},runtimeEligibility:true,location:{directory:'/TEST-unqualified'},
   session:{get(){throw Error('PRIVATE_SENTINEL');},context(){throw Error('PRIVATE_SENTINEL');}},
   permission:{get(){throw Error('PRIVATE_SENTINEL');},list(){throw Error('PRIVATE_SENTINEL');}},
   rpc:{register(){throw Error('PRIVATE_SENTINEL');}},event:{subscribe(){throw Error('PRIVATE_SENTINEL');}}};
  assert.equal(await plugin.setup(context),undefined);
  assert.deepEqual(output,[]);assert.deepEqual(children,[]);
 }finally{dc.unsubscribe('child_process',observer);console.error=previous;}
});

// Red failure: a queued user admission during a held terminal preparation lets
// that obsolete completion emit when the preparation resumes. Exercise the real
// strict SDK and native identity adapter at their source observation boundary;
// the isolated host below grants no installed profile, clock or child authority.
test('queued V2 user admission fences held completion until a fresh native execution', async()=>{
 const {createV2Observer}=await import('universal-agent-plugins-opencode-events/v2');
 const {createNativeV2,createRPCCheckpoint}=await import('./native-v2.mjs');
 const directory='/TEST-queued-source',location={directory,project:{id:'project'}};
 const facts=[],queue=[],waiting=[];let serial=0,sequence=0,rows=[],closed=false,markers=0;
 let release,reached=false,heldOnce=false,queuedConsumed=false,preparationFinished=false;
 const held=new Promise(resolve=>release=resolve),pause=()=>new Promise(setImmediate);
 const until=async(check)=>{const end=Date.now()+2500;while(!check()){assert.ok(Date.now()<end,'source phase did not arrive');await pause();}};
 const push=value=>{queue.push({done:false,value});while(waiting.length && queue.length)waiting.shift()(queue.shift());};
 const context={app:{version:'2.0.21'},location,
  session:{get:async({sessionID})=>({id:sessionID,projectID:'project',location:{directory}}),context:async()=>structuredClone(rows)},
  permission:{get:async()=>undefined,list:async()=>[]},
  rpc:{register:async(definition)=>({events:{emit:async(name,data)=>{markers++;
   push({id:`evt_marker_${markers}`,created:1700000000000,type:`rpc.${definition.id}.${name}`,data,location});}},dispose:async()=>{}})},
  event:{subscribe({signal}){
   const abort=()=>{closed=true;while(waiting.length)waiting.shift()({done:true});};
   signal.addEventListener('abort',abort,{once:true});
   return {[Symbol.asyncIterator](){return this;},next(){return closed?Promise.resolve({done:true}):new Promise(resolve=>{waiting.push(resolve);while(waiting.length && queue.length)waiting.shift()(queue.shift());});},
    return(){signal.removeEventListener('abort',abort);abort();return Promise.resolve({done:true});}};
  }}};
 const view=createNativeV2(context,location,event=>{if(event.type==='session.inbox.enqueued' && event.data.inboxID==='queued')queuedConsumed=true;},()=>{throw Error('unexpected_source_capacity');});
 const observer=createV2Observer({context,location:directory,runtimeEligibility:()=> 'supported',native:view.native,
  checkpoint:createRPCCheckpoint(context,async()=>true),
  beforeEmit:async(event,handoff)=>{
   const fact=view.project(event);if(!fact)return false;
   if(!heldOnce){heldOnce=true;reached=true;await held;}
   const finalized=await view.finalize(event,handoff);preparationFinished=true;return finalized;
  },emit:event=>{const fact=view.project(event);if(fact)facts.push(fact);}});
 const event=(type,data={})=>{serial++;const value={id:`evt_${serial}`,created:1700000000000+serial,type,
  data:{sessionID:'session',...data},durable:{aggregateID:'session',seq:++sequence,version:1}};push(value);return value;};
 const terminal=(user,assistant)=>{
  event('session.step.ended',{assistantMessageID:assistant,type:'assistant'});
  const end=event('session.execution.succeeded');
  rows=[{id:user,type:'user'},{id:assistant,type:'assistant',finish:'stop',time:{created:1700000000001,completed:end.created}},
   {id:`msg_${end.id.slice(4)}`,type:'idle'}];return end;
 };
 observer.start();
 try{
  await until(()=>markers===1);
  event('session.created',{location});event('session.inbox.enqueued',{inboxID:'first',item:{type:'user'}});
  event('session.execution.started');event('session.inbox.delivered',{inboxID:'first'});
  event('session.step.started',{assistantMessageID:'msg_first',type:'assistant'});terminal('first','msg_first');
  await until(()=>reached);
  event('session.inbox.enqueued',{inboxID:'queued',item:{type:'user',delivery:'queue'}});
  await until(()=>queuedConsumed);release();await until(()=>preparationFinished);
  await pause();assert.deepEqual(facts,[]);
  const start=event('session.execution.started');event('session.inbox.delivered',{inboxID:'queued'});
  event('session.step.started',{assistantMessageID:'msg_queued',type:'assistant'});const end=terminal('queued','msg_queued');
  await until(()=>facts.length===1);
  assert.equal(facts[0].kind,'turn_idle_verified');assert.equal(facts[0].turnID,start.id);
  assert.equal(facts[0].provenance.nativeEventID,end.id);assert.equal(facts[0].provenance.nativeTime,end.created);
  assert.equal(facts.length,1);
 }finally{release();observer.dispose();await observer.done();view.reset();}
});

// Native V1 finalization calls Hooks.dispose without publishing a shutdown event.
// Red if the factory omits that hook or repeated disposal settles before the same
// owned reaping promise; existing registry tests exercise actual child/pipe close.
test('native V1 factory disposal revokes synchronously and shares pending owned reaping', async()=>{
 const {build}=await import('esbuild');
 const key='TEST-agent-notifications-v1-dispose-'+process.pid;
 const ports=`globalThis[${JSON.stringify(key)}]`;
 const stubs={
  './owned-host.mjs':`export const prepareOwnedHost=async()=>${ports}.owned;`,
  './clock-cells.mjs':'export const selectClockCell=()=>({});',
  './platform-clock.mjs':'export const createPlatformClock=()=>{throw Error("must_not_sample");};',
  './ipc.mjs':`export const createPreparedDelivery=()=>${ports}.delivery;`,
  './native-v1.mjs':`export const createNativeV1=()=>${ports}.view;`,
  './native-v2.mjs':'export const createNativeV2=()=>{};export const createRPCCheckpoint=()=>{};',
  'universal-agent-plugins-opencode-events/v1':`export const createObserver=()=>${ports}.observer;`,
  'universal-agent-plugins-opencode-events/v2':'export const createV2Observer=()=>{};',
 };
 const built=await build({entryPoints:[new URL('./plugin.mjs',import.meta.url).pathname],
  bundle:true,write:false,platform:'node',format:'esm',plugins:[{name:'native-factory-ports',setup(b){
   b.onResolve({filter:/.*/},args=>Object.hasOwn(stubs,args.path)?{path:args.path,namespace:'test-ports'}:undefined);
   b.onLoad({filter:/.*/,namespace:'test-ports'},args=>({contents:stubs[args.path],loader:'js'}));
  }}]});
 const {default:plugin,AgentNotifications}=await import('data:text/javascript;base64,'+Buffer.from(built.outputFiles[0].text).toString('base64'));
 try {
  for(const ingress of [false,true]){
   const order=[];let reaps=0,observations=0,settled=false,release;
   const reaped=new Promise(resolve=>release=resolve);
   globalThis[key]={owned:{registry:{},dispose(){reaps++;order.push('owned');return reaped;}},
    delivery:{activate:async()=>true,clock:{},dispose(){order.push('delivery');},beginIngress(){},},
    view:{dispose(){order.push('view');},ingest(){throw Error('post-dispose-ingress');}},
    observer:{dispose(){order.push('observer');},observe(){observations++;return Promise.resolve();}}};
   const client={session:{get(){},messages(){}}},input={client,directory:'/TEST-native-finalizer'};
   const hooks=await plugin.server(input);assert.equal(await AgentNotifications(input),hooks);
   assert.equal(typeof hooks.dispose,'function','native finalizer requires a dispose hook');
   if(ingress)hooks.event({event:{type:'server.instance.disposed'}});
   const first=hooks.dispose(),again=hooks.dispose();
   assert.equal(first,reaped);assert.equal(again,first,'repeat must await the same owned close');
   first.then(()=>settled=true);await Promise.resolve();assert.equal(settled,false);
   assert.deepEqual(order,['observer','view','delivery','owned']);assert.equal(reaps,1);
   hooks.event({event:{type:'message.updated',properties:{info:{role:'user'}}}});assert.equal(observations,0);
   release({reaped:true});assert.deepEqual(await first,{reaped:true});assert.equal(await again,await first);
  }
 }finally{delete globalThis[key];}
});
