import { strict as assert } from 'node:assert';
import { inflateRawSync } from 'node:zlib';
const leaves=new Set(['THIRD_PARTY_NOTICES.txt','bin/claude-notifications','mcp.json','plugin.json','skills/agent-notifications/SKILL.md']);
const directories=new Set(['bin/','skills/','skills/agent-notifications/']);
function entries(zip:Buffer,native:boolean):Map<string,Buffer>{
 assert(zip.length>=22&&zip.length<=96<<20,'bounded_ZIP_input');
 let end=-1;
 for(let i=zip.length-22;i>=Math.max(0,zip.length-65557);i--)if(zip.readUInt32LE(i)===0x06054b50){end=i;break;}
 assert(end>=0&&end+22+zip.readUInt16LE(end+20)===zip.length,'ZIP_EOCD_required');
 assert(zip.readUInt16LE(end+4)===0&&zip.readUInt16LE(end+6)===0,'single_disk_ZIP_required');
 const count=zip.readUInt16LE(end+10),offset=zip.readUInt32LE(end+16),size=zip.readUInt32LE(end+12);
 assert(count===zip.readUInt16LE(end+8)&&count>0&&count<=(native?128:8)&&offset+size===end,'finite_central_directory');
 const seen=new Set<string>(),output=new Map<string,Buffer>();let cursor=offset,total=0;
 for(let i=0;i<count;i++){
  assert(cursor+46<=end&&zip.readUInt32LE(cursor)===0x02014b50,'central_header');
  const flags=zip.readUInt16LE(cursor+8),method=zip.readUInt16LE(cursor+10),compressed=zip.readUInt32LE(cursor+20),plain=zip.readUInt32LE(cursor+24);
  const names=zip.readUInt16LE(cursor+28),extra=zip.readUInt16LE(cursor+30),comment=zip.readUInt16LE(cursor+32),local=zip.readUInt32LE(cursor+42);
  const unixMode=zip.readUInt32LE(cursor+38)>>>16;
  assert((flags&1)===0&&(method===0||method===8)&&plain<=80<<20,'ordinary_bounded_ZIP_entry');
  assert(cursor+46+names+extra+comment<=end,'bounded_central_entry');
  const name=zip.subarray(cursor+46,cursor+46+names).toString('utf8');
  const directory=name.endsWith('/');
  assert(!seen.has(name),'duplicate_ZIP_entry');seen.add(name);
  if(native){
   assert((name.startsWith('ClaudeNotifier.app/')||name==='ClaudeNotifier.app.managed-runtime.json')&&!name.includes('\\')&&!name.includes('\0'),'native_inventory');
   const parts=name.replace(/\/$/,'').split('/');assert(parts.every(part=>part&&part!=='.'&&part!=='..'),'native_path_containment');
  }else assert(leaves.has(name)||directories.has(name),'exact_portable_leaf_inventory');
  assert((unixMode&0xf000)===0||(unixMode&0xf000)===(directory?0x4000:0x8000),'regular_ZIP_entry_no_symlinks');
  assert(local+30<=offset&&zip.readUInt32LE(local)===0x04034b50,'local_header');
  const ln=zip.readUInt16LE(local+26),le=zip.readUInt16LE(local+28),data=local+30+ln+le;
  assert(zip.subarray(local+30,local+30+ln).toString('utf8')===name&&zip.readUInt16LE(local+8)===method&&zip.readUInt16LE(local+6)===flags,'local_central_identity');
  assert(data+compressed<=offset,'local_data_bounds');
  const packed=zip.subarray(data,data+compressed),bytes=method===0?packed:inflateRawSync(packed,{maxOutputLength:plain+1});
  assert(bytes.length===plain,'uncompressed_size_exact');total+=plain;assert(total<=96<<20,'archive_total_bound');
  if(directory)assert(plain===0,'directory_must_be_empty');else output.set(name,bytes);
  cursor+=46+names+extra+comment;
 }
 assert(cursor===end,'complete_central_directory');
 if(native)for(const name of ['ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern','ClaudeNotifier.app/Contents/Info.plist','ClaudeNotifier.app/Contents/Resources/managed-runtime.json','ClaudeNotifier.app.managed-runtime.json'])assert(output.has(name),'native_required_leaf_'+name);
 else assert(output.size===5&&[...leaves].every(name=>output.has(name)),'exact_five_regular_portable_leaves');
 return output;
}
export const portableEntries=(zip:Buffer)=>entries(zip,false);
export const nativeEntries=(zip:Buffer)=>entries(zip,true);
