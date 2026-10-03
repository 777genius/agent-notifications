"""TEST-only HTTP byte transport. Does not modify production HTTPS staging."""
import hashlib,json,os,pathlib,re,urllib.parse,urllib.request
url=urllib.parse.urlsplit(os.environ['TEST_SUPPLEMENT_URL']);expected=os.environ['TEST_SUPPLEMENT_SHA256']
assert url.scheme=='http' and url.hostname and url.port and 1024<url.port<65536 and url.path=='/installed12-supplement.tar.gz' and not any((url.username,url.password,url.query,url.fragment))
assert re.fullmatch('[0-9a-f]{64}',expected)
size=int(os.environ['TEST_SUPPLEMENT_SIZE']);head=os.environ['TEST_PRODUCT_HEAD'];manifest=os.environ['TEST_PARENT_MANIFEST_SHA256']
assert 0<size<=16*1024**2 and re.fullmatch('[0-9a-f]{40}',head) and re.fullmatch('[0-9a-f]{64}',manifest)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*a,**k):raise ValueError('TEST redirect forbidden')
path=pathlib.Path('installed12-supplement.tar.gz');digest=hashlib.sha256();total=0
try:
 with urllib.request.build_opener(NoRedirect).open(url.geturl(),timeout=30)as source,path.open('xb')as out:
  assert source.status==200
  while block:=source.read(1024*1024):
   total+=len(block);assert total<=size;out.write(block);digest.update(block)
 assert total==size and digest.hexdigest()==expected
 path.chmod(0o600)
except BaseException:
 path.unlink(missing_ok=True);raise

print(json.dumps({"status":"actual_TEST_tar_bytes_downloaded","tarSHA256":expected,"tarSize":total,"declaredProductHead":head,"declaredParentManifestSHA256":manifest,"nativeExecuted":False,"qualificationGranted":False},sort_keys=True),flush=True)
