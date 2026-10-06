"""Fresh exact-source stock OpenCode packaged-reader primitive for release CI.

Offline --build compiles this .mts consumer with immutable release sources and
vendored SDK 0.3.0. --typecheck-only performs no build/native execution. --execute
requires a sealed seven-cell release manifest. Linux uses a private netns;
Windows uses private files, minimal env and a loopback provider, without a
network isolation claim. Every run uses a new TEST project; reports grant only
a finite reader prerequisite.
"""
import argparse,importlib.util,re,zipfile,base64,hashlib,hmac,http.client,json,os,pathlib,platform,posixpath,secrets,shutil,socket,stat,struct,subprocess,sys,tarfile,threading,time,urllib.parse
from http.server import BaseHTTPRequestHandler
P=pathlib.Path;HERE=P(__file__).resolve().parent
REPO=HERE.parent
CANDIDATE='2692f443a3834c6e4090176d478e69faa684bca0'
SDK_SHA256='c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752'
CELLS={(o,a,v) for o,a in [('linux','amd64'),('linux','arm64'),('windows','amd64')] for v in ['1.18.33','2.0.21']}|{('linux','amd64','1.18.34')}
HTTP_DIAGNOSTICS={};CURRENT_STAGE='not_started'
FLAGS={'productionQualified':False,'installedQualified':False,'timePolicyQualified':False,'sourceEpochQualified':False,'finalSpanQualified':False,'platformLifetimeQualified':False,'loadedMappedBytesQualified':False,'fullNativeQualified':False,'clockQualified':False,'visibleDesktopQualified':False}
def need(v,c):
 if not v:raise ValueError(c)
def blob(name):
 return subprocess.check_output(['git','show',CANDIDATE+':'+name],cwd=REPO)
def candidate_import(name,specifier):
 # Git and JS module specifiers use POSIX logical paths even on Windows.
 need('\\' not in name and '\\' not in specifier,'POSIX_candidate_import_required')
 child=posixpath.normpath(str(pathlib.PurePosixPath(name).parent/specifier))
 need(child.startswith('opencode-plugin/') and '..' not in pathlib.PurePosixPath(child).parts,'candidate_import_escape')
 return child
def esbuild_command_for_magic(executable,node,magic):
 # npm postinstall may replace bin/esbuild with the native executable.
 # A native ELF/PE must never be passed to Node as JavaScript.
 return [str(executable)] if magic.startswith((b'\x7fELF',b'MZ')) else [str(node),str(executable)]
def esbuild_argv(executable,node):
 with executable.open('rb') as stream:magic=stream.read(4)
 return esbuild_command_for_magic(executable,node,magic)
def candidate_checkout_identity(head,top,dirty,untracked):
 need(head==CANDIDATE,'exact_candidate_checkout_HEAD_required')
 need(top==str(REPO),'exact_candidate_checkout_root_required')
 need(not dirty.strip(),'clean_tracked_candidate_checkout_required')
 need(not untracked.strip(),'no_untracked_candidate_source_required')
def verify_candidate_checkout():
 def git(*argv):return subprocess.check_output(['git',*argv],cwd=REPO,text=True).strip()
 candidate_checkout_identity(git('rev-parse','HEAD'),str(P(git('rev-parse','--show-toplevel')).resolve(strict=True)),
  git('status','--porcelain','--untracked-files=no'),
  git('ls-files','--others','--exclude-standard','--','.',':(exclude).task-tools/artifacts'))
def unique(items):
 out={}
 for key,value in items:
  need(key not in out,'duplicate_JSON_key');out[key]=value
 return out
def record_bytes(path,body,origin):
 return {'path':path,'sha256':hashlib.sha256(body).hexdigest(),'bytes':len(body),'origin':origin}
def artifacts_path(path):
 path=path.absolute();need(not any(p.is_symlink() for p in [path,*path.parents]),'artifact_symlink')
 need(path.resolve().is_relative_to(REPO/'.task-tools/artifacts'),'reader_artifact_path_required');return path
def network_guard(a):
 need(not any(os.environ.get(k) for k in ['GH_TOKEN','GITHUB_TOKEN','OPENAI_API_KEY','ANTHROPIC_API_KEY']),'credentials_forbidden')
 if a.os=='linux':
  host=os.environ.get('AN_HOST_NETNS','');current=os.readlink('/proc/self/ns/net')
  need(re.fullmatch(r'net:\[[1-9][0-9]*\]',host) and current!=host and {n for _,n in socket.if_nameindex()}=={'lo'},'fresh_loopback_only_network_namespace_required')
  need(os.geteuid()!=0,'unprivileged_native_execution_required')
  return {'host':host,'private':current,'interfaces':['lo']}
 else:
  # Windows uses the original finite fixture contract: owned private files,
  # minimal environment and only a configured loopback completion provider.
  # No blanket firewall or external-network isolation is asserted or granted.
  return {'platform':'windows','networkIsolationClaimed':False,
   'isolation':'private_files_minimal_env_loopback_provider_no_network_isolation_claim'}
def manifest_inputs(a):
 need(sha(a.manifest)==a.manifest_sha256,'same_original_manifest_SHA')
 m=json.loads(a.manifest.read_text(),object_pairs_hook=unique)
 need(m['candidateCommit']==m['buildRevision']==CANDIDATE and m.get('releaseScope')=='linux-windows','immutable_seven_cell_release_required')
 need({(c['os'],c['arch'],c['version']) for c in m['cells']}==CELLS and len(m['cells'])==7,'seven_unique_cells_required')
 def checked(item):
  need(set(item)=={'path','sha256'},'sealed_record_schema');path=(REPO/item['path']).absolute()
  need(not any(p.is_symlink() for p in [path,*path.parents]) and path.resolve().is_relative_to(REPO) and sha(path)==item['sha256'],'sealed_record_identity');return path
 for key in ['embedded','rebuilt','packageLock','sdkSource','qualificationSource']:checked(m['assets'][key])
 for key,path in [('embedded','internal/opencodeplugin/dist/agent-notifications.js'),('packageLock','opencode-plugin/package-lock.json')]:
  need(m['assets'][key]['sha256']==hashlib.sha256(blob(path)).hexdigest(),'original_candidate_asset_required')
 need(m['assets']['embedded']['sha256']==m['assets']['rebuilt']['sha256'],'original_rebuilt_embedded_identity')
 need(sha(checked(m['sdk']['archive']))==SDK_SHA256,'exact_vendored_SDK_required')
 pins=json.loads(blob('scripts/testdata/opencode-native-e2e/host-pins.json'))['cells']
 cell=next(c for c in m['cells'] if (c['os'],c['arch'],c['version'])==(a.os,a.arch,a.version))
 pin=next(c for c in pins if (c['os'],c['arch'],c['version'])==(a.os,a.arch,a.version))
 need(cell['archive']['sha256']==pin['archiveSHA256'] and cell['archiveSRI']==pin['archiveSRI'] and cell['executableSHA256']==pin['executableSHA256'],'official_cell_archive_custody')
 archive=checked(cell['archive']);binary=checked(cell['candidate'])
 expected=REPO/'dist'/('claude-notifications-'+a.os+'-'+a.arch+('.exe' if a.os=='windows' else ''))
 need(binary==expected,'normal_original_release_binary_required')
 custody={'candidateCommit':CANDIDATE,'embeddedSHA256':m['assets']['embedded']['sha256'],'candidateSHA256':sha(binary),'manifestSHA256':a.manifest_sha256,'sdkArchiveSHA256':SDK_SHA256,'hostArchiveSHA256':sha(archive),'hostSourceCommit':cell['hostSourceCommit']}
 return custody,pin,archive
def prepare_build(root):
 root=artifacts_path(root);need(root.name.startswith('TEST-') and not root.exists(),'fresh_build_root_required');root.mkdir(mode=0o700,parents=True)
 leaves=[];source_paths=set()
 def candidate(name):
  need(name.startswith('opencode-plugin/') and '..' not in P(name).parts,'candidate_import_path');body=blob(name)
  need(sha(REPO/name)==hashlib.sha256(body).hexdigest(),'candidate_source_modified_'+name)
  target=root/'candidate'/name;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(body)
  source_paths.add(name);leaves.append(record_bytes('candidate/'+name,body,'candidate:'+CANDIDATE))
  if name.endswith('.mjs'):
   for value in re.findall(r'(?:from\s*|import\s*)[\'\"](\.[^\'\"]+)[\'\"]',body.decode()):
    child=candidate_import(name,value)
    if child not in source_paths:candidate(child)
 for name in ['opencode-plugin/native-v1.mjs','opencode-plugin/native-v2.mjs','opencode-plugin/protocol.mjs']:candidate(name)
 sdk_name='opencode-plugin/vendor/universal-agent-plugins-opencode-events-0.3.0.tgz';sdk=blob(sdk_name)
 need(hashlib.sha256(sdk).hexdigest()==SDK_SHA256 and sha(REPO/sdk_name)==SDK_SHA256,'exact_original_vendored_SDK')
 (root/'sdk.tgz').write_bytes(sdk);leaves.append(record_bytes('sdk.tgz',sdk,'candidate:'+CANDIDATE))
 package=root/'node_modules/universal-agent-plugins-opencode-events';package.mkdir(parents=True)
 with tarfile.open(root/'sdk.tgz','r:gz') as packed:
  members=packed.getmembers();need(0<len(members)<=64 and sum(m.size for m in members)<=2097152,'bounded_exact_SDK')
  for member in members:
   relative=P(member.name);need(member.isfile() and relative.parts[0]=='package' and '..' not in relative.parts,'SDK_regular_leaf_required')
   body=packed.extractfile(member).read();target=package.joinpath(*relative.parts[1:]);target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(body)
   leaves.append(record_bytes(str(target.relative_to(root)).replace(os.sep,'/'),body,'sdk:'+SDK_SHA256+'#'+member.name))
 need(json.loads((package/'package.json').read_text())['version']=='0.3.0','exact_SDK_version')
 consumer=HERE/'release-opencode-reader.mts';shutil.copyfile(consumer,root/consumer.name)
 leaves.append(record_bytes(consumer.name,consumer.read_bytes(),'fresh-reader-consumer'))
 leaves.append(record_bytes('driver/release-opencode-reader.py',P(__file__).read_bytes(),'fresh-reader-driver'))
 return root,leaves
def compiler_argv(compiler,node):
 if compiler.suffix.lower()=='.cmd':
  # Run the npm-owned JS entry directly, avoiding cmd.exe quoting and batch
  # dispatch differences. This is vendor code, never handwritten Node glue.
  need(node is not None,'Windows_typecheck_node_required')
  entry=compiler.parent.parent/'typescript/bin/tsc';need(entry.is_file(),'pinned_typescript_JS_entry_required')
  return [str(node),str(entry)]
 return [str(compiler)]
def typecheck(root,compiler,node_types,node):
 config={'compilerOptions':{'strict':True,'noEmit':True,'allowJs':True,'checkJs':False,'skipLibCheck':True,'erasableSyntaxOnly':True,'target':'ES2023','module':'NodeNext','moduleResolution':'NodeNext','types':['node'],'typeRoots':[str(node_types)]},'files':['release-opencode-reader.mts']}
 (root/'tsconfig.json').write_text(json.dumps(config));subprocess.run([*compiler_argv(compiler,node),'-p',str(root/'tsconfig.json')],check=True,cwd=root,timeout=90)
def build(a):
 verify_candidate_checkout()
 root,leaves=prepare_build(a.build_root);typecheck(root,a.tsc,a.node_types,a.node)
 if a.typecheck_only:
  print('PASS strict erasable reader consumer typecheck; no build or native execution');return 0
 esbuild=a.esbuild.resolve(strict=True);need(esbuild.name=='esbuild' and esbuild.parent.name=='bin','pinned_esbuild_CLI_required')
 package=json.loads((esbuild.parent.parent/'package.json').read_text());lock=json.loads(blob('opencode-plugin/package-lock.json'))
 need(package['version']==lock['packages']['node_modules/esbuild']['version'],'exact_locked_esbuild_version')
 bundle=root/'reader.js';meta=root/'build-metafile.json'
 subprocess.run([*esbuild_argv(esbuild,a.node),'release-opencode-reader.mts','--bundle','--platform=node','--format=esm','--target=node22','--outfile='+str(bundle),'--metafile='+str(meta)],cwd=root,check=True,timeout=90)
 graph=json.loads(meta.read_text());known={leaf['path']:leaf for leaf in leaves};inputs=sorted(graph['inputs'])
 need(set(inputs)<=set(known) and all(sha(root/name)==known[name]['sha256'] for name in inputs),'closed_fresh_compiled_sourcegraph')
 need({'candidate/opencode-plugin/native-v1.mjs','candidate/opencode-plugin/native-v2.mjs','candidate/opencode-plugin/protocol.mjs','node_modules/universal-agent-plugins-opencode-events/v1.js','node_modules/universal-agent-plugins-opencode-events/v2.js'}<=set(inputs),'both_exact_native_generations_compiled')
 report={'schema':1,'status':'fresh_release_reader_bundle_built','candidateCommit':CANDIDATE,'sdkArchiveSHA256':SDK_SHA256,'sdkVersion':'0.3.0','bundleSHA256':sha(bundle),'metafileSHA256':sha(meta),'leaves':leaves,'compiledInputs':inputs,'compiler':{'esbuildVersion':package['version'],'typescriptVersion':subprocess.check_output([*compiler_argv(a.tsc,a.node),'--version'],text=True).strip(),'strict':True,'erasableSyntaxOnly':True},'qualificationGranted':False}
 write(root/'build-receipt.json',report);print(json.dumps({'bundle':str(bundle),'bundleSHA256':sha(bundle),'buildReceipt':str(root/'build-receipt.json'),'buildReceiptSHA256':sha(root/'build-receipt.json')}));return 0
def validate_build(report,bundle):
 need(report.get('schema')==1 and report.get('status')=='fresh_release_reader_bundle_built' and report.get('candidateCommit')==CANDIDATE and report.get('sdkArchiveSHA256')==SDK_SHA256 and report.get('sdkVersion')=='0.3.0' and report.get('qualificationGranted') is False,'fresh_reader_build_required')
 need(report['bundleSHA256']==sha(bundle) and report['compiler']['strict'] is True and report['compiler']['erasableSyntaxOnly'] is True,'fresh_strict_compilation_required')
 known={leaf['path']:leaf for leaf in report['leaves']};need(len(known)==len(report['leaves']) and set(report['compiledInputs'])<=set(known),'all_imported_leaves_reported')
 need({'candidate/opencode-plugin/native-v1.mjs','candidate/opencode-plugin/native-v2.mjs','candidate/opencode-plugin/protocol.mjs','node_modules/universal-agent-plugins-opencode-events/v1.js','node_modules/universal-agent-plugins-opencode-events/v2.js'}<=set(report['compiledInputs']),'both_real_SDK_readers_compiled')
 sdk_members={}
 with tarfile.open(bundle.parent/'sdk.tgz','r:gz') as packed:
  need(sha(bundle.parent/'sdk.tgz')==SDK_SHA256,'original_build_SDK_archive')
  for member in packed:
   need(member.isfile() and member.name.startswith('package/'),'regular_original_SDK_member')
   sdk_members['node_modules/universal-agent-plugins-opencode-events/'+member.name[len('package/'):]]=packed.extractfile(member).read()
 for name,leaf in known.items():
  need(not P(name).is_absolute() and '..' not in P(name).parts and name.replace('\\','/')==name,'sourcegraph_leaf_path')
  if name=='driver/release-opencode-reader.py':body=P(__file__).read_bytes()
  elif name=='release-opencode-reader.mts':body=(HERE/name).read_bytes()
  else:body=(bundle.parent/name).read_bytes()
  need(record_bytes(name,body,leaf['origin'])==leaf,'fresh_source_leaf_changed')
  if name.startswith('candidate/'):
   need(body==blob(name[len('candidate/'):]),'exact_release_leaf_required')
  if name.startswith('node_modules/'):
   need(body==sdk_members.get(name),'exact_packed_SDK_leaf_required')
  need(name.startswith('candidate/') or name in sdk_members or name in ['sdk.tgz','driver/release-opencode-reader.py','release-opencode-reader.mts'],'unexpected_compilation_leaf')
 need(set(sdk_members)<=set(known),'complete_packed_SDK_leaf_receipt')
def validate_v2_rows(rows,closed):
 registers=[r['value'] for r in rows if r['kind']=='rpc-register'];markers=[r['value'] for r in rows if r['kind']=='rpc-marker-consumed'];native=[r['value'] for r in rows if r['kind']=='native-v2']
 need(len(registers)==len(markers)==2 and [r['number'] for r in registers]==[1,2] and [r['number'] for r in markers]==[1,2],'two_real_RPC_marker_registrations')
 for register,marker in zip(registers,markers):
  envelope=marker['envelope'];need(envelope in native and envelope['type']==register['type'] and isinstance(envelope.get('id'),str) and envelope['id'].startswith('h:') and type(envelope.get('created')) in [int,float] and 0<envelope['created']<1e16 and set(envelope['data'])=={'nonce'} and isinstance(envelope['data']['nonce'],str) and envelope['data']['nonce'].startswith('h:'),'actual_native_RPC_marker_identity_timestamp')
  native_index=next(i for i,row in enumerate(rows) if row['kind']=='native-v2' and row['value']==envelope)
  marker_index=next(i for i,row in enumerate(rows) if row['kind']=='rpc-marker-consumed' and row['value']==marker)
  need(native_index<marker_index,'native_callback_precedes_marker_consumption')
 need(markers[0]['envelope']['id']!=markers[1]['envelope']['id'] and markers[0]['envelope']['data']['nonce']!=markers[1]['envelope']['data']['nonce'],'distinct_native_RPC_markers')
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
class Provider(__import__('http.server',fromlist=['HTTPServer']).HTTPServer):
 def __init__(self,root,key):
  self.lock=threading.Lock();self.http_attempts=0;self.records=[];self.gaps=[]
  super().__init__(('127.0.0.1',0),CountedProvider)
 def get_request(self):
  sock,address=super().get_request();sock.settimeout(4);return sock,address
class CountedProvider(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def denied(self):
  with self.server.lock:self.server.http_attempts+=1
  self.send_error(403)
 def do_POST(self):
  with self.server.lock:self.server.http_attempts+=1;n=self.server.http_attempts
  if self.server.mode!='one-completion' or n!=1 or self.path!='/v1/chat/completions':self.send_error(403);return
  try:
   size=int(self.headers.get('Content-Length','0'));need(0<size<=1048576,'bounded_provider_request')
   body=self.rfile.read(size);need(len(body)==size,'complete_provider_request');request=json.loads(body)
   need(request.get('model')=='p0-completion' and isinstance(request.get('messages'),list),'planned_completion_only')
   need(not any(row.get('role')=='tool' for row in request['messages']),'no_tool_result_or_retry')
   self.server.records.append({'requestKind':'ordinary','requestSHA256':hashlib.sha256(body).hexdigest(),'stream':request.get('stream') is True})
   if request.get('stream'):
    self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
    for delta,finish in [({'role':'assistant'},None),({'content':'TEST completion.'},None),({},'stop')]:
     chunk={'id':'chatcmpl-reader-test','object':'chat.completion.chunk','created':int(time.time()),'model':'p0-completion','choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
     self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode());self.wfile.flush()
    self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()
   else:
    body=json.dumps({'id':'chatcmpl-reader-test','object':'chat.completion','created':int(time.time()),'model':'p0-completion','choices':[{'index':0,'message':{'role':'assistant','content':'TEST completion.'},'finish_reason':'stop'}],'usage':{'prompt_tokens':10,'completion_tokens':6,'total_tokens':16}}).encode()
    self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
  except Exception as error:
   self.server.gaps.append(type(error).__name__);self.close_connection=True
 do_GET=denied;do_PUT=denied;do_PATCH=denied;do_DELETE=denied;do_HEAD=denied;do_OPTIONS=denied
def env(root):
 out={'PATH':os.environ.get('PATH',''),'LANG':'C.UTF-8','TZ':'UTC','CI':'true','OPENCODE_DISABLE_AUTOUPDATE':'true','OPENCODE_DISABLE_LSP_DOWNLOAD':'true','OPENCODE_DISABLE_SHARE':'true','OPENCODE_DISABLE_TELEMETRY':'true','OPENCODE_DISABLE_MODELS_FETCH':'1','NPM_CONFIG_OFFLINE':'true','NPM_CONFIG_FETCH_RETRIES':'0','NPM_CONFIG_AUDIT':'false','NPM_CONFIG_FUND':'false','AN_TEST_ROOT':str(root)}
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
 name='opencode.exe' if os_name=='windows' else 'opencode';target=root/name
 with (zipfile.ZipFile(archive) if archive.suffix=='.zip' else tarfile.open(archive,'r:gz')) as packed:
  zipped=isinstance(packed,zipfile.ZipFile);members=packed.infolist() if zipped else packed.getmembers()
  need(0<len(members)<=4096,'bounded_archive_members')
  matches=[m for m in members if pathlib.PurePosixPath(m.filename if zipped else m.name).name==name]
  need(len(matches)==1,'one_official_image');m=matches[0];size=m.file_size if zipped else m.size
  need(0<size<=256*1024*1024 and ((m.external_attr>>16)&0o170000!=0o120000 if zipped else m.isfile()),'bounded_regular_image')
  with (packed.open(m) if zipped else packed.extractfile(m)) as source,target.open('xb') as out:
   left=size
   while left:
    block=source.read(min(left,1048576));need(block and len(block)<=left,'exact_image_read');out.write(block);left-=len(block)
   need(source.read(1)==b'','exact_image_EOF')
 need(sha(target)==pin['executableSHA256'],'official_image_SHA')
 with target.open('rb') as image:
  data=image.read(64)
  if os_name=='linux':need(data[:4]==b'\x7fELF' and struct.unpack('<H',data[18:20])[0]==(62 if arch=='amd64' else 183),'ELF_arch')
  else:
   off=struct.unpack('<I',data[60:64])[0];need(data[:2]==b'MZ' and off+6<=size,'bounded_PE_header');image.seek(off);pe=image.read(6);need(pe[:4]==b'PE\0\0' and struct.unpack('<H',pe[4:6])[0]==0x8664,'PE_arch')
 target.chmod(0o700);return target

def wait_file(path,end,proc):
 while True:
  need(proc.poll() is None,'owned_host_ended_before_reader');remaining(end)
  if path.is_file():need(path.stat().st_size<=16384,'reader_receipt_bound');return json.loads(path.read_text())
  time.sleep(min(.02,remaining(end)))
def independent_v1_fact(root,key,sid,history):
 rows=[json.loads(l) for l in (root/'reader-private.jsonl').read_text().splitlines()]
 return validate_v1_rows(rows,key,sid,history)
def validate_v1_rows(rows,key,sid,history):
 H=lambda x:'h:'+hmac.new(key,str(x).encode(),hashlib.sha256).hexdigest()[:24]
 facts=[r['value'] for r in rows if r['kind']=='sdk-fact'];need(len(facts)==1,'one_actual_SDK_fact')
 fact=facts[0]['event'];answers=[x['info'] for x in history if x['info']['role']=='assistant'];users=[x['info'] for x in history if x['info']['role']=='user']
 need(len(answers)==len(users)==1,'one_native_user_assistant');a,u=answers[0],users[0]
 need(facts[0]['currentAtCallback'] and facts[0]['clockID']=='local-performance' and fact['rootSession'] and fact['kind']=='turn_idle_verified' and fact['sessionID']==H(sid) and fact['turnID']==H(u['id']) and fact['messageID']==H(a['id']),'native_fact_identity')
 need(a['sessionID']==u['sessionID']==sid and a['parentID']==u['id'] and a.get('finish')=='stop' and not a.get('summary') and not a.get('error') and all(type(value) is int and value>0 for value in [u['time']['created'],a['time']['created'],a['time']['completed']]),'ordinary_final_native_assistant')
 need(not any(p.get('type')=='tool' for x in history for p in x.get('parts',[])),'zero_native_tools')
 p=fact['provenance'];need(p['generation']=='v1' and p['timeBasis']=='assistant_completed' and p['nativeTime']==a['time']['completed'],'original_native_time_no_restamp')
 need(any(r['kind']=='native-v1' and r['value'].get('type')=='message.updated' and r['value'].get('properties',{}).get('info',{}).get('id')==H(a['id']) and r['value']['properties']['info'].get('time',{}).get('completed')==a['time']['completed'] for r in rows),'independent_final_callback')
 return {'sessionID':H(sid),'userID':H(u['id']),'assistantID':H(a['id']),'userCreated':u['time']['created'],'assistantCreated':a['time']['created'],'assistantCompleted':a['time']['completed'],'finish':a['finish']}
def execute(a):
 global CURRENT_STAGE
 HTTP_DIAGNOSTICS.clear()
 def stage(value):
  global CURRENT_STAGE
  CURRENT_STAGE=value
 actual_os={'Linux':'linux','Darwin':'darwin','Windows':'windows'}[platform.system()];actual_arch={'x86_64':'amd64','AMD64':'amd64','arm64':'arm64','aarch64':'arm64'}[platform.machine()]
 need((a.os,a.arch)==(actual_os,actual_arch),'native_runner_cell');need((a.os,a.arch,a.version) in CELLS and a.mode in ['api-only','one-completion'],'closed_scope')
 need(a.mode==('api-only' if a.version=='2.0.21' else 'one-completion'),'exact_provider_budget_for_generation')
 need(os.environ.get('GITHUB_ACTIONS')=='true' and os.environ.get('SOURCE_COMMIT')==CANDIDATE,'explicit_immutable_release_CI_execution_only')
 namespace=network_guard(a)
 verify_candidate_checkout()
 root=artifacts_path(a.root);need(root.name.startswith('TEST-') and not root.exists() and not any(x.is_symlink() for x in root.parents),'fresh_TEST_root')
 custody,pin,archive=manifest_inputs(a);a.archive=archive
 build=json.loads(a.build_receipt.read_text());need(sha(a.bundle)==a.bundle_sha256 and sha(a.build_receipt)==a.build_receipt_sha256,'actual_fresh_bundle_build_custody')
 validate_build(build,a.bundle)
 fixed={str(x):sha(x) for x in [a.archive,a.bundle,a.build_receipt,a.manifest,P(__file__),HERE/'release-opencode-reader.mts']}
 original=json.loads(a.manifest.read_text(),object_pairs_hook=unique)
 for item in [*original['assets'].values(),original['sdk']['archive'],* [c['candidate'] for c in original['cells'] if (c['os'],c['arch'])==(a.os,a.arch)]]:
  path=REPO/item['path'];fixed[str(path)]=sha(path)
 for leaf in build['leaves']:
  if leaf['path'].startswith('candidate/'):
   path=REPO/leaf['path'][len('candidate/'):];fixed[str(path)]=sha(path)
 os.umask(0o077);root.mkdir(mode=0o700)
 if a.os=='windows':private_windows(root)
 project=root/'project';project.mkdir(mode=0o700);host=unpack(a.archive,root,pin,a.os,a.arch);e=env(root)
 if a.version!='2.0.21':e.update(NPM_CONFIG_OFFLINE='true',NPM_CONFIG_FETCH_RETRIES='0',OPENCODE_DISABLE_MODELS_FETCH='1')
 key=secrets.token_bytes(32);(root/'trace-key').write_bytes(key)
 write(root/'.owned-test-root.json',{'purpose':'TEST portable packaged SDK reader'})
 write(root/'reader-authority-private.json',{'version':a.version,'os':a.os,'arch':a.arch,'official':pin,'parentPID':os.getpid(),'directory':str(project.resolve()),'imageSHA256':pin['executableSHA256'],'apiOnlyReadiness':True,'mode':a.mode})
 provider=Provider(root,key);provider.mode=a.mode;provider.http_attempts=0;provider.RequestHandlerClass=CountedProvider
 worker=threading.Thread(target=provider.serve_forever);worker.start();proc=None;log=None;headers={};v2=a.version=='2.0.21';success=False
 result={'status':'unqualified','cell':{'os':a.os,'arch':a.arch,'version':a.version},'mode':a.mode,'schema':1,'purpose':'TEST fresh stock OpenCode packaged reader primitive',**custody,'plannedProviderTransactions':1 if a.mode=='one-completion' else 0,'sourceGraph':build,'nativeNetworkBoundary':namespace,'freshTestRootCreated':True,'packagedReaderTransportObserved':False,'factoryFinalizationObserved':False,'sourceDescriptorGranted':False,**FLAGS}
 stage('configured_loader_preparation')
 try:
  version=subprocess.check_output([str(host),'--version'],cwd=project,env=e,timeout=8,text=True).strip();need(version in [a.version,'opencode v'+a.version],'exact_installed_stock_runtime_version');result['runtimeVersionOutput']=version
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
  stage('owned_host_spawn')
  log=(root/'host-private.log').open('xb');proc=subprocess.Popen([str(host),'serve','--hostname','127.0.0.1','--port',str(port)],cwd=project,env=e,stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=(a.os!='windows'))
  stage('owned_host_health');startup=time.monotonic()+35
  while True:
   need(proc.poll() is None,'host_startup_closed');remaining(startup)
   try:
    info=request(port,project,'/api/info' if v2 else '/global/health',headers,v2,timeout=min(1,remaining(startup)),startup=True);need(info['version']==a.version and (info.get('pid')==proc.pid if v2 else info.get('healthy') is True),'actual_host_identity');result['installedRuntimeVersion']=info['version'];break
   except (ConnectionError,TimeoutError,OSError):time.sleep(min(.05,remaining(startup)))
  stage('native_project_activation')
  request(port,project,'/api/plugin' if v2 else '/config',headers,v2)
  stage('native_reader_readiness')
  ready=wait_file(root/'reader-ready.json',time.monotonic()+6,proc);need(ready['version']==a.version and ready['nativePID']==proc.pid and ready['parentPID']==os.getpid() and P(ready['actualExecPath']).resolve()==host.resolve() and ready['ownedImageSHA256']==pin['executableSHA256'],'actual_native_loader_image_scope')
  if v2:need(ready['firstReaderClosureObserved'] is True and ready['actualAbortRequested'] is True,'actual_first_reader_abort_ready_witness')
  result['readerReady']=ready
  if not v2:
   stage('native_owned_session_create')
   sid=request(port,project,'/session',headers,False,{'title':'TEST reader owned'})['id'];enc=urllib.parse.quote(sid,safe='')
   if a.mode=='api-only':
    stage('native_empty_session_callbacks');request(port,project,'/session/'+enc,headers,False,{'title':'TEST empty reader'},'PATCH');request(port,project,'/session/'+enc,headers,False,method='DELETE')
   else:
    stage('native_one_completion');request(port,project,'/session/'+enc+'/message',headers,False,{'model':{'providerID':'p0','modelID':'p0-completion'},'parts':[{'type':'text','text':'Reply with one short TEST completion.'}]},timeout=35)
  stage('native_reader_disposal')
  closed=wait_file(root/'reader-closed.json',time.monotonic()+6,proc);need(closed['actualSDKDispose'] and closed['ownedImageSHA256']==pin['executableSHA256'],'actual_factory_dispose')
  result['readerClosed']=closed
  stage('independent_reader_oracle')
  rows=[json.loads(x) for x in (root/'reader-private.jsonl').read_text().splitlines()];need(len(rows)<=512 and (root/'reader-private.jsonl').stat().st_size<=1048576 and not any(x['kind']=='uncertainty' for x in rows),'bounded_certain_real_ingress')
  result['observations']=rows
  if v2:
   need(closed['actualSDKDone'] and closed['subscriptions']==closed['actualNativeReaderClosures']==closed['registrations']==closed['actualRegistrationDisposals']==2 and closed['markerReads']==2 and closed['sdkFacts']==0,'two_actual_readers_RPC_markers_and_closures')
   need(closed['actualAbortRequested'] is True,'actual_planned_abort_closed_witness')
   closes=[(i,x['value']) for i,x in enumerate(rows) if x['kind']=='native-reader-close'];aborts=[(i,x['value']) for i,x in enumerate(rows) if x['kind']=='requested-native-abort']
   need(len(closes)==2 and [x['number'] for _,x in closes]==[1,2] and closes[0][1]['actualSignalAborted'] is True and len(aborts)==1 and aborts[0][1]['number']==1 and aborts[0][0]<closes[0][0],'independent_actual_abort_before_first_close')
   validate_v2_rows(rows,closed);result['sourceReplacementDelayMs']=250
  else:
   need(closed['actualTrackedJobsSettled'] and closed['sdkFacts']==1 and closed['nativeCalls']>0 and sum(x['kind']=='sdk-observe-invoked' for x in rows)==closed['nativeCalls'],'real_callback_observe_drain')
   if a.mode=='api-only':
    H=lambda x:'h:'+hmac.new(key,str(x).encode(),hashlib.sha256).hexdigest()[:24]
    matching=[x['value'] for x in rows if x['kind']=='native-v1' and x['value'].get('properties',{}).get('info',{}).get('id')==H(sid)]
    need({'session.created','session.updated','session.deleted'}<={x['type'] for x in matching} and closed['sdkFacts']==0,'actual_empty_session_callback_membership')
   else:
    stage('independent_native_history');result['nativeV1Identity']=independent_v1_fact(root,key,sid,request(port,project,'/session/'+enc+'/message',headers,False));result['factoryFinalizationObserved']=True
  need(provider.http_attempts==(1 if a.mode=='one-completion' else 0) and len(provider.records)==provider.http_attempts and not provider.gaps,'explicit_exact_provider_budget_no_retry')
  result.update(packagedReaderTransportObserved=True,ownedImageSHA256=pin['executableSHA256'],bundleSHA256=a.bundle_sha256,traceSHA256=sha(root/'reader-private.jsonl'),closedReceiptSHA256=sha(root/'reader-closed.json'));success=True
 except Exception as error:
  result['firstFailedStage']=CURRENT_STAGE;result['exceptionKind']=type(error).__name__;result['failure']=type(error).__name__
  write(root/'first-exception-private.json',{'stage':CURRENT_STAGE,'kind':type(error).__name__,'message':str(error)[:512]})
 finally:
  if proc is not None and proc.poll() is None:
   proc.terminate();result['ownedHostStopRequested']=True
   try:proc.wait(timeout=6)
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=2);result['hostForcedKillUsed']=True;success=False
  result['ownedLeaderWaitObserved']=proc is not None and proc.poll() is not None;result['hostProcessTreeClosure']='unproved_portable_leader_only'
  if log is not None:
   log.close();hp=root/'host-private.log';size=hp.stat().st_size;result['hostPrivateLogSHA256']=sha(hp);result['hostPrivateLogBytes']=size
   with hp.open('rb') as source,(root/'host-startup-prefix-private.log').open('xb') as dest:dest.write(source.read(65536))
   result['hostStartupPrefixBytes']=min(size,65536);result['hostStartupPrefixTruncated']=size>65536
   if size>1048576:result['hostPrivateLogOverflow']=True;success=False
  result['ownHTTPDiagnostics']={k:dict(v) for k,v in HTTP_DIAGNOSTICS.items()}
  provider.shutdown();provider.server_close();worker.join(timeout=3);result['providerThreadJoined']=not worker.is_alive()
  result['actualProviderHTTPRequestAttempts']=provider.http_attempts;result['actualProviderTransactions']=len(provider.records);result['providerReceipts']=provider.records;result['actualToolCalls']=0 if not provider.records or all(x.get('requestKind')=='ordinary' for x in provider.records) else None
  result['sourcesArchiveBundleUnchanged']=all(sha(P(n))==h for n,h in fixed.items()) and sha(host)==pin['executableSHA256']
  try:verify_candidate_checkout()
  except Exception:result['sourcesArchiveBundleUnchanged']=False
  if not result['providerThreadJoined'] or not result['sourcesArchiveBundleUnchanged'] or provider.http_attempts!=(1 if a.mode=='one-completion' else 0):success=False
  result['status']='release_packaged_reader_observed' if success else 'unqualified';write(root/'result.json',result);write(artifacts_path(a.report),result)
 print(json.dumps({'status':result['status'],'resultSHA256':sha(root/'result.json'),'cell':result['cell'],'actualProviderTransactions':result['actualProviderTransactions'],'qualificationGranted':False}));return 0 if success else 1

def self_test():
 need(candidate_import('opencode-plugin/subdir/view.mjs','../protocol.mjs')=='opencode-plugin/protocol.mjs','portable_POSIX_import_normalization')
 need(candidate_import('opencode-plugin/native-v2.mjs','./protocol.mjs')=='opencode-plugin/protocol.mjs','portable_same_directory_import')
 for specifier in ['../../outside.mjs','..\\outside.mjs','/outside.mjs']:
  try:candidate_import('opencode-plugin/native-v2.mjs',specifier)
  except ValueError:continue
  raise AssertionError('nonportable_or_escaping_import_accepted')
 for magic in [b'\x7fELF',b'MZ\x90\0']:
  need(esbuild_command_for_magic('esbuild','node',magic)==['esbuild'],'native_esbuild_must_execute_directly')
 need(esbuild_command_for_magic('esbuild','node',b'#!/u')==['node','esbuild'],'JS_esbuild_must_execute_with_Node')
 candidate_checkout_identity(CANDIDATE,str(REPO),'','')
 for values in [('0'*40,str(REPO),'',''),(CANDIDATE,str(REPO/'other'),'',''),
                (CANDIDATE,str(REPO),' M opencode-plugin/native-v1.mjs',''),
                (CANDIDATE,str(REPO),'','unexpected-source.mts')]:
  try:candidate_checkout_identity(*values)
  except ValueError:continue
  raise AssertionError('wrong_or_modified_candidate_checkout_accepted')
 # Reject counterfeit RPC counts without the independent native callback,
 # or with missing original identities/timestamps; no host is ever launched.
 registers=[{'kind':'rpc-register','value':{'number':i,'type':'rpc.test'+str(i)+'.checkpoint'}} for i in [1,2]]
 rows=registers[:]
 for i in [1,2]:
  envelope={'id':'h:'+str(i),'created':100+i,'type':'rpc.test'+str(i)+'.checkpoint','data':{'nonce':'h:nonce'+str(i)}}
  rows.extend([{'kind':'native-v2','value':envelope},{'kind':'rpc-marker-consumed','value':{'number':i,'markerReads':i,'envelope':envelope}}])
 validate_v2_rows(rows,{})
 for alteration in ['missing_callback','missing_identity','restamped_time','duplicate_marker','callback_after_marker']:
  bad=json.loads(json.dumps(rows))
  if alteration=='missing_callback':bad=[r for r in bad if r['kind']!='native-v2']
  elif alteration=='missing_identity':bad[-1]['value']['envelope'].pop('id')
  elif alteration=='restamped_time':bad[-1]['value']['envelope']['created']+=1
  elif alteration=='duplicate_marker':bad[-1]['value']['envelope']=bad[-3]['value']['envelope']
  else:bad[-2],bad[-1]=bad[-1],bad[-2]
  try:validate_v2_rows(bad,{})
  except (ValueError,KeyError):continue
  raise AssertionError('counterfeit_native_marker_accepted_'+alteration)
 key=b'\0'*32;H=lambda x:'h:'+hmac.new(key,str(x).encode(),hashlib.sha256).hexdigest()[:24]
 user={'id':'user-test','sessionID':'session-test','role':'user','time':{'created':100}}
 assistant={'id':'assistant-test','sessionID':'session-test','parentID':'user-test','role':'assistant','finish':'stop','time':{'created':101,'completed':102}}
 fact={'kind':'turn_idle_verified','rootSession':True,'sessionID':H('session-test'),'turnID':H('user-test'),'messageID':H('assistant-test'),'provenance':{'generation':'v1','timeBasis':'assistant_completed','nativeTime':102}}
 callback={'type':'message.updated','properties':{'info':{'id':H('assistant-test'),'time':{'completed':102}}}}
 rows=[{'kind':'sdk-fact','value':{'event':fact,'currentAtCallback':True,'clockID':'local-performance'}},{'kind':'native-v1','value':callback}]
 history=[{'info':user,'parts':[]},{'info':assistant,'parts':[]}]
 validate_v1_rows(rows,key,'session-test',history)
 for alteration in ['missing_final_callback','wrong_user_identity','restamped_completion','duplicate_sdk_fact','tool_called']:
  bad=json.loads(json.dumps(rows));native_history=json.loads(json.dumps(history))
  if alteration=='missing_final_callback':bad.pop()
  elif alteration=='wrong_user_identity':bad[0]['value']['event']['turnID']=H('other-user')
  elif alteration=='restamped_completion':bad[0]['value']['event']['provenance']['nativeTime']=103
  elif alteration=='duplicate_sdk_fact':bad.append(bad[0])
  else:native_history[1]['parts'].append({'type':'tool'})
  try:validate_v1_rows(bad,key,'session-test',native_history)
  except (ValueError,KeyError):continue
  raise AssertionError('counterfeit_native_completion_accepted_'+alteration)
 print('PASS V1 original native completion/identity/callback and V2 original RPC marker identity/time/membership')
 return 0

def main():
 global REPO
 p=argparse.ArgumentParser(description=__doc__);m=p.add_mutually_exclusive_group(required=True)
 for mode in ['build','execute','typecheck-only','self-test']:m.add_argument('--'+mode,action='store_true')
 p.add_argument('--candidate-repo','--source-root',dest='candidate_repo',type=P)
 for n in ['build-root','node','tsc','node-types','esbuild','root','manifest','bundle','build-receipt','report']:p.add_argument('--'+n,type=P)
 for n in ['manifest-sha256','bundle-sha256','build-receipt-sha256','os','arch','version']:p.add_argument('--'+n)
 a=p.parse_args()
 try:
  if a.self_test:return self_test()
  required=['candidate_repo','build_root','tsc','node_types'] if a.build or a.typecheck_only else ['candidate_repo','root','manifest','manifest_sha256','bundle','bundle_sha256','build_receipt','build_receipt_sha256','os','arch','version','report']
  if a.build:required+=['node','esbuild']
  need(all(getattr(a,key) is not None for key in required),'mode_arguments_required')
  REPO=a.candidate_repo.resolve(strict=True)
  if a.build or a.typecheck_only:return build(a)
  need((a.os,a.arch,a.version) in CELLS,'closed_seven_cell_scope')
  a.mode='api-only' if a.version=='2.0.21' else 'one-completion'
  return execute(a)
 except Exception as error:
  if a.report is not None:
   report=artifacts_path(a.report)
   if not report.exists():write(report,{'schema':1,'status':'unqualified','firstFailedPrerequisite':str(error)[:256],'businessPhasesStarted':False,**FLAGS})
  print(json.dumps({'status':'unqualified','failure':str(error)[:256]}),file=sys.stderr);return 1

if __name__=='__main__':sys.exit(main())
