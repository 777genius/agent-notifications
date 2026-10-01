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
