"""TEST hosted asset build only; no installed business, provider, or qualification."""
import argparse,hashlib,json,os,pathlib,platform,re,shutil,subprocess,tarfile
P=pathlib.Path

def need(v,c):
 if not v:raise ValueError(c)
def sha(p):
 need(p.is_file() and not p.is_symlink(),'regular input');return hashlib.sha256(p.read_bytes()).hexdigest()
def inventory(root, source_manifest_sha256):
 entries,filesystem={},{}
 rows=subprocess.check_output(['git','-C',str(root),'ls-tree','-rz','HEAD']).split(b'\0')
 for row in rows:
  if not row:continue
  header,name=row.split(b'\t',1);mode,kind,oid=header.split();name=name.decode('utf-8');p=root/name
  need(kind==b'blob' and mode in (b'100644',b'100755',b'120000') and not P(name).is_absolute() and '..' not in P(name).parts,'closed tracked Git blob')
  need(not any(q.is_symlink() for q in p.parents if q!=root and q.is_relative_to(root)),'tracked ancestor symlink')
  if mode==b'120000':
   if p.is_symlink():raw=os.fsencode(os.readlink(p));representation='native_symlink'
   else:
    need(os.name=='nt' and p.is_file(),'Windows Git symlink text only');raw=p.read_bytes();representation='windows_symlink_text'
   need(0<len(raw)<=4096 and raw.isascii(),'closed ASCII Git link text')
  else:
   need(p.is_file() and not p.is_symlink(),'tracked regular');raw=p.read_bytes();representation='regular'
  need(hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()==oid.decode(),'physical payload differs from actual Git blob')
  entries[name]={'mode':mode.decode(),'sha256':hashlib.sha256(raw).hexdigest()}
  filesystem[name]={'representation':representation,'mode':p.lstat().st_mode&0o777,'payloadSHA256':entries[name]['sha256']}
 head=subprocess.check_output(['git','-C',str(root),'rev-parse','HEAD'],text=True).strip()
 manifest={'entries':entries,'hashes':{n:r['sha256'] for n,r in entries.items()},'head':head,'modes':{n:r['mode'] for n,r in entries.items()}}
 need(len(entries)==1170 and hashlib.sha256((json.dumps(manifest,sort_keys=True,indent=2)+'\n').encode()).hexdigest()==source_manifest_sha256,'exact approved whole-source manifest')
 return {'manifest':manifest,'filesystem':filesystem}

def main(a):
 need(re.fullmatch('[0-9a-f]{40}',a.head) and re.fullmatch('[0-9a-f]{64}',a.source_manifest_sha256) and sha(P(__file__))==a.recipe_sha256,'exact recipe/head/ROOT source manifest')
 src=a.source.resolve();root=a.out.absolute();need(root.name.startswith('TEST-') and not root.exists(),'fresh TEST build')
 need(subprocess.check_output(['git','-C',str(src),'rev-parse','HEAD'],text=True).strip()==a.head and not subprocess.check_output(['git','-C',str(src),'status','--porcelain','--untracked-files=all']),'clean final checkout')
 want={'Linux':'linux','Darwin':'darwin','Windows':'windows'}[platform.system()];arch={'aarch64':'arm64','arm64':'arm64','x86_64':'amd64','AMD64':'amd64'}[platform.machine()]
 need((want,arch)==(a.os,a.arch) and (want,arch) in [('linux','arm64'),('darwin','amd64'),('darwin','arm64'),('windows','amd64')],'actual native cell')
 before=inventory(src, a.source_manifest_sha256);root.mkdir(mode=0o700);logs=root/'logs';logs.mkdir();commands=[]
 go=P(shutil.which('go')).resolve();env={k:os.environ[k] for k in ('PATH','SystemRoot','WINDIR','COMSPEC','PATHEXT') if k in os.environ}
 for key,sub in [('HOME','home'),('USERPROFILE','home'),('TMPDIR','tmp'),('TEMP','tmp'),('TMP','tmp'),('GOCACHE','gocache'),('GOMODCACHE','gomodcache')]:
  (root/sub).mkdir(mode=0o700,exist_ok=True);env[key]=str(root/sub)
 env.update(CGO_ENABLED='1',GOOS=a.os,GOARCH=a.arch,GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOTELEMETRY='off',GOFLAGS='-mod=readonly',GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org',MACOSX_DEPLOYMENT_TARGET='12.0')
 def run(label,argv,cwd=src,timeout=900):
  with (logs/(label+'.stdout')).open('xb') as out,(logs/(label+'.stderr')).open('xb') as err:
   p=subprocess.run(argv,cwd=cwd,env=env,stdout=out,stderr=err,timeout=timeout)
  commands.append({'label':label,'argv':list(map(str,argv)),'exitCode':p.returncode,'stdoutSHA256':sha(logs/(label+'.stdout')),'stderrSHA256':sha(logs/(label+'.stderr'))});need(p.returncode==0,label+' failed')
  return (logs/(label+'.stdout')).read_text()
 version=run('go-version',[str(go),'version'],timeout=15);need(version.startswith('go version go1.27.1 '),'Go1.27.1 required')
 # Public download is a separate factual step. Standard Go proxy/checksum verification stays on.
 run('public-modules',[str(go),'mod','download','-json','all'])
 binary=root/('claude-notifications-'+a.os+'-'+a.arch+('.exe' if a.os=='windows' else ''))
 run('build',[str(go),'build','-trimpath','-buildvcs=true','-ldflags=-s -w','-o',str(binary),'./cmd/claude-notifications'])
 info=run('buildinfo',[str(go),'version','-m',str(binary)],timeout=15)
 for field,value in [('CGO_ENABLED','1'),('GOOS',a.os),('GOARCH',a.arch),('vcs.revision',a.head),('vcs.modified','false')]:need(re.search(r'(?m)^\s*build\s+'+re.escape(field)+'='+re.escape(value)+r'\s*$',info),'build '+field)
 app=None
 if a.os=='darwin' and a.arch=='arm64':
  swiftroot=root/'swift-source';swiftroot.mkdir();shutil.copytree(src/'swift-notifier',swiftroot/'swift-notifier',ignore=shutil.ignore_patterns('.build','*.app','*.app.managed-runtime.json'))
  shutil.copyfile(src/'claude_icon.png',swiftroot/'claude_icon.png')
  run('swift-app',['/bin/bash',str(swiftroot/'swift-notifier/scripts/build-app.sh'),'--no-register'],cwd=swiftroot)
  bundle=swiftroot/'swift-notifier/ClaudeNotifier.app';sidecar=P(str(bundle)+'.managed-runtime.json')
  run('codesign-verify',['/usr/bin/codesign','--verify','--deep','--strict','-R','=identifier "com.777genius.agent-notifications"',str(bundle)],timeout=15)
  executable=bundle/'Contents/MacOS/terminal-notifier-modern';run('lipo-verify',['/usr/bin/lipo','-verify_arch','arm64','x86_64',str(executable)],timeout=15)
  need(json.loads(sidecar.read_text())=={'SchemaVersion':1,'ProtocolVersion':1,'DecoderFloor':1,'ExecutableSHA256':sha(executable)},'actual signed app sidecar')
  appfiles={str(p.relative_to(bundle)):{'sha256':sha(p),'mode':p.stat().st_mode&0o777} for p in sorted(bundle.rglob('*')) if p.is_file()}
  archive=root/'ClaudeNotifier.app.tar';need(not any(p.is_symlink() for p in bundle.rglob('*')),'app symlink unsupported')
  with tarfile.open(archive,'w') as packed:packed.add(bundle,arcname='ClaudeNotifier.app');packed.add(sidecar,arcname=sidecar.name)
  need(appfiles=={str(p.relative_to(bundle)):{'sha256':sha(p),'mode':p.stat().st_mode&0o777} for p in sorted(bundle.rglob('*')) if p.is_file()},'signed app changed')
  app={'archiveSHA256':sha(archive),'executableSHA256':sha(executable),'sidecarSHA256':sha(sidecar),'sidecarMode':sidecar.stat().st_mode&0o777,'files':appfiles,'signatureVerification':'native_codesign_deep_strict_identifier','signatureKind':'TEST_ad_hoc','architectures':['amd64','arm64'],'registered':False,'notarized':False}
 need(before==inventory(src, a.source_manifest_sha256) and not subprocess.check_output(['git','-C',str(src),'status','--porcelain','--untracked-files=all']),'final source changed')
 result={'status':'native_assets_built_only','candidateCommit':a.head,'os':a.os,'arch':a.arch,'goVersion':version.strip(),'goSHA256':sha(go),'binary':{'name':binary.name,'sha256':sha(binary),'size':binary.stat().st_size,'mode':binary.stat().st_mode&0o777},'buildinfo':info,'sourceUnchanged':True,'approvedSourceManifestSHA256':a.source_manifest_sha256,'trackedSymlinkRepresentation':{n:r for n,r in before['filesystem'].items() if before['manifest']['modes'][n]=='120000'},'sourceInventorySHA256':hashlib.sha256(json.dumps(before,sort_keys=True,separators=(',',':')).encode()).hexdigest(),'goModSHA256':sha(src/'go.mod'),'goSumSHA256':sha(src/'go.sum'),'commands':commands,'app':app,'businessExecuted':False,'qualificationGranted':False}
 (root/'result.json').write_text(json.dumps(result,indent=2,sort_keys=True)+'\n')
 # Only finite final assets/logs leave the runner. No private HOME or module cache.
 export=root/'export';export.mkdir();shutil.copyfile(binary,export/binary.name);(export/binary.name).chmod(binary.stat().st_mode&0o777);shutil.copyfile(root/'result.json',export/'result.json');shutil.copytree(logs,export/'logs')
 if app:shutil.copyfile(root/'ClaudeNotifier.app.tar',export/'ClaudeNotifier.app.tar')
 print(str(export))
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--source',type=P,required=True);p.add_argument('--out',type=P,required=True);p.add_argument('--head',required=True);p.add_argument('--recipe-sha256',required=True);p.add_argument('--source-manifest-sha256',required=True);p.add_argument('--os',choices=['linux','darwin','windows'],required=True);p.add_argument('--arch',choices=['amd64','arm64'],required=True);main(p.parse_args())
