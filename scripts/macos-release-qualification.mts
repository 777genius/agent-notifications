/** External, exact-source macOS qualification. Never changes candidate source. */
import { createHash } from 'node:crypto';
import { readFileSync, mkdirSync, writeFileSync, realpathSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { spawnSync } from 'node:child_process';

const candidate = '1f5cf76a6cd3468fb23ef88d762cfb0f6d691820';
const signingRun = '37632615999';
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
  for (const file of ['scripts/macos-release-qualification.mts', 'scripts/macos-release-reader.mts']) {
    const expected = command('git', ['show', `${operatorSHA}:${file}`], operator);
    need(readFileSync(join(operator, file), 'utf8').trimEnd() === expected, 'operator source changed');
  }
  const tools = resolve(source, '.task-tools/artifacts/macos-tools');
  mkdirSync(tools, { recursive: false, mode: 0o700 });
  const consumer = readFileSync(join(operator, 'scripts/macos-release-reader.mts'));
  writeFileSync(join(tools, 'release-opencode-reader.mts'), consumer, { flag: 'wx', mode: 0o600 });
  const config = { mode, source, tools, candidate, signingRun, operatorSHA,
    adapterSHA256: hash(readFileSync(join(operator, 'scripts/macos-release-qualification.mts'))),
    consumerSHA256: hash(consumer), arch: process.env.AN_ARCH, version: process.env.AN_VERSION,
    parentSHA256: process.env.AN_EVIDENCE_SHA256, currentRun: process.env.GITHUB_RUN_ID };
  const configPath = join(tools, 'configuration.json');
  writeFileSync(configPath, JSON.stringify(config), { flag: 'wx', mode: 0o600 });
  const nativeEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    !/(?:TOKEN|API_KEY|PASSWORD|SECRET|CREDENTIAL|AUTH)/i.test(key)));
  nativeEnv.SOURCE_COMMIT = candidate;
  const bridgePath = join(tools, 'bridge.py');
  writeFileSync(bridgePath, bridge, { flag: 'wx', mode: 0o600 });
  const result = spawnSync('python3', ['-B', bridgePath, configPath], { cwd: source, env: nativeEnv, stdio: 'inherit' });
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
             and (root/'candidate-version.txt').read_text().strip()=='v1.48.2','signed_native_source_version')
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
def check_prior_custody():
    value=json.loads((art/'custody-run.json').read_text())
    need(value['id']==37645007119 and value['run_attempt']==1 and value['status']=='completed'
         and value['head_sha']=='23786c97ea926b4dfea5faf8780ccf519b4c09c2'
         and value['event']=='workflow_dispatch' and value['actor']['login']=='777genius'
         and value['triggering_actor']['login']=='777genius','owner_exact_prior_custody_run')
    jobs=json.loads((art/'custody-jobs.json').read_text())
    need(jobs['total_count']==len(jobs['jobs']),'complete_prior_custody_jobs')
    selected=[]
    for platform,arch,job_id in (('linux','amd64',112873389471),('linux','arm64',112873389132),('windows','amd64',112873388956)):
        name='Build additional custody only '+platform+'/'+arch
        matching=[job for job in jobs['jobs'] if job['name']==name]
        need(len(matching)==1 and matching[0]['id']==job_id and matching[0]['run_id']==value['id']
             and matching[0]['head_sha']==value['head_sha']
             and matching[0]['status']=='completed' and matching[0]['conclusion']=='success',
             'successful_exact_prior_custody_job')
        binary=repo/'dist'/('claude-notifications-'+platform+'-'+arch+('.exe' if platform=='windows' else ''))
        need(binary.is_file() and not binary.is_symlink(),'prior_custody_binary_missing')
        selected.append({'jobID':matching[0]['id'],'name':name,'binary':record(binary)})
    return {'runID':value['id'],'runAttempt':value['run_attempt'],'operatorSHA':value['head_sha'],
            'overallConclusion':value['conclusion'],'jobs':selected,'qualificationGranted':False}
r=load('opencode-native-e2e.py')
h=load('opencode-platform-clock-prequalification.py')
need(all(v is False for v in h.QUALIFICATIONS.values()),'clock_grants_must_remain_false')
if c['mode']=='prepare':
    custody=check_run()
    prior_custody=check_prior_custody()
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
    receipt.update(purpose='exact-source signed macOS and prior operator binary custody, no qualification grant',
                   runId=run,operatorRunID=c['currentRun'],operatorSHA=c['operatorSHA'],
                   signingCustody=custody,adapterSHA256=c['adapterSHA256'],consumerSHA256=c['consumerSHA256'],
                   priorOperatorCustody=prior_custody,
                   otherNativeBinaries='prior_operator_run_custody_only_no_platform_promotion')
    (parent/'release-inputs-receipt.json').write_text(json.dumps(receipt,sort_keys=True)+'\n')
    archive.unlink(); digest=inputs.seal_archive(parent,archive)
    with open(os.environ['GITHUB_OUTPUT'],'a') as out: out.write('parent_sha256='+digest+'\n')
    sys.exit(0)
need(sys.platform=='darwin' and c['arch'] in ('amd64','arm64') and c['version'] in ('1.18.33','2.0.21'),
     'four_mac_native_cells_only')
archive=art/'custody/release-opencode-inputs.tar.gz'; archive_sha=c['parentSHA256']
need(re.fullmatch('[a-f0-9]{64}',str(archive_sha)) and sha(archive)==archive_sha,'sealed_parent_digest')
ci=load('testdata/opencode-native-e2e/ci_inputs.py')
manifest_path,manifest_sha=ci.stage_parent_archive(archive,archive_sha,art/'parent-inputs')
m,cell,files,binary,host=r.load_manifest(manifest_path,'darwin',c['arch'],c['version'],manifest_sha)
receipt=json.loads((manifest_path.parent/'release-inputs-receipt.json').read_text())
need(receipt['runId']==run and receipt['candidateCommit']==candidate and receipt['operatorSHA']==c['operatorSHA']
     and receipt['adapterSHA256']==c['adapterSHA256'] and receipt['consumerSHA256']==c['consumerSHA256'],
     'reviewed_operator_and_signing_provenance')
app=r.native_app(cell['nativeApp']); executable=repo/cell['nativeApp']['executable']['path']
need(sha(executable)==receipt['signingCustody']['executableSHA256'],'selected_signed_native_app')
executable.chmod(0o700); binary.chmod(0o700)
subprocess.run(['bash',str(repo/'swift-notifier/scripts/verify-signing.sh'),str(app),'--notarized'],check=True)
dirty=subprocess.check_output(['git','status','--porcelain','--untracked-files=no'],cwd=repo,text=True)
dirty+=subprocess.check_output(['git','ls-files','--others','--exclude-standard','--','.',':(exclude).task-tools/artifacts'],cwd=repo,text=True)
info=subprocess.check_output(['go','version','-m',str(binary)],cwd=repo,text=True)
r.verify_source_binding(m,candidate,info,dirty)
go_version=re.search(r'^.+: (go1\.26\.[0-9]+)\n',info)
need(go_version is not None,'signed_go_126_patch_identity')
clock=load('release-opencode-clock.py',[
    (r'go1\.27\.1',re.escape(go_version[1]),2),
    ("{'Linux': 'linux', 'Windows': 'windows'}","{'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}",1)],True)
clock.CANDIDATE=candidate; clock.ORIGINAL_RUN_ID=run; clock.ORIGINAL_ARCHIVE_SHA256=archive_sha
clock.RELEASE_CELLS=set(r.CELLS)
def custody(_h,args):
    need(args.parent_archive_sha256==archive_sha and sha(args.parent_archive)==archive_sha
         and sha(args.manifest)==manifest_sha,'clock_exact_parent_archive')
    r.custody_cells(m,('darwin',c['arch'],c['version'])); return m
clock.custody=custody
clock_report=art/'clock.json'
args=NS(candidate_repo=repo,original_run_id=run,parent_archive=archive,parent_archive_sha256=archive_sha,
        manifest=manifest_path,manifest_sha256=manifest_sha,os='darwin',arch=c['arch'],version=c['version'],
        temp_base=P(os.environ['RUNNER_TEMP']),go_module_cache=P(subprocess.check_output(['go','env','GOMODCACHE'],text=True).strip()),report=clock_report)
need(clock.execute(args)==0,'fresh_clock_primitive_failed')
need(json.loads(clock_report.read_text())['adapterSHA256']==sha(tools/'release-opencode-clock.py'),
     'fresh_clock_operator_source_closure')
reader=load('release-opencode-reader.py',[],True); reader.CANDIDATE=candidate; reader.CELLS=set(r.CELLS)
def boundary(_):
    need(not any(os.environ.get(k) for k in ('GH_TOKEN','GITHUB_TOKEN','OPENAI_API_KEY','ANTHROPIC_API_KEY')),'native_credentials_forbidden')
    return {'platform':'darwin','networkIsolationClaimed':False,
            'isolation':'private_files_minimal_env_loopback_provider_no_network_isolation_claim'}
reader.network_guard=boundary
def manifest_inputs(a):
    mm,cc,ff,bb,aa=r.load_manifest(a.manifest,'darwin',a.arch,a.version,a.manifest_sha256)
    pin=next(p for p in json.loads(reader.blob('scripts/testdata/opencode-native-e2e/host-pins.json'))['cells']
             if (p['os'],p['arch'],p['version'])==('darwin',a.arch,a.version))
    need(reader.sha(reader.REPO/mm['sdk']['archive']['path'])==reader.SDK_SHA256,'exact_reader_sdk')
    identity={'candidateCommit':candidate,'embeddedSHA256':sha(ff['embedded']),'candidateSHA256':sha(bb),
        'manifestSHA256':a.manifest_sha256,'sdkArchiveSHA256':mm['sdk']['archive']['sha256'],
        'hostArchiveSHA256':sha(aa),'hostSourceCommit':cc['hostSourceCommit']}
    return identity,pin,aa
reader.manifest_inputs=manifest_inputs
def unpack(archive,root,pin,os_name,arch):
    raw=archive.read_bytes()
    need(sha(archive)==pin['archiveSHA256'] and 'sha512-'+base64.b64encode(hashlib.sha512(raw).digest()).decode()==pin['archiveSRI'],'official_reader_archive')
    target=root/'opencode'; r.extract_opencode(archive,target,'darwin')
    need(sha(target)==pin['executableSHA256'],'official_reader_image')
    with target.open('rb') as stream: h.native_header(stream.read(4096),'darwin',arch)
    return target
reader.unpack=unpack
reader.esbuild_command_for_magic=lambda executable,node,magic: [str(executable)] if magic in (b'\xcf\xfa\xed\xfe',b'\xce\xfa\xed\xfe',b'\xca\xfe\xba\xbe') or magic.startswith((b'\x7fELF',b'MZ')) else [str(node),str(executable)]
buildroot=art/'TEST-reader-build'; node=P(shutil.which('node'))
buildargs=NS(build_root=buildroot,tsc=repo/'opencode-plugin/node_modules/.bin/tsc',node_types=repo/'opencode-plugin/node_modules/@types',
             node=node,esbuild=repo/'opencode-plugin/node_modules/esbuild/bin/esbuild',typecheck_only=False)
need(reader.build(buildargs)==0,'fresh_typed_reader_build')
reader_report=art/'reader.json'; bundle=buildroot/'reader.js'; buildreceipt=buildroot/'build-receipt.json'
readargs=NS(root=art/'TEST-reader-native',manifest=manifest_path,manifest_sha256=manifest_sha,os='darwin',arch=c['arch'],
            version=c['version'],mode='api-only' if c['version']=='2.0.21' else 'one-completion',
            bundle=bundle,bundle_sha256=sha(bundle),build_receipt=buildreceipt,build_receipt_sha256=sha(buildreceipt),report=reader_report)
need(reader.execute(readargs)==0,'fresh_reader_primitive_failed')
sealer=load('release-opencode-business-proof.py',[],True); sealer.CANDIDATE=candidate
sealer.ORIGINAL_RUN=run; sealer.ORIGINAL_ARCHIVE=archive_sha; sealer.CELLS=set(r.CELLS)
def selected_manifest(manifest,digest,selected):
    r.custody_cells(manifest,tuple(selected.values()))
    need(manifest['candidateCommit']==manifest['buildRevision']==candidate,'mac_manifest_candidate')
    cc=next(x for x in manifest['cells'] if all(x[k]==v for k,v in selected.items()))
    return {'embeddedSHA256':manifest['assets']['embedded']['sha256'],'candidateSHA256':cc['candidate']['sha256'],
      'manifestSHA256':digest,'sdkArchiveSHA256':manifest['sdk']['archive']['sha256'],'hostArchiveSHA256':cc['archive']['sha256'],
      'ownedImageSHA256':cc['executableSHA256'],'hostSourceCommit':cc['hostSourceCommit']}
sealer.selected_manifest=selected_manifest
def network_boundary(observed,os_name): need(os_name=='darwin' and observed==boundary(None),'honest_mac_network_boundary')
sealer.network_boundary=network_boundary
sys.argv=['sealer','--manifest',str(manifest_path),'--manifest-sha256',manifest_sha,
    '--clock-report',str(clock_report),'--clock-report-sha256',sha(clock_report),
    '--reader-report',str(reader_report),'--reader-report-sha256',sha(reader_report),'--source-root',str(repo),
    '--output',str(art/'sealed-business'),'--os','darwin','--arch',c['arch'],'--version',c['version']]
sealer.main()
proof=art/'sealed-business/business-proof.json'
# Observe exceptions and content-free diagnostics without changing runner predicates.
# Load the exact candidate inertly, then call its original main/global namespace.
# Setup delegates unchanged before the explicit public OS permission operation.
diagnostic_driver=tools/'business-diagnostic.py'
diagnostic_driver.write_text('''import json,os,pathlib,re,runpy,signal,subprocess,sys
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
    if action!='install': return
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
    sys.exit(runner['main']())
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
subprocess.run([sys.executable,'-B',str(diagnostic_driver),str(repo),str(repo/'scripts/opencode-native-e2e.py'),'--manifest',str(manifest_path),
    '--manifest-sha256',manifest_sha,'--binary',str(binary),'--archive',str(host),'--os','darwin',
    '--arch',c['arch'],'--version',c['version'],'--suite','business','--business-proof',str(proof),
    '--business-proof-sha256',sha(proof),'--report',str(art/'native-report.json')],check=True,cwd=repo)
need(json.loads((art/'native-report.json').read_text())['status']=='installed_business_lifecycle_observed','actual_installed_lifecycle_report')
`;
main();
