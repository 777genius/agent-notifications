"""TEST portable genuine packaged-reader observation, never installed AN E2E."""
import argparse,base64,hashlib,hmac,http.client,json,os,pathlib,platform,re,secrets,shutil,socket,stat,struct,subprocess,sys,tarfile,threading,time,urllib.parse
from http.server import BaseHTTPRequestHandler
P=pathlib.Path;HERE=P(__file__).resolve().parent
sys.path.insert(0,str(HERE/'retained'))
from provider import Provider,ProviderHandler
HTTP_DIAGNOSTICS={};CURRENT_STAGE='not_started'
FLAGS={'productionQualified':False,'installedQualified':False,'timePolicyQualified':False,'sourceEpochQualified':False,'finalSpanQualified':False,'platformLifetimeQualified':False,'loadedMappedBytesQualified':False}
def need(v,c):
 if not v:raise ValueError(c)
def sha(p):
 need(p.is_file() and not any(x.is_symlink() for x in [p,*p.parents]),'regular_input')
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def write(p,v):
 with p.open('x') as f:json.dump(v,f,sort_keys=True,indent=2);f.write('\n')
 p.chmod(0o600)
def remaining(end):
 n=end-time.monotonic();need(n>0,'original_deadline');return n
def windows_commandline_record(raw,base):
 # Native amd64 UNICODE_STRING, decoded only from the returned owned allocation.
 need(16<=len(raw)<=4096 and 0<base<1<<64,'Windows_commandline_record_size')
 length,maximum,pointer=struct.unpack_from('<HH4xQ',raw)
 need(length>0 and length%2==maximum%2==0 and maximum>=length and pointer>=base and pointer%2==0,'Windows_commandline_record_header')
 offset=pointer-base
 need(offset>=16 and offset<=len(raw) and maximum<=len(raw)-offset,'Windows_commandline_record_pointer')
 command=raw[offset:offset+length].decode('utf-16-le',errors='strict')
 need('\x00'not in command and len(command.encode('utf-8'))<=4096,'Windows_commandline_record_text')
 return command

def windows_entry_witness(process_handle,pid,argv,host,end):
 # Same fixed class60/no-PEB contract as the existing native Windows runtime port.
 import ctypes
 from ctypes import wintypes as W
 need(ctypes.sizeof(ctypes.c_void_p)==8 and ctypes.sizeof(ctypes.c_wchar)==2,'Windows_native_amd64_ABI')
 remaining(end)
 k=ctypes.WinDLL('kernel32.dll',use_last_error=True,winmode=0x800)
 nt=ctypes.WinDLL('ntdll.dll',use_last_error=True,winmode=0x800)
 signatures={
  'GetCurrentProcess':([],W.HANDLE),'GetProcessId':([W.HANDLE],W.DWORD),
  'DuplicateHandle':([W.HANDLE,W.HANDLE,W.HANDLE,ctypes.POINTER(W.HANDLE),W.DWORD,W.BOOL,W.DWORD],W.BOOL),
  'WaitForSingleObject':([W.HANDLE,W.DWORD],W.DWORD),
  'GetProcessTimes':([W.HANDLE,*([ctypes.POINTER(W.FILETIME)]*4)],W.BOOL),
  'QueryFullProcessImageNameW':([W.HANDLE,W.DWORD,W.LPWSTR,ctypes.POINTER(W.DWORD)],W.BOOL),
  'CloseHandle':([W.HANDLE],W.BOOL)}
 for name,(args,result)in signatures.items():f=getattr(k,name);f.argtypes=args;f.restype=result
 query=nt.NtQueryInformationProcess;query.argtypes=[W.HANDLE,ctypes.c_int,ctypes.c_void_p,W.ULONG,ctypes.POINTER(W.ULONG)];query.restype=ctypes.c_int32
 owned=W.HANDLE();current=k.GetCurrentProcess();remaining(end)
 # Duplicate the already-owned child object, never reopen an arbitrary PID.
 need(k.DuplicateHandle(current,process_handle,current,ctypes.byref(owned),0x101000,False,0),'Windows_owned_limited_handle')
 try:
  remaining(end);need(k.GetProcessId(owned)==pid and k.WaitForSingleObject(owned,0)==258,'Windows_owned_live_process')
  creation,exit_time,kernel,user=W.FILETIME(),W.FILETIME(),W.FILETIME(),W.FILETIME()
  remaining(end);need(k.GetProcessTimes(owned,ctypes.byref(creation),ctypes.byref(exit_time),ctypes.byref(kernel),ctypes.byref(user)),'Windows_owned_birth')
  birth=(creation.dwHighDateTime<<32)|creation.dwLowDateTime;need(birth>0,'Windows_positive_birth')
  storage=(ctypes.c_uint64*512)();size=W.ULONG();remaining(end)
  need(query(owned,60,ctypes.byref(storage),ctypes.sizeof(storage),ctypes.byref(size))==0,'Windows_commandline_query')
  need(16<=size.value<=ctypes.sizeof(storage),'Windows_commandline_return_size');remaining(end)
  command=windows_commandline_record(ctypes.string_at(ctypes.addressof(storage),size.value),ctypes.addressof(storage))
  need(command==subprocess.list2cmdline(argv),'actual_stock_entry_commandline')
  image=ctypes.create_unicode_buffer(2048);chars=W.DWORD(len(image));remaining(end)
  need(k.QueryFullProcessImageNameW(owned,0,image,ctypes.byref(chars)) and 0<chars.value<len(image),'Windows_owned_image_query')
  image_name=ctypes.string_at(ctypes.addressof(image),chars.value*2).decode('utf-16-le',errors='strict')
  need('\x00'not in image_name and os.path.normcase(image_name)==os.path.normcase(str(host)),'actual_stock_entry_image')
  remaining(end);need(k.GetProcessId(owned)==pid and k.WaitForSingleObject(owned,0)==258,'Windows_owned_still_live');remaining(end)
  return {'pid':pid,'commandLine':command,'imagePath':image_name,'creationFILETIME':birth,'osWitness':'NtQueryInformationProcess_class60_owned_handle_and_QueryFullProcessImageNameW','qualificationGranted':False}
 finally:need(k.CloseHandle(owned),'Windows_witness_handle_closed')

class DeadlineSocket(socket.socket):
 def recv(self,*a,**k):self.settimeout(remaining(self.end));return super().recv(*a,**k)
 def recv_into(self,*a,**k):self.settimeout(remaining(self.end));return super().recv_into(*a,**k)
def request(port,project,path,headers,v2,payload=None,method=None,timeout=2,startup=False):
 receipt=HTTP_DIAGNOSTICS.setdefault(CURRENT_STAGE,{'attempts':0,'lastStatus':None});receipt['attempts']+=1;receipt['lastStatus']=None
 end=time.monotonic()+timeout;raw=socket.create_connection(('127.0.0.1',port),timeout=remaining(end))
 sock=DeadlineSocket(fileno=raw.detach());sock.end=end;remaining(end)
 conn=http.client.HTTPConnection('127.0.0.1',port,timeout=remaining(end));conn.sock=sock
 path+=('&' if '?' in path else '?')+urllib.parse.urlencode({'location[directory]' if v2 else 'directory':str(project)})
 try:
  body=json.dumps(payload).encode() if payload is not None else None
  sock.settimeout(remaining(end));conn.request(method or ('POST' if body is not None else 'GET'),path,body,{'Content-Type':'application/json',**headers})
  sock.settimeout(remaining(end));resp=conn.getresponse();receipt['lastStatus']=resp.status;data=resp.read(1048577);remaining(end)
  if startup and resp.status==503:raise ConnectionError('owned_native_starting_503')
  need(200<=resp.status<300 and len(data)<=1048576,'native_HTTP_response')
  value=json.loads(data) if data else None;return value.get('data',value) if isinstance(value,dict) else value
 finally:conn.close()
class CountedProvider(ProviderHandler):
 def do_POST(self):
  with self.server.lock:self.server.http_attempts+=1;n=self.server.http_attempts
  if self.server.mode!='one-completion' or n!=1 or self.path!='/v1/chat/completions':self.send_error(403);return
  return super().do_POST()
 def do_GET(self):
  with self.server.lock:self.server.http_attempts+=1
  self.send_error(403)
 do_PUT=do_GET;do_PATCH=do_GET;do_DELETE=do_GET;do_HEAD=do_GET;do_OPTIONS=do_GET
def env(root):
 out={'PATH':os.environ.get('PATH',''),'LANG':'C.UTF-8','TZ':'UTC','CI':'true','OPENCODE_DISABLE_AUTOUPDATE':'true','OPENCODE_DISABLE_LSP_DOWNLOAD':'true','OPENCODE_DISABLE_SHARE':'true','AN_TEST_ROOT':str(root)}
 for name in ['SystemRoot','WINDIR','COMSPEC','PATHEXT']:
  if os.name=='nt' and name in os.environ:out[name]=os.environ[name]
 for name in ['HOME','USERPROFILE','APPDATA','LOCALAPPDATA','XDG_CONFIG_HOME','XDG_DATA_HOME','XDG_CACHE_HOME','XDG_STATE_HOME','XDG_RUNTIME_DIR','TMPDIR','TEMP','TMP','BUN_INSTALL_CACHE_DIR','OPENCODE_CONFIG_DIR']:
  d=root/name.lower();d.mkdir(mode=0o700);out[name]=str(d)
 return out
def private_windows(root):
 # Fixed Windows ACL command affects only the new exclusive TEST directory.
 raw=subprocess.check_output(['whoami','/user','/fo','csv','/nh'],timeout=2).decode();import re
 sid=re.search(r'S-1-5-[0-9-]+',raw);need(sid is not None,'actual_TEST_owner_SID')
 p=subprocess.run(['icacls',str(root),'/inheritance:r','/grant:r','*'+sid[0]+':(OI)(CI)F'],capture_output=True,timeout=2);need(p.returncode==0,'private_TEST_DACL')
def unpack(archive,root,pin,os_name,arch):
 need(sha(archive)==pin['archiveSHA256'],'official_archive_SHA');raw=archive.read_bytes();alg,b64=pin['archiveSRI'].split('-',1)
 need(alg=='sha512' and base64.b64encode(hashlib.sha512(raw).digest()).decode()==b64,'SRI_before_parse')
 layouts=HERE/'official-archive-layouts.json';need(sha(layouts)=='a0ac3dc8d3bf26956b061e46063efc19257ee3149208f26a763656a585532649','closed_archive_layout_source')
 layout=json.loads(layouts.read_text())['archives'][pin['archiveSHA256']];need(archive.stat().st_size==layout['archiveBytes'],'exact_official_archive_bytes')
 name='package/bin/'+('opencode.exe' if os_name=='windows' else 'opencode');expected=layout['members'];need(set(expected)=={'package/package.json',name},'exact_two_official_members')
 with tarfile.open(archive,'r:gz') as t:
  members=t.getmembers();need(len(members)==len(expected) and len({m.name for m in members})==len(expected) and all(m.isfile() and m.name in expected and m.size==expected[m.name] for m in members),'exact_bounded_official_archive_layout')
  m=next(m for m in members if m.name==name);need(m.size>0,'positive_exact_image_bytes');target=root/('opencode.exe' if os_name=='windows' else 'opencode');imageHash=hashlib.sha256()
  with t.extractfile(m) as source,target.open('xb') as out:
   left=m.size
   while left:
    block=source.read(min(left,1024*1024));need(block and len(block)<=left,'exact_bounded_image_read');out.write(block);imageHash.update(block);left-=len(block)
   need(source.read(1)==b'','exact_image_EOF')
  need(imageHash.hexdigest()==pin['executableSHA256'],'official_image_SHA')
  with target.open('rb') as image:
   data=image.read(64)
   if os_name=='linux':need(data[:4]==b'\x7fELF' and struct.unpack('<H',data[18:20])[0]==(62 if arch=='amd64' else 183),'ELF_arch')
   elif os_name=='darwin':need(data[:4]==b'\xcf\xfa\xed\xfe' and struct.unpack('<I',data[4:8])[0]==(0x1000007 if arch=='amd64' else 0x100000c),'MachO_arch')
   else:
    off=struct.unpack('<I',data[60:64])[0];need(data[:2]==b'MZ' and off+6<=m.size,'bounded_PE_header');image.seek(off);pe=image.read(6);need(pe[:4]==b'PE\0\0' and struct.unpack('<H',pe[4:6])[0]==0x8664,'PE_arch')
  target.chmod(0o700);return target

def wait_file(path,end,proc):
 while True:
  remaining(end)
  if path.is_file():
   need(path.stat().st_size<=16384,'reader_receipt_bound');value=json.loads(path.read_text());remaining(end);return value
  need(proc.poll() is None,'owned_host_ended_before_reader')
  time.sleep(min(.02,remaining(end)))
def independent_v1_fact(root,key,sid,history):
 rows=[json.loads(l) for l in (root/'reader-private.jsonl').read_text().splitlines()];H=lambda x:'h:'+hmac.new(key,str(x).encode(),hashlib.sha256).hexdigest()[:24]
 facts=[r['value'] for r in rows if r['kind']=='sdk-fact'];need(len(facts)==1,'one_actual_SDK_fact')
 fact=facts[0]['event'];answers=[x['info'] for x in history if x['info']['role']=='assistant'];users=[x['info'] for x in history if x['info']['role']=='user']
 need(len(answers)==len(users)==1,'one_native_user_assistant');a,u=answers[0],users[0]
 need(facts[0]['currentAtCallback'] and facts[0]['clockID']=='local-performance' and fact['rootSession'] and fact['kind']=='turn_idle_verified' and fact['sessionID']==H(sid) and fact['turnID']==H(u['id']) and fact['messageID']==H(a['id']),'native_fact_identity')
 need(a['sessionID']==sid and a['parentID']==u['id'] and a.get('finish')=='stop' and not a.get('summary') and not a.get('error') and isinstance(a['time']['completed'],int),'ordinary_final_native_assistant')
 need(not any(p.get('type')=='tool' for x in history for p in x.get('parts',[])),'zero_native_tools')
 p=fact['provenance'];need(p['generation']=='v1' and p['timeBasis']=='assistant_completed' and p['nativeTime']==a['time']['completed'],'original_native_time_no_restamp')
 need(any(r['kind']=='native-v1' and r['value'].get('type')=='message.updated' and r['value'].get('properties',{}).get('info',{}).get('id')==H(a['id']) and r['value']['properties']['info'].get('time',{}).get('completed')==a['time']['completed'] for r in rows),'independent_final_callback')
def tui_input_frame(raw):
 # Native completed render only, not plugin readiness or arbitrary terminal text.
 need(len(raw)<=1048576,'bounded_TUI_readiness_output')
 for frame in raw.split(b'\x1b[?2026h')[1:]:
  stop=frame.find(b'\x1b[?2026l')
  if stop<0:continue
  frame=frame[:stop]
  if not re.search(rb'\x1b\[[1-9][0-9]*;[1-9][0-9]*H\x1b\[\?25h',frame):continue
  text=re.sub(rb'\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))',b'',frame)
  if all(x in text for x in ['Ask anything…'.encode(),b'Build',b'TEST completion',b'TEST loopback']):return True
 return False

def tui_readiness_snapshot(raw,raw_bytes,reader_done,reader_error):
 # Content-free diagnostic only; it never authorizes input or extends a wait.
 error='none' if reader_error is None else 'other_reader_error'
 if reader_error is not None:
  if reader_error.startswith('ConPTY_ReadFile_'):error='read_failure'
  elif reader_error.startswith('capture exceeded'):error='capture_overflow'
  elif reader_error.startswith('console reader failure:'):error='reader_exception'
  elif reader_error in {'PTY_absolute_deadline','PTY_select_failure','PTY_read_failure'}:error=reader_error
 d={'snapshotBeforeCleanup':True,'rawBytes':raw_bytes,'scannedBytes':len(raw),'withinReadinessBound':raw_bytes<=1048576,'readerDone':bool(reader_done),'readerError':error,'syncStartSeen':b'\x1b[?2026h' in raw,'syncEndSeen':b'\x1b[?2026l' in raw,'completeFrameSeen':False,'cursorInCompleteFrame':False,'emptyPromptInCompleteFrame':False,'buildLabelInCompleteFrame':False,'modelLabelInCompleteFrame':False,'providerLabelInCompleteFrame':False}
 for frame in raw.split(b'\x1b[?2026h')[1:]:
  stop=frame.find(b'\x1b[?2026l')
  if stop<0:continue
  frame=frame[:stop];d['completeFrameSeen']=True
  d['cursorInCompleteFrame']|=bool(re.search(rb'\x1b\[[1-9][0-9]*;[1-9][0-9]*H\x1b\[\?25h',frame))
  text=re.sub(rb'\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))',b'',frame)
  for key,label in [('emptyPromptInCompleteFrame','Ask anything…'.encode()),('buildLabelInCompleteFrame',b'Build'),('modelLabelInCompleteFrame',b'TEST completion'),('providerLabelInCompleteFrame',b'TEST loopback')]:d[key]|=label in text
 d['originalReadyPredicate']=raw_bytes<=1048576 and tui_input_frame(raw)
 return d

def main(a):
 global CURRENT_STAGE
 HTTP_DIAGNOSTICS.clear()
 def stage(value):
  global CURRENT_STAGE
  CURRENT_STAGE=value
 actual_os={'Linux':'linux','Darwin':'darwin','Windows':'windows'}[platform.system()];actual_arch={'x86_64':'amd64','AMD64':'amd64','arm64':'arm64','aarch64':'arm64'}[platform.machine()]
 need((a.os,a.arch)==(actual_os,actual_arch),'native_runner_cell');need(a.version in ['1.18.33','1.18.34'] and a.mode=='one-completion' and a.entry in ['tui','run'],'closed_local_V1_scope')
 need(a.mode=='one-completion','one_planned_native_transaction')
 if a.host_netns_fd is not None:
  need(actual_os=='linux','Linux_NET_FD_only');import importlib.util
  hp=HERE/'retained/owned-test-cgroup.py';need(sha(hp)=='8a349bffd8a202daae9173b0686ec547bb3cffa126669e72c73ca0aa744d56f8','NET_helper_pin');sp=importlib.util.spec_from_file_location('owned_reader_NET',hp);hm=importlib.util.module_from_spec(sp);sp.loader.exec_module(hm);hm.private_netns(a.host_netns_fd);os.close(a.host_netns_fd)
  st=dict(x.split(':',1) for x in P('/proc/self/status').read_text().splitlines() if ':' in x);need(os.getresuid()==os.getresgid()==(1000,1000,1000) and not os.getgroups() and all(int(st[k].strip(),16)==0 for k in ['CapInh','CapPrm','CapEff','CapBnd','CapAmb']),'actual_normal_UID_cap0')
 root=a.root.absolute();need(root.name.startswith('TEST-') and not root.exists() and not any(x.is_symlink() for x in root.parents),'fresh_TEST_root')
 need(sha(a.bundle)==a.bundle_sha256 and sha(a.build_receipt)==a.build_receipt_sha256,'actual_bundle_build_custody');build=json.loads(a.build_receipt.read_text());need(build['status']=='genuine_portable_reader_bundle_built' and build['bundleSHA256']==a.bundle_sha256 and build['SDKArchiveSHA256']=='c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752','genuine_packed_factory_build')
 need(all(build['sourceLeafSHA256'].get(n)==sha(HERE/n) for n in ['consumer.mjs','candidate/native-v1.mjs','candidate/native-v2.mjs','candidate/protocol.mjs','retained/sdk.tgz','retained/pack-receipt.json']),'exact_compiled_source_graph')
 pins=HERE/'official-host-pins.json';need(sha(pins)=='59368f673e651f00e8a4d0a675365ba15f0752eacabc3e2ea68a2e91cf6b166f','official_closed_cell_pins')
 pin=next(x for x in json.loads(pins.read_text())['cells'] if (x['os'],x['arch'],x['version'])==(a.os,a.arch,a.version))
 fixed={str(x):sha(x) for x in [a.archive,a.bundle,a.build_receipt,pins,HERE/'official-archive-layouts.json',P(__file__),HERE/'retained/provider.py']}
 os.umask(0o077);root.mkdir(mode=0o700)
 if a.os=='windows':private_windows(root)
 project=root/'project';project.mkdir(mode=0o700);host=unpack(a.archive,root,pin,a.os,a.arch);e=env(root)
 if a.version in ['1.18.33','1.18.34']:e.update(NPM_CONFIG_OFFLINE='true',NPM_CONFIG_FETCH_RETRIES='0',OPENCODE_DISABLE_MODELS_FETCH='1')
 key=secrets.token_bytes(32);(root/'trace-key').write_bytes(key)
 write(root/'.owned-test-root.json',{'purpose':'TEST portable packaged SDK reader'})
 write(root/'reader-authority-private.json',{'version':a.version,'os':a.os,'arch':a.arch,'official':pin,'parentPID':os.getpid(),'directory':str(project.resolve()),'imageSHA256':pin['executableSHA256'],'apiOnlyReadiness':False,'mode':a.mode,'entry':a.entry})
 provider=Provider(root,key);provider.mode=a.mode;provider.http_attempts=0;provider.RequestHandlerClass=CountedProvider
 worker=threading.Thread(target=provider.serve_forever);worker.start();proc=None;log=None;ui_output=bytearray();ui_changed=threading.Condition();pty_master=None;pty_worker=None;pty_overflow=False;pty_failure=None;pty_eof=False;common_end=None;win_terminal=None;hpc_closer=None;win_last_exit=None;headers={};v2=a.version=='2.0.21';success=False
 result={'status':'unqualified','cell':{'os':a.os,'arch':a.arch,'version':a.version,'entry':a.entry},'mode':a.mode,'plannedProviderTransactions':1 if a.mode=='one-completion' else 0,'packagedReaderTransportObserved':False,'factoryFinalizationObserved':False,'sourceDescriptorGranted':False,**FLAGS}
 if a.os=='windows':fixed.update({str(HERE/'retained'/n):sha(HERE/'retained'/n) for n in ['windows_conpty.py','harness.py']})
 stage('configured_loader_preparation')
 try:
  plug=root/'reader.js';shutil.copyfile(a.bundle,plug)
  models={'p0-completion':{'name':'TEST completion','limit':{'context':128000,'output':8192}}};endpoint=f'http://127.0.0.1:{provider.server_port}/v1'
  config={'model':'p0/p0-completion','agent':{'title':{'disable':True}}}
  if v2:
   entry=root/'plugin-entry';entry.mkdir(mode=0o700);(entry/'server.js').write_text('export { default } from '+json.dumps(plug.resolve().as_uri())+';\n')
   config.update(update='disable',share='disabled',warming=False,formatter=False,lsp=False,websearch=False,plugins=[{'package':str(entry.resolve())}],permissions=[{'action':'*','resource':'*','effect':'deny'}],providers={'p0':{'name':'TEST loopback','package':'@opencode/ai/providers/openai-compatible','env':[],'settings':{'baseURL':endpoint,'apiKey':'sandbox-only','timeout':10000},'models':models}})
   password=secrets.token_hex(16);e['OPENCODE_PASSWORD']=password;headers={'Authorization':'Basic '+base64.b64encode(('opencode:'+password).encode()).decode()}
  else:config.update(plugin=[[plug.resolve().as_uri(),{}]],permission={'*':'deny'},provider={'p0':{'npm':'@ai-sdk/openai-compatible','name':'TEST loopback','options':{'baseURL':endpoint,'apiKey':'sandbox-only'},'models':models}})
  write(project/'opencode.json',config);e['OPENCODE_CONFIG']=str(project/'opencode.json')
  stage('owned_local_entry_spawn')
  log=(root/'host-private.log').open('xb')
  prompt='Reply with one short TEST completion.'
  argv=[str(host)] if a.entry=='tui' else [str(host),'run','--format','json',prompt]
  if a.entry=='tui':common_end=time.monotonic()+35+35+6
  def phase_end(seconds):
   end=time.monotonic()+seconds
   return min(common_end,end) if common_end is not None else end
  def tui_current():
   if common_end is not None:remaining(common_end)
  if a.entry=='tui' and actual_os=='windows':
   import ctypes,importlib.util
   wp=HERE/'retained/windows_conpty.py';need(sha(wp)=='8220425ee006e13a95e4c98d215452ac238d257055387c36cec6fc8585f5ea9f','reviewed_direct_ConPTY_source')
   need(sha(HERE/'retained/harness.py')=='cd6147929a58363e24063e4c520b87cf5bc08fe39b16ced689d4cee7750fbb78','ConPTY_inert_harness_source');spec=importlib.util.spec_from_file_location('owned_entry_ConPTY',wp);wm=importlib.util.module_from_spec(spec);spec.loader.exec_module(wm)
   hpc_owner_lock=threading.Lock()
   def close_owned_terminal():
    with hpc_owner_lock:win_terminal.close()
   class EntryConPTY(wm.ConPTY):
    # Same owned creation/Job logic; only actual output termination is evidence.
    def read(self):
     buf,size=ctypes.create_string_buffer(65536),wm.W.DWORD()
     error=None;self.eof_reason=None
     try:
      while True:
       ok=self.k.ReadFile(self.output,buf,len(buf),ctypes.byref(size),None)
       if not ok:
        code=ctypes.get_last_error()
        if code==109:self.eof_reason='broken_pipe_109'
        else:error='ConPTY_ReadFile_'+str(code)
        break
       if not size.value:self.eof_reason='read_zero';break
       with self.output_changed:
        self.raw.extend(buf.raw[:size.value])
        if len(self.raw)>4*1024*1024:error='capture exceeded 4 MiB';break
        self.output_changed.notify_all()
     except Exception as exc:error='console reader failure: '+repr(exc)
     finally:
      with self.output_changed:
       self.error=error;self.reader_done=True;self.output_changed.notify_all()
   win_terminal=EntryConPTY(argv,e,project,remaining(common_end))
   class DirectNativeProcess:
    pid=int(win_terminal.pi.dwProcessId)
    def poll(self):return win_last_exit if win_terminal.closed else win_terminal.poll()
    def wait(self,timeout):
     nonlocal win_last_exit
     end=min(common_end,time.monotonic()+timeout)
     remaining(end)
     while self.poll() is None:remaining(end);time.sleep(min(.02,remaining(end)))
     remaining(end);win_last_exit=self.poll();remaining(end);return win_last_exit
    def terminate(self):close_owned_terminal()
    def kill(self):close_owned_terminal()
   proc=DirectNativeProcess()
  elif a.entry=='tui':
   import pty,select,fcntl,termios
   pty_master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',32,120,0,0));e['TERM']='xterm-256color'
   try:proc=subprocess.Popen(argv,cwd=project,env=e,stdin=slave,stdout=slave,stderr=slave,start_new_session=True)
   finally:os.close(slave)
   def drain_pty():
    nonlocal pty_overflow,pty_failure,pty_eof
    total=0
    while True:
     if time.monotonic()>=common_end:pty_failure="PTY_absolute_deadline";return
     try:ready,_,_=select.select([pty_master],[],[],min(.1,remaining(common_end)))
     except (OSError,ValueError):pty_failure="PTY_select_failure";return
     if not ready:continue
     try:block=os.read(pty_master,65536)
     except OSError as error:
      if error.errno==5 and time.monotonic()<common_end:pty_eof=True;return
      pty_failure="PTY_read_failure";return
     if time.monotonic()>=common_end:pty_failure="PTY_absolute_deadline";return
     if not block:pty_eof=True;return
     total+=len(block)
     if total>1048576:pty_overflow=True
     else:
      log.write(block)
      with ui_changed:ui_output.extend(block);ui_changed.notify_all()
   pty_worker=threading.Thread(target=drain_pty);pty_worker.start()
  else:proc=subprocess.Popen(argv,cwd=project,env=e,stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
  # Private OS argv witness, not own.entry or plugin process.argv authority.
  if actual_os=='linux':
   actual_argv=(P('/proc')/str(proc.pid)/'cmdline').read_bytes().split(b'\0')
   need(actual_argv[-1:]==[b''] and actual_argv[:-1]==[os.fsencode(x) for x in argv],'actual_stock_entry_argv')
   need((P('/proc')/str(proc.pid)/'exe').resolve()==host.resolve(),'actual_stock_entry_image')
   write(root/'native-entry-private.json',{'pid':proc.pid,'argv':argv,'entry':a.entry,'osWitness':'proc_cmdline_and_exe','qualificationGranted':False})
  elif actual_os=='darwin':
   actual_argv=subprocess.check_output(['ps','-ww','-p',str(proc.pid),'-o','command='],timeout=2).decode().strip()
   need(actual_argv==(' '.join(argv)),'actual_stock_entry_argv')
   write(root/'native-entry-private.json',{'pid':proc.pid,'argvText':actual_argv,'entry':a.entry,'osWitness':'ps_command_only','qualificationGranted':False})
  else:
   witness_end=phase_end(2)
   native_handle=win_terminal.pi.hProcess if win_terminal is not None else int(proc._handle)
   witness=windows_entry_witness(native_handle,proc.pid,argv,host,witness_end)
   remaining(witness_end);write(root/'native-entry-private.json',{**witness,'entry':a.entry});remaining(witness_end)
  stage('native_reader_readiness')
  startup_end=phase_end(35)
  ready=wait_file(root/'reader-ready.json',startup_end,proc);tui_current()
  need(ready['version']==a.version and ready['nativePID']==proc.pid and ready['parentPID']==os.getpid() and P(ready['actualExecPath']).resolve()==host.resolve() and ready['ownedImageSHA256']==pin['executableSHA256'],'actual_native_loader_image_scope')
  if a.entry=='tui':
   stage('native_TUI_input_readiness')
   changed=win_terminal.output_changed if win_terminal is not None else ui_changed
   while True:
    remaining(startup_end);need(proc.poll() is None,'native_TUI_ended_before_input')
    with changed:
     raw=bytes(win_terminal.raw) if win_terminal is not None else bytes(ui_output)
     need((win_terminal.error is None if win_terminal is not None else not pty_overflow and pty_failure is None),'native_TUI_readiness_reader')
     if tui_input_frame(raw):remaining(startup_end);break
     changed.wait(min(.02,remaining(startup_end)))
   result['TUIInputReadyOutputSHA256']=hashlib.sha256(raw).hexdigest();result['TUIInputReadyBytes']=len(raw)
   tui_current();remaining(startup_end)
   if win_terminal is not None:win_terminal.send((prompt+'\r').encode())
   else:os.write(pty_master,(prompt+'\r').encode())
  stage('native_reader_disposal')
  closed=wait_file(root/'reader-closed.json',phase_end(35+6),proc);tui_current();need(closed['actualSDKDispose'] and closed['ownedImageSHA256']==pin['executableSHA256'],'actual_factory_dispose')
  stage('native_entry_natural_finish')
  if a.entry=='tui':
   tui_current()
   if win_terminal is not None:win_terminal.send(b'\x03')
   else:os.write(pty_master,b'\x03') # stock app_exit key after settled prompt
  exit_end=phase_end(6);remaining(exit_end);code=proc.wait(timeout=remaining(exit_end));remaining(exit_end);need(code==0,'native_entry_natural_exit')
  if win_terminal is not None:
   accounting=wm.JOB_ACCOUNTING();win_terminal.ok(win_terminal.k.QueryInformationJobObject(win_terminal.job,1,ctypes.byref(accounting),ctypes.sizeof(accounting),None))
   tui_current();need(accounting.active_processes==0,'natural_ConPTY_job_empty')
   # With zero clients, release the pseudoconsole's writer while its reader
   # remains alive. Only a running closer may take HPCON ownership.
   hpc=win_terminal.hpc;need(bool(hpc),'owned_HPCON')
   hpc_errors=[];hpc_completed=[False]
   def release_hpc():
    with hpc_owner_lock:
     # Cleanup owns an unstarted/delayed closer's handle. Never close twice.
     if win_terminal.closed or win_terminal.hpc.value!=hpc.value:
      hpc_errors.append('HPCON_cleanup_owned');return
     win_terminal.hpc=wm.W.HANDLE()
    try:win_terminal.k.ClosePseudoConsole(hpc);hpc_completed[0]=True
    except Exception as error:hpc_errors.append(type(error).__name__)
   close_end=phase_end(3);remaining(close_end)
   hpc_closer=threading.Thread(target=release_hpc,daemon=True);hpc_closer.start()
   hpc_closer.join(timeout=remaining(close_end));remaining(close_end)
   need(not hpc_closer.is_alive() and hpc_completed[0] and not hpc_errors,'HPCON_zero_client_release')
   end=phase_end(2);remaining(end)
   win_terminal.reader.join(timeout=remaining(end));remaining(end)
   need(not win_terminal.reader.is_alive() and win_terminal.reader_done and win_terminal.error is None and
    win_terminal.eof_reason in ['broken_pipe_109','read_zero'] and len(win_terminal.raw)<=1048576,'natural_ConPTY_output_EOF')
   result['ConPTYOutputEOFReason']=win_terminal.eof_reason;result['ConPTYClosedAfterZeroClients']=True
   log.write(win_terminal.raw);close_owned_terminal();tui_current();need(not win_terminal.forced,'ConPTY_no_forced_close')
  if pty_worker is not None:
   eof_end=phase_end(2);remaining(eof_end);pty_worker.join(timeout=remaining(eof_end));remaining(eof_end);need(not pty_worker.is_alive() and pty_eof and not pty_overflow and pty_failure is None,'bounded_PTY_EOF')
  result['nativeEntryTransport']=a.entry;result['nativeEntryNaturalExit']=True
  stage('independent_reader_oracle')
  rows=[json.loads(x) for x in (root/'reader-private.jsonl').read_text().splitlines()];need(len(rows)<=512 and (root/'reader-private.jsonl').stat().st_size<=1048576 and not any(x['kind']=='uncertainty' for x in rows),'bounded_certain_real_ingress')
  if v2:
   need(closed['actualSDKDone'] and closed['subscriptions']==closed['actualNativeReaderClosures']==closed['registrations']==closed['actualRegistrationDisposals']==2 and closed['markerReads']==2 and closed['sdkFacts']==0,'two_actual_readers_RPC_markers_and_closures')
   need(closed['actualAbortRequested'] is True,'actual_planned_abort_closed_witness')
   closes=[(i,x['value']) for i,x in enumerate(rows) if x['kind']=='native-reader-close'];aborts=[(i,x['value']) for i,x in enumerate(rows) if x['kind']=='requested-native-abort']
   need(len(closes)==2 and [x['number'] for _,x in closes]==[1,2] and closes[0][1]['actualSignalAborted'] is True and len(aborts)==1 and aborts[0][1]['number']==1 and aborts[0][0]<closes[0][0],'independent_actual_abort_before_first_close')
   result['sourceReplacementDelayMs']=250
  else:
   need(closed['actualTrackedJobsSettled'] and closed['nativeCalls']>0 and sum(x['kind']=='sdk-observe-invoked' for x in rows)==closed['nativeCalls'],'real_callback_observe_drain')
   if a.mode=='api-only':
    H=lambda x:'h:'+hmac.new(key,str(x).encode(),hashlib.sha256).hexdigest()[:24]
    matching=[x['value'] for x in rows if x['kind']=='native-v1' and x['value'].get('properties',{}).get('info',{}).get('id')==H(sid)]
    need({'session.created','session.updated','session.deleted'}<={x['type'] for x in matching} and closed['sdkFacts']==0,'actual_empty_session_callback_membership')
   else:
    stage('independent_native_history');hp=root/'reader-history-private.json';need(hp.stat().st_size<=1048576,'bounded_native_history');native=json.loads(hp.read_text());independent_v1_fact(root,key,native['sessionID'],native['history']);result['factoryFinalizationObserved']=True;result['privateNativeHistorySHA256']=sha(hp);result['nativeEntryWitnessSHA256']=sha(root/'native-entry-private.json')
    need(sum(x['kind']=='native-cleanup-enter' for x in rows)==sum(x['kind']=='native-cleanup-return' for x in rows)==1 and all(x['value'].get('actualTrackedJobsSettled') is True for x in rows if x['kind']=='native-cleanup-return'),'actual_native_dispose_callback_return')
  need(provider.http_attempts==(1 if a.mode=='one-completion' else 0) and len(provider.records)==provider.http_attempts and not provider.gaps,'explicit_exact_provider_budget_no_retry')
  tui_current();result.update(packagedReaderTransportObserved=True,ownedImageSHA256=pin['executableSHA256'],bundleSHA256=a.bundle_sha256,traceSHA256=sha(root/'reader-private.jsonl'),closedReceiptSHA256=sha(root/'reader-closed.json'));success=True
 except Exception as error:
  result['firstFailedStage']=CURRENT_STAGE;result['exceptionKind']=type(error).__name__;result['failure']=type(error).__name__
  if isinstance(error,ValueError) and str(error) in {'original_deadline','native_TUI_ended_before_input','native_TUI_readiness_reader'}:result['failureCode']=str(error)
  if a.entry=='tui':
   try:
    changed=win_terminal.output_changed if win_terminal is not None else ui_changed
    with changed:
     captured=win_terminal.raw if win_terminal is not None else ui_output
     raw=bytes(captured[:1048576]);raw_bytes=len(captured)
     reader_done=win_terminal.reader_done if win_terminal is not None else pty_eof or pty_failure is not None
     reader_error=win_terminal.error if win_terminal is not None else ('capture exceeded' if pty_overflow else pty_failure)
    result['TUIReadinessBeforeCleanup']=tui_readiness_snapshot(raw,raw_bytes,reader_done,reader_error)
   except Exception:result['TUIReadinessBeforeCleanup']={'snapshotBeforeCleanup':True,'snapshotUnavailable':True}
  write(root/'first-exception-private.json',{'stage':CURRENT_STAGE,'kind':type(error).__name__,'message':str(error)[:512]})
 finally:
  if win_terminal is not None and not win_terminal.closed and proc.poll() is None:
   win_last_exit=97;close_owned_terminal();result['hostForcedKillUsed']=True;success=False
  if proc is not None and proc.poll() is None:
   proc.terminate();result['ownedHostStopRequested']=True
   try:proc.wait(timeout=6)
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=2);result['hostForcedKillUsed']=True;success=False
  if win_terminal is not None and not win_terminal.closed:
   win_last_exit=proc.poll();close_owned_terminal()
   if win_terminal.forced or win_terminal.error is not None:success=False
  if hpc_closer is not None and hpc_closer.ident is not None:
   hpc_closer.join(timeout=2)
   if hpc_closer.is_alive():success=False
  result['ownedLeaderWaitObserved']=proc is not None and proc.poll() is not None;result['hostProcessTreeClosure']='unproved_portable_leader_only'
  if pty_worker is not None:
   pty_worker.join(timeout=2)
   if pty_worker.is_alive() or pty_overflow or pty_failure is not None:success=False
  if pty_master is not None:
   os.close(pty_master)
   if pty_worker is not None:
    pty_worker.join(timeout=2)
    if pty_worker.is_alive():success=False
  if log is not None:
   log.close();hp=root/'host-private.log';size=hp.stat().st_size;result['hostPrivateLogSHA256']=sha(hp);result['hostPrivateLogBytes']=size
   with hp.open('rb') as source,(root/'host-startup-prefix-private.log').open('xb') as dest:dest.write(source.read(65536))
   result['hostStartupPrefixBytes']=min(size,65536);result['hostStartupPrefixTruncated']=size>65536
   if size>1048576:result['hostPrivateLogOverflow']=True;success=False
  result['ownHTTPDiagnostics']={k:dict(v) for k,v in HTTP_DIAGNOSTICS.items()}
  provider.shutdown();provider.server_close();worker.join(timeout=3);result['providerThreadJoined']=not worker.is_alive()
  result['actualProviderHTTPRequestAttempts']=provider.http_attempts;result['actualProviderTransactions']=len(provider.records);result['actualToolCalls']=0 if not provider.records or all(x.get('requestKind')=='ordinary' for x in provider.records) else None
  result['sourcesArchiveBundleUnchanged']=all(sha(P(n))==h for n,h in fixed.items()) and sha(host)==pin['executableSHA256']
  if not result['providerThreadJoined'] or not result['sourcesArchiveBundleUnchanged'] or provider.http_attempts!=(1 if a.mode=='one-completion' else 0):success=False
  result['status']='portable_packaged_reader_observed' if success else 'unqualified';write(root/'result.json',result)
 print(json.dumps({'status':result['status'],'resultSHA256':sha(root/'result.json'),'cell':result['cell'],'actualProviderTransactions':result['actualProviderTransactions'],'qualificationGranted':False}));return 0 if success else 1
if __name__=='__main__':
 p=argparse.ArgumentParser()
 for n in ['root','archive','bundle','build-receipt']:p.add_argument('--'+n,type=P,required=True)
 for n in ['bundle-sha256','build-receipt-sha256','os','arch','version']:p.add_argument('--'+n,required=True)
 p.add_argument('--entry',choices=['tui','run'],required=True);p.add_argument('--host-netns-fd',type=int);p.add_argument('--mode',choices=['one-completion'],default='one-completion');sys.exit(main(p.parse_args()))
