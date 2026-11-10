#!/usr/bin/env python3
"""Observe real OpenCode 2.2.6 V2 source events, without granting notification eligibility.

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
}
OFFICIAL_TAG_SHA = "dc2ffcf8bf26f9e669b6aa22b95f50e12be15e06"
TRACE_PLUGIN = r'''import {appendFileSync} from 'node:fs';
import {createNativeV2} from '../test-support/native-v2.mjs';
import {spawnSync} from 'node:child_process';
const write=value=>appendFileSync(process.env.AN_TEST_TRACE,JSON.stringify(value)+'\n');
export default {id:'test-oc226-source',async setup(ctx){
 let view;
 const nativeSessions=new Map();
 try{ view=createNativeV2(ctx,ctx.location,()=>{},()=>{write({phase:'native-uncertain'});}); }
 catch{ write({phase:'native-constructor-denied'}); }
 // TEST-only adversarial +12s wall-clock modification in the official process.
 // No production module is changed and no notification grant can be created.
 const clockSkewTest=process.env.AN_TEST_CLOCK_SKEW==='12000';
 if(clockSkewTest){const sourceNow=Date.now.bind(Date);Date.now=()=>sourceNow()+12000;}
 write({phase:'loaded',version:ctx.app?.version,clockSkewTest,
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
   const receivedWallMs=Date.now(), receivedMonoNS=process.hrtime.bigint().toString();
   // An independent Go wall/boot sample is requested only in the isolated
   // installed-denial TEST. This is cross-process T0 observation, not trust.
   let goClock;
   if(entry.type==='session.execution.succeeded'&&process.env.AN_TEST_GO_CLOCK){
     try{
       const p=spawnSync(process.env.AN_TEST_GO_CLOCK,['opencode-clock','--protocol','1'],
         {timeout:2000,maxBuffer:1024,env:{PATH:'/usr/bin:/bin'}});
       if(!p.error&&p.status===0&&p.stdout?.length>0&&p.stdout.length<=1024){
         const g=JSON.parse(p.stdout.toString('utf8'));
         if(g.protocol===1&&Object.keys(g).join('|')===
            'protocol|boot|clockDomain|clockKind|monoLoNs|monoHiNs|wallUnixNs|uncertaintyNs')
           goClock={boot:g.boot,domain:g.clockDomain,kind:g.clockKind,
             loNS:g.monoLoNs,hiNS:g.monoHiNs,wallUnixNS:g.wallUnixNs,uncertaintyNS:g.uncertaintyNs};
       }
     }catch{}
   }
   write({phase:'event',type:entry.type,sessionID:e.sessionID??e.form?.sessionID,hasData:typeof e==='object'&&e!==null,
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
       write({phase:'native-error',sessionID:e.sessionID,valid:proof?.final===true&&proof?.outcome==='error',
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
 return ()=>{controller.abort();write({phase:'cleanup'});};
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


def official_binary(archive, target):
    system = sys.platform
    expected = OFFICIAL_ARCHIVE_HASHES.get(system)
    if expected is None or not archive.is_file() or not 0 < archive.stat().st_size <= 160 * 1024 * 1024 or digest(archive) != expected:
        raise RuntimeError('official_archive_unverified')
    if system == 'darwin':
        with zipfile.ZipFile(archive) as packed:
            files = [m for m in packed.infolist() if not m.is_dir() and Path(m.filename).name == 'opencode']
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
    target.chmod(0o700)
    return digest(target)

def run(archive, root, receiver=None, clock_skew_negative=False):
    spec = importlib.util.spec_from_file_location('existing_native_provider',
        Path(__file__).resolve().parent / 'opencode-semantic-e2e.py')
    native = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(native)
    binary = root / 'opencode'
    binary_sha = official_binary(archive, binary)
    print('source-stage: archive-verified', flush=True)
    names = dict(home='home', config='config', project='TEST-project', data='data',
                 cache='cache', state='state', runtime='runtime', temp='temp', bun='bun')
    paths = {k: root / v for k, v in names.items()}
    for directory in paths.values():
        directory.mkdir(mode=0o700)
    subprocess.run(['git', 'init', '-q', str(paths['project'])], check=True, timeout=10)
    plugins = paths['config'] / 'plugins'
    plugins.mkdir(mode=0o700)
    test_support = paths['config'] / 'test-support'
    test_support.mkdir(mode=0o700)
    (test_support / 'native-v2.mjs').write_bytes((Path(__file__).resolve().parent.parent / 'opencode-plugin/native-v2.mjs').read_bytes())
    (plugins / 'test-oc226-source.js').write_text(TRACE_PLUGIN, encoding='utf-8')
    trace = root / 'native-events.private.jsonl'
    env = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': str(paths['home']),
           'LANG': 'C.UTF-8', 'TERM': 'dumb', 'XDG_CONFIG_HOME': str(paths['config']),
           'XDG_DATA_HOME': str(paths['data']), 'XDG_CACHE_HOME': str(paths['cache']),
           'XDG_STATE_HOME': str(paths['state']), 'XDG_RUNTIME_DIR': str(paths['runtime']),
           'BUN_INSTALL_CACHE_DIR': str(paths['bun']), 'TMPDIR': str(paths['temp']),
           'OPENCODE_CONFIG_DIR': str(paths['config']), 'AN_TEST_TRACE': str(trace),
           **({'AN_TEST_CLOCK_SKEW':'12000'} if clock_skew_negative else {}),
           'OPENCODE_DISABLE_AUTOUPDATE': 'true',
           'OPENCODE_DISABLE_DEFAULT_PLUGINS': 'true',
           'OPENCODE_DISABLE_MODELS_FETCH': 'true',
           'OPENCODE_DISABLE_SHARE': 'true',
           'OPENCODE_PASSWORD': 'test-only', 'OPENCODE_SERVER_PASSWORD': 'test-only'}
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
            result = subprocess.run(command, cwd=paths['project'], env=env,
                                    capture_output=True, text=True, timeout=60)
            if result.returncode or not (plugins / 'agent-notifications.js').is_file():
                raise RuntimeError('managed_install_unverified')
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        with (root / 'host.private.log').open('wb') as log:
            host = subprocess.Popen([str(binary), 'serve', '--hostname', '127.0.0.1',
                                     '--port', str(port), '--print-logs'],
                                    cwd=paths['project'], env=env, stdout=log, stderr=log,
                                    start_new_session=True)
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
                loaded = wait(lambda: next((r for r in rows() if r.get('phase') == 'loaded'), None),
                              25, 'source_autoload_missing')
                print('source-stage: plugin-loaded', flush=True)
                capabilities = ('subscribe', 'sessionGet', 'sessionContext', 'permissionGet',
                                'permissionList', 'rpcRegister')
                if loaded.get('version') != '2.2.6' or any(loaded.get(k) is not True for k in capabilities):
                    raise RuntimeError('host_capabilities_mismatch')
                if loaded.get('clockSkewTest') is not clock_skew_negative:
                    raise RuntimeError('source_clock_skew_injection_missing')
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
                if clock_skew_negative:
                    # The independent runner wall clock rejects the original
                    # host envelope after the deliberate 12s future shift.
                    # Check native correlator continues to observe real events
                    # without mistaking R0 observation for business authority.
                    if not all(r['createdMs'] > source_wall_end_ms + 5000 for r in source_rows):
                        raise RuntimeError('source_clock_skew_not_refused')
                    negative_native = [r for r in rows() if r.get('phase') == 'native-projection' and r.get('sessionID') == sid]
                    if not all(any(r.get('type') == kind and r.get('observedSequence') for r in negative_native)
                               for kind in terminal):
                        raise RuntimeError('negative_native_reader_projection_unverified')
                    return {'status':'passed_clock_skew_negative_only','version':'2.2.6',
                            'clockSkewTestMS':12000,'sourceFutureTimestampRejected':True,
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
                    if not isinstance(go, dict) or set(go) != {'boot', 'domain', 'kind', 'loNS', 'hiNS', 'wallUnixNS', 'uncertaintyNS'} or \
                       not isinstance(go['boot'], str) or not isinstance(go['domain'], str) or \
                       go['kind'] not in {'linux-boottime', 'darwin-monotonic-raw'} or \
                       any(not isinstance(go.get(key), str) or not re.fullmatch(r'[1-9][0-9]*', go[key])
                           for key in ('loNS', 'hiNS', 'wallUnixNS', 'uncertaintyNS')):
                        raise RuntimeError('source_go_clock_sample_unavailable')
                    if int(go['hiNS']) < int(go['loNS']) or int(go['uncertaintyNS']) < 0:
                        raise RuntimeError('source_go_clock_sample_invalid')
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
                provider.mode = 'success'
                print('source-stage: terminal-error-verified', flush=True)
                if webhook is not None:
                    log_file = root / 'host.private.log'
                    if log_file.stat().st_size > 512 * 1024:
                        raise RuntimeError('private_host_log_limit')
                    host_log = log_file.read_bytes().decode('utf-8', errors='replace')
                    if host_log.count('notifications disabled (host runtime not qualified)') != 1:
                        raise RuntimeError('single_denial_diagnostic_missing')
                    time.sleep(.5)  # bounded post-terminal guard against delayed callbacks
                    if webhook.bodies:
                        raise RuntimeError('denied_host_delivered_webhook')
                return {'status': 'passed_denied_only' if webhook else 'passed_source_only', 'version': '2.2.6',
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
                        'sourceToReceiverLagMinMS': min(receiver_lags),
                        'sourceToReceiverLagMaxMS': max(receiver_lags),
                        'sourceToReceiverSampleCount': len(receiver_lags),
                        'independentGoWallT0Observed': go_source_lag_ns is not None,
                        'sourceToGoWallDeltaNS': str(go_source_lag_ns) if go_source_lag_ns is not None else None,
                        'nativeSessionProjectionObserved': True,
                        'sourceQuestionObserved': True, 'sourcePermissionObserved': True,
                        'sourceTerminalErrorObserved': True, 'attentionEventsAreEphemeral': True,
                        'installedDeniedStartup': webhook is not None,
                        'observedEventTypes': sorted(observed),
                        'nativeObserverQualified': False, 'nativeClockQualified': False,
                        'notificationDeliveryQualified': False}
            finally:
                if host.poll() is None:
                    os.killpg(host.pid, signal.SIGTERM)
                    try:
                        host.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(host.pid, signal.SIGKILL)
                        host.wait(timeout=5)
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
                        help='Exact-source unreleased AN candidate; prove fail-closed denial only')
    parser.add_argument('--clock-skew-negative', action='store_true',
                        help='Isolated TEST-only +12s host wall shift; require source epoch refusal')
    args = parser.parse_args()
    if args.clock_skew_negative and args.receiver is not None:
        parser.error('clock-skew-negative and installed receiver are mutually exclusive')
    with tempfile.TemporaryDirectory(prefix='TEST-opencode-v226-', dir=os.environ.get('RUNNER_TEMP')) as temp:
        try:
            result = run(args.archive, Path(temp), args.receiver, args.clock_skew_negative)
        except Exception as error:
            # Never leak native session IDs, paths, model messages or provider errors.
            safe_errors = {'official_archive_unverified', 'official_executable_not_unique',
                           'official_version_command_failed', 'official_version_format_unexpected', 'official_host_exited',
                           'native_startup_timeout', 'source_autoload_missing',
                           'host_capabilities_mismatch', 'private_trace_limit',
                           'native_source_events_missing', 'local_provider_unused', 'native_execution_start_unseen',
                           'native_source_envelope_unverified', 'native_source_epoch_unverified', 'managed_install_unverified',
                           'private_host_log_limit', 'single_denial_diagnostic_missing',
                           'denied_host_delivered_webhook', 'source_native_question_missing',
                           'source_native_question_unverified', 'source_question_reply_unseen',
                           'source_question_terminal_missing', 'source_native_permission_missing',
                           'source_native_permission_unverified', 'source_permission_reply_unseen',
                           'source_terminal_error_missing', 'source_terminal_error_unverified',
                           'source_terminal_provider_unused',
                           'native_session_projection_missing', 'native_session_projection_unverified',
                           'source_receiver_sample_invalid', 'source_receiver_counter_nonmonotonic',
                           'source_go_clock_sample_unavailable', 'source_go_clock_sample_invalid',
                           'source_go_clock_epoch_unverified',
                           'native_reader_projection_unverified',
                           'source_clock_skew_injection_missing', 'source_clock_skew_not_refused',
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
