// Private release operator: explicit phases only; never logs in or dispatches a workflow.
import { strict as assert } from 'node:assert';
import { readFile, realpath, lstat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { spawn } from 'node:child_process';

import { validateInputs } from './inputs.mts';

const [requestedPhase,inputPath,...extra]=process.argv.slice(2);
if(requestedPhase==='--help'||requestedPhase==='help'){
  console.log('Usage: node run-phase.mts plan|prepare|server|trust|turn|verify INPUTS.json\nprepare: private TEST staging and preflight; server: loopback fixture; trust: real TTY /hooks; turn: exactly once; verify: after server closure. No automatic turn retry.');
  process.exit(0);
}
assert(requestedPhase&&['plan','prepare','server','trust','tui','turn','verify'].includes(requestedPhase)&&inputPath&&!extra.length,
  'usage: node run-phase.mts plan|prepare|server|trust|turn|verify INPUTS.json');
const phase=requestedPhase==='trust'?'tui':requestedPhase;
const file=await realpath(inputPath),fileStat=await lstat(file);
assert(fileStat.isFile()&&fileStat.size<16384,'bounded_regular_inputs_JSON');
assert.equal(fileStat.mode&0o777,0o600,'private_inputs_JSON_required');
const d=validateInputs(JSON.parse(await readFile(file,'utf8')));
const custody=['--candidate-sha',d.candidateSHA,'--operator-sha',d.operatorSHA,'--version',d.version,
  '--test-thread',d.testThread,'--native-custody-json',d.nativeCustodyJSON,'--native-custody-sha256',d.nativeCustodySHA256,'--signing-attempt',d.signingAttempt,'--signing-run',d.signingRun,'--asset-sha256',d.draftBinarySHA256,
  '--binary-sha256',d.draftBinarySHA256,'--portable-zip-sha256',d.portableZipSHA256,
  '--native-zip-sha256',d.nativeZipSHA256,'--native-zip',d.nativeZip,'--run-json-sha256',d.signingRunJSONSHA256];
const observer=join(dirname(fileURLToPath(import.meta.url)),'observer.mts');
const args=[observer,phase,d.root,...(phase==='prepare'?[d.source,d.portableZip,d.draftBinarySHA256,String(d.modelPort),String(d.webhookPort),d.codex,d.draftBinary,d.signingRunJSON]:[]),...custody];
if(phase==='plan'){
 console.log(JSON.stringify({scope:'offline actual Codex CLI Stop/webhook only',version:d.version,candidate:d.candidateSHA,
   phaseOrder:['prepare','server (owned process, wait ready)','trust (genuine TTY /hooks review, persist trust, exit)','turn exactly once','SIGTERM exact server PID + wait','verify'],
   liveProvider:false,fixtureModel:true,realAccounts:false,desktop:false,argvForPrepare:[process.execPath,...args.slice(0,1),'prepare',...args.slice(2,3),d.source,d.portableZip,d.draftBinarySHA256,String(d.modelPort),String(d.webhookPort),d.codex,d.draftBinary,d.signingRunJSON,...custody]},null,2));
}else{
 assert(Number(process.versions.node.split('.')[0])>=24,'Node_24_or_newer_required');
 const child=spawn(process.execPath,args,{cwd:'/private/tmp',env:{PATH:'/usr/bin:/bin:/usr/sbin:/sbin',TERM:'xterm-256color',LANG:'en_US.UTF-8'},stdio:'inherit',detached:phase!=='tui'});
 let timedOut=false; const signal=(s:NodeJS.Signals)=>{if(child.pid){try{process.kill(phase==='tui'?child.pid:-child.pid,s);}catch{}}};
 const timer=setTimeout(()=>{timedOut=true;signal('SIGTERM');setTimeout(()=>signal('SIGKILL'),2000).unref();},phase==='server'?600_000:300_000);
 for(const sig of ['SIGINT','SIGTERM'] as const)process.once(sig,()=>signal(sig));
 const code=await new Promise<number|null>((resolve,reject)=>{child.once('error',reject);child.once('close',resolve);});
 clearTimeout(timer);assert(!timedOut,'operator_phase_timeout_no_automatic_retry');
 process.exitCode=code??1;
}
