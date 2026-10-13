// Read-only independent native facts. Never substitutes for installed AN callbacks/checkpoints.
import { appendFileSync, readFileSync, realpathSync } from 'node:fs';
import { resolve, relative, isAbsolute } from 'node:path';
import { createHmac } from 'node:crypto';
import { spawn } from 'node:child_process';
const root = realpathSync(process.env.AN_TEST_ROOT || '/missing');
const file = resolve(process.env.AN_TEST_TRACE || '/missing');
if (relative(root, file).startsWith('..') || isAbsolute(relative(root, file))) throw Error('TEST trace guard');
const marker = JSON.parse(readFileSync(resolve(root, '.owned-test-root.json')));
if (marker.purpose !== 'TEST installed AN dual native') throw Error('TEST marker');
const key = readFileSync(resolve(root, 'trace-key'));
// Keep summary/control classification readable; IDs, text, agent names and native
// lineage remain HMAC-correlated in the original envelopes, without new aliases.
const safe = new Set(['v1','v2','question','bash','shell','pending','answered','cancelled','running','idle','failed','completed','succeeded','interrupted','user','assistant','tool','text','reject','eligible','unverified','none','reaped_or_not_started','manual','compaction']);
let bytes = 0, count = 0, overflow = false;
function redact(v, name = '', depth = 0) {
  if (depth > 12) throw Error('capture depth');
  if (v === null || typeof v === 'number' || typeof v === 'boolean') return v;
  if (typeof v === 'string') {
    if (safe.has(v) || (name === 'type' && /^[a-z][a-z0-9_.-]{0,96}$/.test(v))) return v;
    return 'h:' + createHmac('sha256', key).update(v).digest('hex').slice(0,24);
  }
  if (Array.isArray(v)) { if (v.length > 96) throw Error('capture array'); return v.map(x => redact(x,name,depth+1)); }
  if (typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k,x]) => [k,redact(x,k,depth+1)]));
  return '[unsupported]';
}
function record(kind, value) {
  try {
    const line = JSON.stringify({kind,value:redact(value)}) + '\n';
    if (count++ >= 512 || (bytes += Buffer.byteLength(line)) > 1024*1024-128) throw Error('capture limit');
    appendFileSync(file,line,{mode:0o600});
  } catch {
    if (!overflow) { overflow=true; appendFileSync(file,JSON.stringify({kind:'overflow',value:{}})+'\n',{mode:0o600}); }
  }
}
async function profile(branch) {
  // This invokes the genuine read-only candidate command from the native parent.
  // Descriptor paths/origin are read from actual installation custody; no qualified fields.
  const owned = JSON.parse(readFileSync(resolve(root,'profile-descriptor.json')));
  const input = {protocol:1,hostExecutable:process.execPath,origin:owned.origin,
    controlRoot:owned.controlRoot,nativePID:process.pid,entry:'serve',publicExecPath:process.execPath};
  const env = Object.fromEntries(['HOME','USERPROFILE','XDG_CONFIG_HOME','APPDATA','SystemRoot','WINDIR','TEMP','TMP'].filter(k=>process.env[k]!==undefined).map(k=>[k,process.env[k]]));
  Object.assign(env,{AGENT_NOTIFICATIONS_NATIVE_PID:String(process.pid),AGENT_NOTIFICATIONS_HOST_EXECUTABLE:input.hostExecutable,
    AGENT_NOTIFICATIONS_ORIGIN:input.origin,AGENT_NOTIFICATIONS_CONTROL_ROOT:input.controlRoot,
    AGENT_NOTIFICATIONS_HOST_ENTRY:'serve',AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH:input.publicExecPath});
  await new Promise(resolveDone => {
    const child=spawn(owned.executable,['opencode-runtime-profile','--protocol','1'],{env,stdio:['pipe','pipe','pipe'],windowsHide:true});
    let out='',bad=false,forced=false;
    const timer=setTimeout(()=>{forced=true;child.kill();},10000);
    child.stdout.on('data',b=>{out+=b; if(out.length>1024){bad=true;child.kill();}});
    child.stderr.on('data',()=>{bad=true;});
    child.on('error',()=>{bad=true;});
    child.stdin.on('error',()=>{bad=true;});
    child.on('close',code=>{
      clearTimeout(timer);
      let receipt;try{receipt=JSON.parse(out);}catch{bad=true;}
      record('profile',{branch,nativePID:process.pid,code,bad,forced,actualClose:true,receipt:receipt||{}});
      resolveDone();
    });
    child.stdin.end(JSON.stringify(input));
  });
}
export default {
  id:'an-test-read-only-capture',
  async server(input) {
    record('loader',{branch:'v1',directory:input.directory});
    await profile('v1');
    return {event:async({event})=>record('native-v1',event)};
  },
  async setup(ctx) {
    record('loader',{branch:'v2',location:ctx.location});
    await profile('v2');
    const ctrl=new AbortController();
    const job=(async()=>{try{for await(const event of ctx.event.subscribe({signal:ctrl.signal}))record('native-v2',event);record('stream-end',{});}catch{record('stream-error',{});}})();
    return async()=>{ctrl.abort();await job;record('capture-close',{actualClose:true});};
  }
};
