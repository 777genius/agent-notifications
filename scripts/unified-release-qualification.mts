/** Explicit all-platform artifact/business qualification; original semantic validators and false full grants remain authoritative. */
import { createHash } from 'node:crypto';
import { readFileSync, mkdirSync, writeFileSync, realpathSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { spawnSync } from 'node:child_process';

const candidate = process.env.RELEASE_CANDIDATE_SHA ?? '';
const signingRun = process.env.RELEASE_SIGNING_RUN ?? '';
const releaseTag = process.env.RELEASE_TAG ?? '';
need(/^[a-f0-9]{40}$/.test(candidate) && /^[1-9][0-9]*$/.test(signingRun) && releaseTag === 'v1.48.4', 'explicit immutable v1.48.4 candidate and signing run required');
function need(value: unknown, reason: string): asserts value { if (!value) throw Error(reason); }
const hash = (body: Buffer | string): string => createHash('sha256').update(body).digest('hex');
function command(binary: string, args: string[], cwd: string, env = process.env): string {
  const result = spawnSync(binary, args, { cwd, env, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw Error(`${binary} failed: ${result.error ?? result.stderr}`);
  return result.stdout.trim();
}
function main(): void {
const mode = process.argv[2];
need(mode === 'prepare' || mode === 'execute', 'explicit mode required');
  need(process.env.GITHUB_ACTIONS === 'true', 'disposable GitHub execution only');
  const source = realpathSync(process.env.CANDIDATE_ROOT ?? '/missing');
  const operator = realpathSync(process.env.OPERATOR_ROOT ?? '/missing');
  const operatorSHA = process.env.OPERATOR_SHA;
  need(operatorSHA && /^[a-f0-9]{40}$/.test(operatorSHA), 'immutable operator SHA required');
  need(command('git', ['rev-parse', 'HEAD'], source) === candidate, 'exact candidate checkout');
  need(command('git', ['rev-parse', 'HEAD'], operator) === operatorSHA, 'exact operator checkout');
  for (const file of ['scripts/unified-release-qualification.mts', 'scripts/macos-release-reader.mts']) {
    const expected = command('git', ['show', `${operatorSHA}:${file}`], operator);
    need(readFileSync(join(operator, file), 'utf8').trimEnd() === expected, 'operator source changed');
  }
  const tools = resolve(source, '.task-tools/artifacts/macos-tools');
  mkdirSync(tools, { recursive: false, mode: 0o700 });
  const consumer = readFileSync(join(operator, 'scripts/macos-release-reader.mts'));
  writeFileSync(join(tools, 'release-opencode-reader.mts'), consumer, { flag: 'wx', mode: 0o600 });
  const config = { mode, source, tools, candidate, signingRun, releaseTag, operatorSHA, platform: process.env.AN_OS,
    adapterSHA256: hash(readFileSync(join(operator, 'scripts/unified-release-qualification.mts'))),
    consumerSHA256: hash(consumer), arch: process.env.AN_ARCH, version: process.env.AN_VERSION,
    parentSHA256: process.env.AN_EVIDENCE_SHA256, currentRun: process.env.GITHUB_RUN_ID, currentAttempt: process.env.GITHUB_RUN_ATTEMPT };
  const configPath = join(tools, 'configuration.json');
  writeFileSync(configPath, JSON.stringify(config), { flag: 'wx', mode: 0o600 });
  const nativeEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/(?:TOKEN|API_KEY|PASSWORD|SECRET|CREDENTIAL|AUTH)/i.test(key)));
  nativeEnv.SOURCE_COMMIT = candidate;
  const bridgePath = join(tools, 'bridge.py');
  writeFileSync(bridgePath, bridge, { flag: 'wx', mode: 0o600 });
  const result = spawnSync(process.platform === 'win32' ? 'python' : 'python3', ['-B', bridgePath, configPath], { cwd: source, env: nativeEnv, stdio: 'inherit' });
  need(!result.error && result.status === 0, 'qualification stopped; preserve failed report');
}

// Verified candidate functions remain the semantic validators. Adaptations below
// bind new provenance/platform custody; they never modify the installed runner,
// native clock predicates, admission budgets or production qualification rows.
const bridge = String.raw`
import base64, hashlib, json, os, pathlib, re, shutil, subprocess, sys, tarfile, types, zipfile
from types import SimpleNamespace as NS
P=pathlib.Path
c=json.loads(P(sys.argv[1]).read_text()); repo=P(c['source']); tools=P(c['tools'])
art=repo/'.task-tools/artifacts'; candidate=c['candidate']; run=c['signingRun']
def need(ok, reason):
    if not ok: raise ValueError(reason)
def sha(path): return hashlib.sha256(P(path).read_bytes()).hexdigest()
def checked_source(name):
    body=(repo/name).read_bytes()
    need(body==subprocess.check_output(['git','show',candidate+':'+name],cwd=repo),'candidate_source_changed')
    return body.decode()
def load(name, edits=(), external=False):
    body=checked_source('scripts/'+name)
    for before,after,count in edits:
        need(body.count(before)==count,'adapter_source_contract_changed:'+name)
        body=body.replace(before,after)
    path=tools/name if external else repo/'scripts'/name
    if external: path.write_text(body)
    module=types.ModuleType('macos_'+name.replace('.','_')); module.__file__=str(path)
    sys.modules[module.__name__]=module
    exec(compile(body,str(path),'exec'),module.__dict__)
    if hasattr(module,'REPO'): module.REPO=repo
    return module
def record(path): return {'path':str(path.relative_to(repo)), 'sha256':sha(path)}
def check_run():
    value=json.loads((art/'signing-run.json').read_text())
    need(value['id']==int(run) and value['head_sha']==candidate and value['conclusion']=='success'
         and value['event']=='workflow_dispatch' and value['head_branch']=='release/macos-signing'
         and value['actor']['login']=='777genius' and value['triggering_actor']['login']=='777genius',
         'owner_exact_signing_run_required')
    custody=json.loads((art/'signed/ClaudeNotifier-smoke.custody.json').read_text())
    archive=art/'signed/ClaudeNotifier-smoke.app.zip'
    need(custody['sourceSHA']==candidate and custody['workflowSHA']==candidate
         and custody['runID']==run and custody['runAttempt']==str(value['run_attempt'])
         and custody['archiveSHA256']==sha(archive) and custody['signingTeam']=='86399583GS'
         and custody['bundleID']=='com.777genius.agent-notifications' and custody['signatureVerified'] is True,
         'same_run_notifier_custody')
    for arch in ('amd64','arm64'):
        root=art/('signed-'+arch)
        need((root/'source-sha.txt').read_text().strip()==candidate
             and (root/'candidate-version.txt').read_text().strip()==c['releaseTag'],'signed_native_source_version')
        checked=set()
        for line in (root/'SHA256SUMS').read_text().splitlines():
            digest,name=line.split(None,1); name=name.lstrip('*')
            need(P(name).name==name and re.fullmatch('[a-f0-9]{64}',digest)
                 and name not in checked and sha(root/name)==digest,'signed_native_checksum')
            checked.add(name)
        binary='claude-notifications-darwin-'+arch
        need(binary in checked and {'source-sha.txt','candidate-version.txt'}<=checked
             and sha(repo/'dist'/binary)==sha(root/binary),'signed_native_bytes_changed')
    return custody
def check_current_custody():
    need(re.fullmatch('[1-9][0-9]*',str(c['currentRun'])) is not None
         and re.fullmatch('[1-9][0-9]*',str(c['currentAttempt'])) is not None,'current_custody_identity_required')
    value=json.loads((art/'custody-run.json').read_text())
    need(value['id']==int(c['currentRun']) and value['run_attempt']==int(c['currentAttempt'])
         and value['head_sha']==c['operatorSHA']
         and ((value['status']=='in_progress' and value['conclusion'] is None)
              or (value['status']=='completed' and value['conclusion']=='success'))
         and value['event']=='workflow_dispatch' and value['actor']['login']=='777genius'
         and value['triggering_actor']['login']=='777genius','owner_exact_current_custody_run')
    jobs=json.loads((art/'custody-jobs.json').read_text())
    need(jobs['total_count']==len(jobs['jobs']),'complete_current_custody_jobs')
    job_ids=[job['id'] for job in jobs['jobs']]
    need(all(type(job_id) is int and job_id>0 for job_id in job_ids)
         and len(job_ids)==len(set(job_ids)),'unique_current_custody_job_ids')
    expected={ 'Build '+platform+' '+arch
               for platform,arch in (('linux','amd64'),('linux','arm64'),('windows','amd64')) }
    custody_jobs=[job for job in jobs['jobs'] if job['name'] in expected]
    need(len(custody_jobs)==3 and {job['name'] for job in custody_jobs}==expected,
         'exact_three_current_custody_job_names')
    selected=[]
    for platform,arch in (('linux','amd64'),('linux','arm64'),('windows','amd64')):
        name='Build '+platform+' '+arch
        matching=[job for job in custody_jobs if job['name']==name]
        need(len(matching)==1 and matching[0]['run_id']==value['id']
             and matching[0]['head_sha']==value['head_sha']
             and matching[0]['status']=='completed' and matching[0]['conclusion']=='success',
             'successful_exact_current_custody_job')
        binary=repo/'dist'/('claude-notifications-'+platform+'-'+arch+('.exe' if platform=='windows' else ''))
        need(binary.is_file() and not binary.is_symlink(),'current_custody_binary_missing')
        selected.append({'jobID':matching[0]['id'],'name':name,'binary':record(binary)})
    return {'runID':value['id'],'runAttempt':value['run_attempt'],'operatorSHA':value['head_sha'],
            'overallConclusion':value['conclusion'],'jobs':selected,'qualificationGranted':False}
r=load('opencode-native-e2e.py')
h=load('opencode-platform-clock-prequalification.py')
need(all(v is False for v in h.QUALIFICATIONS.values()),'clock_grants_must_remain_false')
if c['mode']=='prepare':
    custody=check_run()
    current_custody=check_current_custody()
    inputs=load('release-opencode-inputs.py'); inputs.FIXTURES=repo/'scripts/testdata/opencode-native-e2e'
    helper=art/'release-helper/ClaudeNotifier.app.zip'; helper.parent.mkdir(mode=0o700)
    shutil.copyfile(art/'signed/ClaudeNotifier-smoke.app.zip',helper)
    archive=art/'release-opencode-inputs.tar.gz'
    inputs.prepare(art/'rebuilt-observer.js',candidate,'all')
    parent=art/'release-parent'; native=parent/'native'; native.mkdir(mode=0o700)
    with zipfile.ZipFile(helper) as packed:
        names=set(); total=0
        for member in packed.infolist():
            name=pathlib.PurePosixPath(member.filename); total+=member.file_size
            need(not name.is_absolute() and '..' not in name.parts and member.filename not in names
                 and (member.external_attr>>16)&0o170000!=0o120000 and total<16*1024*1024,'private_app_archive')
            names.add(member.filename); target=native.joinpath(*name.parts)
            if member.is_dir(): target.mkdir(mode=0o700,parents=True,exist_ok=True)
            else:
                target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
                with target.open('xb') as stream: stream.write(packed.read(member))
    executable=native/'ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern'
    sidecar=native/'ClaudeNotifier.app.managed-runtime.json'
    need(sha(executable)==custody['executableSHA256'] and sha(sidecar)==custody['attestationSHA256'],
         'signed_app_and_sidecar_custody')
    manifest=json.loads((parent/'manifest.json').read_text())
    for cell in manifest['cells']:
        if cell['os']=='darwin':
            prefix='.task-tools/artifacts/parent-inputs/native/'
            cell['nativeApp']={'root':prefix+'ClaudeNotifier.app',
                'executable':{'path':prefix+'ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern','sha256':sha(executable)},
                'sidecar':{'path':prefix+'ClaudeNotifier.app.managed-runtime.json','sha256':sha(sidecar)}}
    (parent/'manifest.json').write_text(json.dumps(manifest,sort_keys=True)+'\n')
    receipt=json.loads((parent/'release-inputs-receipt.json').read_text())
    receipt.update(purpose='exact-source signed macOS and current operator binary custody, no qualification grant',
                   runId=run,operatorRunID=c['currentRun'],operatorSHA=c['operatorSHA'],
                   signingCustody=custody,adapterSHA256=c['adapterSHA256'],consumerSHA256=c['consumerSHA256'],
                   currentOperatorCustody=current_custody,
                   otherNativeBinaries='current_operator_run_custody_only_no_platform_promotion')
    (parent/'release-inputs-receipt.json').write_text(json.dumps(receipt,sort_keys=True)+'\n')
    archive.unlink(); digest=inputs.seal_archive(parent,archive)
    with open(os.environ['GITHUB_OUTPUT'],'a') as out: out.write('parent_sha256='+digest+'\n')
    sys.exit(0)
need((c['platform'],c['arch'],c['version']) in r.CELLS,'eleven_native_cells_only')
archive=art/'custody/release-opencode-inputs.tar.gz'; archive_sha=c['parentSHA256']
need(re.fullmatch('[a-f0-9]{64}',str(archive_sha)) and sha(archive)==archive_sha,'sealed_parent_digest')
ci=load('testdata/opencode-native-e2e/ci_inputs.py')
manifest_path,manifest_sha=ci.stage_parent_archive(archive,archive_sha,art/'parent-inputs')
m,cell,files,binary,host=r.load_manifest(manifest_path,c['platform'],c['arch'],c['version'],manifest_sha)
receipt=json.loads((manifest_path.parent/'release-inputs-receipt.json').read_text())
need(receipt['runId']==run and receipt['candidateCommit']==candidate and receipt['operatorSHA']==c['operatorSHA']
     and receipt['adapterSHA256']==c['adapterSHA256'] and receipt['consumerSHA256']==c['consumerSHA256'],
     'reviewed_operator_and_signing_provenance')
if c['platform']=='darwin':
    app=r.native_app(cell['nativeApp']); executable=repo/cell['nativeApp']['executable']['path']
    need(sha(executable)==receipt['signingCustody']['executableSHA256'],'selected_signed_native_app')
    executable.chmod(0o700)
    subprocess.run(['bash',str(repo/'swift-notifier/scripts/verify-signing.sh'),str(app),'--notarized'],check=True)
if c['platform']!='windows': binary.chmod(0o700)
dirty=subprocess.check_output(['git','status','--porcelain','--untracked-files=no'],cwd=repo,text=True)
dirty+=subprocess.check_output(['git','ls-files','--others','--exclude-standard','--','.',':(exclude).task-tools/artifacts'],cwd=repo,text=True)
info=subprocess.check_output(['go','version','-m',str(binary)],cwd=repo,text=True)
r.verify_source_binding(m,candidate,info,dirty)
go_version=re.search(r'^.+: (go1\.[0-9]+\.[0-9]+)\r?\n',info)
need(go_version is not None,'actual_go_patch_identity')
need(subprocess.check_output(['go','version'],text=True).strip()=='go version '+go_version[1]+' '+c['platform']+'/'+c['arch'],'same_native_go_validation_toolchain')
clock=load('release-opencode-clock.py',[
    (r'go1\.27\.1',re.escape(go_version[1]),2),
    ("{'Linux': 'linux', 'Windows': 'windows'}","{'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}",1)],True)
clock.CANDIDATE=candidate; clock.ORIGINAL_RUN_ID=run; clock.ORIGINAL_ARCHIVE_SHA256=archive_sha
clock.RELEASE_CELLS=set(r.CELLS); clock.HARNESS_SHA256=sha(repo/clock.HARNESS)
def custody(_h,args):
    need(args.parent_archive_sha256==archive_sha and sha(args.parent_archive)==archive_sha
         and sha(args.manifest)==manifest_sha,'clock_exact_parent_archive')
    r.custody_cells(m,(c['platform'],c['arch'],c['version'])); return m
clock.custody=custody
if c['platform']=='darwin':
    # Prepare the final staged inode; original staging and measured case stay unchanged.
    import time
    original_exact_harness=clock.exact_harness
    def prepared_exact_harness(source):
        harness_result=original_exact_harness(source)
        h,harness_sha=harness_result; original_stage_case=h.stage_case; preflight_used=False
        def prepared_stage_case(owned,env,parent_root,version,item,leaves,helper,helper_sha,commit,os_name,arch,origin):
            nonlocal preflight_used
            need(not preflight_used,'one_staged_version_preflight'); preflight_used=True
            result=original_stage_case(owned,env,parent_root,version,item,leaves,helper,helper_sha,commit,os_name,arch,origin)
            case_root,metadata=result; staged=case_root/helper.name; owner_root=P(os.environ['RUNNER_TEMP']).resolve(strict=True)
            need(case_root.resolve(strict=True)==case_root and case_root.is_relative_to(owner_root)
                 and staged.resolve(strict=True)==staged and not any(p.is_symlink() for p in (staged,*staged.parents))
                 and os_name=='darwin' and arch==c['arch'] and version==c['version'] and commit==candidate,
                 'staged_version_preflight_custody')
            need(h.sha(staged)==helper_sha==metadata['helperSha256']==sha(binary),'staged_version_preflight_hash')
            identity=staged.stat(); version_started=time.monotonic(); version_process=None
            version_stdout=b''; version_stderr=b''; version_timed_out=False
            try:
                version_process=subprocess.Popen([str(staged),'--version'],cwd=case_root,env=h.environment(case_root),
                    stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
                try: version_stdout,version_stderr=version_process.communicate(timeout=min(10,h.remaining(owned.end)))
                except subprocess.TimeoutExpired:
                    version_timed_out=True
                    if version_process.poll() is None:
                        need(os.getpgid(version_process.pid)==version_process.pid,'owned_version_preflight_group')
                        os.killpg(version_process.pid,9)
                    version_stdout,version_stderr=version_process.communicate(timeout=2)
            finally:
                if version_process is not None:
                    for stream in (version_process.stdout,version_process.stderr):
                        if stream is not None: stream.close()
                receipt={'schema':1,'candidate':candidate,'binarySHA256':helper_sha,'command':'--version',
                    'stagedPath':str(staged.relative_to(owner_root)),'device':identity.st_dev,'inode':identity.st_ino,
                    'elapsedMs':round((time.monotonic()-version_started)*1000,3),'oneAttemptOnly':True,
                    'timedOut':version_timed_out,'exitCode':None if version_process is None else version_process.returncode,
                    'naturalClose':version_process is not None and version_process.returncode==0 and not version_timed_out,
                    'stdoutBytes':len(version_stdout),'stderrBytes':len(version_stderr),
                    'scope':'installed_binary_after_public_version_check','coldStartQualified':False,
                    'priorTimingFailuresPreserved':True,'unchangedHelperBudgetMs':224,'harnessSHA256':harness_sha}
                with os.fdopen(os.open(tools/'artifact-version-preflight.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as output:
                    json.dump(receipt,output,sort_keys=True); output.write('\n')
            after=staged.stat()
            need(receipt['naturalClose'] and version_stdout==('claude-notifications '+c['releaseTag']+'\n').encode() and not version_stderr
                 and all(getattr(identity,k)==getattr(after,k) for k in ('st_dev','st_ino','st_size','st_mtime_ns','st_ctime_ns'))
                 and h.sha(staged)==helper_sha,'staged_version_preflight_required')
            h.remaining(owned.end)
            return result
        h.stage_case=prepared_stage_case
        return harness_result
    clock.exact_harness=prepared_exact_harness
clock_report=art/'clock.json'
args=NS(candidate_repo=repo,original_run_id=run,parent_archive=archive,parent_archive_sha256=archive_sha,
        manifest=manifest_path,manifest_sha256=manifest_sha,os=c['platform'],arch=c['arch'],version=c['version'],
        temp_base=P(os.environ['RUNNER_TEMP']),go_module_cache=P(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip()),report=clock_report)
need(clock.execute(args)==0,'fresh_clock_primitive_failed')
need(json.loads(clock_report.read_text())['adapterSHA256']==sha(tools/'release-opencode-clock.py'),
     'fresh_clock_operator_source_closure')
reader=load('release-opencode-reader.py',[(
    ("stage('native_project_activation')\n  request(port,project,'/api/plugin' if v2 else '/config',headers,v2)"),
    ("stage('native_project_activation')\n  request(port,project,'/api/plugin' if v2 else '/config',headers,v2,timeout=15)" if c['platform']=='darwin' and c['arch']=='amd64' else "stage('native_project_activation')\n  request(port,project,'/api/plugin' if v2 else '/config',headers,v2)"),1),
    ("subprocess.check_output([str(host),'--version'],cwd=project,env=e,timeout=8,text=True).strip()",
     "subprocess.check_output([str(host),'--version'],cwd=project,env=e,timeout=30,text=True).strip()" if c['platform']=='darwin' and c['arch']=='amd64' and c['version']=='2.0.21' else "subprocess.check_output([str(host),'--version'],cwd=project,env=e,timeout=8,text=True).strip()",1)],True); reader.CANDIDATE=candidate; reader.CELLS=set(r.CELLS)
r.write_json(tools/'reader-preparation-adaptation.json',{'scope':'execution_only_configured_loader_preparation','originalActivationSeconds':2,'adaptedActivationSeconds':15 if c['platform']=='darwin' and c['arch']=='amd64' else 2,'originalVersionSeconds':8,'adaptedVersionSeconds':30 if c['platform']=='darwin' and c['arch']=='amd64' and c['version']=='2.0.21' else 8,'originalDriverSHA256':sha(repo/'scripts/release-opencode-reader.py'),'adaptedDriverSHA256':sha(tools/'release-opencode-reader.py'),'readinessSeconds':6,'clockBudgetMs':224,'qualificationGranted':False})
def boundary(_):
    need(not any(os.environ.get(k) for k in ('GH_TOKEN','GITHUB_TOKEN','OPENAI_API_KEY','ANTHROPIC_API_KEY')),'native_credentials_forbidden')
    if c['platform']=='linux':
        import socket
        host=os.environ.get('AN_HOST_NETNS',''); current=os.readlink('/proc/self/ns/net')
        need(os.geteuid()!=0 and re.fullmatch(r'net:\[[1-9][0-9]*\]',host) and host!=current and {name for _,name in socket.if_nameindex()}=={'lo'},'private_unprivileged_loopback_namespace')
        return {'host':host,'private':current,'interfaces':['lo']}
    return {'platform':c['platform'],'networkIsolationClaimed':False,
            'isolation':'private_files_minimal_env_loopback_provider_no_network_isolation_claim'}
reader.network_guard=boundary
def manifest_inputs(a):
    mm,cc,ff,bb,aa=r.load_manifest(a.manifest,c['platform'],a.arch,a.version,a.manifest_sha256)
    pin=next(p for p in json.loads(reader.blob('scripts/testdata/opencode-native-e2e/host-pins.json'))['cells']
             if (p['os'],p['arch'],p['version'])==(c['platform'],a.arch,a.version))
    need(reader.sha(reader.REPO/mm['sdk']['archive']['path'])==reader.SDK_SHA256,'exact_reader_sdk')
    identity={'candidateCommit':candidate,'embeddedSHA256':sha(ff['embedded']),'candidateSHA256':sha(bb),
        'manifestSHA256':a.manifest_sha256,'sdkArchiveSHA256':mm['sdk']['archive']['sha256'],
        'hostArchiveSHA256':sha(aa),'hostSourceCommit':cc['hostSourceCommit']}
    return identity,pin,aa
reader.manifest_inputs=manifest_inputs
def unpack(archive,root,pin,os_name,arch):
    raw=archive.read_bytes()
    need(sha(archive)==pin['archiveSHA256'] and 'sha512-'+base64.b64encode(hashlib.sha512(raw).digest()).decode()==pin['archiveSRI'],'official_reader_archive')
    target=root/('opencode.exe' if os_name=='windows' else 'opencode'); r.extract_opencode(archive,target,os_name)
    need(sha(target)==pin['executableSHA256'],'official_reader_image')
    with target.open('rb') as stream: h.native_header(stream.read(4096),os_name,arch)
    return target
reader.unpack=unpack
reader.esbuild_command_for_magic=lambda executable,node,magic: [str(executable)] if magic in (b'\xcf\xfa\xed\xfe',b'\xce\xfa\xed\xfe',b'\xca\xfe\xba\xbe') or magic.startswith((b'\x7fELF',b'MZ')) else [str(node),str(executable)]
buildroot=art/'TEST-reader-build'; node=P(shutil.which('node'))
buildargs=NS(build_root=buildroot,tsc=repo/('opencode-plugin/node_modules/.bin/tsc.cmd' if sys.platform=='win32' else 'opencode-plugin/node_modules/.bin/tsc'),node_types=repo/'opencode-plugin/node_modules/@types',
             node=node,esbuild=repo/'opencode-plugin/node_modules/esbuild/bin/esbuild',typecheck_only=False)
need(reader.build(buildargs)==0,'fresh_typed_reader_build')
reader_report=art/'reader.json'; bundle=buildroot/'reader.js'; buildreceipt=buildroot/'build-receipt.json'
readargs=NS(root=art/'TEST-reader-native',manifest=manifest_path,manifest_sha256=manifest_sha,os=c['platform'],arch=c['arch'],
            version=c['version'],mode='api-only' if c['version']=='2.0.21' else 'one-completion',
            bundle=bundle,bundle_sha256=sha(bundle),build_receipt=buildreceipt,build_receipt_sha256=sha(buildreceipt),report=reader_report)
need(reader.execute(readargs)==0,'fresh_reader_primitive_failed')
sealer=load('release-opencode-business-proof.py',[],True); sealer.CANDIDATE=candidate
sealer.ORIGINAL_RUN=run; sealer.ORIGINAL_ARCHIVE=archive_sha; sealer.CELLS=set(r.CELLS)
sealer.CLOCK_VALIDATOR_SHA=sha(repo/clock.HARNESS)
def selected_manifest(manifest,digest,selected):
    r.custody_cells(manifest,tuple(selected.values()))
    need(manifest['candidateCommit']==manifest['buildRevision']==candidate,'mac_manifest_candidate')
    cc=next(x for x in manifest['cells'] if all(x[k]==v for k,v in selected.items()))
    return {'embeddedSHA256':manifest['assets']['embedded']['sha256'],'candidateSHA256':cc['candidate']['sha256'],
      'manifestSHA256':digest,'sdkArchiveSHA256':manifest['sdk']['archive']['sha256'],'hostArchiveSHA256':cc['archive']['sha256'],
      'ownedImageSHA256':cc['executableSHA256'],'hostSourceCommit':cc['hostSourceCommit']}
sealer.selected_manifest=selected_manifest
def network_boundary(observed,os_name): need(os_name==c['platform'] and observed==boundary(None),'honest_actual_platform_network_boundary')
sealer.network_boundary=network_boundary
sys.argv=['sealer','--manifest',str(manifest_path),'--manifest-sha256',manifest_sha,
    '--clock-report',str(clock_report),'--clock-report-sha256',sha(clock_report),
    '--reader-report',str(reader_report),'--reader-report-sha256',sha(reader_report),'--source-root',str(repo),
    '--output',str(art/'sealed-business'),'--os',c['platform'],'--arch',c['arch'],'--version',c['version']]
sealer.main()
proof=art/'sealed-business/business-proof.json'
if c['platform']=='linux':
    receiver=art/'TEST-dbus-receiver'
    subprocess.run(['go','build','-o',str(receiver),str(repo/'scripts/testdata/opencode-native-e2e/receiver.go')],check=True)
    r.write_json(art/'receiver-record.json',record(receiver))
# Observe exceptions and content-free diagnostics without changing runner predicates.
# Load the exact candidate inertly, then call its original main/global namespace.
# Setup delegates unchanged before the explicit public OS permission operation.
diagnostic_driver=tools/'business-diagnostic.py'
diagnostic_driver.write_text('''import json,os,pathlib,re,runpy,signal,subprocess,sys,hashlib
repo=pathlib.Path(sys.argv.pop(1)); output=repo/'.task-tools/artifacts/business-diagnostic.json'
sys.argv=sys.argv[1:]
sys.path[0]=str(repo/'scripts')
allowed={'duplicate_diagnostic_key','diagnostic_line_bound','diagnostic_schema','diagnostic_actual_closed_dual_submission_required','native_test_permission_not_allowed','native_test_permission_custody','native_test_permission_command_failed'}
events=[]; permissions=[]
def permission_operation(binary,action,root,env,require):
    row={'operation':action,'permission':'unavailable','outcome':'unavailable'}
    permissions.append(row)
    proc=subprocess.Popen([str(binary),'setup-opencode',action,'--control-root',str(root/'control')],
                          cwd=root,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    try:
        stdout,stderr=proc.communicate(timeout=28)
    except subprocess.TimeoutExpired:
        row['outcome']='timeout'
        for sig in (signal.SIGTERM,signal.SIGKILL):
            try: os.killpg(proc.pid,sig)
            except ProcessLookupError: pass
            try:
                proc.communicate(timeout=1)
                break
            except subprocess.TimeoutExpired: pass
        row['reaped']=proc.poll() is not None
        require(False,'native_test_permission_command_failed')
    row['reaped']=proc.poll() is not None
    match=re.fullmatch(rb'OpenCode notification permission: (allowed|undetermined|denied|unavailable)\\n',stderr) if len(stderr)<=4096 else None
    require(proc.returncode==0 and not stdout and match is not None,'native_test_permission_command_failed')
    row.update(permission=match[1].decode('ascii'),outcome='observed')
    return row['permission']
def install_with_permission(candidate,action,root,env,args,native_app=None):
    original_setup(candidate,action,root,env,args,native_app)
    if action!='install' or args.os!='darwin': return
    require=runner['require']
    require(os.environ.get('GITHUB_ACTIONS')=='true' and sys.platform=='darwin' and args.os=='darwin',
            'native_test_permission_custody')
    require(root.parent==repo/'.task-tools/artifacts/native' and re.fullmatch('TEST-installed-[a-f0-9]{24}',root.name)
            and root.resolve(strict=True)==root,'native_test_permission_custody')
    marker=json.loads((root/'.owned-test-root.json').read_text())
    require(marker.get('purpose')=='TEST installed AN dual native' and env.get('AN_TEST_ROOT')==str(root)
            and env.get('HOME')==str(root/'home'),'native_test_permission_custody')
    ledger=json.loads((root/'control/ownership.json').read_text())
    helper=pathlib.Path(ledger['Native']['Path'])/'Contents/MacOS/terminal-notifier-modern'
    binary=root/'runtime'/('claude-notifications-darwin-'+args.arch)
    require(helper.is_file() and helper.resolve(strict=True).is_relative_to(root) and binary.is_file()
            and not any(p.is_symlink() for p in (helper,*helper.parents,binary,*binary.parents)),
            'native_test_permission_custody')
    require(runner['digest'](binary)==runner['digest'](candidate) and native_app is not None
            and runner['digest'](helper)==runner['digest'](native_app/'Contents/MacOS/terminal-notifier-modern'),
            'native_test_permission_custody')
    permission=permission_operation(binary,'permission-status',root,env,require)
    if permission=='undetermined':
        # One explicit request on this disposable VM; uncertain outcomes are never retried.
        permission=permission_operation(binary,'request-permission',root,env,require)
    require(permission=='allowed','native_test_permission_not_allowed')
def observe(frame,event,arg):
    if event=='call' and not frame.f_code.co_filename.startswith(str(repo/'scripts')+'/'): return None
    if event=='exception':
        try: relative=pathlib.Path(frame.f_code.co_filename).relative_to(repo/'scripts')
        except ValueError: return None
        kind,value,_=arg
        label=str(value)
        if kind.__name__ in ('ValueError','Unqualified'):
            row={'file':str(relative),'line':frame.f_lineno,'class':kind.__name__}
            if label in allowed: row['reason']=label
            events.append(row)
            del events[:-64]
    return observe
sys.settrace(observe)
try:
    namespace=runpy.run_path(sys.argv[0],run_name='TEST_native_diagnostic')
    runner=namespace['main'].__globals__
    original_setup=runner['setup']
    runner['setup']=install_with_permission
    if '--os' in sys.argv and sys.argv[sys.argv.index('--os')+1]=='linux':
        import socket
        def guard():
            host=os.environ.get('AN_HOST_NETNS',''); current=os.readlink('/proc/self/ns/net')
            runner['require'](os.geteuid()!=0 and re.fullmatch(r'net:\\[[1-9][0-9]*\\]',host) and host!=current and {name for _,name in socket.if_nameindex()}=={'lo'},'private_unprivileged_loopback_namespace')
        runner['network_guard']=guard
    if '--os' in sys.argv and sys.argv[sys.argv.index('--os')+1]=='windows':
        acl=runpy.run_path(str(repo/'scripts/release-opencode-recovery.py'),run_name='TEST_current_acl')['windows_acl_preparer'].__globals__
        acl['WINDOWS_CANDIDATE']=runner['subprocess'].check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
        acl['WINDOWS_ACL_HELPER_SHA']=runner['digest'](repo/acl['WINDOWS_ACL_HELPER'])
        canonical=(repo/'internal/opencodeplugin/dist/agent-notifications.js').read_bytes()
        binary=pathlib.Path(sys.argv[sys.argv.index('--binary')+1]); image=binary.read_bytes()
        require=runner['require']
        require(canonical.count(b'\\r')==0 and image.count(canonical)==1 and canonical.replace(b'\\n',b'\\r\\n') not in image,'unique_current_canonical_LF_embed_required')
        acl.update(WINDOWS_SOURCE_SHA=hashlib.sha256(canonical).hexdigest(),WINDOWS_EMBEDDED_SHA=hashlib.sha256(canonical).hexdigest(),WINDOWS_BINARY_SHA=hashlib.sha256(image).hexdigest(),WINDOWS_EMBEDDED_OFFSET=image.find(canonical))
        import inspect
        embedding=inspect.getsource(acl['windows_embedded_asset'])
        require(embedding.count('176304')==1 and embedding.count('4206')==1,'exact_historical_embed_shape_contract')
        embedding=embedding.replace('176304',str(len(canonical))).replace('4206',str(canonical.count(b'\\n')))
        exec(compile(embedding,'TEST_current_windows_embedding','exec'),acl)
        acl['windows_embedded_asset'](canonical,image,acl['WINDOWS_EMBEDDED_OFFSET'])
        registration=inspect.getsource(acl['windows_registration_oracle'])
        require(registration.count('176304')==1,'exact_historical_render_metadata_contract')
        exec(compile(registration.replace('176304',str(len(canonical))),'TEST_current_windows_registration','exec'),acl)
        render_receipts=[]
        runner['registration']=acl['windows_registration_oracle'](runner['registration'],render_receipts)

        cache=runner['subprocess'].check_output(['go','env','GOMODCACHE'],text=True).strip()
        runner['prepare_sandbox_root']=acl['windows_acl_preparer'](repo,cache,[],repo/'.task-tools/artifacts/windows-acl-preparation.json')
    import inspect,hashlib,ast,time
    original_business=inspect.getsource(runner['qualify'])
    require=runner['require']; business_adapted=False
    initial_config="request(base, projects[0], '/api/plugin' if v2 else '/config', v2=v2, auth_headers=headers)"
    restart_boundary="readiness(server,base,projects[0],args.version,headers)\\n        native_case("
    require(original_business.count(initial_config)==1 and original_business.count(restart_boundary)==2,'exact_business_preparation_boundaries')
    business_v2_prepared=('--version' in sys.argv and sys.argv[sys.argv.index('--version')+1]=='2.0.21')
    if business_v2_prepared:
        require(not business_adapted,'separate_v1_v2_preparation_scope')
        v2_business=original_business
        launch_spaced='        server = owner.launch('
        launch_compact='        server=owner.launch('
        require(v2_business.count(launch_spaced)==2 and v2_business.count(launch_compact)==1,'three_exact_v2_host_launch_boundaries')
        for launch in (launch_spaced,launch_compact):
            v2_business=v2_business.replace(launch,'        an_v2_host_trace_start=len(trace(root))\\n'+launch)
        v2_call=lambda stage,project: "prepare_v2_business_location(server,base,projects["+str(project)+"],root,headers,report,"+repr(stage)+",provider,webhook,key,an_v2_host_trace_start)"
        scope_boundary='            scope_roots(base,projects,root,v2,headers,provider,webhook,key,report)'
        require(original_business.count(scope_boundary)==1,'one_original_second_location_scope_boundary')
        require(original_business.count('manual_compaction(base,projects[0],root,v2,headers,provider,webhook,key,report)')==1
                and original_business.count('task_child(base,projects[0],root,v2,headers,provider,webhook,key,report)')==1
                and original_business.index('manual_compaction(base,projects[0],root,v2,headers,provider,webhook,key,report)')
                <original_business.index('task_child(base,projects[0],root,v2,headers,provider,webhook,key,report)')
                <original_business.index(scope_boundary),'second_location_follows_manual_compaction_and_task_child')
        v2_business=v2_business.replace(initial_config,v2_call('initial_project_a',0),1)
        v2_business=v2_business.replace(scope_boundary,'            '+v2_call('second_location_project_b',1)+'\\n'+scope_boundary,1)
        for preparation_stage in ('updated_project_a','reinstalled_project_a'):
            v2_business=v2_business.replace(restart_boundary,"readiness(server,base,projects[0],args.version,headers)\\n        "+v2_call(preparation_stage,0)+"\\n        native_case(",1)
        require(v2_business.count('prepare_v2_business_location(')==4 and v2_business.count('an_v2_host_trace_start=len(trace(root))')==3,'four_v2_preparations_three_fresh_trace_frontiers')
        v2_preparations=[]; v2_prepared_locations=set()
        v2_transport=runpy.run_path(str(repo/'.task-tools/artifacts/macos-tools/release-opencode-reader.py'),run_name='TEST_v2_startup_transport')
        v2_transport=v2_transport['request'].__globals__
        original_reader=(repo/'scripts/release-opencode-reader.py').read_text()
        request_node=next(node for node in ast.parse(original_reader).body if isinstance(node,ast.FunctionDef) and node.name=='request')
        original_request=inspect.getsource(v2_transport['request']).strip()
        require(original_request==ast.get_source_segment(original_reader,request_node).strip(),'unchanged_v2_preparation_transport')
        status_check="need(200<=resp.status<300 and len(data)<=1048576,'native_HTTP_response')"
        unwrap="return value.get('data',value) if isinstance(value,dict) else value"
        require(original_request.count(status_check)==1 and original_request.count('def request(')==1 and original_request.count(unwrap)==1,'one_v2_RPC_error_transport_boundary')
        rpc_request=original_request.replace('def request(','def request_rpc(',1).replace(status_check,"need(resp.status==400 and len(data)<=1048576,'native_HTTP_response')",1)
        rpc_request=rpc_request.replace(unwrap,'return value',1)
        exec(compile(rpc_request,'TEST_v2_preparation_RPC_transport','exec'),v2_transport)
        def prepare_v2_business_location(server,base,project,root,headers,report,stage,provider,webhook,key,trace_start):
            require(server.poll() is None and len(v2_preparations)<4 and root.resolve(strict=True)==root and project.resolve(strict=True)==project and project in (root/'project-a',root/'project-b'),'owned_live_v2_project_preparation')
            identity=(server.pid,str(project))
            require(identity not in v2_prepared_locations,'one_v2_preparation_per_host_project')
            v2_prepared_locations.add(identity)
            endpoint=runner['urllib'].parse.urlparse(base)
            require(endpoint.scheme=='http' and endpoint.hostname=='127.0.0.1' and endpoint.port is not None and not endpoint.path and not endpoint.query,'loopback_v2_preparation_endpoint')
            effects_before=(len(provider.records),len(provider.gaps),webhook.count(),len(runner['desktop_rows'](root)))
            started=time.monotonic(); until=started+45
            row={'stage':stage,'hostPID':server.pid,'project':project.name,'deadlineSeconds':45,'oneAttemptOnly':True,'activationCalls':0,'registrationProbeCalls':0,'status':'pending','qualificationGranted':False,'checkpointConsumptionProven':False}
            v2_preparations.append(row); report['v2StartupPreparations']=v2_preparations
            def remaining():
                require(server.poll() is None and time.monotonic()<until,'v2_startup_preparation_deadline_or_host_exit')
                require((len(provider.records),len(provider.gaps),webhook.count(),len(runner['desktop_rows'](root)))==effects_before,'effect_during_v2_startup_preparation')
                return until-time.monotonic()
            try:
                remaining(); row['activationCalls']=1
                v2_transport['CURRENT_STAGE']=stage+'_activation'
                v2_transport['request'](endpoint.port,project,'/api/plugin',headers,True,timeout=min(15,remaining()))
                location=runner['private_redact'](str(project),key)
                pattern=re.compile(r'^rpc\\.agent-notifications-([a-f0-9]{32})\\.checkpoint$')
                while True:
                    remaining(); captured=[]
                    for item in runner['trace'](root)[trace_start:]:
                        value=item.get('value',{}); match=pattern.fullmatch(value.get('type',''))
                        if item.get('kind')=='native-v2' and match and value.get('location',{}).get('directory')==location:
                            captured.append(match.group(1))
                    require(len(set(captured))<=1,'ambiguous_v2_product_checkpoint_registration')
                    if captured: namespace=captured[0]; break
                    time.sleep(min(.05,remaining()))
                row['actualProductCheckpointPublication']=True
                rpc_id='agent-notifications-'+namespace
                method='TEST-readiness-no-method-'+__import__('secrets').token_hex(16)
                row['registrationProbeCalls']=1
                v2_transport['CURRENT_STAGE']=stage+'_registration'
                receipt=v2_transport['request_rpc'](endpoint.port,project,'/api/rpc/'+rpc_id+'/'+method,headers,True,payload={},method='POST',timeout=remaining())
                row['HTTPStatus']=v2_transport['HTTP_DIAGNOSTICS'][stage+'_registration']['lastStatus']
                require(row['HTTPStatus']==400 and isinstance(receipt,dict) and set(receipt)<= {'_tag','type','message','data'} and receipt.get('_tag')=='RpcError' and receipt.get('type')=='rpc.method_not_found' and isinstance(receipt.get('message'),str),'v2_live_registration_not_observed')
                remaining()
                row.update(status='observed',liveProductRPCRegistration=True,hostActivationSettled=True,responseType='rpc.method_not_found')
            except Exception as error:
                row.update(status='failed',exception=type(error).__name__)
                raise
            finally:
                row['elapsedMs']=round((time.monotonic()-started)*1000,3)
                receipt=repo/'.task-tools/artifacts/business-v2-startup-preparations.json'
                receipt.write_text(json.dumps({'scope':'actual_product_publication_live_registration_and_settled_host_activation','plannedPreparations':4,'calls':v2_preparations,'coldStartQualified':False,'qualificationGranted':False,'checkpointConsumptionProven':False},sort_keys=True)); receipt.chmod(0o600)
        runner['prepare_v2_business_location']=prepare_v2_business_location
        receipt=repo/'.task-tools/artifacts/business-v2-startup-adaptation.json'
        with receipt.open('x') as record:
            record.write(json.dumps({'scope':'finite_actual_v2_product_startup_preparation','activationDeadlineSeconds':45,'plannedPreparations':4,'preparationStages':['initial_project_a','second_location_project_b','updated_project_a','reinstalled_project_a'],'fixtureTopologyCorrection':'project_B_activation_delayed_until_original_scope_roots_boundary','preparationContract':'reviewed_v1483_V4_topology_and_live_RPC','oneAttemptOnly':True,'originalCandidateDriverSHA256':hashlib.sha256((repo/'scripts/opencode-native-e2e.py').read_bytes()).hexdigest(),'originalQualifySHA256':hashlib.sha256(original_business.encode()).hexdigest(),'adaptedExecutionQualifySHA256':hashlib.sha256(v2_business.encode()).hexdigest(),'originalPreparationTransportSHA256':hashlib.sha256(original_request.encode()).hexdigest(),'RPCErrorPreparationTransportSHA256':hashlib.sha256(rpc_request.encode()).hexdigest(),'RPCTransportAdaptation':'exact_HTTP400_and_preserve_tagged_JSON_for_nonexistent_method_preparation','semanticSessionDeadlineSeconds':8,'readerReadinessWitnessDeadlineSeconds':6,'nativeDisposalDeadlineSeconds':3,'nativeClockHelperBudgetMs':224,'coldStartQualified':False,'qualificationGranted':False,'checkpointConsumptionProven':False},sort_keys=True))
        receipt.chmod(0o600)
        exec(compile(v2_business,str(repo/'scripts/opencode-native-e2e.py'),'exec'),runner)

    native_code=runner['main']()
    if '--os' in sys.argv and sys.argv[sys.argv.index('--os')+1]=='windows':
        if native_code==0: runner['require'](len(render_receipts)==3,'three_actual_registration_receipts_required')
        report_path=repo/'.task-tools/artifacts/native-report.json'
        value=json.loads(report_path.read_text()); value['unifiedWindowsRenderCustody']=render_receipts
        runner['write_json'](report_path,value)
    sys.exit(native_code)
finally:
    sys.settrace(None)
    rows=[]
    for log in (repo/'.task-tools/artifacts/native').glob('TEST-installed*/*host-private.log'):
        for line in log.read_text(errors='replace').splitlines():
            marker='[agent-notifications] '
            if marker not in line or len(line)>4096: continue
            try: value=json.loads(line.split(marker,1)[1])
            except ValueError: continue
            if not isinstance(value,dict): continue
            row={}
            for key in ('ipc','childClosure','exitCode','forcedKill'):
                item=value.get(key)
                if isinstance(item,(bool,int)) or item in ('ok','invalidated','closed','unproved','spawn_failed','aborted','deadline','ipc_termination_unproved','exited','stream_error','output_limit'): row[key]=item
            receipt=value.get('receipt',{})
            if isinstance(receipt,dict):
                row['receipt']={key:item for key,item in receipt.items() if key in ('status','desktop','webhook') and item in ('submitted','unknown','unavailable','rejected','suppressed')}
            rows.append(row)
    output.write_text(json.dumps({'exceptions':events,'diagnostics':rows[:32],'permissionOperations':permissions}))
''')
business_result=subprocess.run([sys.executable,'-B',str(diagnostic_driver),str(repo),str(repo/'scripts/opencode-native-e2e.py'),'--manifest',str(manifest_path),
    '--manifest-sha256',manifest_sha,'--binary',str(binary),'--archive',str(host),'--os',c['platform'],
    '--arch',c['arch'],'--version',c['version'],'--suite','business','--business-proof',str(proof),
    '--business-proof-sha256',sha(proof),'--report',str(art/'native-report.json')]+(['--receiver-record',str(art/'receiver-record.json')] if c['platform']=='linux' else []),check=False,cwd=repo)
report=json.loads((art/'native-report.json').read_text())
report.update(unifiedReleaseTag=c['releaseTag'],unifiedCandidate=candidate,unifiedSigningRun=run,unifiedOperatorSHA=c['operatorSHA'],unifiedParentSHA256=archive_sha,unifiedAdapterSHA256=c['adapterSHA256'],fullNativeQualified=False,coldStartQualified=False)
r.write_json(art/'native-report.json',report)
need(business_result.returncode==0 and report['status']=='installed_business_lifecycle_observed','actual_installed_lifecycle_report')
`;
main();
