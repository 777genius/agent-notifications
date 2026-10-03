"""TEST-only local artifact custody, then unchanged exact production parser."""
import hashlib,importlib.util,os,pathlib,re,stat,subprocess
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
with P(os.environ['GITHUB_OUTPUT']).open('a')as outputs:outputs.write('manifest='+str(manifest.relative_to(repo))+'\nsha256='+sha+'\n')
