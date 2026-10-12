"""Installed Windows Cursor lifecycle with genuine OS submission, no GUI claim."""
import atexit, ast, base64, hashlib, http.server, json, os, pathlib
import subprocess, threading, time, uuid

REPO = pathlib.Path(__file__).resolve().parents[2]

def prepare(root, agent):
    # Reuse the release E2E's exact TEST-root ACL preparation.
    source = REPO/'scripts/release-artifact-e2e.py'
    namespace = {'os': os, 'subprocess': subprocess, 'Path': pathlib.Path}
    for node in ast.parse(source.read_text()).body:
        if isinstance(node, ast.Assign) and any(isinstance(t, ast.Name) and t.id == '_WINDOWS_PRIVATE_ROOT_SCRIPT' for t in node.targets):
            exec(compile(ast.Module(body=[node], type_ignores=[]), str(source), 'exec'), namespace)
        if isinstance(node, ast.FunctionDef) and node.name == '_windows_private_scratch_root':
            exec(compile(ast.Module(body=[node], type_ignores=[]), str(source), 'exec'), namespace)
    namespace['_windows_private_scratch_root'](root)
    binary = root/'stage/claude-notifications-windows-amd64.exe'
    package = root/'agent-notify-portable-windows-amd64.zip'
    assert binary.is_file() and package.is_file()
    target = root/'home/.local/bin'; target.mkdir(parents=True)
    control = root/'home/.config/agent-notifications'
    cfg = root/'home/TEST-config.json'
    env = {k: os.environ[k] for k in ('SystemRoot','WINDIR','COMSPEC','PATH','PATHEXT') if k in os.environ}
    for name, directory in [('HOME','home'),('USERPROFILE','home'),('LOCALAPPDATA','localappdata'),('APPDATA','appdata'),('TEMP','tmp'),('TMP','tmp')]:
        path = root/directory; path.mkdir(exist_ok=True)
        env[name] = str(path)
    env.update(CI='1', AGENT_CLI_CREDENTIAL_STORE='file', AGENT_NOTIFICATIONS_CONFIG=str(cfg))
    results = []
    primary = binary
    def cli(name, args, data=None):
        p = subprocess.run([str(primary), *args], env=env, cwd=root/'workspace', input=data, capture_output=True, timeout=45)
        (root/(name+'.stdout')).write_bytes(p.stdout)
        (root/(name+'.stderr')).write_bytes(p.stderr)
        results.append({'name': name, 'exit': p.returncode})
        assert p.returncode == 0, (name, p.returncode, p.stdout[-2000:], p.stderr[-2000:])
        return p.stdout
    cli('runtime-install', ['internal-install-runtime','--stage',str(binary.parent),'--entry',binary.name,'--target',str(target),'--control-root',str(control)])
    primary = target/binary.name
    assert hashlib.sha256(primary.read_bytes()).digest() == hashlib.sha256(binary.read_bytes()).digest()
    cli('config-init', ['config','init','--json'])
    (control/'agent-notifications.json').write_text('{"schemaVersion":1,"enabled":false}\n')
    records = []
    class Sink(http.server.BaseHTTPRequestHandler):
        def log_message(self,*a): pass
        def do_POST(self):
            assert self.path == '/TEST-cursor-stop'
            size = int(self.headers['Content-Length']); assert 0 < size <= 1048576
            records.append(json.loads(self.rfile.read(size)))
            self.send_response(200); self.send_header('Content-Length','0'); self.end_headers()
    sink = http.server.HTTPServer(('127.0.0.1',0),Sink)
    thread = threading.Thread(target=sink.serve_forever); thread.start()
    def close_sink():
        sink.shutdown(); sink.server_close(); thread.join(timeout=5)
        assert not thread.is_alive()
    atexit.register(close_sink)
    inspection = json.loads(cli('config-inspect',['config','inspect','--json']))
    edits = {'set':{'/notifications/desktop/enabled':True,'/notifications/webhook/enabled':True,
        '/notifications/webhook/url':'http://127.0.0.1:'+str(sink.server_port)+'/TEST-cursor-stop',
        '/notifications/webhook/preset':'custom','/notifications/webhook/format':'json',
        '/statuses/agent_stopping/enabled':True,'/statuses/agent_stopping/desktop/enabled':True,
        '/statuses/agent_stopping/webhook/enabled':True}}
    cli('config-edit',['config','edit','--stdin','--expect-revision',inspection['revision']],json.dumps(edits).encode())
    common = ['--scope-root',str(root/'home/.cursor'),'--client-executable',str(agent),'--control-root',str(control)]
    def install(name, enabled):
        intent = primary.parent/(name+'-intent.json')
        channels = ['--products','cursor','--agent-notify'] + (['--desktop','--webhook'] if enabled else [])
        cli(name+'-prepare',['setup-products','prepare',*channels,*common,'--intent-file',str(intent)])
        frozen = json.loads(intent.read_bytes())
        scopes = {k:base64.b64decode(v,validate=True).decode() for k,v in frozen['scopes'].items()}
        assert frozen['provenance']['SHA256'] == hashlib.sha256(primary.read_bytes()).hexdigest()
        args = ['setup-notifications','wizard','--action','install','--install-or-update','--agents','cursor','--hooks','false','--agent-notify','true','--yes','--json','--package',str(package),'--bootstrap-intent-file',str(intent)]
        for key in ['control-root','runtime-root','global-config','scope-root','client-executable']:
            args += ['--'+key,scopes[key]]
        cli(name,args)
        policy = json.loads((control/'agent-notifications.json').read_bytes())
        assert policy['route']['cursorNotifications'] == {'desktop':enabled,'webhook':enabled}
    install('cursor-enable',True)
    hooks = root/'home/.cursor/hooks.json'
    installed = hooks.read_bytes()
    entries = json.loads(installed)['hooks']['stop']
    assert len(entries)==2, entries
    selected = [e for e in entries if str(primary) in e['command']]
    assert len(selected)==1
    # Owned hook must name the real installed binary and SDK-owned selector.
    command = selected[0]['command']
    assert command.startswith('& ')
    marker = " '--binding' '"; assert marker in command
    selector = command.split(marker,1)[1][:-1].replace("''", "'")
    assert pathlib.Path(selector).is_file()
    watcher_env = {**env, 'CURSOR_TEST_ROOT':str(root)}
    watcher = subprocess.Popen([str(pathlib.Path(os.environ['SystemRoot'])/'System32/WindowsPowerShell/v1.0/powershell.exe'),
        '-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',str(pathlib.Path(__file__).with_name('cursor_windows_toast_watch.ps1'))],
        env=watcher_env, cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    def close_watcher():
        (root/'toast-watch-done').write_text('done')
        out, err = b'', b''
        try:
            out, err = watcher.communicate(timeout=15)
        except subprocess.TimeoutExpired:
            watcher.kill(); out,err=watcher.communicate(timeout=5)
            raise AssertionError('TEST watcher did not exit naturally')
        finally:
            (root/'toast-watch.stdout').write_bytes(out)
            (root/'toast-watch.stderr').write_bytes(err)
        assert watcher.returncode==0, err
    atexit.register(close_watcher)
    limit = time.monotonic()+15
    while not (root/'toast-watch-ready').exists() and time.monotonic()<limit and watcher.poll() is None:
        time.sleep(.05)
    assert (root/'toast-watch-ready').exists(), 'OS process watcher not ready'
    return {'root':root,'primary':primary,'env':env,'cli':cli,'install':install,'common':common,
            'records':records,'results':results,'hooks':hooks,'installed':installed,'selector':selector,
            'close':close_sink,'close_watcher':close_watcher}

def finish(test, native):
    root = test['root']; cli = test['cli']
    assert test['hooks'].read_bytes() == test['installed']
    records = test['records']
    assert len(records)==1, records
    expected = {'notification_type':'agent_stopping','status':'agent_stopping','agent_source':'cursor',
                'message':'Cursor CLI is stopping','title':'Cursor CLI','source':'claude-notifications'}
    assert all(records[0].get(k)==v for k,v in expected.items()), records
    payload = (root/'native-stop.json').read_bytes()
    cli('cursor-replay',['cursor-event','stop','--binding',test['selector']],payload)
    assert len(records)==1, 'duplicate webhook'
    test['install']('cursor-update',True)
    cli('cursor-replay-after-update',['cursor-event','stop','--binding',test['selector']],payload)
    assert len(records)==1, 'update lost duplicate guard'
    test['install']('cursor-disable',False)
    disabled = json.loads(payload); disabled['generation_id']=str(uuid.uuid4())
    cli('cursor-disabled-event',['cursor-event','stop','--binding',test['selector']],json.dumps(disabled).encode())
    assert len(records)==1, 'disabled webhook'
    cli('cursor-uninstall',['setup-notifications','wizard','--action','uninstall','--agents','cursor','--hooks','false','--agent-notify','true',*test['common'],'--yes','--json'])
    remaining = json.loads(test['hooks'].read_bytes())['hooks']['stop']
    assert len(remaining)==1 and str(test['primary']) not in remaining[0]['command'], 'foreign Stop changed'
    assert not pathlib.Path(test['selector']).exists(), 'locator remains active'
    test['close_watcher'](); atexit.unregister(test['close_watcher'])
    toast = json.loads((root/'toast-processes.json').read_text(encoding='utf-8-sig'))
    starts = [r for r in toast if r['kind']=='start']; stops = [r for r in toast if r['kind']=='stop']
    assert len(starts)==len(stops)==1 and starts[0]['pid']==stops[0]['pid'] and stops[0]['exit']==0, toast
    test['close'](); atexit.unregister(test['close'])
    result = {'native':native,'lifecycle':test['results'],'webhooks':records,'toastOSSubmission':toast,'visibleToast':'NOT_RUN',
              'installedBinarySHA256':hashlib.sha256(test['primary'].read_bytes()).hexdigest()}
    (root/'installed-result.json').write_text(json.dumps(result,indent=2))
    return result
