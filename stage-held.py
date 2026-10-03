"""TEST-only copy exact held driver into ignored artifact area; product untouched."""
import hashlib,pathlib,shutil,stat
P=pathlib.Path;source=P(__file__).resolve().with_name('held-native-driver.py');out=P.cwd()/'.task-tools/artifacts/held-native-driver.py'
assert not source.is_symlink()and stat.S_ISREG(source.lstat().st_mode)and 0<source.stat().st_size<=2*1024*1024
raw=source.read_bytes();assert hashlib.sha256(raw).hexdigest()=='92eaac38ca6186a0e411292d71afb295fc9d00be4c45826da38d2ada5f64e2d3'
assert not out.exists()and not any(p.is_symlink()for p in out.parents)
out.parent.mkdir(parents=True,exist_ok=True);out.write_bytes(raw);out.chmod(0o444)

permission=P(__file__).resolve().with_name('held-portable_permission.py');dest=P.cwd()/'.task-tools/artifacts/held-portable_permission.py'
assert not permission.is_symlink()and stat.S_ISREG(permission.lstat().st_mode)and 0<permission.stat().st_size<=2*1024*1024
raw=permission.read_bytes();assert hashlib.sha256(raw).hexdigest()=='f499590b9c8c3bfee37156633bf8a5a36024be208b9c26addfd70cbd000874cd'
assert not dest.exists()and not any(p.is_symlink()for p in dest.parents)
dest.write_bytes(raw);dest.chmod(0o444)

provider=P(__file__).resolve().with_name('held-provider.py');dest=P.cwd()/'.task-tools/artifacts/held-provider.py'
assert not provider.is_symlink()and stat.S_ISREG(provider.lstat().st_mode)and 0<provider.stat().st_size<=2*1024*1024
raw=provider.read_bytes();assert hashlib.sha256(raw).hexdigest()=='57633468d4187d44b65abd245ad29d49a380619fe1e61049a1612272cad80e49'
assert not dest.exists()and not any(p.is_symlink()for p in dest.parents)
dest.write_bytes(raw);dest.chmod(0o444)
