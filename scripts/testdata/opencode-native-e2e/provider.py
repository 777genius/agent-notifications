"""Private deterministic provider and bounded HTTP helpers adapted from P0.
No replacement product plugin, reducer, qualified profile or event injection.
"""
import hashlib, hmac, json, pathlib, socket, threading, time, urllib.request, urllib.error, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
SCENARIOS = ('completion', 'form', 'permission', 'error')
ALL_SCENARIOS = SCENARIOS + ('retry', 'interrupt')
SAFE = {'question','bash','shell','pending','answered','cancelled','running','idle','failed','completed','succeeded','interrupted','user','assistant','tool','text','once','always','reject'}

def redact(value, key, name='', depth=0):
    if depth>12: return '[depth-limit]'
    if value is None or isinstance(value,(bool,int,float)): return value
    if isinstance(value,str):
        if name=='type' and len(value)<97 and all(c.isalnum() or c in '._-' for c in value): return value
        if value in SAFE: return value
        return 'h:'+hmac.new(key,value.encode(),hashlib.sha256).hexdigest()[:24]
    if isinstance(value,list): return [redact(v,key,name,depth+1) for v in value[:48]]
    if isinstance(value,dict): return {k:redact(v,key,k,depth+1) for k,v in list(value.items())[:96]}
    return '[unsupported]'

class OwnedHTTPServer(ThreadingHTTPServer):
    """Own accepted sockets too: partial headers/bodies must not trap close()."""
    daemon_threads = False
    block_on_close = True
    def __init__(self, address, handler):
        self.connections, self.connections_lock = set(), threading.Lock()
        super().__init__(address, handler)
    def get_request(self):
        connection, address = super().get_request()
        connection.settimeout(8)  # Existing P0 transport bound, not settlement.
        with self.connections_lock: self.connections.add(connection)
        return connection, address
    def close_request(self, connection):
        with self.connections_lock: self.connections.discard(connection)
        super().close_request(connection)
    def server_close(self):
        # Owned.shutdown() stops accepting before this call. Wake blocked request
        # readers before ThreadingMixIn joins every non-daemon handler.
        with self.connections_lock: connections = tuple(self.connections)
        for connection in connections:
            try: connection.shutdown(socket.SHUT_RDWR)
            except OSError: pass
            connection.close()
        super().server_close()

class Provider(OwnedHTTPServer):
    def __init__(self, root, key):
        super().__init__(('127.0.0.1',0), ProviderHandler)
        self.root, self.key = root, key
        self.records, self.gaps = [], []
        self.lock = threading.Lock()
        self.attempts = {}
        self.interrupt_started = threading.Event()
        self.interrupt_release = threading.Event()
        self.interrupt_hold_expired = False
    def record(self, obj):
        with self.lock:
            model=obj['model']
            self.attempts[model]=self.attempts.get(model,0)+1
            obj['attempt']=self.attempts[model]
            self.records.append(obj)
            with (self.root/'provider-redacted.jsonl').open('a') as out: out.write(json.dumps(redact(obj,self.key))+'\n')
            return obj['attempt']

class ProviderHandler(BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def do_POST(self):
        size = int(self.headers.get('Content-Length','0'))
        if not 0<=size<=1024*1024 or self.path!='/v1/chat/completions': self.send_error(400); return
        body = self.rfile.read(size)
        if len(body)!=size: self.close_connection=True; return
        request = json.loads(body)
        model = request.get('model','')
        scenario = model.removeprefix('p0-')
        if scenario not in ALL_SCENARIOS: self.send_error(400); return
        attempt=self.server.record({'model':model,'stream':request.get('stream',False),'toolSchemas':request.get('tools',[]),'messageRoles':[x.get('role') for x in request.get('messages',[])]})
        if len(self.server.records)>32: self.send_error(429); return
        if scenario=='error':
            data=json.dumps({'error':{'message':'P0 intentional permanent error','type':'invalid_request_error'}}).encode()
            self.send_response(400); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data); return
        if scenario=='retry' and attempt==1:
            # Exactly one real transient provider failure. The native host owns
            # any retry decision; the harness never retries a prompt/session.
            body=json.dumps({'error':{'message':'P0 intentional service unavailable','type':'server_error'}}).encode()
            self.send_response(503); self.send_header('Content-Type','application/json'); self.send_header('Retry-After','0.1'); self.send_header('Content-Length',str(len(body))); self.end_headers(); self.wfile.write(body); return
        if scenario=='interrupt':
            if not request.get('stream') or attempt!=1:
                with self.server.lock: self.server.gaps.append({'scenario':scenario,'reason':'stream_or_single_request_required'})
                self.send_error(400); return
            self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
            chunk={'id':'chatcmpl-p0','object':'chat.completion.chunk','created':int(time.time()),'model':model,'choices':[{'index':0,'delta':{'role':'assistant','content':'P0 ongoing stream.'},'finish_reason':None}]}
            try:
                self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode()); self.wfile.flush()
                self.server.interrupt_started.set()
                if not self.server.interrupt_release.wait(timeout=12): self.server.interrupt_hold_expired=True
            except (BrokenPipeError,ConnectionResetError): pass
            # Do not send stop/[DONE]. Release follows the real abort API reply
            # or fixture cleanup; an expired hold is a gap, never success.
            self.close_connection=True
            return
        call=None
        seen_tool=any(x.get('role')=='tool' for x in request.get('messages',[]))
        if scenario in ('form','permission') and not seen_tool:
            # Select only actually declared native tools. No event injection or arbitrary shell tool.
            tools=[x.get('function',{}) for x in request.get('tools',[]) if x.get('type')=='function']
            if scenario=='form':
                tool=next((x for x in tools if x.get('name')=='question' and 'questions' in x.get('parameters',{}).get('properties',{})),None)
                args={'questions':[{'question':'P0 test choice?','header':'P0','options':[{'label':'Yes','description':'Test choice'}]}]}
            else:
                tool=next((x for x in tools if x.get('name') in ('bash','shell') and 'command' in x.get('parameters',{}).get('properties',{})),None)
                args={'command':'printf P0_OWNED_TEST'}
                # V2 shell has no description field; V1 may require one.
                if tool and 'description' in tool.get('parameters',{}).get('properties',{}):
                    args['description']='P0 sandbox permission probe'
            if tool:
                params=tool.get('parameters',{})
                if not set(params.get('required',[])).issubset(args) or (params.get('additionalProperties') is False and not set(args).issubset(params.get('properties',{}))): tool=None
            if tool:
                call={'index':0,'id':'call_p0_'+scenario,'type':'function','function':{'name':tool['name'],'arguments':json.dumps(args)}}
            else:
                with self.server.lock: self.server.gaps.append({'scenario':scenario,'reason':'qualified_tool_schema_missing'})
        if request.get('stream'):
            self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
            sequence=[({'role':'assistant'},None)]
            sequence += [({'tool_calls':[call]},None),({},'tool_calls')] if call else [({'content':'P0 deterministic completion.'},None),({},'stop')]
            try:
                for delta,finish in sequence:
                    chunk={'id':'chatcmpl-p0','object':'chat.completion.chunk','created':int(time.time()),'model':model,'choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
                    self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode()); self.wfile.flush()
                self.wfile.write(b'data: [DONE]\n\n'); self.wfile.flush()
            except (BrokenPipeError,ConnectionResetError): pass
        else:
            message={'role':'assistant','content':None,'tool_calls':[{k:v for k,v in call.items() if k!='index'}]} if call else {'role':'assistant','content':'P0 deterministic completion.'}
            data=json.dumps({'id':'chatcmpl-p0','object':'chat.completion','created':int(time.time()),'model':model,'choices':[{'index':0,'message':message,'finish_reason':'tool_calls' if call else 'stop'}],'usage':{'prompt_tokens':10,'completion_tokens':6,'total_tokens':16}}).encode()
            self.send_response(200); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data)

def request(base, project, path, payload=None, method=None, v2=False, timeout=8, auth_headers=None):
    url=base+path
    if not v2: url+= ('&' if '?' in url else '?')+'directory='+urllib.parse.quote(str(project),safe='')
    data=json.dumps(payload).encode() if payload is not None else None
    req=urllib.request.Request(url,data=data,method=method or ('POST' if data is not None else 'GET'),headers={'Content-Type':'application/json',**(auth_headers or {})})
    # Disable ambient proxy routing and redirects away from the explicit loopback URL.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args): raise RuntimeError('host redirect rejected')
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
    with opener.open(req,timeout=timeout) as response:
        body=response.read(1024*1024+1)
        if len(body)>1024*1024: raise RuntimeError('host response limit')
        return json.loads(body) if body else None

def data(result): return result.get('data',result) if isinstance(result,dict) else result
