"""Finite checkout capsule adapter; original frozen stage.py remains unchanged."""
import argparse,base64,hashlib,importlib.util,json,pathlib,tarfile
P=pathlib.Path;HERE=P(__file__).resolve().parent
PINS={'consumer.mjs': '678f7b015f83af4caa2ec5acc87c44d85b5994a2f80a923f79274ee92a891f80', 'candidate/native-v1.mjs': 'ee83f9ce361a1ed17dbbc5dd0f77577c7e11160cffc7080925cf1d7a4166f4c3', 'candidate/native-v2.mjs': '873f97c4b9c1992dce9fb2cc646237c75f92766341de545e85e81cdd85f6c3bb', 'candidate/protocol.mjs': '770b8cc905b9b36622b8a33779687f48f4a64a9c42c3dacbb713209c46e511b2', 'retained/sdk.tgz': 'c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752', 'retained/pack-receipt.json': 'a196e5e4105f2362f6c5ffbf08096cc5de371987d8a22bd78bd70612deb62c56', 'retained/provider.py': 'e20390e5efdef97ce3fc2987fa739564ad3b43cafb021a8e8da74f51b0cbb653', 'retained/owned-test-cgroup.py': '8a349bffd8a202daae9173b0686ec547bb3cffa126669e72c73ca0aa744d56f8', 'driver.py': 'b4f76bec84e9aa1cc9fcf87c653eec757c608b092ad92252984176be1b07e244', 'stage.py': '9850b8afd4744ab7831681f9a2daf77c1f541a22954feaf6c2f8a1b5198674c7', 'official-host-pins.json': '59368f673e651f00e8a4d0a675365ba15f0752eacabc3e2ea68a2e91cf6b166f', 'SOURCE-MANIFEST.json': '0bd622a14cb104663a908e0cf859120d6f757c09c10348fd3f87de82916479a7', 'capsule.tar': 'f9921873a344ea17e6dd77521be84e42942c945e9a969b8b6a52b0a1c98c5fd6', 'capsule-seal.json': '4c3a2844ce97ff2caf83ebaa712fe1e1c0b42e51ea13be2387388604c7ac6e6a'}
CELLS={('linux','arm64','1.18.33'),('linux','arm64','2.0.21'),('darwin','amd64','1.18.33'),('darwin','amd64','2.0.21'),('darwin','arm64','1.18.33'),('darwin','arm64','2.0.21'),('windows','amd64','1.18.33')}
def need(ok,cause):
 if not ok:raise ValueError(cause)
def sha(p):
 need(p.is_file() and not any(x.is_symlink() for x in [p,*p.parents]),'regular_pinned_input');return hashlib.sha256(p.read_bytes()).hexdigest()
def main(a):
 for n,h in PINS.items():need(sha(HERE/n)==h,'checkout_pin_'+n)
 need((a.os,a.arch,a.version) in CELLS,'closed_seven_cells')
 need(a.root.name.startswith('TEST-') and not a.root.exists() and not any(p.is_symlink() for p in a.root.parents),'fresh_TEST_input');a.root.mkdir(mode=0o700)
 capsule=HERE/'capsule.tar';need(capsule.stat().st_size<=2097152,'capsule_2MiB')
 seal=json.loads((HERE/'capsule-seal.json').read_text());need(seal['qualificationGranted'] is False,'no_qualification')
 with tarfile.open(capsule,'r:') as t:
  ms=t.getmembers();need(len(ms)==2 and {m.name for m in ms}=={'reader.js','build-receipt.json'} and all(m.isfile() and m.size<=1048576 for m in ms),'closed_two_file_capsule')
  for m in ms:
   raw=t.extractfile(m).read();want=seal['bundleSHA256'] if m.name=='reader.js' else seal['buildReceiptSHA256'];need(hashlib.sha256(raw).hexdigest()==want,'capsule_member');(a.root/m.name).write_bytes(raw)
 build=json.loads((a.root/'build-receipt.json').read_bytes());need(build['bundleSHA256']==seal['bundleSHA256'] and build['status']=='genuine_portable_reader_bundle_built','actual_build')
 for n,h in build['sourceLeafSHA256'].items():need(n in PINS and sha(HERE/n)==h,'compiled_graph')
 spec=importlib.util.spec_from_file_location('frozen_stage',HERE/'stage.py');stage=importlib.util.module_from_spec(spec);spec.loader.exec_module(stage)
 pin=next(x for x in json.loads((HERE/'official-host-pins.json').read_text())['cells'] if (x['os'],x['arch'],x['version'])==(a.os,a.arch,a.version))
 raw=stage.fetch(pin['hostURL'],160*1024*1024);need(hashlib.sha256(raw).hexdigest()==pin['archiveSHA256'] and pin['archiveSRI'].startswith('sha512-') and base64.b64encode(hashlib.sha512(raw).digest()).decode()==pin['archiveSRI'].split('-',1)[1],'official_SHA_SRI_before_parse')
 (a.root/(a.version+'.tgz')).write_bytes(raw)
 for n,h in PINS.items():need(sha(HERE/n)==h,'preserved_checkout_'+n)
 print(json.dumps({'stageOnly':True,'qualificationGranted':False,'capsuleSHA256':PINS['capsule.tar'],'archiveSHA256':pin['archiveSHA256']}))
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--root',type=P,required=True)
 for n in ['os','arch','version']:p.add_argument('--'+n,required=True)
 main(p.parse_args())
