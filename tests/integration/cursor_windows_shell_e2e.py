"""Owned Windows Cursor shell qualification only; no installed AN proof."""
import ast, gzip, hashlib, http.server, importlib.util, io, json, os
import pathlib, struct, subprocess, threading, time, sys, ctypes
from ctypes import wintypes

ROOT = pathlib.Path(os.environ['CURSOR_TEST_ROOT']).resolve()
ROOT.mkdir(parents=True, exist_ok=True)
(ROOT/'home').mkdir()
(ROOT/'workspace').mkdir()
assert os.name == 'nt' and ROOT.name.startswith('TEST-')
SOURCE = pathlib.Path(__file__).resolve().parents[2]/'.github/fixtures/cursor-installed-e/proof-source/native_case.py'
LIMIT = 1048576
spec = importlib.util.spec_from_file_location('wire', pathlib.Path(__file__).with_name('cursor_windows_wire.py'))
w = importlib.util.module_from_spec(spec)
spec.loader.exec_module(w)
# Reuse only the existing pure wire readers, not Linux observer machinery.
tree = ast.parse(SOURCE.read_text())
for node in tree.body:
    if isinstance(node, ast.FunctionDef) and node.name in ('body', 'append'):
        exec(compile(ast.Module(body=[node], type_ignores=[]), str(SOURCE), 'exec'))

profile = ROOT/'home'/'.cursor'
profile.mkdir(exist_ok=True)
observer = ROOT/"TEST observer's argv.py"
capture = ROOT/'native-stop.json'
argv_file = ROOT/'native-argv.json'
observer.write_text('import sys,json,pathlib\np=pathlib.Path('+repr(str(ROOT))+')\nraw=sys.stdin.buffer.read(1048577)\nassert len(raw)<=1048576\n(p/"native-stop.json").write_bytes(raw)\n(p/"native-argv.json").write_text(json.dumps(sys.argv[1:]))\nprint("{}")\n')

expected = ['cursor-event', 'stop', '--binding', str(ROOT/"TEST binding's selector")]
def ps_literal(value):
    assert not any(c in value for c in '\x00\r\n$`')
    return "'"+value.replace("'", "''")+"'"
command = '& '+ ' '.join(ps_literal(v) for v in [sys.executable, str(observer), *expected])
document = {'version': 1, 'hooks': {'stop': [{'command': command}]}}
(profile/'hooks.json').write_text(json.dumps(document))
condition = threading.Condition()
state = {'run': None, 'stop': False, 'close': False, 'sent': False, 'terminal': False, 'error': None}
rows, pending = [], {}
next_sequence = 0
connections = set()
threads = set()
deadline = time.monotonic()+45

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def setup(self):
        super().setup()
        self.connection.settimeout(10)
        with condition:
            connections.add(self.connection)
            threads.add(threading.current_thread())
    def finish(self):
        try: super().finish()
        finally:
            with condition: connections.discard(self.connection)
    def log_message(self, *args): pass
    def answer(self, status, data, content='application/proto'):
        self.send_response(status)
        self.send_header('Content-Type', content)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        self.wfile.flush()
    def chunk(self, data):
        self.wfile.write(hex(len(data))[2:].encode()+b'\r\n'+data+b'\r\n')
        self.wfile.flush()
    def wait_for(self, predicate):
        with condition:
            assert condition.wait_for(lambda:predicate() or state['error'], max(0,deadline-time.monotonic()))
            assert not state['error'], state['error']
    def do_POST(self):
        global next_sequence
        try:
            assert self.headers.get('Authorization') == 'Bearer TEST-loopback-not-a-secret'
            data = body(self)
            with condition:
                rows.append({'path':self.path})
                assert len(rows)<=128
            if self.path in w.unary_responses():
                self.answer(200,w.unary_responses()[self.path]); return
            if self.path == '/aiserver.v1.BidiService/BidiAppend':
                rid, seq, message = append(data)
                logical = w.classify_client_message(message)
                self.answer(200,b'')
                with condition:
                    assert seq not in pending and seq>=next_sequence
                    pending[seq] = (rid,logical)
                    while next_sequence in pending:
                        rid,q = pending.pop(next_sequence); next_sequence+=1
                        rows.append(q)
                        if q['kind']=='run':
                            assert state['run'] is None
                            state['run']={'requestId':rid,**q}
                        elif q['kind']=='stop-result':
                            assert state['sent'] and q['hasStop'] and not q['followup']
                            state['stop']=True
                        elif q['kind']=='exec-close':
                            assert state['stop']; state['close']=True
                        else: assert q['kind']=='heartbeat',q
                    condition.notify_all()
                return
            if self.path == '/agent.v1.AgentService/RunSSE':
                assert len(data)>=5 and struct.unpack('>I',data[1:5])[0]==len(data)-5
                assert data[0] in (0,1)
                if data[0]==1:
                    with gzip.GzipFile(fileobj=io.BytesIO(data[5:])) as compressed:
                        payload=compressed.read(LIMIT+1)
                    assert len(payload)<=LIMIT
                else: payload=data[5:]
                rid=w.first(payload,1).decode()
                self.send_response(200)
                self.send_header('Content-Type','application/connect+proto')
                self.send_header('Transfer-Encoding','chunked'); self.end_headers()
                self.wait_for(lambda:state['run'] is not None)
                run=state['run']; assert run['requestId']==rid
                state['sent']=True
                self.chunk(w.stop_frame(run['conversation'],run['generation']))
                self.wait_for(lambda:state['stop'] and state['close'])
                self.chunk(w.text_frame()); self.chunk(w.terminal_frames())
                self.wfile.write(b'0\r\n\r\n'); self.wfile.flush()
                state['terminal']=True
                return
            self.answer(501,b'{"code":"unimplemented","message":"TEST"}','application/json')
        except Exception as error:
            with condition:
                state['error']=state['error'] or type(error).__name__+':'+str(error)[:200]
                condition.notify_all()

server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
server.daemon_threads=True
serving=threading.Thread(target=server.serve_forever)
agent=pathlib.Path(os.environ['CURSOR_TEST_AGENT']).resolve()
assert agent.is_file() and agent.name=='cursor-agent.cmd'
# A gated TEST-only parent enters the job before launching the genuine CLI.
# No vendor code or installed hook is altered by this containment gate.
class BasicLimits(ctypes.Structure):
    _fields_=[('processTime',ctypes.c_int64),('jobTime',ctypes.c_int64),('flags',wintypes.DWORD),
      ('minWS',ctypes.c_size_t),('maxWS',ctypes.c_size_t),('activeLimit',wintypes.DWORD),
      ('affinity',ctypes.c_size_t),('priority',wintypes.DWORD),('scheduling',wintypes.DWORD)]
class IOCounts(ctypes.Structure):
    _fields_=[(n,ctypes.c_uint64) for n in ('readOps','writeOps','otherOps','readBytes','writeBytes','otherBytes')]
class Limits(ctypes.Structure):
    _fields_=[('basic',BasicLimits),('io',IOCounts),('processMemory',ctypes.c_size_t),
      ('jobMemory',ctypes.c_size_t),('peakProcess',ctypes.c_size_t),('peakJob',ctypes.c_size_t)]
class Accounting(ctypes.Structure):
    _fields_=[(n,ctypes.c_int64) for n in ('user','kernel','periodUser','periodKernel')]+[(n,wintypes.DWORD) for n in ('faults','total','active','terminated')]
kernel=ctypes.WinDLL('kernel32',use_last_error=True)
kernel.CreateJobObjectW.argtypes=[ctypes.c_void_p,wintypes.LPCWSTR]; kernel.CreateJobObjectW.restype=wintypes.HANDLE
kernel.SetInformationJobObject.argtypes=[wintypes.HANDLE,ctypes.c_int,ctypes.c_void_p,wintypes.DWORD]
kernel.AssignProcessToJobObject.argtypes=[wintypes.HANDLE,wintypes.HANDLE]
kernel.QueryInformationJobObject.argtypes=[wintypes.HANDLE,ctypes.c_int,ctypes.c_void_p,wintypes.DWORD,ctypes.c_void_p]
kernel.CloseHandle.argtypes=[wintypes.HANDLE]
job=kernel.CreateJobObjectW(None,None); assert job
limits=Limits(); limits.basic.flags=0x2000
if not kernel.SetInformationJobObject(job,9,ctypes.byref(limits),ctypes.sizeof(limits)):
    kernel.CloseHandle(job); raise ctypes.WinError(ctypes.get_last_error())
serving.start()
argv=[str(agent),'--endpoint','http://127.0.0.1:'+str(server.server_port),
 '--workspace',str(ROOT/'workspace'),'--model','TEST-native-stop','--trust','--print',
 '--output-format','json','TEST deterministic stop mechanics']
assert all(not any(c in v for c in '%!&|<>^\r\n') for v in argv)
gate=ROOT/'TEST-gate.cmd'
gate.write_text('@echo off\r\necho TEST_GATE_READY\r\nset /p TEST_GATE=\r\necho TEST_NATIVE_BEGIN\r\ncall '+subprocess.list2cmdline(argv)+'\r\nset TEST_EXIT=%ERRORLEVEL%\r\necho TEST_NATIVE_END\r\nexit /b %TEST_EXIT%\r\n')
# The fresh Windows home/profile/appdata contains no user credentials.
env={k:os.environ[k] for k in ('SystemRoot','WINDIR','COMSPEC','PATH','PATHEXT') if k in os.environ}
(ROOT/'localappdata').mkdir()
(ROOT/'appdata').mkdir()
env.update(HOME=str(ROOT/'home'),USERPROFILE=str(ROOT/'home'),LOCALAPPDATA=str(ROOT/'localappdata'),
 APPDATA=str(ROOT/'appdata'),TEMP=str(ROOT),TMP=str(ROOT),CI='1',
 AGENT_CLI_CREDENTIAL_STORE='file',CURSOR_AUTH_TOKEN='TEST-loopback-not-a-secret')
try:
    proc=subprocess.Popen([os.environ['COMSPEC'],'/d','/s','/c',str(gate)],env=env,cwd=ROOT/'workspace',
     stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
except BaseException:
    kernel.CloseHandle(job); server.shutdown(); server.server_close(); serving.join(timeout=5)
    raise
try:
    assert kernel.AssignProcessToJobObject(job,wintypes.HANDLE(int(proc._handle)))
except BaseException:
    proc.kill(); proc.wait(timeout=5); kernel.CloseHandle(job)
    server.shutdown(); server.server_close(); serving.join(timeout=5)
    raise
forced=False
try:
    out,err=proc.communicate(input=b"start\r\n", timeout=max(0.1,deadline-time.monotonic()))
except subprocess.TimeoutExpired:
    forced=True
    kernel.CloseHandle(job); job=None
    out,err=proc.communicate(timeout=5)
finally:
    server.shutdown(); server.server_close(); serving.join(timeout=5)
    with condition:
        for connection in connections.copy():
            try: connection.shutdown(2)
            except OSError: pass
            connection.close()
    for thread in threads: thread.join(timeout=5)
    accounting=Accounting()
    owned_active=-1
    if job:
        assert kernel.QueryInformationJobObject(job,1,ctypes.byref(accounting),ctypes.sizeof(accounting),None)
        owned_active=accounting.active
        kernel.CloseHandle(job)

assert hashlib.sha256(agent.with_name('index.js').read_bytes()).hexdigest()=='0e7e28c34e050c6b83fe574e1fefa8d95d7c80bea6c4a4d3ae2726e5191243ef', 'unqualified Windows CLI source'
(ROOT/'cli-stdout.txt').write_bytes(out)
(ROOT/'cli-stderr.txt').write_bytes(err)
result={'exit':proc.returncode,'forcedCleanup':forced,'state':state,'protocol':rows,
        'threadsJoined':not serving.is_alive() and all(not t.is_alive() for t in threads),
        'argvMatches':argv_file.exists() and json.loads(argv_file.read_text())==expected,
        'hookExists':capture.exists(),'installedANProof':False,
        'ownedActiveProcesses':owned_active,
        'indexSHA256':hashlib.sha256(agent.with_name('index.js').read_bytes()).hexdigest()}
if capture.exists():
    event=json.loads(capture.read_bytes()); result['event']=event
    result['stopMatches']=event.get('hook_event_name')=='stop' and event.get('status')=='completed' and event.get('conversation_id')==state['run']['conversation'] and event.get('generation_id')==state['run']['generation']
result['pass']=owned_active==0 and proc.returncode==0 and not forced and state['terminal'] and not state['error'] and result['argvMatches'] and result.get('stopMatches') and result['threadsJoined']
(ROOT/'native-shell-result.json').write_text(json.dumps(result,indent=2))
print(json.dumps(result,indent=2))
raise SystemExit(0 if result['pass'] else 1)

assert result['pass'], 'Windows native shell qualification failed; not installed AN E2E'
