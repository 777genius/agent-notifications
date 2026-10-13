import { strict as assert } from 'node:assert';
export type Inputs = {
  candidateSHA:string; operatorSHA:string; version:string; testThread:string;
  signingRun:string;signingAttempt:string;nativeCustodyJSON:string;nativeCustodySHA256:string; draftBinarySHA256:string; portableZipSHA256:string;
  nativeZipSHA256:string; signingRunJSONSHA256:string;
  root:string; source:string; portableZip:string; draftBinary:string; nativeZip:string;
  signingRunJSON:string; codex:string; modelPort:number; webhookPort:number;
};

export function validateInputs(value:unknown):Inputs {
assert(value&&typeof value==='object'&&!Array.isArray(value),'inputs_object_required');
const d=value as Inputs;
const keys=['candidateSHA','operatorSHA','version','testThread','signingRun','signingAttempt','nativeCustodyJSON','nativeCustodySHA256','draftBinarySHA256','portableZipSHA256','nativeZipSHA256','signingRunJSONSHA256','root','source','portableZip','draftBinary','nativeZip','signingRunJSON','codex','modelPort','webhookPort'];
assert(Object.keys(d).length===keys.length&&Object.keys(d).every(k=>keys.includes(k)),'exact_input_keys');
assert(typeof d.version==='string'&&/^[0-9]+\.[0-9]+\.[0-9]+$/.test(d.version)&&/^[a-f0-9]{40}$/.test(d.candidateSHA)&&/^[a-f0-9]{40}$/.test(d.operatorSHA),'exact_final_source');
assert(/^[1-9][0-9]*$/.test(d.signingAttempt),'signing_attempt');
assert(/^[1-9][0-9]*$/.test(d.signingRun),'signing_run_ID');
assert(/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(d.testThread),'TEST_thread_UUID');
assert([d.draftBinarySHA256,d.portableZipSHA256,d.nativeZipSHA256,d.signingRunJSONSHA256,d.nativeCustodySHA256].every(v=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v)),'all_exact_artifact_hashes');
assert(/^\/private\/tmp\/TEST-an-codex-[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(d.root),'strict_fresh_TEST_namespace');
assert([d.source,d.portableZip,d.draftBinary,d.nativeZip,d.signingRunJSON,d.nativeCustodyJSON,d.codex].every(v=>typeof v==='string'&&v.startsWith('/')&&!v.includes('\0')),'absolute_inputs');
assert(d.modelPort!==d.webhookPort&&[d.modelPort,d.webhookPort].every(v=>Number.isInteger(v)&&v>1024&&v<65536),'two_loopback_ports');
return d;
}
