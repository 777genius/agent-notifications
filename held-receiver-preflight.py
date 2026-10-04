"""TEST-only Linux ARM receiver build/stage. No receiver execution or product VCS claim."""
import argparse,hashlib,importlib.util,json,os,pathlib,re,shutil,signal,stat,struct,subprocess,time
P=pathlib.Path
SOURCE_COMMIT='9b964362482af82656deddf8122ba7e821f962c6'
SOURCE_SHA='e5d5d1d699c2fbd0e3024ae0b2ed5772d19b36238952fc6b40f84d8dde2e5e77'
SUM='h1:TUR3TgtSVDmjiXOgAAyaZbYmIeP3DPkld3jgKGV8mXQ='
MODSUM='h1:3AAv2+hPq5rdnr5txxxRwiGjPXamgoIHgz9FPBfOp3c='
CACHE={'v5.2.2.info':'cf1089214706c7c724443cc011ca409692e4bd55ec96d292078c40797f9113a6','v5.2.2.mod':'d008ac84f404c6fbc7a1f378099dd21794843d10d6cdc129cfc403d39656f5a6','v5.2.2.zip':'5c6f37c725a481fb0f1e7866ae273429525d8cc6a6cace87da28e53370f5596a','v5.2.2.ziphash':'7543c99811ce98d1fe2b6e2a7919b20c5f52b019b9d8340a7b42326d5c0d5b5f'}
def need(v,m):
 if not v:raise ValueError(m)
def sha(p):
 with p.open('rb')as f:return hashlib.file_digest(f,'sha256').hexdigest()
def regular(p,limit):
 need(not any(q.is_symlink()for q in (p,*p.parents))and stat.S_ISREG(p.lstat().st_mode)and 0<p.stat().st_size<=limit,'regular_bounded_TEST_input');return p

def put(p,value):
 with p.open('x')as f:json.dump(value,f,sort_keys=True,indent=2);f.write('\n')
 p.chmod(0o444)
def run(argv,cwd,env,deadline,label):
 remain=deadline-time.monotonic();need(remain>0,'original_build_deadline');started=time.monotonic();process=subprocess.Popen(argv,cwd=cwd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True);forced=False
 try:out,err=process.communicate(timeout=remain)
 except subprocess.TimeoutExpired:
  forced=True;os.killpg(process.pid,signal.SIGKILL);out,err=process.communicate(timeout=5)
 (cwd/(label+'.stdout')).write_bytes(out);(cwd/(label+'.stderr')).write_bytes(err)
 try:os.killpg(process.pid,0);absent=False
 except ProcessLookupError:absent=True
 row={'label':label,'argv':argv,'exitCode':process.returncode,'forcedKillUsed':forced,'naturalWait':not forced,'pipeEOF':True,'processGroupAbsent':absent,'durationSeconds':time.monotonic()-started,'stdoutSHA256':hashlib.sha256(out).hexdigest(),'stderrSHA256':hashlib.sha256(err).hexdigest()};put(cwd/(label+'-command.json'),row)
 need(not forced and absent and process.returncode==0 and time.monotonic()<deadline,'command_closed_within_original_deadline');return out,row

def elf(p):
 raw=regular(p,32*1024*1024).read_bytes();need(raw[:6]==b'\x7fELF\x02\x01'and struct.unpack_from('<H',raw,18)[0]==183,'actual_Linux_ARM64_ELF');return hashlib.sha256(raw).hexdigest()
def build(a):
 source=a.source.absolute();regular(source,16384);need(sha(source)==SOURCE_SHA,'exact_current9b_receiver');repo=source.parents[3]
 go=P(shutil.which('go')).resolve();regular(go,64*1024*1024);root=a.output.absolute();need(repo.name=='receiver-source'and root==repo/'.task-tools/artifacts/TEST-linux-arm64-receiver-build'and not root.exists()and not any(q.is_symlink()for q in root.parents),'fresh_exact_TEST_build_root');root.parent.mkdir(parents=True,exist_ok=True);root.mkdir(mode=0o700)
 env={'PATH':str(go.parent)+':/usr/bin:/bin','HOME':str(root/'home'),'TMPDIR':str(root/'tmp'),'GOCACHE':str(root/'gocache'),'GOMODCACHE':str(root/'gomodcache'),'GOPATH':str(root/'gopath'),'GOTOOLCHAIN':'local','GOENV':'off','GOWORK':'off','GOTELEMETRY':'off','GOVCS':'*:off','CGO_ENABLED':'1','LANG':'C.UTF-8','GOPROXY':'https://proxy.golang.org','GOSUMDB':'sum.golang.org'}
 for name in ('home','tmp','gocache','gomodcache','gopath'):(root/name).mkdir(mode=0o700)
 deadline=time.monotonic()+180;commands=[]
 for args,expected in ((['rev-parse','HEAD'],SOURCE_COMMIT),(['status','--porcelain=v1','--untracked-files=no'],'')):
  need(subprocess.check_output(['git','-C',str(repo),*args],text=True,timeout=10).strip()==expected,'actual_9b_clean_source')
 before=source.read_bytes();shutil.copyfile(source,root/'receiver.go');(root/'receiver.go').chmod(0o444)
 (root/'go.mod').write_text('module test.invalid/an-receiver-capabilities\n\ngo 1.27.1\n\nrequire github.com/godbus/dbus/v5 v5.2.2\n')
 (root/'go.sum').write_text('github.com/godbus/dbus/v5 v5.2.2 '+SUM+'\ngithub.com/godbus/dbus/v5 v5.2.2/go.mod '+MODSUM+'\n')
 mod_before={n:sha(root/n)for n in ('go.mod','go.sum')};tool_before=sha(go)
 version,row=run([str(go),'version'],root,env,deadline,'go-version');commands.append(row);need(re.fullmatch(rb'go version go1\.27\.1 linux/arm64\s*',version) is not None,'exact_native_Go1271')
 downloaded,row=run([str(go),'mod','download','-json','github.com/godbus/dbus/v5@v5.2.2'],root,env,deadline,'public-module');commands.append(row);module=json.loads(downloaded);need(not module.get('Error')and module['Path']=='github.com/godbus/dbus/v5'and module['Version']=='v5.2.2'and module['Sum']==SUM and module['GoModSum']==MODSUM,'authentic_public_Godbus_sums')
 cache=root/'gomodcache/cache/download/github.com/godbus/dbus/v5/@v';need(all(sha(regular(cache/n,4*1024*1024))==h for n,h in CACHE.items()),'exact_public_four_cache_bytes')
 env.update(GOPROXY='off',GOSUMDB='off');_,row=run([str(go),'build','-trimpath','-mod=readonly','-buildvcs=false','-o','receiver','receiver.go'],root,env,deadline,'build');commands.append(row)
 info,row=run([str(go),'version','-m','receiver'],root,env,deadline,'buildinfo');commands.append(row);need(b'go1.27.1' in info and ('github.com/godbus/dbus/v5\tv5.2.2\t'+SUM).encode()in info and b'GOARCH=arm64'in info and b'GOOS=linux'in info and b'CGO_ENABLED=1'in info and b'vcs.revision='not in info,'actual_TEST_buildinfo_no_product_VCS')
 (root/'buildinfo.txt').write_bytes(info);binary=root/'receiver';binary_sha=elf(binary);binary.chmod(0o555)
 need(subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD'],text=True,timeout=10).strip()==SOURCE_COMMIT and source.read_bytes()==before and sha(go)==tool_before and all(sha(root/n)==h for n,h in mod_before.items())and all(sha(cache/n)==h for n,h in CACHE.items()),'source_tools_module_unchanged')
 put(root/'result.json',{'status':'ACTUAL_TEST_RECEIVER_BUILD_PASS','sourceCommit':SOURCE_COMMIT,'sourceSHA256':SOURCE_SHA,'sourceAndToolsUnchanged':True,'goSHA256':tool_before,'goVersion':version.decode().strip(),'module':{'path':'github.com/godbus/dbus/v5','version':'v5.2.2','sum':SUM,'goModSum':MODSUM},'cacheSHA256':CACHE,'commands':commands,'binarySHA256':binary_sha,'buildInfoSHA256':sha(root/'buildinfo.txt'),'productVCSClaimed':False,'nativeExecuted':False,'providerRequests':0,'qualificationGranted':False})

def checked_receiver_artifact(root):
 need(os.uname().sysname=='Linux'and os.uname().machine in ('aarch64','arm64'),'actual_Linux_ARM_stage');need({p.name for p in root.iterdir()}=={'receiver','result.json','buildinfo.txt'},'sole_three_receiver_artifact_files')
 result=json.loads(regular(root/'result.json',32768).read_text());need(result['status']=='ACTUAL_TEST_RECEIVER_BUILD_PASS'and result['sourceCommit']==SOURCE_COMMIT and result['sourceSHA256']==SOURCE_SHA and result['sourceAndToolsUnchanged'] is True and result['goVersion']=='go version go1.27.1 linux/arm64'and result['module']=={'path':'github.com/godbus/dbus/v5','version':'v5.2.2','sum':SUM,'goModSum':MODSUM}and result['cacheSHA256']==CACHE and result['productVCSClaimed'] is False and result['nativeExecuted'] is False and result['providerRequests']==0 and result['qualificationGranted'] is False,'genuine_separate_TEST_receiver_build')
 need([c['label']for c in result['commands']]==['go-version','public-module','build','buildinfo']and all(c['exitCode']==0 and c['forcedKillUsed'] is False and c['naturalWait'] is True and c['pipeEOF'] is True and c['processGroupAbsent'] is True for c in result['commands']),'actual_build_commands_closed')
 need(elf(root/'receiver')==result['binarySHA256']and sha(regular(root/'buildinfo.txt',16384))==result['buildInfoSHA256'],'actual_artifact_bytes')
 return result

def stage(a):
 repo=P.cwd().resolve();root=a.input.absolute();result=checked_receiver_artifact(root)
 dest=repo/'.task-tools/artifacts/TEST-linux-arm64-receiver';need(not dest.exists()and not any(p.is_symlink()for p in dest.parents),'fresh_receiver_stage');dest.mkdir(mode=0o700);shutil.copyfile(root/'receiver',dest/'receiver');(dest/'receiver').chmod(0o555);shutil.copyfile(root/'result.json',dest/'result.json');(dest/'result.json').chmod(0o444)
 need(sha(dest/'receiver')==result['binarySHA256'],'actual_staged_receiver_hash');put(repo/'.task-tools/artifacts/receiver-record.json',{'path':str((dest/'receiver').relative_to(repo)),'sha256':result['binarySHA256']})

def verify(a):
 repo=P.cwd().resolve();root=a.input.absolute();result=checked_receiver_artifact(root)
 ci=repo/'scripts/testdata/opencode-native-e2e/ci_inputs.py';harness=repo/'scripts/opencode-native-e2e.py'
 for p,key in ((ci,'TEST_CI_INPUTS_SHA256'),(harness,'TEST_HARNESS_SHA256')):need(sha(regular(p,256*1024))==os.environ[key],'exact_original_public_preflight_source')
 spec=importlib.util.spec_from_file_location('original_receiver_ci_inputs',ci);inputs=importlib.util.module_from_spec(spec);spec.loader.exec_module(inputs);r=inputs.r
 manifest=repo/os.environ['AN_MANIFEST'];manifest_sha=os.environ['AN_MANIFEST_SHA256']
 m,cell,files,binary,archive=r.load_manifest(manifest,os.environ['AN_OS'],os.environ['AN_ARCH'],os.environ['AN_VERSION'],manifest_sha)
 r.require((cell['os'],cell['arch'])==('linux','arm64'),'exact_Linux_ARM_receiver_preflight')
 tracked=subprocess.check_output(['git','ls-files','--',str(manifest.resolve().relative_to(repo))],text=True);r.require(not tracked,'external_parent_manifest_required')
 checkout=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();dirty=subprocess.check_output(['git','status','--porcelain','--untracked-files=no'],text=True)
 dirty+=subprocess.check_output(['git','ls-files','--others','--exclude-standard','--','.',':(exclude).task-tools/artifacts'],text=True)
 build=subprocess.check_output(['go','version','-m',str(binary)],text=True);r.verify_source_binding(m,checkout,build,dirty);r.require(checkout==os.environ['TEST_PRODUCT_HEAD'],'actual_product_checkout')
 record=inputs.artifacts/'receiver-record.json';before=record.read_bytes();need(stat.S_IMODE(regular(record,4096).stat().st_mode)==0o444,'readonly_existing_receiver_record');data=json.loads(before)
 staged=repo/'.task-tools/artifacts/TEST-linux-arm64-receiver';expected={'path':str((staged/'receiver').relative_to(repo)),'sha256':result['binarySHA256']};need(data==expected,'exact_current_staged_receiver_record')
 receiver=r.checked_file(repo,data);need(receiver==staged/'receiver' and elf(receiver)==result['binarySHA256'] and stat.S_IMODE(receiver.stat().st_mode)==0o555,'actual_staged_current_ARM_receiver')
 need(sha(regular(staged/'result.json',32768))==sha(root/'result.json') and record.read_bytes()==before,'actual_build_receipt_and_record_unchanged')
 print(json.dumps({'status':'existing_public_and_current_receiver_preflight_passed','productCommit':checkout,'receiverSHA256':result['binarySHA256'],'receiverRecordUnchanged':True,'receiverRebuilt':False,'nativeExecuted':False,'providerRequests':0,'qualificationGranted':False}))

if __name__=='__main__':
 p=argparse.ArgumentParser();sub=p.add_subparsers(dest='mode',required=True);b=sub.add_parser('build');b.add_argument('--source',type=P,required=True);b.add_argument('--output',type=P,required=True);s=sub.add_parser('stage');s.add_argument('--input',type=P,required=True);v=sub.add_parser('verify');v.add_argument('--input',type=P,required=True);a=p.parse_args();{'build':build,'stage':stage,'verify':verify}[a.mode](a)
