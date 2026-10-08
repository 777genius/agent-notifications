import assert from 'node:assert/strict';
import { callbackJoin, derivedURI, prerequisiteAdmission, type Receiver } from './evidence.mts';
// Breakage: a candidate Accepted result is treated as proof despite absent/late
// actual operation drain, wrong retained generation/reference or foreign source.
// Fixed independent wire facts are not computed by the production codec.
const tuple = {source:'1'.repeat(40),nonce:'2'.repeat(32)};
const snapshot = ['a'.repeat(32),'unused','unused','unused','unused','unused','unused','unused','unused','OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0'];
const record = ['a'.repeat(32),'b'.repeat(32),'codex','TEST/a%雪','c'.repeat(64)];
const receiver: Receiver = {...tuple,snapshot_sha:'c'.repeat(64),reference:'b'.repeat(32),generation:'a'.repeat(32),
  selected_full_name:snapshot[9]!,collected:true,receiver_exit_code:0,receiver_pid:41,receiver_birth:'133700000000000000',
  click_boot_ms:1000,receiver_collected_boot_ms:61000};
const intent = `WinAttempt1 ${'d'.repeat(32)} 1002 31002 ${'b'.repeat(32)} ${'c'.repeat(64)}\n`;
const terminal = 'accepted effect_entered=1 target_confirmed=0 full_name_atomic=0\n';
const drained = 'actual_operation_completion=1 collected_boot_ms=1400 deadline_boot_ms=31002\n';
const worker = 'worker_returned=1 operations_completion_known=1\n';
const join = (r=receiver,drain=drained,names=new Set<string>(),expectedAttempt='d'.repeat(32)) => callbackJoin(tuple,r,snapshot,record,expectedAttempt,intent,terminal,drain,'query 31002\n','launch 31002\n',worker,names);
assert.equal(join().uri_derived,'codex://threads/TEST%2Fa%25%E9%9B%AA');
// Breakage: a different filename borrows an embedded attempt to bypass its own late marker.
assert.throws(() => join(receiver,drained,new Set(['e'.repeat(32)+'.late']),'e'.repeat(32)));
assert.throws(() => join(receiver,''));
assert.throws(() => join(receiver,'actual_operation_completion=1 collected_boot_ms=31002 deadline_boot_ms=31002\n'));
assert.throws(() => join(receiver,drained,new Set(['d'.repeat(32)+'.late'])));
assert.throws(() => join({...receiver,source:'3'.repeat(40)}));
assert.throws(() => join({...receiver,generation:'e'.repeat(32)}));
assert.throws(() => join({...receiver,collected:false}));
assert.throws(() => derivedURI('..'));

// Breakage: denied/unknown registry access is confused with a missing parent,
// or missing parents are accepted after the actual normal product setup.
const keys = ['Software\\Classes\\CLSID','Software\\Classes\\AppUserModelId'];
const census = {...tuple,sid:'S-1-5-21-1-2-3-500',session:2,integrity_rid:12288,
  native_machine:43620,process_machine:0,windows_build:26200,windows_product_type:1,windows11_client:true,
  [keys[0]!]: 'present',[keys[0]!+'_open_status']:0,[keys[1]!]: 'refused',[keys[1]!+'_open_status']:2};
const baseline = prerequisiteAdmission(census,tuple,'pre-setup');
const ready = {...census,[keys[1]!]: 'present',[keys[1]!+'_open_status']:0};
prerequisiteAdmission(ready,tuple,'pre-setup',baseline);
prerequisiteAdmission(ready,tuple,'post-setup',baseline);
assert.throws(() => prerequisiteAdmission(census,tuple,'post-setup',baseline));
for (const key of keys) {
  prerequisiteAdmission({...ready,[key]:'refused',[key+'_open_status']:2},tuple,'pre-setup');
  assert.throws(() => prerequisiteAdmission({...ready,[key]:'refused'},tuple,'pre-setup'));
  assert.throws(() => prerequisiteAdmission({...ready,[key]:undefined},tuple,'pre-setup'));
  for (const status of [5,87,0.5,undefined])
    assert.throws(() => prerequisiteAdmission({...census,[key+'_open_status']:status},tuple,'pre-setup'));
  assert.throws(() => prerequisiteAdmission({...ready,[key]:'refused'},tuple,'post-setup',baseline));
  assert.throws(() => prerequisiteAdmission({...census,[key]:'present',[key+'_open_status']:2},tuple,'pre-setup'));
}
for (const key of ['source','nonce','sid','session','integrity_rid','native_machine','process_machine','windows_build','windows_product_type','windows11_client'])
  assert.throws(() => prerequisiteAdmission({...ready,[key]: typeof ready[key as keyof typeof ready] === 'number' ? 99 : 'changed'},tuple,'post-setup',baseline));
console.log('TEST installed evidence joins passed; no native effects executed');
