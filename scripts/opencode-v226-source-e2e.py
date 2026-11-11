#!/usr/bin/env python3
"""Observe real OpenCode 2.2.6 V2 source events and optional installed delivery.

Run in disposable CI with a pinned official archive. Private logs never enter the report.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request
import zipfile

OFFICIAL_ARCHIVE_HASHES = {
    'linux': '08bb21d6326ce740d91cce7ebe88a6536933f6e1e30353b91a06b01f25da306c',
    'darwin': '15491608f6b0bc4debe5eac35eab629a2b07ea9a1fbac68ad53bcea59fcc4b45',
    'win32': 'f75c16b33b8523fe1ff41e7d97e3ae654a47d59a0e327def24db2d9291bb24be',
}
OFFICIAL_TAG_SHA = "dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06"
TRACE_PLUGIN = r'''import {appendFileSync,existsSync} from 'node:fs';
import {createNativeV2} from '../test-support/native-v2.mjs';
import {spawnSync} from 'node:child_process';
const write=value=>appendFileSync(process.env.AN_TEST_TRACE,JSON.stringify(value)+'\n');
export default {id:'test-oc226-source',async setup(ctx){
 let view;
 const nativeSessions=new Map();
 let compactionHook;
 if(process.env.AN_TEST_COMPACTION==='1')compactionHook=await ctx.session.hook('http.request',input=>{
   const headers=new Headers(input.request.headers);headers.set('x-test-kind',input.kind);
   input.request=new Request(input.request,{headers});
 });
 try{ view=createNativeV2(ctx,ctx.location,()=>{},()=>{write({phase:'native-uncertain'});}); }
 catch{ write({phase:'native-constructor-denied'}); }
 // Test-only clock perturbation. Keep an unmodified receiver wall witness
 // before skewing Date.now; neither witness nor observer authorizes delivery.
 const baselineNow=Date.now.bind(Date);
 const skew=process.env.AN_TEST_CLOCK_SKEW;
 const clockSkewMS=skew==='12000'||skew==='-12000'?Number(skew):0;
 const skewTrigger=process.env.AN_TEST_CLOCK_SKEW_TRIGGER;
 if(clockSkewMS)Date.now=()=>baselineNow()+(skewTrigger&&!existsSync(skewTrigger)?0:clockSkewMS);
 write({phase:'loaded',version:ctx.app?.version,clockSkewMS,
   subscribe:typeof ctx.event?.subscribe==='function',
   sessionGet:typeof ctx.session?.get==='function',
   sessionContext:typeof ctx.session?.context==='function',
   permissionGet:typeof ctx.permission?.get==='function',
   permissionList:typeof ctx.permission?.list==='function',
   rpcRegister:typeof ctx.rpc?.register==='function'});
 const controller=new AbortController();
 void(async()=>{try{for await(const entry of ctx.event.subscribe({signal:controller.signal})){
   if(!entry||typeof entry.type!=='string')continue;
   const e=entry.data??entry.properties??{};
   const nativeSID=e.sessionID??e.form?.sessionID;
   let sessionState;
   if(typeof nativeSID==='string'){
     sessionState=nativeSessions.get(nativeSID);
     if(!sessionState){ sessionState={};nativeSessions.set(nativeSID,sessionState); }
     if(entry.type==='session.execution.started')sessionState.runID=entry.id;
     if(entry.type==='session.inbox.delivered')sessionState.userID=e.inboxID;
     if(entry.type==='session.step.started')sessionState.assistantID=e.assistantMessageID;
   }
   try{
     const mapped=view?.native.correlate(entry);
     if(mapped?.sessionID)write({phase:'native-projection',type:entry.type,sessionID:mapped.sessionID,
       observedSequence:Number.isSafeInteger(mapped.sequence?.seq),
       nativeScope:typeof mapped.location==='string',
       nativeEventKey:typeof entry.id==='string'});
   }catch{write({phase:'native-projection-denied',type:entry.type});}
   // Private receive-side samples only; shared process/clock cannot grant Q.
   const receivedWallMs=baselineNow(), receivedMonoNS=process.hrtime.bigint().toString();
   // An independent Go wall/boot sample is requested only in the isolated
   // installed-denial TEST. This is cross-process T0 observation, not trust.
   let goClock;
   if(entry.type==='session.execution.succeeded'&&process.env.AN_TEST_GO_CLOCK){
     try{
       // Windows native loader inputs are required for an independent Go
       // witness; all other inherited environment (including tokens) is omitted.
       const clockEnv={PATH:process.platform==='win32'?(process.env.PATH??''):'/usr/bin:/bin'};
       if(process.platform==='win32')for(const k of ['SystemRoot','WINDIR']){
         if(typeof process.env[k]==='string')clockEnv[k]=process.env[k];
       }
       const p=spawnSync(process.env.AN_TEST_GO_CLOCK,['opencode-clock','--protocol','1'],
         {timeout:2000,maxBuffer:1024,env:clockEnv});
       if(!p.error&&p.status===0&&p.stdout?.length>0&&p.stdout.length<=1024){
         const g=JSON.parse(p.stdout.toString('utf8'));
         if(g.protocol===1&&Object.keys(g).join('|')===
            'protocol|boot|clockDomain|clockKind|monoLoNs|monoHiNs|wallUnixNs|uncertaintyNs')
           goClock={boot:g.boot,domain:g.clockDomain,kind:g.clockKind,
             loNS:g.monoLoNs,hiNS:g.monoHiNs,wallUnixNS:g.wallUnixNs,uncertaintyNS:g.uncertaintyNs};
       }
     }catch{}
   }
   write({phase:'event',type:entry.type,id:entry.id,sessionID:e.sessionID??e.form?.sessionID,hasData:typeof e==='object'&&e!==null,
    receivedWallMs,receivedMonoNS,...(goClock?{goClock}:{}),
    hasSequence:Number.isSafeInteger(entry.durable?.seq),
    hasTime:Number.isSafeInteger(entry.created)&&entry.created>0,
    // Private trace only, never included in exported CI results.
    createdMs:Number.isSafeInteger(entry.created)&&entry.created>0?entry.created:null,
    toolBinding:entry.type==='form.created'?typeof e.form?.metadata?.tool?.messageID==='string'&&typeof e.form?.metadata?.tool?.id==='string':
      entry.type==='permission.asked'?e.source?.type==='tool'&&typeof e.source.messageID==='string'&&typeof e.source.id==='string':undefined,
    userInbox:e.item?.type==='user'});
   if(entry.type==='form.created' && typeof nativeSID==='string' && sessionState){
     void(async()=>{try{
       const tool=e.form?.metadata?.tool;
       const run={sessionID:nativeSID,turnID:sessionState.runID,userID:sessionState.userID,
         messageID:tool?.messageID,requestID:e.form?.id,callID:tool?.id};
       const proof=await view?.native.questionSource(run,new AbortController().signal);
       write({phase:'native-question',sessionID:nativeSID,valid:proof?.rootSession===true &&
         proof?.tool==='question' && proof?.requestID===run.requestID,
         hasSource:!!tool,runParts:[run.turnID,run.userID,run.messageID,run.callID].filter(x=>typeof x==='string'&&x).length});
     }catch{write({phase:'native-question',sessionID:nativeSID,valid:false,runParts:0});}})();
   }
   if(entry.type==='permission.asked' && typeof nativeSID==='string' && sessionState){
     void(async()=>{try{
       const source=e.source;
       const run={sessionID:nativeSID,turnID:sessionState.runID,userID:sessionState.userID,
         messageID:source?.messageID,requestID:e.id,callID:source?.id};
       const proof=await view?.native.currentPermission(run,new AbortController().signal);
       write({phase:'native-permission',sessionID:nativeSID,valid:proof?.rootSession===true &&
         proof?.pending===true && proof?.requestID===run.requestID,
         runParts:[run.turnID,run.userID,run.messageID,run.callID].filter(x=>typeof x==='string'&&x).length});
     }catch{write({phase:'native-permission',sessionID:nativeSID,valid:false,runParts:0});}})();
   }
   if(entry.type==='session.execution.failed'&&typeof e.sessionID==='string'){
     void(async()=>{try{
       const run={sessionID:e.sessionID,turnID:sessionState?.runID,userID:sessionState?.userID,
         messageID:sessionState?.assistantID,terminalEventID:entry.id,terminalCreated:entry.created};
       const proof=await view?.native.assistant(run,new AbortController().signal);
       const rows=await ctx.session.context({sessionID:e.sessionID});
       const idle=Array.isArray(rows)&&rows.find(x=>x.type==='idle'&&x.id===`msg_${entry.id.slice(4)}`);
       const assistant=Array.isArray(rows)&&rows.find(x=>x.type==='assistant'&&x.id===run.messageID);
       write({phase:'native-error',sessionID:e.sessionID,valid:proof?.final===true&&proof?.outcome==='error',
         outputLimitIdle:idle?.outcome==='failed'&&idle.error?.type==='output-limit',
         assistantErrorAbsent:assistant?.type==='assistant'&&assistant.error==null,
         runParts:[run.turnID,run.userID,run.messageID].filter(x=>typeof x==='string'&&x.length>0).length});
     }catch{write({phase:'native-error',sessionID:e.sessionID,valid:false,runParts:0});}})();
   }
   if(entry.type==='session.execution.succeeded'&&typeof e.sessionID==='string'){
     try{
       const info=await ctx.session.get({sessionID:e.sessionID});
       const value=await ctx.session.context({sessionID:e.sessionID});
       write({phase:'projection',sessionID:e.sessionID,matched:info?.id===e.sessionID,
         root:info?.parentID===undefined,projectID:typeof info?.projectID==='string',
         location:typeof info?.location?.directory==='string',rows:Array.isArray(value),
         assistant:Array.isArray(value)&&value.some(x=>x.type==='assistant')});
     }catch{write({phase:'projection',sessionID:e.sessionID,matched:false});}
     try{
       const run={sessionID:e.sessionID,turnID:sessionState?.runID,userID:sessionState?.userID,
         messageID:sessionState?.assistantID,terminalEventID:entry.id,terminalCreated:entry.created};
       const proof=await view?.native.assistant(run,new AbortController().signal);
       write({phase:'native-assistant',sessionID:e.sessionID,valid:proof?.final===true&&proof?.outcome==='success',
         hasOriginalMessage:typeof proof?.messageID==='string',runParts:
         [run.turnID,run.userID,run.messageID].filter(x=>typeof x==='string'&&x.length>0).length});
     }catch{write({phase:'native-assistant',sessionID:e.sessionID,valid:false,runParts:0});}
   }
 }}catch{write({phase:'reader_closed'});}})();
 return async()=>{controller.abort();await compactionHook?.dispose();write({phase:'cleanup'});};
}};'''


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def wait(fn, seconds, label):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        value = fn()
        if value:
            return value
        time.sleep(.08)
    raise RuntimeError(label)


def wait_for_delivery(webhook, event_type, message):
    def matching():
        with webhook.lock:
            bodies = list(webhook.bodies)
        for raw in bodies:
            try:
                payload = json.loads(raw)
            except (ValueError, UnicodeDecodeError):
                raise RuntimeError('delivery_payload_invalid')
            if not isinstance(payload, dict):
                raise RuntimeError('delivery_payload_invalid')
            if payload.get('notification_type') == event_type:
                if (payload.get('agent_source') != 'opencode' or payload.get('message') != message or
                        payload.get('title') != 'OpenCode' or payload.get('session_id') != '' or
                        set(payload) != {'schema_version', 'status', 'notification_type', 'agent_source',
                                         'message', 'timestamp', 'session_id', 'source', 'title'} or
                        b'PRIVATE_' in raw):
                    raise RuntimeError('delivery_payload_invalid')
                return True
        return False
    wait(matching, 15, 'expected_delivery_missing')


def verify_macos_desktop(root):
    """Correlate this TEST install's private submissions to OS banner logs."""
    spool = root / 'control' / 'opencode-native-spool'
    uuid_pattern = r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}'
    def submitted():
        ids = set()
        if not spool.is_dir():
            return ids
        for directory in spool.iterdir():
            if not directory.is_dir() or not re.fullmatch(uuid_pattern, directory.name):
                continue
            for receipt in directory.glob('*.receipt'):
                try:
                    with receipt.open('rb') as stream:
                        raw = stream.read(8193)
                    if len(raw) > 8192:
                        raise ValueError('receipt too large')
                    value = json.loads(raw)
                    if (value.get('schemaVersion') == 1 and value.get('status') == 'submitted' and
                            value.get('correlationID') == directory.name and
                            value.get('notificationID') == directory.name):
                        ids.add(directory.name)
                except (OSError, ValueError, TypeError, AttributeError):
                    raise RuntimeError('desktop_receipt_invalid')
        return ids
    # Completion, question, permission, provider failure and output-limit failure.
    ids = wait(lambda: (values if len(values := submitted()) >= 5 else None), 15,
               'desktop_submissions_missing')
    time.sleep(2)  # Allow macOS unified logging to flush this private test's submissions.
    log = subprocess.run(['/usr/bin/log', 'show', '--last', '10m', '--style', 'syslog',
                          '--predicate', 'process == "usernoted" AND eventMessage CONTAINS '
                                         '"com.777genius.agent-notifications"'],
                         capture_output=True, text=True, timeout=60)
    if log.returncode:
        raise RuntimeError('desktop_presentation_log_unavailable')
    presented = set(re.findall(r'Presenting <NotificationRecord app:"com\.777genius\.agent-notifications"'
                               r'[^\n]*?req:"(' + uuid_pattern + r')"[^\n]*?> as banner', log.stdout))
    count = len(ids & presented)
    if count < 5:
        raise RuntimeError('desktop_presentations_missing')
    return count



def valid_native_go_t0_sample(value, platform):
    """Strict, observational Go E0 frame check; never grants native qualification."""
    keys = {'boot', 'domain', 'kind', 'loNS', 'hiNS', 'wallUnixNS', 'uncertaintyNS'}
    if not isinstance(value, dict) or set(value) != keys:
        return False
    boot, domain, kind = value['boot'], value['domain'], value['kind']
    if not isinstance(boot, str) or not re.fullmatch(
            r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}', boot) or (
            boot == '00000000-0000-0000-0000-000000000000'):
        return False
    if platform == 'linux':
        parts = domain.split(':') if isinstance(domain, str) else []
        if kind != 'linux-boottime' or len(parts) != 3 or parts[0] != 'linux-time' or any(
                not re.fullmatch(r'[1-9][0-9]{0,19}', p) or int(p) > 18446744073709551615
                for p in parts[1:]):
            return False
    elif (platform, kind, domain) not in (
            ('darwin', 'darwin-monotonic-raw', 'darwin-kernel'),
            ('win32', 'windows-interrupt-precise', 'windows-kernel')):
        return False
    values = [value[k] for k in ('loNS', 'hiNS', 'wallUnixNS', 'uncertaintyNS')]
    if any(not isinstance(n, str) or not re.fullmatch(r'(?:0|[1-9][0-9]{0,18})', n) or
           int(n) > 9223372036854775807 for n in values):
        return False
    lo, hi, wall, uncertainty = map(int, values)
    return lo > 0 and wall > 0 and 0 <= hi - lo <= 100000000 and (
        uncertainty == hi - lo + 3000000)


def official_binary(archive, target):
    system = sys.platform
    expected = OFFICIAL_ARCHIVE_HASHES.get(system)
    if expected is None or not archive.is_file() or not 0 < archive.stat().st_size <= 160 * 1024 * 1024 or digest(archive) != expected:
        raise RuntimeError('official_archive_unverified')
    if system in ('darwin', 'win32'):
        name = 'opencode.exe' if system == 'win32' else 'opencode'
        with zipfile.ZipFile(archive) as packed:
            files = [m for m in packed.infolist() if not m.is_dir() and Path(m.filename).name == name]
            if len(files) != 1 or not 0 < files[0].file_size <= 350 * 1024 * 1024:
                raise RuntimeError('official_executable_not_unique')
            with packed.open(files[0]) as source, target.open('xb') as dest:
                while chunk := source.read(1024 * 1024):
                    dest.write(chunk)
    else:
        with tarfile.open(archive, mode='r:gz') as packed:
            files = [m for m in packed.getmembers() if Path(m.name).name == 'opencode' and m.isfile()]
            if len(files) != 1 or not 0 < files[0].size <= 250 * 1024 * 1024:
                raise RuntimeError('official_executable_not_unique')
            with packed.extractfile(files[0]) as source, target.open('xb') as dest:
                while chunk := source.read(1024 * 1024):
                    dest.write(chunk)
    if system != 'win32':
        target.chmod(0o700)
    return digest(target)

def prepare_windows_private_root(root):
    """Use the established native ACL fixture before creating any owned children.

    Windows installed notifications reject a sandbox inheriting broad runner
    permissions. This hardens TEST-only custody; it never grants observer admission.
    """
    if sys.platform != 'win32':
        return
    helper = Path(__file__).resolve().parent / 'opencode-private-root-windows.go'
    if not helper.is_file():
        raise RuntimeError('windows_private_root_unverified')
    try:
        result = subprocess.run(
            ['go', 'run', str(helper), str(root)],
            cwd=helper.parent.parent, capture_output=True, timeout=90,
            check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise RuntimeError('windows_private_root_unverified') from None
    if result.returncode != 0:
        # Do not log helper output or any Windows profile / ACL identities.
        raise RuntimeError('windows_private_root_unverified')


def stop_official_host_tree(host):
    """Reap the actual test-host process tree on restart and final cleanup."""
    if host.poll() is not None:
        host.wait(timeout=5)
        return
    if sys.platform == 'win32':
        try:
            subprocess.run(['taskkill', '/PID', str(host.pid), '/T', '/F'],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                           timeout=10, check=False)
        except (OSError, subprocess.TimeoutExpired):
            pass
        if host.poll() is None:
            host.kill()
        host.wait(timeout=5)
    else:
        os.killpg(host.pid, signal.SIGTERM)
        try:
            host.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(host.pid, signal.SIGKILL)
            host.wait(timeout=5)


def cleanup_with_sharing_retry(cleanup, windows, clock=time.monotonic, pause=time.sleep, owned_root=None):
    """Retry narrowly identified Windows cleanup races; never ignore failure."""
    deadline = clock() + 5
    while True:
        try:
            return cleanup()
        except PermissionError as error:
            code = getattr(error, 'winerror', None)
            # Windows CI returned access denied for this SQLite file after
            # host exit. Do not retry other ACL failures, paths or platforms.
            shm = os.path.join(owned_root, 'data', 'opencode', 'opencode.db-shm') if owned_root else None
            same_shm = shm and error.filename and os.path.normcase(os.path.abspath(error.filename)) == os.path.normcase(os.path.abspath(shm))
            if not windows or not (code == 32 or code == 5 and same_shm) or clock() >= deadline:
                raise
            pause(.1)


class OwnedTemporaryDirectory(tempfile.TemporaryDirectory):
    def cleanup(self):
        return cleanup_with_sharing_retry(super().cleanup, sys.platform == 'win32', owned_root=self.name)


PEER_PLUGIN = r'''import {appendFileSync} from 'node:fs';
const write=x=>appendFileSync(process.env.AN_TEST_PEER_TRACE,JSON.stringify(x)+'\n');
export default {id:'test-oc226-peer',async setup(ctx){
 const controller=new AbortController();
 void(async()=>{try{
  const stream=ctx.event.subscribe({signal:controller.signal});
  const iterator=stream[Symbol.asyncIterator]();
  write({phase:'ready'});
  for(;;){const next=await iterator.next();if(next.done)break;
   const e=next.value;
   if(!['session.execution.started','session.step.ended','session.execution.succeeded'].includes(e?.type))continue;
   const original={type:e.type,id:e.id,created:e.created,sessionID:e.data?.sessionID};
   let blocked=false;try{e.created=1;e.id='TEST-mutated-peer-id';e.data.sessionID='TEST-mutated-peer-session';}catch{blocked=true;}
   write({phase:'peer',...original,mutationBlocked:blocked,
     mutationApplied:e.created===1&&e.id==='TEST-mutated-peer-id'&&e.data?.sessionID==='TEST-mutated-peer-session'});
  }
 }catch{write({phase:'failed'});}})();
 return ()=>controller.abort();
}};'''


def run(archive, root, receiver=None, clock_skew_ms=0, restart_denial=False, expect_delivery=False, native_app=None, skew_after_delivery=False, service=False, live_signer=None, mutate_peer=False, observe_compaction=False):
    spec = importlib.util.spec_from_file_location('existing_native_provider',
        Path(__file__).resolve().parent / 'opencode-semantic-e2e.py')
    native = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(native)
    binary = root / ('opencode.exe' if sys.platform == 'win32' else 'opencode')
    binary_sha = official_binary(archive, binary)
    print('source-stage: archive-verified', flush=True)
    names = dict(home='home', config='config', project='TEST-project', data='data',
                 cache='cache', state='state', runtime='runtime', temp='temp', bun='bun')
    paths = {k: root / v for k, v in names.items()}
    for directory in paths.values():
        directory.mkdir(mode=0o700)
    if service:
        # Service mode ignores OPENCODE_PASSWORD and reads its own config.
        # Both config and registration remain in this disposable TEST root.
        (paths['config'] / 'service.json').write_text(json.dumps({'password': 'test-only'}), encoding='utf-8')
    subprocess.run(['git', 'init', '-q', str(paths['project'])], check=True, timeout=10)
    plugins = paths['config'] / 'plugins'
    plugins.mkdir(mode=0o700)
    test_support = paths['config'] / 'test-support'
    test_support.mkdir(mode=0o700)
    (test_support / 'native-v2.mjs').write_bytes((Path(__file__).resolve().parent.parent / 'opencode-plugin/native-v2.mjs').read_bytes())
    (plugins / 'test-oc226-source.js').write_text(TRACE_PLUGIN, encoding='utf-8')
    trace = root / 'native-events.private.jsonl'
    peer_trace = root / 'peer-events.private.jsonl'
    if mutate_peer:
        (plugins / 'test-oc226-peer.js').write_text(PEER_PLUGIN, encoding='utf-8')
    env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': str(paths['home']),
           'LANG': 'C.UTF-8', 'TERM': 'dumb', 'XDG_CONFIG_HOME': str(paths['config']),
           'XDG_DATA_HOME': str(paths['data']), 'XDG_CACHE_HOME': str(paths['cache']),
           'XDG_STATE_HOME': str(paths['state']), 'XDG_RUNTIME_DIR': str(paths['runtime']),
           'BUN_INSTALL_CACHE_DIR': str(paths['bun']), 'TMPDIR': str(paths['temp']),
           'OPENCODE_CONFIG_DIR': str(paths['config']), 'AN_TEST_TRACE': str(trace),
           **({'AN_TEST_PEER_TRACE':str(peer_trace)} if mutate_peer else {}),
           **({'AN_TEST_COMPACTION':'1'} if observe_compaction else {}),
           **({'AN_TEST_CLOCK_SKEW':str(clock_skew_ms)} if clock_skew_ms else {}),
           **({'AN_TEST_CLOCK_SKEW_TRIGGER':str(root / 'skew.trigger')} if skew_after_delivery else {}),
           'OPENCODE_DISABLE_AUTOUPDATE': 'true',
           'OPENCODE_DISABLE_DEFAULT_PLUGINS': 'true',
           'OPENCODE_DISABLE_MODELS_FETCH': 'true',
           'OPENCODE_DISABLE_SHARE': 'true',
           'OPENCODE_PASSWORD': 'test-only', 'OPENCODE_SERVER_PASSWORD': 'test-only'}
    if sys.platform == 'win32':
        # Preserve only Windows process-loader inputs, never the runner's tokens.
        env.update({k: os.environ[k] for k in ('SystemRoot', 'WINDIR', 'ComSpec', 'PATHEXT')
                    if k in os.environ})
        env.update({'USERPROFILE': str(paths['home']), 'APPDATA': str(paths['config']),
                    'LOCALAPPDATA': str(paths['data']), 'TEMP': str(paths['temp']),
                    'TMP': str(paths['temp'])})
    version = subprocess.run([str(binary), '--version'], cwd=paths['project'], env=env,
                             text=True, capture_output=True, timeout=20)
    if version.returncode:
        raise RuntimeError('official_version_command_failed')
    if not re.search(r'(?<![0-9])2\.2\.6(?![0-9])', version.stdout):
        raise RuntimeError('official_version_format_unexpected')
    print('source-stage: version-verified', flush=True)
    provider = native.Endpoint()
    provider.version = '2.2.6'
    threading.Thread(target=provider.serve_forever, daemon=True).start()
    webhook = native.Endpoint(webhook=True) if receiver is not None else None
    if webhook is not None:
        threading.Thread(target=webhook.serve_forever, daemon=True).start()
    try:
        settings = {'baseURL': f'http://127.0.0.1:{provider.server_port}/v1', 'apiKey': 'test-only'}
        config = {'model': 'test/test', 'providers': {'test': {
            'package': '@opencode/ai/providers/openai-compatible', 'settings': settings,
            'models': {'test': {'name': 'TEST model', 'limit': {'context': 32000, 'output': 1024}}}}}}
        config['permissions'] = [{'action':'shell','resource':'*','effect':'ask'}]
        (paths['project'] / 'opencode.json').write_text(json.dumps(config), encoding='utf-8')
        if receiver is not None:
            receiver = receiver.resolve(strict=True)
            # Exact-source unreleased Go executable for the source-only clock
            # comparison, not a bypass to native admission or a delivery grant.
            env['AN_TEST_GO_CLOCK'] = str(receiver)
            env['AGENT_NOTIFICATIONS_CONFIG'] = str(root / 'notifications.json')
            (root / 'notifications.json').write_text(json.dumps({'notifications': {
                'desktop': {'enabled': False},
                'webhook': {'enabled': True, 'preset': 'custom', 'format': 'json',
                            'url': f'http://127.0.0.1:{webhook.server_port}/webhook'}}}), encoding='utf-8')
            command = [str(receiver), 'setup-opencode', 'install',
                       '--control-root', str(root / 'control'),
                       '--runtime-root', str(root / 'native-runtime'),
                       '--opencode-config-dir', str(paths['config']),
                       '--home', str(paths['home']),
                       '--xdg-config-home', str(paths['config']),
                       '--binary', str(receiver), '--webhook']
            if native_app is not None:
                command += ['--desktop', '--native-app', str(native_app.resolve(strict=True))]
            result = subprocess.run(command, cwd=paths['project'], env=env,
                                    capture_output=True, text=True, timeout=100 if native_app else 60)
            if result.returncode:
                # Installer stdout/stderr are private. Report only the stage.
                raise RuntimeError('managed_install_command_failed')
            if not (plugins / 'agent-notifications.js').is_file():
                raise RuntimeError('managed_plugin_file_missing')
            # The consumer reads the managed policy snapshot, not the ambient
            # AGENT_NOTIFICATIONS_CONFIG document. Configure the actual channel
            # through the public revision-fenced interface before starting the
            # host; otherwise a qualified observer can never send a webhook.
            config_args = [str(receiver), 'config']
            target_args = ['--target', 'opencode', '--control-root', str(root / 'control')]
            inspection = subprocess.run(config_args + ['inspect', *target_args, '--json'],
                                        cwd=paths['project'], env=env, capture_output=True,
                                        text=True, timeout=60)
            try:
                inspected = json.loads(inspection.stdout)
                if (inspection.returncode or inspected.get('valid') is not True or
                        Path(inspected['selection']['path']).resolve() !=
                        (root / 'control' / 'agent-notifications.json').resolve()):
                    raise ValueError('invalid managed configuration')
                revision = inspected['revision']
            except (ValueError, KeyError, TypeError):
                raise RuntimeError('managed_webhook_configuration_failed')
            edits = {'/notifications/desktop/enabled': native_app is not None,
                     '/notifications/webhook/enabled': True,
                     '/notifications/webhook/preset': 'custom',
                     '/notifications/webhook/format': 'json',
                     '/notifications/webhook/url': f'http://127.0.0.1:{webhook.server_port}/webhook'}
            for status in ('task_complete', 'question', 'permission_request', 'opencode_error'):
                edits['/statuses/' + status + '/enabled'] = True
                edits['/statuses/' + status + '/webhook/enabled'] = True
            configured = subprocess.run(config_args + ['edit', *target_args, '--stdin',
                                        '--expect-revision', revision], cwd=paths['project'], env=env,
                                        input=json.dumps({'set': edits}), capture_output=True,
                                        text=True, timeout=60)
            if configured.returncode:
                raise RuntimeError('managed_webhook_configuration_failed')
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        with (root / 'host.private.log').open('wb') as log:
            host = subprocess.Popen([str(binary), 'serve', *(['--service'] if service else []),
                                     '--hostname', '127.0.0.1', '--port', str(port), '--print-logs'],
                                    cwd=paths['project'], env=env, stdout=log, stderr=log,
                                    start_new_session=(sys.platform != 'win32'),
                                    creationflags=(subprocess.CREATE_NEW_PROCESS_GROUP
                                                   if sys.platform == 'win32' else 0))
            try:
                def request(endpoint, body=None, method=None):
                    payload = None if body is None else json.dumps(body).encode()
                    headers = {'Content-Type': 'application/json',
                               'x-opencode-directory': str(paths['project']),
                               'Authorization': 'Basic ' + base64.b64encode(b'opencode:test-only').decode()}
                    req = urllib.request.Request(f'http://127.0.0.1:{port}' + endpoint,
                                                 data=payload, method=method, headers=headers)
                    with urllib.request.urlopen(req, timeout=35) as resp:
                        raw = resp.read()
                        return json.loads(raw) if raw else None

                def rows():
                    if not trace.exists():
                        return []
                    # The official host appends trace records concurrently. A
                    # snapshot may end mid-append: inspect only newline-terminated
                    # records, but reject any malformed completed record.
                    with trace.open('rb') as stream:
                        payload = stream.read(2 * 1024 * 1024 + 1)
                    if len(payload) > 2 * 1024 * 1024:
                        raise RuntimeError('private_trace_limit')
                    last_line = payload.rfind(b'\n')
                    if last_line < 0:
                        return []
                    return [json.loads(line) for line in payload[:last_line].splitlines()]

                def ready():
                    if host.poll() is not None:
                        raise RuntimeError('official_host_exited')
                    try:
                        return request('/api/location')
                    except OSError:
                        # Includes urllib errors, timeouts, and raw socket resets.
                        return None

                wait(ready, 70, 'native_startup_timeout')
                print('source-stage: native-started', flush=True)
                if live_signer is not None:
                    def live_signature(pid):
                        result = subprocess.run([str(live_signer), str(pid)], cwd=root, env=env,
                                                capture_output=True, timeout=30, check=True)
                        return json.loads(result.stdout)
                    own_signature = live_signature(os.getpid())
                    if (host.poll() is not None or
                            live_signature(host.pid) != {'publisherMatches': True, 'wrongTeamRefused': True} or
                            set(own_signature) != {'publisherMatches', 'wrongTeamRefused'} or
                            own_signature['publisherMatches'] is not False or
                            host.poll() is not None):
                        raise RuntimeError('live_signer_observation_unverified')
                loaded = wait(lambda: next((r for r in rows() if r.get('phase') == 'loaded'), None),
                              25, 'source_autoload_missing')
                print('source-stage: plugin-loaded', flush=True)
                capabilities = ('subscribe', 'sessionGet', 'sessionContext', 'permissionGet',
                                'permissionList', 'rpcRegister')
                if loaded.get('version') != '2.2.6' or any(loaded.get(k) is not True for k in capabilities):
                    raise RuntimeError('host_capabilities_mismatch')
                if loaded.get('clockSkewMS') != clock_skew_ms:
                    raise RuntimeError('source_clock_skew_injection_missing')
                if mutate_peer:
                    def peer_rows():
                        if not peer_trace.exists():
                            return []
                        payload = peer_trace.read_bytes()
                        if len(payload) > 2 * 1024 * 1024:
                            raise RuntimeError('private_trace_limit')
                        return [json.loads(line) for line in payload[:payload.rfind(b'\n') + 1].splitlines()]
                    wait(lambda: any(r.get('phase') == 'ready' for r in peer_rows()), 15, 'peer_subscription_missing')
                if skew_after_delivery:
                    # Same host and receiver must deliver before perturbation;
                    # otherwise a dead or unqualified receiver could make the
                    # negative assertion pass without testing clock handling.
                    baseline = request('/api/session', {'title': 'TEST-skew-control',
                                       'location': {'directory': str(paths['project'])}})
                    baseline_sid = baseline['data']['id']
                    request('/api/session/' + baseline_sid + '/prompt', {'text': 'TEST-clock-control'})
                    request('/api/experimental/session/' + baseline_sid + '/wait', method='POST')
                    wait_for_delivery(webhook, 'task_complete', 'Task completed')
                    time.sleep(.5)
                    with webhook.lock:
                        webhook.bodies.clear()
                    (root / 'skew.trigger').touch(exist_ok=False)
                source_wall_start_ms = int(time.time() * 1000)
                session = request('/api/session', {'title': 'TEST-source-2.2.6',
                                     'location': {'directory': str(paths['project'])}})
                sid = session['data']['id']
                print('source-stage: session-created', flush=True)
                request('/api/session/' + sid + '/prompt', {'text': 'TEST-native-source-turn'})
                request('/api/experimental/session/' + sid + '/wait', method='POST')
                observed = wait(lambda: {r['type'] for r in rows() if r.get('phase') == 'event'}
                                if any(r.get('phase') == 'event' and r.get('type') == 'session.execution.succeeded' and r.get('sessionID') == sid
                                       for r in rows()) else None, 35, 'native_source_events_missing')
                source_wall_end_ms = int(time.time() * 1000)
                print('source-stage: provider-request='+str(bool(provider.bodies)).lower()+
                      ',execution-start='+str('session.execution.started' in observed).lower(), flush=True)
                if not provider.bodies:
                    raise RuntimeError('local_provider_unused')
                if 'session.execution.started' not in observed:
                    raise RuntimeError('native_execution_start_unseen')
                # Native V2 envelope time is on entry.created, not event.data.time.
                terminal = {'session.execution.started', 'session.step.ended',
                            'session.execution.succeeded'}
                if not all(any(r.get('type') == kind and r.get('sessionID') == sid and r.get('hasSequence') and r.get('hasTime')
                               for r in rows()) for kind in terminal) or not any(
                        r.get('type') == 'session.inbox.enqueued' and r.get('sessionID') == sid and
                        r.get('userInbox') for r in rows()):
                    raise RuntimeError('native_source_envelope_unverified')
                # Observation only: native event creation should fall within the
                # independent runner wall-time window of this genuine turn.
                # This coarse check is NOT a Bun 1.4.4 clock accuracy proof.
                source_rows = [r for r in rows() if r.get('phase') == 'event' and
                               r.get('sessionID') == sid and r.get('type') in terminal and
                               r.get('hasSequence') and r.get('hasTime')]
                if not source_rows or any(not isinstance(r.get('createdMs'), int) for r in source_rows):
                    raise RuntimeError('native_source_epoch_unverified')
                if mutate_peer:
                    def peer_isolated():
                        peer = [r for r in peer_rows() if r.get('phase') == 'peer' and r.get('sessionID') == sid]
                        return all(any(p.get('type') == kind and p.get('id') == r.get('id') and
                                       p.get('created') == r.get('createdMs') and
                                       (p.get('mutationApplied') is True or p.get('mutationBlocked') is True)
                                       for p in peer for r in source_rows if r.get('type') == kind)
                                   for kind in terminal)
                    wait(peer_isolated, 15, 'peer_stream_isolation_unverified')
                if clock_skew_ms:
                    # This is an independent TEST assertion, not an OpenCode
                    # admission decision. Baseline wall was captured before the
                    # test plugin changes Date.now, and is checked against the
                    # separate runner clock to prevent a same-process false pass.
                    if any(type(r.get('receivedWallMs')) is not int or
                           not source_wall_start_ms - 5000 <= r['receivedWallMs'] <= source_wall_end_ms + 5000
                           for r in source_rows):
                        raise RuntimeError('source_receiver_sample_invalid')
                    if clock_skew_ms > 0:
                        skew_observed = all(r['createdMs'] > r['receivedWallMs'] + 5000 for r in source_rows)
                    else:
                        skew_observed = all(r['createdMs'] < r['receivedWallMs'] - 5000 for r in source_rows)
                    if not skew_observed:
                        raise RuntimeError('source_clock_skew_not_refused')
                    negative_native = [r for r in rows() if r.get('phase') == 'native-projection' and r.get('sessionID') == sid]
                    if not all(any(r.get('type') == kind and r.get('observedSequence') for r in negative_native)
                               for kind in terminal):
                        raise RuntimeError('negative_native_reader_projection_unverified')
                    if webhook is not None:
                        # Observe the actual installed receiver with the same
                        # skewed source. A missing webhook is not proof of the
                        # precise rejection phase or of qualified admission.
                        until = time.monotonic() + 15
                        while time.monotonic() < until:
                            with webhook.lock:
                                if webhook.bodies:
                                    raise RuntimeError('skewed_host_delivered_webhook')
                            time.sleep(.08)
                        return {'status': 'passed_installed_skew_no_webhook_observed',
                                'version': '2.2.6', 'clockSkewTestMS': clock_skew_ms,
                                'sourceClockMismatchObserved': True,
                                'installedReceiverObserved': True,
                                'sameHostPriorDeliveryObserved': skew_after_delivery,
                                'receiverRejectionPhaseKnown': False,
                                'nativeObserverQualified': False, 'nativeClockQualified': False,
                                'notificationDeliveryQualified': False}
                    # This assertion is the TEST HARNESS comparing native
                    # source epoch to a separate wall witness. No installed
                    # receiver is running in skew mode, so never describe this
                    # as a production timestamp rejection or delivery denial.
                    return {'status':'passed_clock_skew_observation_only','version':'2.2.6',
                            'clockSkewTestMS':clock_skew_ms,
                            'sourceFutureWallMismatchObserved':clock_skew_ms > 0,
                            'sourcePastWallMismatchObserved':clock_skew_ms < 0,
                            'productionAdmissionExercised':False,
                            'nativeR0StillObserved':True,
                            'nativeObserverQualified':False,'nativeClockQualified':False,
                            'notificationDeliveryQualified':False}
                if any(not source_wall_start_ms - 5000 <= r['createdMs'] <= source_wall_end_ms + 5000
                       for r in source_rows):
                    raise RuntimeError('native_source_epoch_unverified')
                # Observational Q0: receiver wall/monotonic vs original event
                # timestamp on the official image, not an independent source,
                # clock qualification, policy row or authorization grant.
                if any(type(r.get('receivedWallMs')) is not int or
                       not isinstance(r.get('receivedMonoNS'), str) or
                       not re.fullmatch(r'[1-9][0-9]*', r['receivedMonoNS'])
                       for r in source_rows):
                    raise RuntimeError('source_receiver_sample_invalid')
                # Source-only R0: execute the actual production native-v2 correlator
                # against the genuine host stream, without granting admission.
                native_snapshot = rows()
                native_rows = [r for r in native_snapshot if r.get('phase') == 'native-projection' and r.get('sessionID') == sid]
                if any(r.get('phase') in ('native-uncertain', 'native-constructor-denied') for r in native_snapshot) or not all(
                    any(r.get('type') == kind and r.get('observedSequence') for r in native_rows)
                    for kind in terminal):
                    raise RuntimeError('native_reader_projection_unverified')
                receipt_ticks = [int(r['receivedMonoNS']) for r in source_rows]
                if any(b < a for a, b in zip(receipt_ticks, receipt_ticks[1:])):
                    raise RuntimeError('source_receiver_counter_nonmonotonic')
                receiver_lags = [r['receivedWallMs'] - r['createdMs'] for r in source_rows]
                go_source_lag_ns = None
                if receiver is not None:
                    go_rows = [r for r in source_rows if r.get('type') == 'session.execution.succeeded']
                    go = go_rows[0].get('goClock') if go_rows else None
                    if not valid_native_go_t0_sample(go, sys.platform):
                        raise RuntimeError('source_go_clock_sample_unavailable')
                    go_source_lag_ns = int(go['wallUnixNS']) - go_rows[0]['createdMs'] * 1000000
                    # Preserve the existing coarse observational wall window.
                    # A bounded observed delta is not a worst-case independent T.
                    if abs(go_source_lag_ns) > 5000000000:
                        raise RuntimeError('source_go_clock_epoch_unverified')
                projection = wait(lambda: next((r for r in rows() if r.get('phase') == 'projection' and
                                    r.get('sessionID') == sid), None), 20, 'native_session_projection_missing')
                if not all(projection.get(k) is True for k in
                           ('matched', 'root', 'projectID', 'location', 'rows', 'assistant')):
                    raise RuntimeError('native_session_projection_unverified')
                # R1 source-only adapter check, not independent native authority.
                # Fail closed if the exact production projection cannot read this turn.
                r1 = wait(lambda: next((r for r in rows() if r.get('phase')=='native-assistant' and
                                        r.get('sessionID')==sid),None), 20, 'native_reader_r1_sample_missing')
                if r1.get('valid') is not True or r1.get('runParts') != 3:
                    raise RuntimeError('native_assistant_projection_unverified')
                print('source-stage: native-session-projection-verified', flush=True)
                if expect_delivery:
                    wait_for_delivery(webhook, 'task_complete', 'Task completed')
                if observe_compaction:
                    # The first turn seeded context. The loopback provider now
                    # returns a context-length error once, forcing a genuine
                    # native automatic compaction and subsequent completion.
                    compaction_begin = len(rows())
                    with provider.lock:
                        provider.mode, provider.attempts = 'overflow', 0
                    if expect_delivery:
                        with webhook.lock:
                            prior_delivery_count = len(webhook.bodies)
                    request('/api/session/' + sid + '/prompt', {'text': 'TEST-source-compaction'})
                    request('/api/experimental/session/' + sid + '/wait', method='POST')
                    def compacted():
                        snapshot = rows()[compaction_begin:]
                        kinds = {r.get('type') for r in snapshot if r.get('sessionID') == sid}
                        proof = next((r for r in snapshot if r.get('phase') == 'native-assistant' and
                                      r.get('sessionID') == sid), None)
                        return {'session.compaction.started', 'session.compaction.ended',
                                'session.execution.succeeded'}.issubset(kinds) and proof
                    proof = wait(compacted, 35, 'source_auto_compaction_missing')
                    if proof.get('valid') is not True or proof.get('runParts') != 3:
                        raise RuntimeError('source_auto_compaction_unverified')
                    if expect_delivery:
                        def new_completion():
                            with webhook.lock:
                                new_bodies = list(webhook.bodies[prior_delivery_count:])
                            return any(json.loads(body).get('notification_type') == 'task_complete' for body in new_bodies)
                        wait(new_completion, 15, 'source_auto_compaction_delivery_missing')
                    provider.mode = 'success'
                    print('source-stage: auto-compaction-verified', flush=True)
                # Independently verify native pending question, reply and settled turn.
                question_begin = len(rows())
                prior_requests = len(provider.bodies)
                provider.mode = 'question'
                question = request('/api/session', {'title': 'TEST-source-question',
                                    'location': {'directory': str(paths['project'])}})
                question_sid = question['data']['id']
                request('/api/session/' + question_sid + '/prompt', {'text': 'TEST-source-question'})
                forms = wait(lambda: request('/api/session/' + question_sid + '/form').get('data', []),
                             35, 'source_native_question_missing')
                question_rows = rows()[question_begin:]
                if len(provider.bodies) <= prior_requests or not any(
                        r.get('type') == 'form.created' and r.get('sessionID') == question_sid and
                        not r.get('hasSequence') and r.get('hasTime') and r.get('toolBinding') for r in question_rows):
                    raise RuntimeError('source_native_question_unverified')
                qr1=wait(lambda: next((r for r in rows() if r.get('phase')=='native-question' and
                                      r.get('sessionID')==question_sid),None),10,'native_r1_question_missing')
                if qr1.get('valid') is not True or qr1.get('runParts') != 4:
                    raise RuntimeError('native_question_projection_unverified')
                if expect_delivery:
                    wait_for_delivery(webhook, 'question', 'OpenCode asked a question')
                request('/api/session/' + question_sid + '/form/' + forms[0]['id'] + '/reply',
                        {'answer': {'q0': 'Yes'}})
                wait(lambda: any(r.get('type') == 'form.replied' and r.get('sessionID') == question_sid
                                 for r in rows()[question_begin:]), 15, 'source_question_reply_unseen')
                request('/api/experimental/session/' + question_sid + '/wait', method='POST')
                wait(lambda: any(r.get('type') == 'session.execution.succeeded' and r.get('sessionID') == question_sid
                                 for r in rows()[question_begin:]), 35, 'source_question_terminal_missing')
                provider.mode = 'success'
                print('source-stage: question-verified', flush=True)
                # Permission.asked is likewise ephemeral, and needs an independent live pending snapshot.
                permission_begin = len(rows())
                prior_requests = len(provider.bodies)
                provider.mode = 'permission'
                permission = request('/api/session', {'title': 'TEST-source-permission',
                                      'location': {'directory': str(paths['project'])}})
                permission_sid = permission['data']['id']
                request('/api/session/' + permission_sid + '/prompt', {'text': 'TEST-source-permission'})
                pending = wait(lambda: request('/api/session/' + permission_sid + '/permission').get('data', []),
                               35, 'source_native_permission_missing')
                permission_rows = rows()[permission_begin:]
                if len(provider.bodies) <= prior_requests or not any(
                        r.get('type') == 'permission.asked' and r.get('sessionID') == permission_sid and
                        not r.get('hasSequence') and r.get('hasTime') and r.get('toolBinding') for r in permission_rows):
                    raise RuntimeError('source_native_permission_unverified')
                pr1=wait(lambda: next((r for r in rows() if r.get('phase')=='native-permission' and
                                      r.get('sessionID')==permission_sid),None),10,'native_r1_permission_missing')
                if pr1.get('valid') is not True or pr1.get('runParts') != 4:
                    raise RuntimeError('native_permission_projection_unverified')
                if expect_delivery:
                    wait_for_delivery(webhook, 'permission_request', 'OpenCode requested permission')
                request('/api/session/' + permission_sid + '/permission/' + pending[0]['id'] + '/reply',
                        {'decision': 'reject'})
                wait(lambda: any(r.get('type') == 'permission.replied' and r.get('sessionID') == permission_sid
                                 for r in rows()[permission_begin:]), 15, 'source_permission_reply_unseen')
                provider.mode = 'success'
                print('source-stage: permission-verified', flush=True)
                # Permanent provider failure must produce a durable native terminal (not a received clock).
                failure_begin = len(rows())
                prior_requests = len(provider.bodies)
                provider.mode = 'failure'
                failed = request('/api/session', {'title': 'TEST-source-terminal',
                                  'location': {'directory': str(paths['project'])}})
                failed_sid = failed['data']['id']
                request('/api/session/' + failed_sid + '/prompt', {'text': 'TEST-source-terminal'})
                try:
                    request('/api/experimental/session/' + failed_sid + '/wait', method='POST')
                except urllib.error.HTTPError as err:
                    if not 400 <= err.code <= 599:
                        raise
                def failed_events():
                    return [r for r in rows()[failure_begin:] if
                            r.get('type') == 'session.execution.failed' and r.get('sessionID') == failed_sid]
                # An initial non-durable event can precede its completed native
                # envelope. Never count that event as valid terminal evidence.
                wait(failed_events, 35, 'source_terminal_error_missing')
                if len(provider.bodies) <= prior_requests:
                    raise RuntimeError('source_terminal_provider_unused')
                wait(lambda: [r for r in failed_events() if r.get('hasSequence') is True and
                              r.get('hasTime') is True], 5, 'source_terminal_error_unverified')
                er1=wait(lambda: next((r for r in rows() if r.get('phase')=='native-error' and
                                      r.get('sessionID')==failed_sid),None),10,'native_r1_error_missing')
                if er1.get('valid') is not True or er1.get('runParts') != 3:
                    raise RuntimeError('native_error_projection_unverified')
                if expect_delivery:
                    wait_for_delivery(webhook, 'opencode_error', 'An error needs your attention')
                provider.mode = 'success'
                print('source-stage: terminal-error-verified', flush=True)
                # A real output-limit terminal stores its error on the native
                # idle row, rather than on the preceding assistant row.
                if expect_delivery:
                    with webhook.lock:
                        webhook.bodies.clear()
                limit_begin = len(rows())
                prior_requests = len(provider.bodies)
                provider.mode = 'output-limit'
                limited = request('/api/session', {'title': 'TEST-source-output-limit',
                                  'location': {'directory': str(paths['project'])}})
                limited_sid = limited['data']['id']
                request('/api/session/' + limited_sid + '/prompt', {'text': 'TEST-source-output-limit'})
                try:
                    request('/api/experimental/session/' + limited_sid + '/wait', method='POST')
                except urllib.error.HTTPError as err:
                    if not 400 <= err.code <= 599:
                        raise
                wait(lambda: any(r.get('type') == 'session.execution.failed' and
                                 r.get('sessionID') == limited_sid and r.get('hasSequence') is True and
                                 r.get('hasTime') is True for r in rows()[limit_begin:]),
                     35, 'source_output_limit_missing')
                if len(provider.bodies) < prior_requests + 3:
                    raise RuntimeError('source_output_limit_provider_unused')
                limit_proof = wait(lambda: next((r for r in rows() if r.get('phase') == 'native-error' and
                                                 r.get('sessionID') == limited_sid), None),
                                   10, 'native_r1_output_limit_missing')
                if (limit_proof.get('valid') is not True or limit_proof.get('runParts') != 3 or
                        limit_proof.get('outputLimitIdle') is not True or
                        limit_proof.get('assistantErrorAbsent') is not True):
                    raise RuntimeError('native_output_limit_projection_unverified')
                if expect_delivery:
                    wait_for_delivery(webhook, 'opencode_error', 'An error needs your attention')
                provider.mode = 'success'
                print('source-stage: output-limit-verified', flush=True)
                desktop_count = verify_macos_desktop(root) if native_app is not None else 0
                if webhook is not None and not expect_delivery:
                    log_file = root / 'host.private.log'
                    if log_file.stat().st_size > 512 * 1024:
                        raise RuntimeError('private_host_log_limit')
                    host_log = log_file.read_bytes().decode('utf-8', errors='replace')
                    if host_log.count('notifications disabled (host runtime not qualified)') != 1:
                        raise RuntimeError('single_denial_diagnostic_missing')
                    time.sleep(.5)  # bounded post-terminal guard against delayed callbacks
                    if webhook.bodies:
                        raise RuntimeError('denied_host_delivered_webhook')
                    if restart_denial:
                        # Two genuine host lifetimes against the SAME managed
                        # plugin, consent scope and private receiver. This
                        # tests fail-closed restart, never delivery eligibility.
                        stop_official_host_tree(host)
                        previous_trace_length = len(rows())
                        host = subprocess.Popen(
                            [str(binary), 'serve', *(['--service'] if service else []), '--hostname', '127.0.0.1',
                             '--port', str(port), '--print-logs'],
                            cwd=paths['project'], env=env, stdout=log, stderr=log,
                            start_new_session=(sys.platform != 'win32'),
                            creationflags=(subprocess.CREATE_NEW_PROCESS_GROUP
                                           if sys.platform == 'win32' else 0))
                        wait(ready, 70, 'restart_host_unavailable')
                        wait(lambda: next((r for r in rows()[previous_trace_length:]
                                          if r.get('phase') == 'loaded'
                                          and r.get('version') == '2.2.6'), None),
                             25, 'restart_plugin_not_loaded')
                        before_restart_requests = len(provider.bodies)
                        new_session = request('/api/session', {
                            'title': 'TEST-source-restart',
                            'location': {'directory': str(paths['project'])}})
                        new_sid = new_session['data']['id']
                        request('/api/session/' + new_sid + '/prompt',
                                {'text': 'TEST-native-source-restart'})
                        request('/api/experimental/session/' + new_sid + '/wait',
                                method='POST')
                        wait(lambda: any(
                            r.get('phase') == 'event' and
                            r.get('type') == 'session.execution.succeeded' and
                            r.get('sessionID') == new_sid
                            for r in rows()[previous_trace_length:]),
                             35, 'restart_source_completion_missing')
                        if len(provider.bodies) <= before_restart_requests:
                            raise RuntimeError('restart_provider_unused')
                        time.sleep(.5)  # Cover late callbacks after the new turn.
                        if webhook.bodies:
                            raise RuntimeError('restart_denied_host_delivered_webhook')
                        log.flush()
                        if log_file.stat().st_size > 512 * 1024:
                            raise RuntimeError('private_host_log_limit')
                        # Exactly one content-free refusal for each lifetime.
                        if log_file.read_bytes().decode('utf-8', errors='replace').count(
                                'notifications disabled (host runtime not qualified)') != 2:
                            raise RuntimeError('restart_denial_diagnostic_missing')
                        print('source-stage: restart-denial-verified', flush=True)
                return {'status': ('passed_delivery_observed' if expect_delivery else
                                   'passed_restart_denied_only' if restart_denial else
                                   'passed_denied_only' if webhook else 'passed_source_only'),
                        'installedRestartDenialObserved': restart_denial,
                        'version': '2.2.6',
                        'upstreamTagCommit': OFFICIAL_TAG_SHA,
                        'officialArchiveSHA256': OFFICIAL_ARCHIVE_HASHES[sys.platform],
                        'officialBinarySHA256': binary_sha,
                        'realNativeProviderTurn': True, 'sourceSubscriberLoaded': True,
                        'sourceEnvelopeShapeObserved': True,
                        'sourceEpochPlausibilityObserved': True,
                        'sameProcessReceiverQ0Observed': True,
                        'productionNativeV2CorrelationR0Observed': True,
                        'nativeV2R0SampleCount': len(native_rows),
                        'productionNativeV2AssistantR1Observed': True,
                        'productionNativeV2QuestionR1Observed': True,
                        'productionNativeV2PermissionR1Observed': True,
                        'productionNativeV2TerminalErrorR1Observed': True,
                        'productionNativeV2OutputLimitR1Observed': True,
                        'sourceToReceiverLagMinMS': min(receiver_lags),
                        'sourceToReceiverLagMaxMS': max(receiver_lags),
                        'sourceToReceiverSampleCount': len(receiver_lags),
                        'independentGoWallT0Observed': go_source_lag_ns is not None,
                        'sourceToGoWallDeltaNS': str(go_source_lag_ns) if go_source_lag_ns is not None else None,
                        'nativeSessionProjectionObserved': True,
                        'sourceQuestionObserved': True, 'sourcePermissionObserved': True,
                        'sourceTerminalErrorObserved': True, 'attentionEventsAreEphemeral': True,
                        'installedDeniedStartup': webhook is not None and not expect_delivery,
                        'installedDeliveryObserved': expect_delivery,
                        'desktopSubmissionAndBannerCount': desktop_count,
                        'observedEventTypes': sorted(observed),
                        'nativeObserverQualified': False, 'nativeClockQualified': False,
                        'notificationDeliveryQualified': False}
            finally:
                stop_official_host_tree(host)
    finally:
        provider.shutdown()
        provider.server_close()
        if webhook is not None:
            webhook.shutdown()
            webhook.server_close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--receiver', type=Path,
                        help='Exact-source AN candidate to install for denial or delivery observation')
    parser.add_argument('--native-app', type=Path,
                        help='Verified signed macOS app; require correlated desktop banner presentation')
    skew = parser.add_mutually_exclusive_group()
    skew.add_argument('--clock-skew-negative', action='store_true',
                      help='Isolated TEST-only +12s host wall shift; assert source epoch mismatch')
    skew.add_argument('--clock-skew-past-negative', action='store_true',
                      help='Isolated TEST-only -12s host wall shift; assert source epoch mismatch')
    parser.add_argument('--restart-denial', action='store_true',
                        help='With installed receiver, restart the official host and prove denial persists')
    parser.add_argument('--expect-delivery', action='store_true',
                        help='Require actual installed delivery for each genuine source event')
    parser.add_argument('--installed-clock-skew', action='store_true',
                        help='With a skew mode and receiver, observe that no webhook arrives')
    parser.add_argument('--skew-after-delivery', action='store_true',
                        help='First require successful delivery in the same host, then apply the skew')
    parser.add_argument('--service', action='store_true',
                        help='Exercise the isolated OpenCode serve --service entry')
    parser.add_argument('--darwin-live-signer', type=Path,
                        help='TEST-only helper to observe the running Darwin process signature')
    parser.add_argument('--mutate-peer-events', action='store_true',
                        help='Test whether another plugin can mutate this observer subscription')
    parser.add_argument('--observe-auto-compaction', action='store_true',
                        help='Observe a real automatic compaction and its final native assistant')
    args = parser.parse_args()
    if args.native_app is not None and (sys.platform != 'darwin' or not args.expect_delivery):
        parser.error('native app requires macOS and delivery mode')
    if args.darwin_live_signer is not None and sys.platform != 'darwin':
        parser.error('live signature observation requires macOS')
    if args.mutate_peer_events and (args.clock_skew_negative or args.clock_skew_past_negative):
        parser.error('peer mutation and clock skew require separate observations')
    if args.observe_auto_compaction and (args.clock_skew_negative or args.clock_skew_past_negative):
        parser.error('compaction and clock skew require separate observations')
    if args.installed_clock_skew and (args.receiver is None or args.restart_denial or
                                      not (args.clock_skew_negative or args.clock_skew_past_negative)):
        parser.error('installed clock skew requires a receiver and a clock-skew mode')
    if args.skew_after_delivery and not args.installed_clock_skew:
        parser.error('skew-after-delivery requires installed clock skew')
    if args.expect_delivery and (args.receiver is None or args.restart_denial or
                                 args.clock_skew_negative or args.clock_skew_past_negative):
        parser.error('delivery requires an installed receiver and excludes denial and clock-skew modes')
    if args.restart_denial and (args.receiver is None or
                                args.clock_skew_negative or args.clock_skew_past_negative):
        parser.error('restart denial requires an installed receiver and no clock-skew mode')
    if (args.clock_skew_negative or args.clock_skew_past_negative) and args.receiver is not None and not args.installed_clock_skew:
        parser.error('clock skew tests and installed receiver are mutually exclusive')
    skew_ms = 12000 if args.clock_skew_negative else -12000 if args.clock_skew_past_negative else 0
    with OwnedTemporaryDirectory(prefix='TEST-opencode-v226-', dir=os.environ.get('RUNNER_TEMP')) as temp:
        try:
            # On macOS the default /var temporary path aliases /private/var.
            # The owned-host boundary deliberately rejects alias paths.
            root = Path(temp).resolve(strict=True)
            prepare_windows_private_root(root)
            result = run(args.archive, root, args.receiver, skew_ms, args.restart_denial, args.expect_delivery, args.native_app, args.skew_after_delivery, args.service, args.darwin_live_signer.resolve(strict=True) if args.darwin_live_signer else None, args.mutate_peer_events, args.observe_auto_compaction)
            result['serviceMode'] = args.service
            result['liveSignerObserved'] = args.darwin_live_signer is not None
            result['peerEventIDTimestampAndSessionIsolationObserved'] = args.mutate_peer_events
            result['autoCompactionObserved'] = args.observe_auto_compaction
        except Exception as error:
            # Never leak native session IDs, paths, model messages or provider errors.
            safe_errors = {'official_archive_unverified', 'official_executable_not_unique',
                           'official_version_command_failed', 'official_version_format_unexpected', 'official_host_exited',
                           'native_startup_timeout', 'source_autoload_missing',
                           'host_capabilities_mismatch', 'private_trace_limit',
                           'native_source_events_missing', 'local_provider_unused', 'native_execution_start_unseen',
                           'native_source_envelope_unverified', 'native_source_epoch_unverified',
                           'windows_private_root_unverified', 'managed_install_command_failed',
                           'managed_plugin_file_missing', 'managed_install_unverified',
                           'managed_webhook_configuration_failed',
                           'private_host_log_limit', 'single_denial_diagnostic_missing',
                           'expected_delivery_missing', 'delivery_payload_invalid',
                           'desktop_receipt_invalid', 'desktop_submissions_missing',
                           'desktop_presentation_log_unavailable', 'desktop_presentations_missing',
                           'skewed_host_delivered_webhook',
                           'denied_host_delivered_webhook', 'restart_host_unavailable',
                           'restart_plugin_not_loaded', 'restart_source_completion_missing',
                           'restart_provider_unused', 'restart_denied_host_delivered_webhook',
                           'restart_denial_diagnostic_missing', 'source_native_question_missing',
                           'source_native_question_unverified', 'source_question_reply_unseen',
                           'source_question_terminal_missing', 'source_native_permission_missing',
                           'source_native_permission_unverified', 'source_permission_reply_unseen',
                           'source_terminal_error_missing', 'source_terminal_error_unverified',
                           'source_terminal_provider_unused', 'source_output_limit_missing',
                           'source_output_limit_provider_unused', 'native_r1_output_limit_missing',
                           'native_output_limit_projection_unverified',
                           'native_session_projection_missing', 'native_session_projection_unverified',
                           'source_receiver_sample_invalid', 'source_receiver_counter_nonmonotonic',
                           'source_go_clock_sample_unavailable', 'source_go_clock_sample_invalid',
                           'source_go_clock_epoch_unverified',
                           'native_reader_projection_unverified',
                           'source_clock_skew_injection_missing', 'source_clock_skew_not_refused',
                           'live_signer_observation_unverified',
                           'peer_subscription_missing', 'peer_stream_isolation_unverified',
                           'source_auto_compaction_missing', 'source_auto_compaction_unverified', 'source_auto_compaction_delivery_missing',
                           'negative_native_reader_projection_unverified',
                           'native_reader_r1_sample_missing', 'native_r1_question_missing',
                           'native_r1_permission_missing', 'native_r1_error_missing',
                           'native_assistant_projection_unverified', 'native_question_projection_unverified',
                           'native_permission_projection_unverified', 'native_error_projection_unverified'}
            reason = str(error) if type(error) is RuntimeError and str(error) in safe_errors else 'unexpected'
            result = {'status': 'unqualified', 'failureType': type(error).__name__, 'failureCode': reason,
                      'nativeObserverQualified': False, 'nativeClockQualified': False,
                      'notificationDeliveryQualified': False}
            args.report.write_text(json.dumps(result) + '\n')
            print(json.dumps(result))
            return 1
    args.report.write_text(json.dumps(result, sort_keys=True) + '\n')
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
