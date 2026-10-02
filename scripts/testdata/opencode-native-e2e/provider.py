"""Private deterministic provider and bounded HTTP helpers adapted from P0.
No replacement product plugin, reducer, qualified profile or event injection.
"""
import hashlib, hmac, json, pathlib, re, socket, threading, time, urllib.request, urllib.error, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
SCENARIOS = ('completion', 'form', 'permission', 'error')
ALL_SCENARIOS = SCENARIOS + ('retry', 'interrupt', 'compaction', 'child')
SAFE = {'question','bash','shell','pending','answered','cancelled','running','idle','failed','completed','succeeded','interrupted','user','assistant','tool','text','once','always','reject','manual','compaction'}
CHILD_AGENT = 'an-e2e-child'
CHILD_PROMPT = 'AN_TEST_CHILD_REPLY_ONLY. Return a short text answer. Do not call tools.'
CHILD_CALL = 'call_an_test_child'
SUMMARY = '''## Objective
- Continue the private TEST conversation.
## Requirements
- Use only the private TEST conversation.
## Decisions
- (none)
## Work State
### Completed
- The previous text turn completed.
### Active
- (none)
### Blocked
- (none)
## Next Move
1. Await the next user turn.
## Relevant Files
- (none)
## Important Context
- (none)
'''
# V1 core compaction.ts:16-46 has a different inline template from V2.
V1_SUMMARY = '''## Objective
- Continue the private TEST conversation.
## Important Details
- Use only the private TEST conversation; the previous text turn completed.
## Work State
### Completed
- The previous text turn completed.
### Active
- (none)
### Blocked
- (none)
## Next Move
1. Await the next user turn.
2. (none)
## Relevant Files
- (none)
'''


def summary_response(v2):
    return SUMMARY if v2 else V1_SUMMARY


def child_config(v2):
    # Native V2 config/plugin/agent.ts consumes plural document.info.agents;
    # its child catalog uses selected AGENT permissions, not session-only rules.
    rules = [{'action':'*','resource':'*','effect':'deny'},
             {'action':'question','resource':'*','effect':'allow'},
             {'action':'shell','resource':'*','effect':'ask'},
             {'action':'subagent','resource':CHILD_AGENT,'effect':'allow'}]
    if v2:
        return {'permissions':rules,'agents':{CHILD_AGENT:{'description':'Private TEST text-only child',
                'mode':'subagent','hidden':False,'permissions':[rules[0]]}}}
    return {'permission':{'*':'deny','question':'allow','bash':'ask','shell':'ask',
            'task':{'*':'deny',CHILD_AGENT:'allow'}},
            'agent':{CHILD_AGENT:{'description':'Private TEST text-only child','mode':'subagent'}}}


def text_content(value):
    if isinstance(value,str): return value
    if isinstance(value,list):
        return '\n'.join(p.get('text','') for p in value if isinstance(p,dict) and p.get('type')=='text')
    return ''


def summary_request(body, v2=True):
    # V1 compaction.test.ts:1376-1423,1499 directly asserts the actual LLM user
    # request. V2 compaction.ts:139-158 supplies its distinct new-summary literal.
    messages = body.get('messages',[])
    if not v2:
        if len(messages)!=1 or messages[0].get('role')!='user': return False
        text = text_content(messages[0].get('content'))
        return all(marker in text for marker in ('Here is the conversation so far:',
                   '<conversation>','</conversation>','Create a new anchored summary'))
    return any('You MUST summarize the conversation above into a structured summary' in text_content(m.get('content'))
               for m in messages if m.get('role')=='user')


def child_call(tools, v2):
    """Only the frozen built-in foreground tool and specifically advertised TEST agent."""
    name = 'subagent' if v2 else 'task'
    matches = [t.get('function',{}) for t in tools if t.get('type')=='function' and t.get('function',{}).get('name')==name]
    if len(matches)!=1: raise ValueError('native_child_tool_missing_or_ambiguous')
    tool = matches[0]
    if not any(line.startswith('- '+CHILD_AGENT+':') for line in tool.get('description','').splitlines()):
        raise ValueError('configured_test_child_agent_not_advertised')
    args = {'description':'Private TEST child reply','prompt':CHILD_PROMPT,
            ('agent' if v2 else 'subagent_type'):CHILD_AGENT}
    params = tool.get('parameters',{})
    props = params.get('properties',{})
    if (params.get('type')!='object' or not set(args).issubset(props)
            or not set(params.get('required',[])).issubset(args)
            or any(props[k].get('type')!='string' or ('enum' in props[k] and v not in props[k]['enum'])
                   or ('const' in props[k] and v!=props[k]['const']) for k,v in args.items())):
        raise ValueError('qualified_child_tool_schema_missing')
    return {'index':0,'id':CHILD_CALL,'type':'function','function':{'name':name,'arguments':json.dumps(args)}}


def child_result(messages, v2):
    """Read the REAL host's completed foreground tool response, not a supplied parentID."""
    matches = [m for m in messages if m.get('role')=='tool' and m.get('tool_call_id')==CHILD_CALL]
    if len(matches)!=1: raise ValueError('native_child_result_binding_missing')
    content = text_content(matches[0].get('content'))
    pattern = (r'<subagent sessionID="([^"<>\s]+)" state="completed">\n[\s\S]*\n</subagent>' if v2
               else r'<task id="([^"<>\s]+)" state="completed">\n<task_result>\n[\s\S]*\n</task_result>\n</task>')
    match = re.fullmatch(pattern,content)
    if not match: raise ValueError('native_foreground_child_did_not_complete')
    return match[1]


def child_response(body, v2, phase):
    """Pure provider protocol planner. No host calls, native events or product grants."""
    messages = body.get('messages',[])
    if phase==0:
        if any(m.get('role')=='tool' for m in messages): raise ValueError('child_initial_request_has_history')
        return 'launch',child_call(body.get('tools',[]),v2),None
    if phase==1:
        users = [text_content(m.get('content')) for m in messages if m.get('role')=='user']
        if not users or CHILD_PROMPT not in users[-1] or any(m.get('role')=='tool' for m in messages):
            raise ValueError('native_child_prompt_missing')
        return 'child',None,None
    if phase==2: return 'resume',None,child_result(messages,v2)
    raise ValueError('unexpected_child_provider_request_no_retry')

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
        self.v2 = False
        self.manual_active = False
        self.child_phase = 0
        self.child_resumed = threading.Event()
        self.child_release = threading.Event()
        self.child_hold_expired = False
        self.child_session = None
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
        call, response_text, kind, result_session = None, 'P0 deterministic completion.', 'ordinary', None
        try:
            if scenario=='child':
                with self.server.lock:
                    kind,call,result_session = child_response(request,self.server.v2,self.server.child_phase)
                    self.server.child_phase += 1
            elif summary_request(request,self.server.v2):
                if not self.server.manual_active or scenario not in ('compaction','completion'):
                    raise ValueError('unplanned_native_compaction_request')
                kind,response_text = 'summary',summary_response(self.server.v2)
            elif scenario=='compaction' or self.server.manual_active:
                raise ValueError('manual_compaction_provider_prompt_missing')
        except ValueError as error:
            with self.server.lock: self.server.gaps.append({'scenario':scenario,'reason':str(error)})
            self.send_error(400); return
        attempt=self.server.record({'model':model,'stream':request.get('stream',False),'toolSchemas':request.get('tools',[]),
                                    'messageRoles':[x.get('role') for x in request.get('messages',[])],
                                    'requestKind':kind,'childResultSession':result_session})
        if len(self.server.records)>32: self.send_error(429); return
        if kind=='resume':
            self.server.child_session = result_session
            self.server.child_resumed.set()
            if not self.server.child_release.wait(timeout=12):
                self.server.child_hold_expired = True
                self.close_connection=True
                return
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
            sequence += [({'tool_calls':[call]},None),({},'tool_calls')] if call else [({'content':response_text},None),({},'stop')]
            try:
                for delta,finish in sequence:
                    chunk={'id':'chatcmpl-p0','object':'chat.completion.chunk','created':int(time.time()),'model':model,'choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
                    self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode()); self.wfile.flush()
                self.wfile.write(b'data: [DONE]\n\n'); self.wfile.flush()
            except (BrokenPipeError,ConnectionResetError): pass
        else:
            message={'role':'assistant','content':None,'tool_calls':[{k:v for k,v in call.items() if k!='index'}]} if call else {'role':'assistant','content':response_text}
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
