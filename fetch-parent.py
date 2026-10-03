"""TEST-only HTTP byte transport. Does not modify production HTTPS staging."""
import hashlib,os,pathlib,re,urllib.parse,urllib.request
url=urllib.parse.urlsplit(os.environ['TEST_PARENT_URL']);expected=os.environ['TEST_PARENT_SHA256']
assert url.scheme=='http' and url.hostname and url.port and 1024<url.port<65536 and url.path=='/parent-evidence.tar.gz' and not any((url.username,url.password,url.query,url.fragment))
assert re.fullmatch('[0-9a-f]{64}',expected)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*a,**k):raise ValueError('TEST redirect forbidden')
path=pathlib.Path('parent-evidence.tar.gz');digest=hashlib.sha256();total=0
try:
 with urllib.request.build_opener(NoRedirect).open(url.geturl(),timeout=30)as source,path.open('xb')as out:
  assert source.status==200
  while block:=source.read(1024*1024):
   total+=len(block);assert total<=2*1024**3;out.write(block);digest.update(block)
 assert total>0 and digest.hexdigest()==expected
 path.chmod(0o600)
except BaseException:
 path.unlink(missing_ok=True);raise
