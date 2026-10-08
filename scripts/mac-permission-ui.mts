/** Recovery-only genuine OS consent. No TCC writes, notification send, or qualification grants. */
import assert from 'node:assert/strict';
import {createHash,randomUUID} from 'node:crypto';
import {execFile,spawn} from 'node:child_process';
import {chmodSync,existsSync,mkdirSync,readFileSync,realpathSync,writeFileSync,lstatSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {EventEmitter} from 'node:events';
import {PassThrough} from 'node:stream';
import {runInNewContext} from 'node:vm';
const C='6a67f084eca6e424a8e72602463f34814c451210',O='f8231b21751744806a7e333903ad7029cf307711',parentSHA='869507bfba78e1bed5f1b749f7efb5b3638caef7e4a84906451d4512592ad0ec';
const bundle='com.777genius.agent-notifications',display='Agent Notifications';
const systemBundles=['com.apple.UserNotificationCenter','com.apple.notificationcenterui'];
interface Button {name:string;path:number[];enabled:boolean;}
interface Window {hostBundle:string;index:number;role:string;texts:string[];buttons:Button[];}
interface Snapshot {uiEnabled:boolean;requesterPID:number;requesterBundle:string;windows:Window[];}
function normalized(value:string):string{return value.replace(/[“”]/g,'"').replace(/’/g,"'").replace(/\s+/g,' ').trim();}
function candidates(snapshot:Snapshot):Window[]{
 assert.equal(snapshot.uiEnabled,true,'actual UI accessibility required');assert(Array.isArray(snapshot.windows)&&snapshot.windows.length<=32);
 return snapshot.windows.filter(w=>systemBundles.includes(w.hostBundle)&&['AXWindow','AXDialog','AXSheet'].includes(w.role)&&w.texts.some(t=>normalized(t).replace(/"/g,'').toLowerCase()===display.toLowerCase()+' would like to send you notifications'));
}
function select(snapshot:Snapshot,pid:number):{window:Window;button:Button}{
 assert(Number.isSafeInteger(pid)&&pid>1&&snapshot.requesterPID===pid&&snapshot.requesterBundle===bundle,'owned pending bundle request required');
 const found=candidates(snapshot);assert.equal(found.length,1,'one exact notification authorization dialog required');const w=found[0]!;
 assert.equal(w.buttons.length,2);assert.deepEqual(w.buttons.map(b=>normalized(b.name)).sort(),["Don't Allow",'Allow'].sort());
 const allow=w.buttons.find(b=>normalized(b.name)==='Allow')!;assert(allow.enabled&&allow.path.length>0&&allow.path.length<=8&&allow.path.every(i=>Number.isSafeInteger(i)&&i>=0));
 return {window:w,button:allow};
}
function requireClickBudget(remainingMS:number,requiredMS:number):void{assert(Number.isFinite(remainingMS)&&remainingMS>requiredMS,'owned request click-budget margin required');}
function safeUI(snapshot:Snapshot):unknown{return {uiEnabled:snapshot.uiEnabled,windowCount:snapshot.windows.length,titleExactCount:candidates(snapshot).length,requesterBundleMatches:snapshot.requesterBundle===bundle,windows:snapshot.windows.map(w=>({hostBundle:w.hostBundle,role:['AXWindow','AXDialog','AXSheet'].includes(w.role)?w.role:'other',brandPresent:w.texts.some(t=>t.toLowerCase().includes(display.toLowerCase())),titleExact:w.texts.some(t=>normalized(t).replace(/"/g,'').toLowerCase()===display.toLowerCase()+' would like to send you notifications'),buttonCount:w.buttons.length,allowCount:w.buttons.filter(b=>normalized(b.name)==='Allow').length,denyCount:w.buttons.filter(b=>normalized(b.name)==="Don't Allow").length}))};}
const hash=(p:string)=>createHash('sha256').update(readFileSync(p)).digest('hex');
function envelope(text:string,correlation:string,nonce:string):string{
 assert(Buffer.byteLength(text)<=4096&&text.endsWith('\n')&&text.trim().split('\n').length===1);const row=JSON.parse(text) as Record<string,unknown>;
 assert.deepEqual(Object.keys(row).sort(),['schemaVersion','correlationID','nonce','backend','permission'].sort());assert(row.schemaVersion===1&&row.correlationID===correlation&&row.nonce===nonce&&row.backend==='macos.usernotifications');
 assert(['allowed','undetermined','denied','unavailable'].includes(String(row.permission)));return String(row.permission);
}
// macOS supplies these two trusted command spellings through system symlinks.
const systemAliases:Readonly<Record<string,string>>={
 '/usr/bin/tar':'/usr/bin/bsdtar',
 '/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister':'/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister',
};
function canonicalExecutable(requested:string,physical:(path:string)=>string=realpathSync):string{
 assert(requested.startsWith('/'),'absolute owned executable required');const resolved=physical(requested);
 assert(resolved===requested||systemAliases[requested]===resolved,'unrecognized executable alias');return resolved;
}
interface AppProcess {pid:number;ppid:number;birth:string;args:string;}
function parseProcesses(text:string):AppProcess[]{return text.trim().split('\n').filter(Boolean).map(line=>{const m=/^\s*([0-9]+)\s+([0-9]+)\s+(\S+\s+\S+\s+[0-9]+\s+[0-9:]+\s+[0-9]{4})\s+(.*)$/.exec(line);assert(m,'process census schema');return {pid:Number(m[1]),ppid:Number(m[2]),birth:m[3]!.replace(/\s+/g,' '),args:m[4]!};});}
function sameProcess(a:AppProcess,b:AppProcess):boolean{return a.pid===b.pid&&a.birth===b.birth&&a.args===b.args;}
function ownApp(rows:AppProcess[],argv:string):AppProcess|undefined{const matches=rows.filter(row=>row.args===argv);assert(matches.length<=1,'ambiguous owned LS request');return matches[0];}
function descendants(rows:AppProcess[],pid:number):AppProcess[]{const found:AppProcess[]=[];let parents=[pid];while(parents.length){const next=rows.filter(row=>parents.includes(row.ppid)&&!found.some(p=>p.pid===row.pid));found.push(...next);assert(found.length<32,'LS descendant bound');parents=next.map(row=>row.pid);}return found;}
interface ProbeContext {root:string;env:NodeJS.ProcessEnv;execute:typeof execFile;record:(pid:number|undefined,actualClose:boolean,failed:boolean)=>void;}
async function processProbe(c:ProbeContext,remainingMS:number):Promise<AppProcess[]>{return await new Promise((yes,no)=>{
 let callbackDone=false,closed=false,error:unknown,text='',settled=false;
 const settle=()=>{if(!callbackDone||!closed||settled)return;settled=true;clearTimeout(fence);c.record(child.pid,true,Boolean(error));if(error)return no(error);try{yes(parseProcesses(text));}catch(err){no(err);}};
 const child=c.execute('/bin/ps',['-ww','-axo','pid=,ppid=,lstart=,args='],{cwd:c.root,env:c.env,encoding:'utf8',timeout:Math.max(1,Math.min(500,remainingMS)),killSignal:'SIGKILL',maxBuffer:1024*1024},(err,value)=>{callbackDone=true;error=err;text=value;settle();});
 const fence=setTimeout(()=>{if(settled)return;settled=true;child.kill('SIGKILL');child.stdout?.destroy();child.stderr?.destroy();c.record(child.pid,closed,true);no(Error('process probe actual close deadline'));},Math.max(1,Math.min(1000,remainingMS)));
 child.once('close',()=>{closed=true;settle();});
});}
interface LSCleanup {state:{lease?:AppProcess;argv?:string;unexpectedDescendants:boolean};report:Record<string,unknown>;rows:(remainingMS:number)=>Promise<AppProcess[]>;verify:()=>void;signal:(pid:number,sig:'SIGTERM'|'SIGKILL')=>void;pause:(ms:number)=>Promise<void>;now:()=>number;}
async function closeLSApp(c:LSCleanup):Promise<void>{
 if(!c.state.argv)return;const start=c.now();let term=false,kill=false;
 while(true){const remaining=3000-(c.now()-start);assert(remaining>0,'LS app closure deadline');const rows=await c.rows(remaining),found=ownApp(rows,c.state.argv);assert(c.now()-start<3000,'LS absence observation deadline');
  if(!found){assert(c.state.lease,'never observed LS launch cannot grant absence');c.report.LSAppAbsent=true;assert(!c.state.unexpectedDescendants,'LS descendants unproved');return;}
  c.verify();if(c.state.lease)assert(sameProcess(c.state.lease,found),'LS app incarnation changed');else c.state.lease=found;
  if(descendants(rows,found.pid).length){c.state.unexpectedDescendants=true;c.report.LSDescendantClosureProved=false;}
  const sig=c.now()-start>=1000?'SIGKILL':'SIGTERM';if(sig==='SIGTERM'?!term:!kill){try{c.signal(found.pid,sig);}catch(err){if((err as NodeJS.ErrnoException).code!=='ESRCH')throw err;}if(sig==='SIGTERM')term=true;else kill=true;}await c.pause(20);
 }
}
interface OutputSink {feed:(chunk:Buffer)=>void;finish:()=>void;}
function registrationAbsence(app:string,record:Record<string,unknown>,limit=64*1024*1024):OutputSink{
 let carry=Buffer.alloc(0),bytes=0,lines=0,matched=false;const digest=createHash('sha256');
 return {feed(chunk){bytes+=chunk.length;assert(bytes<=limit,'LS total output bound');digest.update(chunk);carry=Buffer.concat([carry,chunk]);
  let index:number;while((index=carry.indexOf(10))>=0){assert(index<=64*1024,'LS line bound');const line=carry.subarray(0,index).toString('utf8');carry=carry.subarray(index+1);lines++;
   if(/^\s*path:\s*/.test(line)&&line.replace(/^\s*path:\s*/,'').trim().replace(/ \(0x[0-9a-f]+\)$/,'')===app)matched=true;
  }assert(carry.length<=64*1024,'LS carry bound');
 },finish(){assert(bytes>0&&lines>0&&carry.length===0,'LS complete EOF lines required');Object.assign(record,{bytes,lines,streamSHA256:digest.digest('hex'),ownedPathPresent:matched,actualEOF:true});assert(!matched,'owned TEST registration still present');}};
}
interface OwnedContext {root:string;env:NodeJS.ProcessEnv;report:Record<string,unknown>;census:()=>Promise<{pid:number;pgid:number}[]>;pauses:(ms:number)=>Promise<void>;spawnChild:typeof spawn;}
function createOwned(context:OwnedContext,executable:string,args:string[],budget:number,sink?:OutputSink){
 const {root,env,report,census,pauses,spawnChild}=context;
  const requestedExecutable=executable;executable=canonicalExecutable(executable);const deadline=performance.now()+budget,p=spawnChild(executable,args,{cwd:root,env,detached:true,stdio:['ignore','pipe','pipe']});let stdout='',bytes=0,closed=false,code:number|null=null,error:unknown,finishing=false,notifyClose:()=>void=()=>{};
  const closeEvent=new Promise<void>(done=>{notifyClose=done;});let complete:(value:string)=>void=()=>{},reject:(error:unknown)=>void=()=>{};
  const done=new Promise<string>((yes,no)=>{complete=yes;reject=no;});done.catch(()=>{});
  const finish=()=>{if(finishing)return;finishing=true;clearTimeout(timer);const start=performance.now();void(async()=>{
   let groupAbsent=false;try{assert(p.pid&&p.pid>1&&p.pid!==process.pid);const control=(await census()).find(row=>row.pid===process.pid);assert(control&&control.pgid!==p.pid);
    let term=false,kill=false;while(true){assert(performance.now()-start<3000);const members=(await census()).filter(row=>row.pgid===p.pid);assert(!members.some(row=>row.pid===process.pid));if(!members.length){groupAbsent=true;break;}
     const signal=performance.now()-start>=1000?'SIGKILL':'SIGTERM';if(signal==='SIGTERM'?!term:!kill){try{process.kill(-p.pid,signal);}catch(err){if((err as NodeJS.ErrnoException).code!=='ESRCH')throw err;}if(signal==='SIGTERM')term=true;else kill=true;}await pauses(20);
    }
    if(!closed)await new Promise<void>((yes,no)=>{const timer=setTimeout(()=>no(Error('owned EOF deadline')),Math.max(0,3000-(performance.now()-start)));void closeEvent.then(()=>{clearTimeout(timer);yes();});});
    assert(closed&&performance.now()-start<3000&&!error&&code===0,'actual owned close required');sink?.finish();complete(stdout);
   }catch(err){p.stdout.destroy();p.stderr.destroy();reject(err);}finally{(report.children as unknown[]).push({pid:p.pid,requestedExecutable,executable,groupAbsent,actualClose:closed,elapsedMS:performance.now()-start});}
  })();};
  const timer=setTimeout(()=>{error=Error('owned operation deadline');finish();},budget);
  p.stdout.on('data',b=>{try{if(sink)sink.feed(b);else{stdout+=b.toString();bytes+=b.length;assert(bytes<=1024*1024,'owned output bound');}}catch(err){error=err;finish();}});p.stderr.on('data',b=>{bytes+=b.length;if(bytes>1024*1024){error=Error('owned output bound');finish();}});
  p.once('error',err=>{error=err;finish();});p.once('exit',()=>finish());p.once('close',value=>{code=value;closed=true;notifyClose();finish();});
  return {p,done,remainingMS:()=>Math.max(0,deadline-performance.now()),pending:()=>p.exitCode===null&&p.signalCode===null&&!finishing,stop:()=>{error=Error('owned operation cancelled');finish();return done.catch(()=>{});}};
 }

async function main():Promise<void>{
 assert.equal(process.argv.length,2,'closed recovery CLI');assert(process.platform==='darwin'&&process.arch==='x64','native Intel controller only');
 const e=process.env;assert(e.GITHUB_ACTIONS==='true'&&e.GITHUB_REPOSITORY==='777genius/agent-notifications'&&e.GITHUB_ACTOR==='777genius'&&e.GITHUB_TRIGGERING_ACTOR==='777genius');
 assert(e.GITHUB_REF==='refs/heads/chore/unified-release-macos-recovery'&&e.RELEASE_CANDIDATE_SHA===C&&e.OPERATOR_SHA===O&&e.AN_EVIDENCE_SHA256===parentSHA&&e.RELEASE_SIGNING_RUN==='37762717929'&&e.ORIGINAL_SEAL_RUN==='37773996005');
 assert(e.AN_OS==='darwin'&&e.AN_ARCH==='amd64'&&['1.18.33','2.0.21'].includes(e.AN_VERSION??''));
 const source=realpathSync(e.CANDIDATE_ROOT!),recovery=realpathSync(e.RECOVERY_ROOT!);assert.equal(realpathSync(fileURLToPath(import.meta.url)),join(recovery,'scripts/mac-permission-ui.mts'));const root=join(source,'.task-tools/artifacts/TEST-mac-permission-'+randomUUID());mkdirSync(root,{mode:0o700});assert.equal(realpathSync(root),root);
 const report:Record<string,unknown>={scope:'actual_signed_helper_OS_consent_only',candidate:C,operatorSHA:O,recoverySHA:e.GITHUB_SHA,originalSealRun:'37773996005',parentSHA256:parentSHA,signingRun:'37762717929',version:e.AN_VERSION,helperSourceSHA256:hash(fileURLToPath(import.meta.url)),root,qualificationGranted:false,fullNativeQualified:false,coldStartQualified:false,notificationSent:false,uiClickCount:0,children:[]};
 const env:NodeJS.ProcessEnv={PATH:'/usr/bin:/bin:/usr/sbin:/sbin',LANG:'en_US.UTF-8'};
 for(const key of ['HOME','USERPROFILE','CODEX_HOME','CLAUDE_CONFIG_DIR','XDG_CONFIG_HOME','XDG_DATA_HOME','XDG_CACHE_HOME','XDG_STATE_HOME','XDG_RUNTIME_DIR','TMPDIR','TMP','TEMP']){env[key]=join(root,key.toLowerCase());mkdirSync(env[key]!,{mode:0o700});}
 const pauses=(ms:number)=>new Promise<void>(done=>setTimeout(done,ms));
 async function census():Promise<{pid:number;pgid:number}[]>{return await new Promise((yes,no)=>execFile('/bin/ps',['-axo','pid=,pgid='],{env,timeout:500,maxBuffer:1024*1024},(err,text)=>{if(err)return no(err);try{yes(text.trim().split('\n').map(line=>{const [pid,pgid]=line.trim().split(/\s+/).map(Number);assert(Number.isSafeInteger(pid)&&Number.isSafeInteger(pgid));return {pid:pid!,pgid:pgid!};}));}catch(error){no(error);}}));}
 const owned=(executable:string,args:string[],budget:number,sink?:OutputSink)=>createOwned({root,env,report,census,pauses,spawnChild:spawn},executable,args,budget,sink);
 const run=async(executable:string,args:string[],budget=10000)=>await owned(executable,args,budget).done;
 const ls='/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister';
 const app=join(root,'native/ClaudeNotifier.app'),exe=join(app,'Contents/MacOS/terminal-notifier-modern');let registrationAttempted=false,request:ReturnType<typeof owned>|undefined,failure:unknown,appLease:AppProcess|undefined,requestArgv:string|undefined,unexpectedDescendants=false;
 const processRows=async(remainingMS=1000)=>await processProbe({root,env,execute:execFile,record:(pid,actualClose,failed)=>(report.children as unknown[]).push({pid,executable:'/bin/ps',processIdentityProbe:true,actualClose,failed})},remainingMS);
 const callback=join(root,'request-stdout.jsonl'),callbackError=join(root,'request-stderr.log');
 const closeApp=async()=>await closeLSApp({state:{lease:appLease,argv:requestArgv,unexpectedDescendants},report,rows:processRows,verify:()=>assert(hash(exe)===report.executableSHA256&&realpathSync(exe)===exe),signal:(pid,sig)=>process.kill(pid,sig),pause:pauses,now:()=>performance.now()});
 try{
  assert.equal((await run('/usr/bin/uname',['-m'])).trim(),'x86_64');report.cpuBrand=(await run('/usr/sbin/sysctl',['-n','machdep.cpu.brand_string'])).trim();assert(String(report.cpuBrand).includes('Intel'));
  const archive=join(source,'.task-tools/artifacts/custody/release-opencode-inputs.tar.gz');assert.equal(hash(archive),parentSHA);
  const members=(await run('/usr/bin/tar',['-tzf',archive],30000)).trim().split('\n');assert(new Set(members).size===members.length&&members.every(p=>!p.startsWith('/')&&!p.split('/').includes('..')));
  const selected=members.filter(p=>p.startsWith('native/ClaudeNotifier.app/')||['native/ClaudeNotifier.app.managed-runtime.json','manifest.json','release-inputs-receipt.json'].includes(p));assert(selected.length>4&&selected.length<128);
  await run('/usr/bin/tar',['-xzf',archive,'-C',root,...selected],30000);assert.equal(realpathSync(app),app);assert.equal(realpathSync(exe),exe);
  const receipt=JSON.parse(readFileSync(join(root,'release-inputs-receipt.json'),'utf8')) as {candidateCommit:string;runId:string;operatorSHA:string;signingCustody:{bundleID:string;signingTeam:string;sourceSHA:string;executableSHA256:string;attestationSHA256:string}};
  assert(receipt.candidateCommit===C&&receipt.runId==='37762717929'&&receipt.operatorSHA===O&&receipt.signingCustody.sourceSHA===C&&receipt.signingCustody.bundleID===bundle&&receipt.signingCustody.signingTeam==='86399583GS');
  assert.equal(hash(exe),receipt.signingCustody.executableSHA256);assert.equal(hash(app+'.managed-runtime.json'),receipt.signingCustody.attestationSHA256);chmodSync(exe,0o700);report.executableSHA256=hash(exe);
  await run('/bin/bash',[join(source,'swift-notifier/scripts/verify-signing.sh'),app,'--notarized'],30000);
  const plist=join(app,'Contents/Info.plist');for(const [key,expected] of [['CFBundleIdentifier',bundle],['CFBundleDisplayName',display],['CFBundleName',display],['CFBundlePackageType','APPL']])assert.equal((await run('/usr/libexec/PlistBuddy',['-c','Print :'+key,plist])).trim(),expected);
  const uiPath=join(root,'permission-ui.jxa');writeFileSync(uiPath,jxa,{mode:0o600,flag:'wx'});
  const ui=async(mode:string,pid=0,choice:unknown=null):Promise<Snapshot>=>JSON.parse(await run('/usr/bin/osascript',['-l','JavaScript',uiPath,mode,String(pid),bundle,JSON.stringify(choice)],4000)) as Snapshot;
  const before=await ui('probe');report.lastUIObservation=safeUI(before);assert.equal(candidates(before).length,0,'no preexisting matching alert');report.actualUICapability=true;
  registrationAttempted=true;await run(ls,['-f',app]);
  const caps=JSON.parse(await run(exe,['--capabilities-json'],2000));assert.deepEqual(caps,{schemaVersion:1,protocolVersions:[1],actionKinds:['none','desktop_thread_v1'],receiptSupport:true,backend:'macos.usernotifications',explicitFeatureEnabledByDefault:false});
  assert.deepEqual(JSON.parse(await run(exe,['--capabilities-json','--setup'],2000)),{schemaVersion:1,permissionRequestVersions:[1],backend:'macos.usernotifications'});
  const query=async()=>{const id=randomUUID(),nonce=randomUUID();return envelope(await run(exe,['--capabilities-json','--permission-status','--correlation-id',id,'--nonce',nonce],2000),id,nonce);};
  const initial=await query();report.initialPermission=initial;report.consoleUID=lstatSync('/dev/console').uid;report.controllerUID=process.getuid?.();assert(initial==='allowed'||initial==='undetermined','denied/unavailable fail closed');
  if(initial==='undetermined'){
   const id=randomUUID(),nonce=randomUUID(),requestArgs=['--request-permission-json','--correlation-id',id,'--nonce',nonce,'-launchedViaLaunchServices'];requestArgv=[exe,...requestArgs].join(' ');
   assert(!ownApp(await processRows(),requestArgv),'no preexisting UUID request');writeFileSync(callback,'',{mode:0o600,flag:'wx'});writeFileSync(callbackError,'',{mode:0o600,flag:'wx'});
   request=owned('/usr/bin/open',['-n','-W','--stdout',callback,'--stderr',callbackError,'-a',app,'--args',...requestArgs],28000);const until=performance.now()+20000;let clicked=false;
   while(request.pending()&&performance.now()<until){
    const rows=await processRows(),found=ownApp(rows,requestArgv);if(!found){await pauses(50);continue;}if(appLease)assert(sameProcess(appLease,found));else appLease=found;
    if(descendants(rows,found.pid).length){unexpectedDescendants=true;report.LSDescendantClosureProved=false;throw Error('unexpected LS request descendant');}report.LSDescendantClosureProved=true;
    const identity=JSON.parse(await run('/usr/bin/osascript',['-l','JavaScript',uiPath,'identify',String(found.pid),bundle,JSON.stringify({app,exe})],2000)) as {pid:number;launchMS:number;bundle:string;app:string;exe:string};
    assert(identity.pid===found.pid&&identity.bundle===bundle&&identity.app===app&&identity.exe===exe&&Number.isFinite(identity.launchMS)&&identity.launchMS>0);report.LSAppIdentity={...identity,birth:found.birth,argvSHA256:createHash('sha256').update(requestArgv).digest('hex')};
    for(const file of [callback,callbackError])assert(!lstatSync(file).isSymbolicLink()&&lstatSync(file).size<=4096,'bounded owned callback files');
    const snapshot=await ui('scan',found.pid,{app,exe,launchMS:identity.launchMS});report.lastUIObservation=safeUI(snapshot);const matches=candidates(snapshot);assert(matches.length<=1,'ambiguous exact notification alerts');if(matches.length){const choice=select(snapshot,found.pid);assert(request.pending()&&hash(exe)===receipt.signingCustody.executableSHA256);
     requireClickBudget(request.remainingMS(),6000);const latest=ownApp(await processRows(),requestArgv);assert(latest&&sameProcess(found,latest)&&request.pending());requireClickBudget(request.remainingMS(),5000);
     await ui('click',found.pid,{hostBundle:choice.window.hostBundle,index:choice.window.index,path:choice.button.path,requestArgv,app,exe,launchMS:identity.launchMS});clicked=true;report.uiClickCount=1;break;
    }await pauses(100);
   }
   await request.done;assert(appLease,'actual independent LS app PID required');assert(!ownApp(await processRows(),requestArgv),'actual LS app absent after open wait');report.LSAppAbsent=true;
   assert(!lstatSync(callback).isSymbolicLink()&&lstatSync(callback).size<=4096&&lstatSync(callbackError).size<=4096);const permission=envelope(readFileSync(callback,'utf8'),id,nonce);assert.equal(permission,'allowed','actual correlated authorization callback required');assert(clicked,'one actual owned Allow click required');report.actualCallbackPermission=permission;report.callbackWriterEOF={openActualClosed:true,LSAppAbsent:true,bytes:lstatSync(callback).size};
  }else report.preAuthorizedPermissionObserved=true;
  assert.equal(await query(),'allowed','post-grant actual IPC permission required');report.actualIPCReadinessPermission='allowed';assert.equal(hash(exe),receipt.signingCustody.executableSHA256);report.status='permission_allowed';
 }catch(err){failure=err instanceof Error?err.message:'permission_preparation_failed';report.status='unqualified';report.failure=failure;}finally{
  let cleanupFailure:unknown;try{await closeApp();}catch(err){cleanupFailure='owned_LS_app_cleanup_unproved';report.LSAppCleanupFailure=String(err);}finally{if(request)await request.stop();}
  try{if(registrationAttempted){await run(ls,['-u',app]);const stream:Record<string,unknown>={};report.registrationCleanup=stream;await owned(ls,['-dump'],30000,registrationAbsence(app,stream)).done;}report.ownedRegistrationAbsent=true;}catch(err){cleanupFailure=cleanupFailure??'owned_registration_cleanup_unproved';}
  report.cleanupFailure=cleanupFailure;if(cleanupFailure){report.status='unqualified';failure=failure??cleanupFailure;}
  writeFileSync(join(source,'.task-tools/artifacts/mac-permission-ui.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600,flag:'wx'});
 }
 if(failure)throw Error('permission UI preparation failed; preserve content-free custody');
}
// Actual OS enumeration/click. Unknown/ambiguous paths and any AX/AE error propagate.
const jxa=String.raw`
function run(argv){
 const mode=argv[0],pid=Number(argv[1]),bundle=argv[2],choice=JSON.parse(argv[3]);
 function workspace(){ObjC.import('AppKit');const apps=$.NSWorkspace.sharedWorkspace.runningApplications.js.filter(function(a){if(ObjC.unwrap(a.bundleIdentifier)!==bundle)return false;if(!a.bundleURL||!a.executableURL)throw Error('owned_NSWorkspace_missing_URL');return ObjC.unwrap(a.bundleURL.path)===choice.app&&ObjC.unwrap(a.executableURL.path)===choice.exe;});if(apps.length!==1)throw Error('owned_NSWorkspace_app_missing');const a=apps[0];if(!a.launchDate)throw Error('owned_NSWorkspace_missing_launchDate');const identity={pid:Number(a.processIdentifier),bundle:ObjC.unwrap(a.bundleIdentifier),app:ObjC.unwrap(a.bundleURL.path),exe:ObjC.unwrap(a.executableURL.path),launchMS:Number(a.launchDate.timeIntervalSince1970)*1000};if(identity.pid!==pid||identity.bundle!==bundle||Number(a.isTerminated)!==0)throw Error('owned_NSWorkspace_identity');return identity;}
 if(mode==='identify')return JSON.stringify(workspace());
 if(pid&&choice&&choice.app){const identity=workspace();if(identity.launchMS!==choice.launchMS)throw Error('owned_NSWorkspace_incarnation');}
 const se=Application('System Events');
 if(se.uiElementsEnabled()!==true)throw Error('AX_disabled');
 const systems=['com.apple.UserNotificationCenter','com.apple.notificationcenterui'];let requesterBundle='';
 if(pid){const requests=se.applicationProcesses.whose({unixId:pid})();if(requests.length!==1)throw Error('owned_request_missing');requesterBundle=requests[0].bundleIdentifier();if(requesterBundle!==bundle)throw Error('owned_request_bundle');}
 const windows=[];let count=0;
 se.applicationProcesses().forEach(function(proc){const b=proc.bundleIdentifier();if(systems.indexOf(b)<0)return;
  proc.windows().forEach(function(win,index){const texts=[],buttons=[];
   function walk(element,path){if(++count>2048||path.length>8)throw Error('AX_tree_bound');const props=element.properties();
    if(props.role==='AXStaticText'){if(typeof props.value==='string')texts.push(props.value);if(typeof props.name==='string')texts.push(props.name);}
    if(props.role==='AXButton')buttons.push({name:String(props.name),enabled:props.enabled===true,path:path});
    element.uiElements().forEach(function(child,i){walk(child,path.concat([i]));});
   }
   const props=win.properties();if(typeof props.name==='string')texts.push(props.name);walk(win,[]);windows.push({hostBundle:b,index:index,role:props.role,texts:texts,buttons:buttons});
  });
 });
 if(windows.length>32)throw Error('AX_windows_bound');const snapshot={uiEnabled:true,requesterPID:pid,requesterBundle:requesterBundle,windows:windows};
 if(mode==='click'){
  function norm(t){return t.replace(/[“”]/g,'"').replace(/’/g,"'").replace(/\s+/g,' ').trim();}
  const matches=windows.filter(function(w){return ['AXWindow','AXDialog','AXSheet'].indexOf(w.role)>=0&&w.texts.some(function(t){return norm(t).replace(/"/g,'').toLowerCase()==='agent notifications would like to send you notifications';});});
  if(matches.length!==1||matches[0].hostBundle!==choice.hostBundle||matches[0].index!==choice.index)throw Error('AX_unique_dialog_changed');
  const w=matches[0];if(w.buttons.length!==2||w.buttons.map(function(b){return norm(b.name);}).sort().join('|')!=="Allow|Don't Allow")throw Error('AX_exact_actions');
  const allow=w.buttons.filter(function(b){return norm(b.name)==='Allow'&&b.enabled;});if(allow.length!==1||JSON.stringify(allow[0].path)!==JSON.stringify(choice.path)||!choice.path.length)throw Error('AX_exact_allow_changed');
  const processes=se.applicationProcesses.whose({bundleIdentifier:choice.hostBundle})();if(processes.length!==1)throw Error('AX_system_owner_changed');let target=processes[0].windows()[choice.index];choice.path.forEach(function(i){target=target.uiElements()[i];});
  if(target.role()!=='AXButton'||target.name()!=='Allow'||target.enabled()!==true)throw Error('AX_target_changed');
  // Revalidate after the entire AX walk, at the actual click boundary.
  if(!Number.isSafeInteger(pid)||pid<=1)throw Error('owned_request_PID');
  const scripting=Application.currentApplication();scripting.includeStandardAdditions=true;
  if(scripting.doShellScript('/bin/ps -ww -p '+String(pid)+' -o args=').trim()!==choice.requestArgv)throw Error('owned_request_argv_changed');
  const current=se.applicationProcesses.whose({unixId:pid})();if(current.length!==1||current[0].unixId()!==pid||current[0].bundleIdentifier()!==bundle)throw Error('owned_request_closed_before_click');
  if(choice.app){const identity=workspace();if(identity.launchMS!==choice.launchMS)throw Error('owned_NSWorkspace_changed_before_click');}
  se.click(target);
 }
 return JSON.stringify(snapshot);
}
`;
async function selfTest():Promise<void>{
 let probeClosed=false,probeRecorded=false;
 const execute=((...params:unknown[])=>{const p=Object.assign(new EventEmitter(),{pid:12345,stdout:new PassThrough(),stderr:new PassThrough(),kill:()=>true});const callback=params[3] as (err:Error,text:string)=>void;queueMicrotask(()=>{callback(Error('injected spawn error'),'');assert(!probeRecorded);queueMicrotask(()=>{probeClosed=true;p.emit('close',1);});});return p;}) as unknown as typeof execFile;
 await assert.rejects(processProbe({root:'/TEST',env:{},execute,record:(_pid,actualClose,failed)=>{assert(probeClosed&&actualClose&&failed);probeRecorded=true;}},1000));assert(probeRecorded);
 const args='/TEST/private/helper --request-permission-json --correlation-id UUID --nonce NONCE -launchedViaLaunchServices',lease=parseProcesses('123 1 Thu Oct  8 10:00:00 2026 '+args+'\n')[0]!;
 assert.equal(ownApp([lease],args),lease);assert.throws(()=>ownApp([lease,lease],args));assert(!sameProcess(lease,{...lease,birth:'different'}));assert.equal(ownApp([{...lease,args:args+' foreign'}],args),undefined);
 for(const mode of ['normal','late-empty','changed','descendant','never-seen']){
  let now=0,calls=0;const signals:number[]=[],proof:Record<string,unknown>={};
  const c:LSCleanup={state:{lease:mode==='never-seen'?undefined:lease,argv:args,unexpectedDescendants:false},report:proof,now:()=>now,pause:async ms=>{now+=ms;},verify:()=>{},signal:pid=>{signals.push(pid);},rows:async remaining=>{assert(remaining<=3000);calls++;if(mode==='late-empty'){now=3001;return [];}if(mode==='never-seen')return [];if(mode==='changed')return [{...lease,birth:'different'}];if(calls>1)return [];return mode==='descendant'?[lease,{pid:124,ppid:123,birth:lease.birth,args:'/FOREIGN/process'}]:[lease];}};
  if(mode==='normal'){await closeLSApp(c);assert.equal(proof.LSAppAbsent,true);assert.deepEqual(signals,[123]);}else{await assert.rejects(closeLSApp(c));assert(signals.every(pid=>pid===123));if(mode==='late-empty'||mode==='never-seen'||mode==='changed')assert.equal(signals.length,0);}
 }
 const literal='/bin/bash',tar='/usr/bin/tar',ls='/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister';
 assert.equal(canonicalExecutable(literal,p=>p),literal);assert.equal(canonicalExecutable(tar,()=>'/usr/bin/bsdtar'),'/usr/bin/bsdtar');assert.equal(canonicalExecutable(ls,()=>'/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister'),'/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister');
 assert.throws(()=>canonicalExecutable(tar,()=>'/TEST/foreign'));assert.throws(()=>canonicalExecutable('/TEST/helper',()=>'/TEST/foreign'));assert.throws(()=>canonicalExecutable('tar',()=>'/usr/bin/bsdtar'));
 const win:Window={hostBundle:systemBundles[0]!,index:0,role:'AXWindow',texts:['“Agent Notifications” Would Like to Send You Notifications'],buttons:[{name:'Allow',path:[0,1],enabled:true},{name:'Don’t Allow',path:[0,2],enabled:true}]};
 assert.throws(()=>requireClickBudget(5000,5000));assert.throws(()=>requireClickBudget(4000,5000));requireClickBudget(6001,6000);
 const snapshot:Snapshot={uiEnabled:true,requesterPID:123,requesterBundle:bundle,windows:[win]};assert.equal(select(snapshot,123).button.name,'Allow');
 for(const s of [{...snapshot,uiEnabled:false},{...snapshot,requesterPID:124},{...snapshot,requesterBundle:'foreign'},{...snapshot,windows:[]},{...snapshot,windows:[win,win]},{...snapshot,windows:[{...win,texts:['Other would like to send you notifications']}]},{...snapshot,windows:[{...win,buttons:[win.buttons[0]!,win.buttons[0]!]}]},{...snapshot,windows:[{...win,buttons:win.buttons.map(b=>({...b,enabled:false}))}]}])assert.throws(()=>select(s,123));
 const id=randomUUID(),nonce=randomUUID(),row={schemaVersion:1,correlationID:id,nonce,backend:'macos.usernotifications',permission:'allowed'};assert.equal(envelope(JSON.stringify(row)+'\n',id,nonce),'allowed');assert.throws(()=>envelope(JSON.stringify({...row,nonce:randomUUID()})+'\n',id,nonce));
 // Run the actual JXA with pure UI/OS mocks: a request vanishing during AX walk gets zero clicks.
 let clicks=0,ownerReads=0,closeAtRead=2;const node=(role:string,name:string,children:unknown[]=[])=>({properties:()=>({role,name,enabled:true}),uiElements:()=>children,role:()=>role,name:()=>name,enabled:()=>true});
 const allow=node('AXButton','Allow'),deny=node('AXButton',"Don't Allow"),window=node('AXWindow','"Agent Notifications" Would Like to Send You Notifications',[allow,deny]);
 const system={bundleIdentifier:()=>systemBundles[0],windows:()=>[window]},requester={bundleIdentifier:()=>bundle,unixId:()=>123};
 const processes=Object.assign(()=>[system],{whose:(where:{unixId?:number;bundleIdentifier?:string})=>()=>where.unixId?(++ownerReads<closeAtRead?[requester]:[]):[system]});
 const se={uiElementsEnabled:()=>true,applicationProcesses:processes,click:()=>{clicks++;}};
 const application=Object.assign(()=>se,{currentApplication:()=>({includeStandardAdditions:false,doShellScript:()=>'/TEST-owned/helper --request-permission-json'})});
 const choice={hostBundle:systemBundles[0],index:0,path:[0],requestArgv:'/TEST-owned/helper --request-permission-json'};
 assert.throws(()=>runInNewContext(jxa+'\nrun(argv)',{Application:application,argv:['click','123',bundle,JSON.stringify(choice)]}),/owned_request_closed_before_click/);assert.equal(clicks,0);
 let termReads=0;const appPath='/TEST/ClaudeNotifier.app',exePath=appPath+'/Contents/MacOS/terminal-notifier-modern';
 const nsApp={bundleIdentifier:bundle,bundleURL:{path:appPath},executableURL:{path:exePath},processIdentifier:123,launchDate:{timeIntervalSince1970:100},get isTerminated(){return {valueOf:()=>++termReads>=2?1:0};}};
 const bridge={import:()=>{},unwrap:(x:unknown)=>x},workspace={NSWorkspace:{sharedWorkspace:{runningApplications:{js:[{bundleIdentifier:'unrelated',bundleURL:null,executableURL:null},nsApp]}}}};
 const nsChoice={...choice,app:appPath,exe:exePath,launchMS:100000};ownerReads=0;closeAtRead=Infinity;
 assert.throws(()=>runInNewContext(jxa+'\nrun(argv)',{Application:application,ObjC:bridge,$:workspace,argv:['click','123',bundle,JSON.stringify(nsChoice)]}),/owned_NSWorkspace_identity/);assert.equal(clicks,0);
 termReads=-100;const identity=JSON.parse(runInNewContext(jxa+'\nrun(argv)',{ObjC:bridge,$:workspace,argv:['identify','123',bundle,JSON.stringify(nsChoice)]}) as string);assert.equal(identity.pid,123);assert.equal(identity.exe,exePath);
 const app='/TEST-owned/native/ClaudeNotifier.app',proof:Record<string,unknown>={};
 const exact=registrationAbsence(app,proof);exact.feed(Buffer.from('pa'));exact.feed(Buffer.from('th: '+app+' (0x'));exact.feed(Buffer.from('1a2f)\n'));assert.throws(()=>exact.finish());assert.equal(proof.ownedPathPresent,true);
 const largeProof:Record<string,unknown>={},large=registrationAbsence(app,largeProof),line=Buffer.from('other: '+ 'x'.repeat(4090)+'\n');for(let i=0;i<300;i++)large.feed(line);large.finish();assert(Number(largeProof.bytes)>1024*1024&&largeProof.ownedPathPresent===false);
 const nearProof:Record<string,unknown>={},near=registrationAbsence(app,nearProof);for(const part of ['path: '+app+'-foreign (0x1a)\n','path: '+app+' (unexpected)\n','path: /FOREIGN/ClaudeNotifier.app\n'])near.feed(Buffer.from(part));near.finish();assert.equal(nearProof.ownedPathPresent,false);
 const incomplete=registrationAbsence(app,{});incomplete.feed(Buffer.from('path: '+app));assert.throws(()=>incomplete.finish());
 assert.throws(()=>registrationAbsence(app,{},2).feed(Buffer.from('abc')));assert.throws(()=>registrationAbsence(app,{}).feed(Buffer.alloc(65537,120)));
 // Exercise the actual lifecycle function with no native spawn/kill: only absent owned groups.
 let mode='closed',destroyed=0;const report:Record<string,unknown>={children:[]};
 const spawnChild=((..._args:unknown[])=>{const p=Object.assign(new EventEmitter(),{pid:1234567,exitCode:null as number|null,signalCode:null,stdout:new PassThrough(),stderr:new PassThrough()});
  const destroy=p.stdout.destroy.bind(p.stdout);p.stdout.destroy=(...args)=>{destroyed++;return destroy(...args);};
  queueMicrotask(()=>{p.exitCode=mode==='nonzero'?1:0;p.emit('exit',p.exitCode);if(mode!=='missing-close')p.emit('close',p.exitCode);});return p;
 }) as unknown as typeof spawn;
 const ctx:OwnedContext={root:'/TEST-pure-mock',env:{},report,spawnChild,census:async()=>[{pid:process.pid,pgid:process.pid}],pauses:async()=>{}};
 // /bin/bash identity check is read-only; no execution occurs through this injected spawn.
 assert.equal(await createOwned(ctx,'/bin/bash',[],10000).done,'');mode='nonzero';const nonzeroProof:Record<string,unknown>={};await assert.rejects(createOwned(ctx,'/bin/bash',[],10000,registrationAbsence(app,nonzeroProof)).done);assert.equal(nonzeroProof.actualEOF,undefined);
 mode='missing-close';const noEOFProof:Record<string,unknown>={};await assert.rejects(createOwned(ctx,'/bin/bash',[],10000,registrationAbsence(app,noEOFProof)).done);assert.equal(noEOFProof.actualEOF,undefined);assert(destroyed>0);
 assert((report.children as {actualClose:boolean}[]).some(row=>row.actualClose===false));
 console.log('permission UI uniqueness/ownership/AX failure, envelope, nonzero missing-close, late-request zero-click, click-margin, canonical custody, LS incarnation/cleanup/NSWorkspace and bounded streaming tests passed');
}
if(process.argv[2]==='--self-test'){assert.equal(process.argv.length,3);await selfTest();}else await main();
