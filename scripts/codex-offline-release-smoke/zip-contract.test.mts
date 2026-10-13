import { strict as assert } from 'node:assert';
import { deflateRawSync } from 'node:zlib';
import { portableEntries,nativeEntries } from './zip-contract.mts';
type Entry={name:string;bytes?:Buffer;mode?:number};
function zip(entries:Entry[]):Buffer{
 const locals:Buffer[]=[],central:Buffer[]=[];let offset=0;
 for(const e of entries){const name=Buffer.from(e.name),bytes=e.bytes??Buffer.from('TEST'),compressed=deflateRawSync(bytes);
  const l=Buffer.alloc(30);l.writeUInt32LE(0x04034b50);l.writeUInt16LE(20,4);l.writeUInt16LE(8,8);l.writeUInt32LE(compressed.length,18);l.writeUInt32LE(bytes.length,22);l.writeUInt16LE(name.length,26);
  const c=Buffer.alloc(46);c.writeUInt32LE(0x02014b50);c.writeUInt16LE(3<<8|20,4);c.writeUInt16LE(20,6);c.writeUInt16LE(8,10);c.writeUInt32LE(compressed.length,20);c.writeUInt32LE(bytes.length,24);c.writeUInt16LE(name.length,28);c.writeUInt32LE(((e.mode??0x81a4)<<16)>>>0,38);c.writeUInt32LE(offset,42);
  locals.push(l,name,compressed);central.push(c,name);offset+=l.length+name.length+compressed.length;
 }
 const table=Buffer.concat(central),end=Buffer.alloc(22);end.writeUInt32LE(0x06054b50);end.writeUInt16LE(entries.length,8);end.writeUInt16LE(entries.length,10);end.writeUInt32LE(table.length,12);end.writeUInt32LE(offset,16);
 return Buffer.concat([...locals,table,end]);
}
const names=['THIRD_PARTY_NOTICES.txt','bin/claude-notifications','mcp.json','plugin.json','skills/agent-notifications/SKILL.md'];
const valid=names.map(name=>({name}));
assert.equal(portableEntries(zip(valid)).size,5);
assert.throws(()=>portableEntries(zip(valid.slice(1))));
assert.throws(()=>portableEntries(zip([...valid,valid[0]!])));
assert.throws(()=>portableEntries(zip([...valid,{name:'../escape'}])));
assert.throws(()=>portableEntries(zip(valid.map((e,i)=>i===1?{...e,mode:0xa1ff}:e))));
const inconsistent=zip(valid);inconsistent[30]=0x58;assert.throws(()=>portableEntries(inconsistent));
const native=['ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern','ClaudeNotifier.app/Contents/Info.plist','ClaudeNotifier.app/Contents/Resources/managed-runtime.json','ClaudeNotifier.app.managed-runtime.json'].map(name=>({name}));
assert.equal(nativeEntries(zip(native)).size,4);
assert.throws(()=>nativeEntries(zip([...native,{name:'ClaudeNotifier.app/../escape'}])));
assert.throws(()=>nativeEntries(zip([...native,{name:'ClaudeNotifier.app/Contents/link',mode:0xa1ff}])));
assert.throws(()=>nativeEntries(zip([...native,{name:'other.app/Contents/file'}])));
assert.throws(()=>nativeEntries(zip(native.slice(1))));
console.log(JSON.stringify({portable_five_leaves:true,missing_duplicate_traversal_symlink_and_mismatch_rejected:true,native_required_leaves:true,native_escape_symlink_foreign_and_missing_rejected:true,runtimeExecution:false}));
