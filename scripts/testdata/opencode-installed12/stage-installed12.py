"""TEST authenticated finite source supplement; gate() only, never main()."""
import argparse, hashlib, importlib.util, json, os, pathlib, re, stat, subprocess, tarfile, types
P=pathlib.Path
PLAN_SHA='43e800a04df0dac060c3e969dd8f461ee93c36b08be64654b65b68ecd5175f26'
PINS={'installed-local-driver.py':'fd0b6b98a5f7029c004fa5bc59c81776d72bc2f20277b984f000ad6d5ebbf49f','direct-entry.py':'3ad0ddaf09878924858cc680730a383a74015b04503755974ce9d211418f12ed','native-history-witness.ts':'944b9d47d8dd4b6291ec1ef3374defa6d21cd68c147b1329826ea0494b082193','native-history-witness.js':'584d425f4e9f91456657faf25c9cecb2fbdd4b4430d9d020bf40a8ee233d5a2b','witness-build-receipt.json':'27d490820ce474ca0e9057082c79c7feee6587fba38eaf750c49487ba1919b3e','donor/driver.py':'e25f238074854e8cc3d3b14638e3b4f52429fbc3e337ec73633b7f6e0885cd69','donor/retained/windows_conpty.py':'8220425ee006e13a95e4c98d215452ac238d257055387c36cec6fc8585f5ea9f','donor/retained/harness.py':'cd6147929a58363e24063e4c520b87cf5bc08fe39b16ced689d4cee7750fbb78','retained/installed_business.py':'c39d33626abe9217bbfad05178927658c210290649f1fd1bccdd2b755d6aace3','retained/provider.py':'57633468d4187d44b65abd245ad29d49a380619fe1e61049a1612272cad80e49','retained/portable_permission.py':'f499590b9c8c3bfee37156633bf8a5a36024be208b9c26addfd70cbd000874cd','retained/portable.py':'d7861c41fc43a3bcfea54ebd8b23bc0ebe53c452b992af6756960537974f91c6','retained-source-custody.json':'8d59ab02226cb44fc3b3f5de8e33826f9e581a6f658f0527e5720148a8eb1388'}
def need(v,c):
 if not v:raise ValueError(c)
def digest(p):
 need(not any(x.is_symlink() for x in (p,*p.parents)) and stat.S_ISREG(p.lstat().st_mode),'regular_custody')
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def pairs(items):
 d={}
 for k,v in items:need(k not in d,'duplicate_JSON_key');d[k]=v
 return d
def load(raw):return json.loads(raw,object_pairs_hook=pairs)
def main(a):
 repo=P.cwd().resolve();need(any(x.startswith('TEST-') for x in repo.parts),'exclusive_TEST_checkout')
 head=os.environ['TEST_PRODUCT_HEAD'];need(re.fullmatch('[0-9a-f]{40}',head) and subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==head,'final_checkout')
 plan_path=P(__file__).resolve().with_name('installed12-plan.json');need(digest(plan_path)==PLAN_SHA,'one_approved_plan');plan=load(plan_path.read_bytes())
 ids={c['id'] for c in plan['cells']};need(len(ids)==12 and len([c for c in plan['cells'] if c['route']=='HOST'])==4,'twelve_cell_scope')
 selected=[c for c in plan['cells'] if (c['os'],c['arch'],c['version'],c['entry'])==(a.os,a.arch,a.version,a.entry)]
 need(len(selected)==1,'unknown_cell')
 folder=a.directory.absolute();archive=folder/'installed12-supplement.tar.gz'
 need(list(folder.iterdir())==[archive] and digest(archive)==a.sha256 and archive.stat().st_size<=16*1024**2,'authenticated_supplement_archive')
 with tarfile.open(archive,'r:gz') as packed:
  members=packed.getmembers();need(0<len(members)<=128 and len({m.name for m in members})==len(members),'finite_unique_members')
  total=0;data={}
  for m in members:
   name=pathlib.PurePosixPath(m.name);need(m.isfile() and not name.is_absolute() and name.parts and all(re.fullmatch('[A-Za-z0-9._-]+',p) and p not in ('.','..') for p in name.parts) and (m.name=='supplement.json' or name.parts[0]=='installed12'),'regular_supplement_member')
   total+=m.size;need(0<m.size<=2*1024**2 and total<=16*1024**2,'supplement_size')
   with packed.extractfile(m) as f:raw=f.read(m.size+1)
   need(len(raw)==m.size,'member_size');data[m.name]=raw
 packet=load(data['supplement.json'])
 need(set(packet)=={'schema','status','purpose','candidateCommit','parentManifestSHA256','planSHA256','files','gates'} and packet['schema']==1 and packet['status']=='FINAL_SOURCE_REVIEWED' and packet['purpose']=='TEST installed12 authenticated thinR3 supplement','SOURCE_SUPPLEMENT_UNRESOLVED')
 need(packet['candidateCommit']==head and packet['parentManifestSHA256']==a.manifest_sha256==digest(a.manifest) and packet['planSHA256']==PLAN_SHA,'final_parent_and_plan')
 files=packet['files'];need(isinstance(files,dict) and set(data)=={'supplement.json',*files} and isinstance(packet['gates'],dict) and set(packet['gates'])==ids,'exact_packet_files_and_twelve_gates')
 for name,h in files.items():need(re.fullmatch('[0-9a-f]{64}',str(h)) and hashlib.sha256(data[name]).hexdigest()==h,'sealed_supplement_leaf')
 for name,h in PINS.items():need(files.get('installed12/'+name)==h,'accepted_supplier_bytes')
 base=repo/'.task-tools/artifacts';need(not (base/'installed12').exists() and not any(p.is_symlink() for p in (base,*base.parents)),'fresh_supplement_stage')
 for name,raw in data.items():
  if name=='supplement.json':continue
  target=base/name;target.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
  with target.open('xb') as out:out.write(raw)
  target.chmod(0o400)
 driver=base/'installed12/installed-local-driver.py';spec=importlib.util.spec_from_file_location('exact_inert_thin_gate',driver);module=importlib.util.module_from_spec(spec)
 raw=driver.read_bytes();need(hashlib.sha256(raw).hexdigest()==PINS['installed-local-driver.py'],'actual_gate_source');exec(compile(raw,str(driver),'exec'),module.__dict__)
 common=None
 for c in plan['cells']:
  record=packet['gates'][c['id']];need(set(record)=={'path','sha256'} and record['path'].startswith('.task-tools/artifacts/installed12/'),'finite_gate_path')
  gate_file=repo/record['path'];need(record['sha256']==files.get(record['path'].removeprefix('.task-tools/artifacts/')),'packet_gate_binding')
  gate,_,_,_=module.gate(types.SimpleNamespace(repo=repo,manifest=a.manifest,manifest_sha256=a.manifest_sha256,final_gate=gate_file,final_gate_sha256=record['sha256']))
  need(gate['candidateCommit']==head,'actual_gate_final_head')
  coherent={k:v for k,v in gate.items() if k!='cellProof'}
  need(common is None or coherent==common,'one_coherent_final_source');common=coherent
 record=packet['gates'][selected[0]['id']]
 with P(os.environ['GITHUB_OUTPUT']).open('a') as out:out.write('driver='+str(driver)+'\ngate='+str(repo/record['path'])+'\ngate_sha256='+record['sha256']+'\n')
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--directory',type=P,required=True);p.add_argument('--manifest',type=P,required=True)
 for n in ('sha256','manifest-sha256','os','arch','version','entry'):p.add_argument('--'+n,required=True)
 main(p.parse_args())
