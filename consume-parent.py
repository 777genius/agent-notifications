"""TEST-only local artifact custody, then unchanged exact production parser."""
import base64,hashlib,importlib.util,json,os,pathlib,re,stat,subprocess,tarfile
P=pathlib.Path;repo=P.cwd();head=os.environ['TEST_PRODUCT_HEAD'];expected=os.environ['TEST_PARENT_SHA256']
assert re.fullmatch('[0-9a-f]{40}',head)and re.fullmatch('[0-9a-f]{64}',expected)
assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==head
folder=P(os.environ['TEST_PARENT_DIRECTORY']).absolute();entries=list(folder.iterdir());archive=folder/'parent-evidence.tar.gz'
assert entries==[archive] and not any(p.is_symlink()for p in [archive,*archive.parents]) and stat.S_ISREG(archive.lstat().st_mode)and 0<archive.stat().st_size<=2*1024**3
with archive.open('rb')as stream:assert hashlib.file_digest(stream,'sha256').hexdigest()==expected
source=repo/'scripts/testdata/opencode-native-e2e/ci_inputs.py';sourcehash=os.environ['TEST_CI_INPUTS_SHA256'];harnesshash=os.environ['TEST_HARNESS_SHA256']
for p,h in [(source,sourcehash),(repo/'scripts/opencode-native-e2e.py',harnesshash)]:
 assert re.fullmatch('[0-9a-f]{64}',h) and not p.is_symlink()
 with p.open('rb')as stream:assert hashlib.file_digest(stream,'sha256').hexdigest()==h
spec=importlib.util.spec_from_file_location('exact_parent_stage',source);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
m.artifacts.mkdir(parents=True,exist_ok=True);manifest,sha=m.stage_parent_archive(archive,expected,m.artifacts/'parent-inputs')
# Materialize only the source alias omitted from the sealed parent; production
# load_manifest/checked_file remains the independent unchanged preflight.
# Fresh final manifest is independently accepted, not the historical H5 pin.
manifest_pin=os.environ['TEST_PARENT_MANIFEST_SHA256']
assert re.fullmatch('[0-9a-f]{64}',manifest_pin) and sha==manifest_pin
parent=json.loads(manifest.read_text())
alias_record={'path':'.task-tools/artifacts/parent-inputs/sdk-index.js','sha256':'8e0c343aa9ea29bfce3e4d53c0d979ffc447af96c36b10c5253e3a766a6454a5'}
tar_record={'path':'opencode-plugin/vendor/universal-agent-plugins-opencode-events-0.3.0.tgz','sha256':'c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752'}
# This narrow supplier supports only the accepted unchanged twelve-member SDK.
assert parent['candidateCommit']==head and parent['buildRevision']==head
assert parent['assets']['sdkSource']==alias_record and parent['sdk']['archive']==tar_record
lock_pin=parent['assets']['packageLock']['sha256']
assert re.fullmatch('[0-9a-f]{64}',lock_pin)
sdk=repo/tar_record['path'];lockfile=repo/'opencode-plugin/package-lock.json';alias=repo/alias_record['path']
for p in (sdk,lockfile):
 assert not any(q.is_symlink()for q in [p,*p.parents]) and stat.S_ISREG(p.lstat().st_mode)
 assert subprocess.check_output(['git','ls-files','--error-unmatch','--',p.relative_to(repo).as_posix()],text=True).strip()==p.relative_to(repo).as_posix()
assert 0<sdk.stat().st_size<=1024*1024 and 0<lockfile.stat().st_size<=1024*1024
sdkbytes=sdk.read_bytes();assert 0<len(sdkbytes)<=1024*1024 and hashlib.sha256(sdkbytes).hexdigest()==tar_record['sha256']
assert hashlib.sha256(lockfile.read_bytes()).hexdigest()==lock_pin
sri='sha512-'+base64.b64encode(hashlib.sha512(sdkbytes).digest()).decode()
assert sri==parent['sdk']['sri']
lock=json.loads(lockfile.read_text())['packages']['node_modules/universal-agent-plugins-opencode-events']
assert lock['version']=='0.3.0' and lock['resolved']==parent['sdk']['lockResolved']=='file:vendor/universal-agent-plugins-opencode-events-0.3.0.tgz' and lock.get('integrity')==parent['sdk']['lockIntegrity'] and lock.get('integrity') in (None,sri)
assert (lockfile.parent/lock['resolved'][5:]).resolve(strict=True)==sdk.resolve(strict=True)
with tarfile.open(sdk,'r:gz')as packed:
 members=packed.getmembers();names=[p.name for p in members]
 assert len(members)==12 and len(set(names))==12 and all(p.isfile()and p.size<=65536 and p.name.startswith('package/')and '..'not in P(p.name).parts for p in members)and sum(p.size for p in members)<=1024*1024
 index=[p for p in members if p.name=='package/index.js'];metadata=[p for p in members if p.name=='package/package.json']
 assert len(index)==len(metadata)==1 and index[0].size==311
 package=json.loads(packed.extractfile(metadata[0]).read())
 assert package['name']=='universal-agent-plugins-opencode-events' and package['version']==lock['version']
 content=packed.extractfile(index[0]).read();assert len(content)==311 and hashlib.sha256(content).hexdigest()==alias_record['sha256']
assert alias.parent==manifest.parent and not any(p.is_symlink()for p in [alias,*alias.parents])
if alias.exists():
 assert stat.S_ISREG(alias.lstat().st_mode) and alias.read_bytes()==content
else:
 with alias.open('xb')as target:target.write(content)
 alias.chmod(0o600)
assert hashlib.sha256(alias.read_bytes()).hexdigest()==alias_record['sha256'] and sdk.read_bytes()==sdkbytes and hashlib.sha256(lockfile.read_bytes()).hexdigest()==lock_pin and hashlib.sha256(manifest.read_bytes()).hexdigest()==sha
with P(os.environ['GITHUB_OUTPUT']).open('a')as outputs:outputs.write('manifest='+str(manifest.relative_to(repo))+'\nsha256='+sha+'\n')
