"""TEST-only readability for two content-free SAFE JSON receipts, never private traces."""
import json,os,pathlib,stat
P=pathlib.Path;root=P.cwd()/'.task-tools/artifacts'
for name in ('native-report.json','held-driver-supplier.json'):
 p=root/name
 if not p.exists():continue
 assert not any(q.is_symlink()for q in [p,*p.parents])and stat.S_ISREG(p.lstat().st_mode)and 0<p.stat().st_size<=1024*1024
 assert isinstance(json.loads(p.read_text()),dict)
 p.chmod(0o644)
