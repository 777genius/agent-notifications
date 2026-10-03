"""CI input fetch only, before native launch; no install or provider."""
import argparse,base64,hashlib,json,pathlib,tarfile,urllib.request,urllib.parse
P=pathlib.Path;HERE=P(__file__).resolve().parent
def need(v,c):
 if not v:raise ValueError(c)
def fetch(url,limit):
 u=urllib.parse.urlsplit(url);need(u.scheme=='https' and u.hostname and not u.username and not u.password,'HTTPS_input_only')
 class NoRedirect(urllib.request.HTTPRedirectHandler):
  def redirect_request(self,*a):raise ValueError('input_redirect_refused')
 with urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect()).open(url,timeout=30) as r:
  data=r.read(limit+1);need(len(data)<=limit,'bounded_public_input');return data
def main(a):
 need(a.root.name.startswith('TEST-') and not a.root.exists(),'fresh_TEST_input');a.root.mkdir(mode=0o700)
 data=fetch(a.capsule_url,2097152);need(hashlib.sha256(data).hexdigest()==a.capsule_sha256,'ROOT_capsule_SHA');(a.root/'capsule.tar').write_bytes(data)
 with tarfile.open(a.root/'capsule.tar') as t:
  ms=t.getmembers();need(len(ms)==2 and {m.name for m in ms}=={'reader.js','build-receipt.json'} and all(m.isfile() and m.size<=1048576 for m in ms),'closed_ROOT_build_capsule')
  for m in ms:(a.root/m.name).write_bytes(t.extractfile(m).read())
 pins=HERE/'official-host-pins.json';need(hashlib.sha256(pins.read_bytes()).hexdigest()=='59368f673e651f00e8a4d0a675365ba15f0752eacabc3e2ea68a2e91cf6b166f','official_source_pins')
 for v in ['1.18.33','2.0.21']:
  p=next(x for x in json.loads(pins.read_text())['cells'] if (x['os'],x['arch'],x['version'])==(a.os,a.arch,v));raw=fetch(p['hostURL'],160*1024*1024)
  need(hashlib.sha256(raw).hexdigest()==p['archiveSHA256'] and base64.b64encode(hashlib.sha512(raw).digest()).decode()==p['archiveSRI'].split('-',1)[1],'official_SHA_SRI_before_parse');(a.root/(v+'.tgz')).write_bytes(raw)
 print(json.dumps({'stageOnly':True,'nativeExecuted':False,'qualificationGranted':False}))
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--root',type=P,required=True)
 for n in ['capsule-url','capsule-sha256','os','arch']:p.add_argument('--'+n,required=True)
 main(p.parse_args())
