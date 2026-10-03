"""TEST portable genuine packaged-reader observation, never installed AN E2E."""
import argparse,base64,hashlib,hmac,http.client,json,os,pathlib,platform,secrets,shutil,socket,stat,struct,subprocess,sys,tarfile,threading,time,urllib.parse
from http.server import BaseHTTPRequestHandler
P=pathlib.Path;HERE=P(__file__).resolve().parent
sys.path.insert(0,str(HERE/'retained'))
from provider import Provider,ProviderHandler
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
class DeadlineSocket(socket.socket):
 def recv(self,*a,**k):self.settimeout(remaining(self.end));return super().recv(*a,**k)
 def recv_into(self,*a,**k):self.settimeout(remaining(self.end));return super().recv_into(*a,**k)
def request(port,project,path,headers,v2,payload=None,method=None,timeout=2,startup=False):
 end=time.monotonic()+timeout;raw=socket.create_connection(('127.0.0.1',port),timeout=remaining(end))
 sock=DeadlineSocket(fileno=raw.detach());sock.end=end;remaining(end)
 conn=http.client.HTTPConnection('127.0.0.1',port,timeout=remaining(end));conn.sock=sock
 path+=('&' if '?' in path else '?')+urllib.parse.urlencode({'location[directory]' if v2 else 'directory':str(project)})
 try:
  body=json.dumps(payload).encode() if payload is not None else None
  sock.settimeout(remaining(end));conn.request(method or ('POST' if body is not None else 'GET'),path,body,{'Content-Type':'application/json',**headers})
  sock.settimeout(remaining(end));resp=conn.getresponse();data=resp.read(1048577);remaining(end)
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
 with tarfile.open(archive,'r:gz') as t:
  members=t.getmembers();need(len(members)<=24 and sum(m.size for m in members)<=160*1024*1024,'bounded_archive')
  bins=[m for m in members if m.isfile() and m.name in ['package/bin/opencode','package/bin/opencode.exe']];need(len(bins)==1,'one_regular_stock_image')
  m=bins[0];need(m.size<150*1024*1024 and not any(x.islnk() or x.issym() for x in members),'no_archive_alias')
  target=root/('opencode.exe' if os_name=='windows' else 'opencode');data=t.extractfile(m).read();need(hashlib.sha256(data).hexdigest()==pin['executableSHA256'],'official_image_SHA')
  if os_name=='linux':need(data[:4]==b'\x7fELF' and struct.unpack('<H',data[18:20])[0]==(62 if arch=='amd64' else 183),'ELF_arch')
  elif os_name=='darwin':need(data[:4]==b'\xcf\xfa\xed\xfe' and struct.unpack('<I',data[4:8])[0]==(0x1000007 if arch=='amd64' else 0x100000c),'MachO_arch')
  else:
   off=struct.unpack('<I',data[60:64])[0];need(data[:2]==b'MZ' and data[off:off+4]==b'PE\0\0' and struct.unpack('<H',data[off+4:off+6])[0]==0x8664,'PE_arch')
  target.write_bytes(data);target.chmod(0o700);return target
def wait_file(path,end,proc):
 while True:
  need(proc.poll() is None,'owned_host_ended_before_reader');remaining(end)
  if path.is_file():need(path.stat().st_size<=16384,'reader_receipt_bound');return json.loads(path.read_text())
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
def main(a):
 actual_os={'Linux':'linux','Darwin':'darwin','Windows':'windows'}[platform.system()];actual_arch={'x86_64':'amd64','AMD64':'amd64','arm64':'arm64','aarch64':'arm64'}[platform.machine()]
 need((a.os,a.arch)==(actual_os,actual_arch),'native_runner_cell');need(a.version in ['1.18.33','2.0.21'] and a.mode in ['api-only','one-completion'],'closed_scope')
 need(a.version=='1.18.33' or a.mode=='api-only','completion_fallback_V1_only')
 if a.host_netns_fd is not None:
  need(actual_os=='linux','Linux_NET_FD_only');import importlib.util
  hp=HERE/'retained/owned-test-cgroup.py';need(sha(hp)=='8a349bffd8a202daae9173b0686ec547bb3cffa126669e72c73ca0aa744d56f8','NET_helper_pin');sp=importlib.util.spec_from_file_location('owned_reader_NET',hp);hm=importlib.util.module_from_spec(sp);sp.loader.exec_module(hm);hm.private_netns(a.host_netns_fd);os.close(a.host_netns_fd)
  st=dict(x.split(':',1) for x in P('/proc/self/status').read_text().splitlines() if ':' in x);need(os.getresuid()==os.getresgid()==(1000,1000,1000) and not os.getgroups() and all(int(st[k].strip(),16)==0 for k in ['CapInh','CapPrm','CapEff','CapBnd','CapAmb']),'actual_normal_UID_cap0')
 root=a.root.absolute();need(root.name.startswith('TEST-') and not root.exists() and not any(x.is_symlink() for x in root.parents),'fresh_TEST_root')
 need(sha(a.bundle)==a.bundle_sha256 and sha(a.build_receipt)==a.build_receipt_sha256,'actual_bundle_build_custody');build=json.loads(a.build_receipt.read_text());need(build['status']=='genuine_portable_reader_bundle_built' and build['bundleSHA256']==a.bundle_sha256 and build['SDKArchiveSHA256']=='c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752','genuine_packed_factory_build')
 need(all(build['sourceLeafSHA256'].get(n)==sha(HERE/n) for n in ['consumer.mjs','candidate/native-v1.mjs','candidate/native-v2.mjs','candidate/protocol.mjs','retained/sdk.tgz','retained/pack-receipt.json']),'exact_compiled_source_graph')
 pins=HERE/'official-host-pins.json';need(sha(pins)=='59368f673e651f00e8a4d0a675365ba15f0752eacabc3e2ea68a2e91cf6b166f','official_closed_cell_pins')
 pin=next(x for x in json.loads(pins.read_text())['cells'] if (x['os'],x['arch'],x['version'])==(a.os,a.arch,a.version))
 fixed={str(x):sha(x) for x in [a.archive,a.bundle,a.build_receipt,pins,P(__file__),HERE/'retained/provider.py']}
 os.umask(0o077);root.mkdir(mode=0o700)
 if a.os=='windows':private_windows(root)
 project=root/'project';project.mkdir(mode=0o700);host=unpack(a.archive,root,pin,a.os,a.arch);e=env(root);key=secrets.token_bytes(32);(root/'trace-key').write_bytes(key)
 write(root/'.owned-test-root.json',{'purpose':'TEST portable packaged SDK reader'})
 write(root/'reader-authority-private.json',{'version':a.version,'os':a.os,'arch':a.arch,'official':pin,'parentPID':os.getpid(),'directory':str(project.resolve()),'imageSHA256':pin['executableSHA256'],'apiOnlyReadiness':True,'mode':a.mode})
 provider=Provider(root,key);provider.mode=a.mode;provider.http_attempts=0;provider.RequestHandlerClass=CountedProvider
 worker=threading.Thread(target=provider.serve_forever);worker.start();proc=None;log=None;headers={};v2=a.version=='2.0.21';success=False
 result={'status':'unqualified','cell':{'os':a.os,'arch':a.arch,'version':a.version},'mode':a.mode,'plannedProviderTransactions':1 if a.mode=='one-completion' else 0,'packagedReaderTransportObserved':False,'factoryFinalizationObserved':False,'sourceDescriptorGranted':False,**FLAGS}
 try:
  plug=root/'reader.js';shutil.copyfile(a.bundle,plug)
  models={'p0-completion':{'name':'TEST completion','limit':{'context':128000,'output':8192}}};endpoint=f'http://127.0.0.1:{provider.server_port}/v1'
  config={'model':'p0/p0-completion'}
  if v2:
   entry=root/'plugin-entry';entry.mkdir(mode=0o700);(entry/'server.js').write_text('export { default } from '+json.dumps(plug.resolve().as_uri())+';\n')
   config.update(update='disable',share='disabled',warming=False,formatter=False,lsp=False,websearch=False,plugins=[{'package':str(entry.resolve())}],permissions=[{'action':'*','resource':'*','effect':'deny'}],providers={'p0':{'name':'TEST loopback','package':'@opencode/ai/providers/openai-compatible','env':[],'settings':{'baseURL':endpoint,'apiKey':'sandbox-only','timeout':10000},'models':models}})
   password=secrets.token_hex(16);e['OPENCODE_PASSWORD']=password;headers={'Authorization':'Basic '+base64.b64encode(('opencode:'+password).encode()).decode()}
  else:config.update(plugin=[[plug.resolve().as_uri(),{}]],permission={'*':'deny'},provider={'p0':{'npm':'@ai-sdk/openai-compatible','name':'TEST loopback','options':{'baseURL':endpoint,'apiKey':'sandbox-only'},'models':models}})
  write(project/'opencode.json',config);e['OPENCODE_CONFIG']=str(project/'opencode.json')
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  log=(root/'host-private.log').open('xb');proc=subprocess.Popen([str(host),'serve','--hostname','127.0.0.1','--port',str(port)],cwd=project,env=e,stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=(a.os!='windows'))
  startup=time.monotonic()+35
  while True:
   need(proc.poll() is None,'host_startup_closed');remaining(startup)
   try:
    info=request(port,project,'/api/info' if v2 else '/global/health',headers,v2,timeout=min(1,remaining(startup)),startup=True);need(info['version']==a.version and (info.get('pid')==proc.pid if v2 else info.get('healthy') is True),'actual_host_identity');break
   except (ConnectionError,TimeoutError,OSError):time.sleep(min(.05,remaining(startup)))
  request(port,project,'/api/plugin' if v2 else '/config',headers,v2)
  ready=wait_file(root/'reader-ready.json',time.monotonic()+6,proc);need(ready['version']==a.version and ready['nativePID']==proc.pid and ready['parentPID']==os.getpid() and P(ready['actualExecPath']).resolve()==host.resolve() and ready['ownedImageSHA256']==pin['executableSHA256'],'actual_native_loader_image_scope')
  if v2:need(ready['firstReaderClosureObserved'] is True and ready['actualAbortRequested'] is True,'actual_first_reader_abort_ready_witness')
  if not v2:
   sid=request(port,project,'/session',headers,False,{'title':'TEST reader owned'})['id'];enc=urllib.parse.quote(sid,safe='')
   if a.mode=='api-only':request(port,project,'/session/'+enc,headers,False,{'title':'TEST empty reader'},'PATCH');request(port,project,'/session/'+enc,headers,False,method='DELETE')
   else:
    request(port,project,'/session/'+enc+'/message',headers,False,{'model':{'providerID':'p0','modelID':'p0-completion'},'parts':[{'type':'text','text':'Reply with one short TEST completion.'}]},timeout=35)
  closed=wait_file(root/'reader-closed.json',time.monotonic()+6,proc);need(closed['actualSDKDispose'] and closed['ownedImageSHA256']==pin['executableSHA256'],'actual_factory_dispose')
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
   else:independent_v1_fact(root,key,sid,request(port,project,'/session/'+enc+'/message',headers,False));result['factoryFinalizationObserved']=True
  need(provider.http_attempts==(1 if a.mode=='one-completion' else 0) and len(provider.records)==provider.http_attempts and not provider.gaps,'explicit_exact_provider_budget_no_retry')
  result.update(packagedReaderTransportObserved=True,ownedImageSHA256=pin['executableSHA256'],bundleSHA256=a.bundle_sha256,traceSHA256=sha(root/'reader-private.jsonl'),closedReceiptSHA256=sha(root/'reader-closed.json'));success=True
 except Exception as error:result['failure']=type(error).__name__+': '+str(error)
 finally:
  if proc is not None and proc.poll() is None:
   proc.terminate();result['ownedHostStopRequested']=True
   try:proc.wait(timeout=6)
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=2);result['hostForcedKillUsed']=True;success=False
  result['ownedLeaderWaitObserved']=proc is not None and proc.poll() is not None;result['hostProcessTreeClosure']='unproved_portable_leader_only'
  if log is not None:log.close()
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
 p.add_argument('--host-netns-fd',type=int);p.add_argument('--mode',choices=['api-only','one-completion'],default='api-only');sys.exit(main(p.parse_args()))
