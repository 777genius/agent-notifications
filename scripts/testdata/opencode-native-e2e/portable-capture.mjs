// Read-only independent native facts. Never substitutes for installed AN callbacks/checkpoints.
import { appendFileSync, readFileSync, realpathSync } from 'node:fs';
import { resolve, relative, isAbsolute } from 'node:path';
import { createHmac } from 'node:crypto';
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
export default {
  id:'an-test-read-only-capture',
  async server(input) {
    record('loader',{branch:'v1',directory:input.directory});
    return {event:async({event})=>record('native-v1',event)};
  },
  async setup(ctx) {
    record('loader',{branch:'v2',location:ctx.location});
    const ctrl=new AbortController();
    const job=(async()=>{try{for await(const event of ctx.event.subscribe({signal:ctrl.signal}))record('native-v2',event);record('stream-end',{});}catch{record('stream-error',{});}})();
    return async()=>{ctrl.abort();await job;record('capture-close',{actualClose:true});};
  }
};
