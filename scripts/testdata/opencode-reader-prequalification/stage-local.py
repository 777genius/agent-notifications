"""Finite checkout capsule adapter; original frozen stage.py remains unchanged."""
import argparse,base64,hashlib,importlib.util,json,pathlib,tarfile
P=pathlib.Path;HERE=P(__file__).resolve().parent
PINS={'consumer.mjs': 'e568cd36834fe4ba0646e76134de26179a432e85c12c77f075ba05e21fea5d39', 'candidate/native-v1.mjs': 'ee83f9ce361a1ed17dbbc5dd0f77577c7e11160cffc7080925cf1d7a4166f4c3', 'candidate/native-v2.mjs': '873f97c4b9c1992dce9fb2cc646237c75f92766341de545e85e81cdd85f6c3bb', 'candidate/protocol.mjs': '770b8cc905b9b36622b8a33779687f48f4a64a9c42c3dacbb713209c46e511b2', 'retained/sdk.tgz': 'c3d5aaaf6ecc3116b48ab1ae3f0e00b47720f239df9938f0c499d03f2c21a752', 'retained/pack-receipt.json': 'a196e5e4105f2362f6c5ffbf08096cc5de371987d8a22bd78bd70612deb62c56', 'retained/provider.py': 'e20390e5efdef97ce3fc2987fa739564ad3b43cafb021a8e8da74f51b0cbb653', 'retained/owned-test-cgroup.py': '8a349bffd8a202daae9173b0686ec547bb3cffa126669e72c73ca0aa744d56f8', 'driver.py': 'bac355a347893736df124dd26d59b447cc0ea2de7e836f328fd783d37b484061', 'stage.py': '9850b8afd4744ab7831681f9a2daf77c1f541a22954feaf6c2f8a1b5198674c7', 'official-host-pins.json': '59368f673e651f00e8a4d0a675365ba15f0752eacabc3e2ea68a2e91cf6b166f', 'SOURCE-MANIFEST.json': 'dd61e5f43d4ba8d1ef1b655ee8420ea25b482c3d72067ed9a55a85d97fb810f0', 'capsule.tar': '474a0872d01d2e7fd28b55aced03dce4e0dca5fdbfadb6bc7af2eb5329787f3c', 'capsule-seal.json': '8eeb08fe25c916c7908a04a4033aaff457445c89f9036097222bdbde3abe5958', 'official-archive-layouts.json': 'a0ac3dc8d3bf26956b061e46063efc19257ee3149208f26a763656a585532649', 'retained/windows_conpty.py': '8220425ee006e13a95e4c98d215452ac238d257055387c36cec6fc8585f5ea9f', 'retained/harness.py': 'cd6147929a58363e24063e4c520b87cf5bc08fe39b16ced689d4cee7750fbb78', 'LOCAL-ENTRY-SOURCE.json': '7b4fbdc27945a6caf121515fa0cf6462044866ede46901d1b38e2ab7fd8fd3f0'}
CELLS={('windows','amd64','1.18.33','tui')}
CAPSULE_BYTES=122880 # ROOT observed actual two-file tar size
def need(ok,cause):
 if not ok:raise ValueError(cause)
def sha(p):
 need(p.is_file() and not any(x.is_symlink() for x in [p,*p.parents]),'regular_pinned_input');return hashlib.sha256(p.read_bytes()).hexdigest()
def main(a):
 need(all(len(h)==64 and all(c in '0123456789abcdef' for c in h) for h in PINS.values()),'actual_ROOT_capsule_bindings_required')
 need(type(CAPSULE_BYTES) is int and 0<CAPSULE_BYTES<=2097152,'actual_ROOT_capsule_size_required')
 for n,h in PINS.items():need(sha(HERE/n)==h,'checkout_pin_'+n)
 need((a.os,a.arch,a.version,a.entry) in CELLS,'closed_one_Windows_TUI_cell')
 need(a.root.name.startswith('TEST-') and not a.root.exists() and not any(p.is_symlink() for p in a.root.parents),'fresh_TEST_input');a.root.mkdir(mode=0o700)
 capsule=HERE/'capsule.tar';need(capsule.stat().st_size==CAPSULE_BYTES,'exact_capsule_bytes')
 seal=json.loads((HERE/'capsule-seal.json').read_text());need(seal['qualificationGranted'] is False and seal['capsuleSHA256']==PINS['capsule.tar'] and seal['bundleSHA256']=='d48042526d8b657ad48c461c8abae4ea9254a9e3b1390085e0dd20ccdb7eab9a' and seal['buildReceiptSHA256']=='a1e49f8f8393fef92099013e8088d30c5bee178c3bd9823341e85c619380dd9d','actual_current_build_no_qualification')
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
 for n in ['os','arch','version','entry']:p.add_argument('--'+n,required=True)
 main(p.parse_args())
