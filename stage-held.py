"""TEST-only copy exact held driver into ignored artifact area; product untouched."""
import hashlib,pathlib,shutil,stat,os
P=pathlib.Path;source=P(__file__).resolve().with_name('held-native-driver.py');out=P.cwd()/'.task-tools/artifacts/held-native-driver.py'
assert not source.is_symlink()and stat.S_ISREG(source.lstat().st_mode)and 0<source.stat().st_size<=2*1024*1024
raw=source.read_bytes();assert hashlib.sha256(raw).hexdigest()=='46d455cbbc70549971eaf760bd325ed79902b6ea1ab2bc5cd8dcfc79acb20f1c'
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

portable=P(__file__).resolve().with_name('held-portable.py');dest=P.cwd()/'.task-tools/artifacts/held-portable.py'
assert not portable.is_symlink()and stat.S_ISREG(portable.lstat().st_mode)and 0<portable.stat().st_size<=2*1024*1024
raw=portable.read_bytes();assert hashlib.sha256(raw).hexdigest()=='3315bfaade44fa1a328269e36954a7858c7b5631ecc41171ba0ccc9116634cac'
assert not dest.exists()and not any(p.is_symlink()for p in dest.parents)
dest.write_bytes(raw);dest.chmod(0o444)

if os.environ.get('AN_OS') == 'windows':
 helper=P(__file__).resolve().with_name('held-installer-stderr-encrypt.mts');dest=P.cwd()/'.task-tools/artifacts/held-installer-stderr-encrypt.mts'
 assert not helper.is_symlink()and stat.S_ISREG(helper.lstat().st_mode)and 0<helper.stat().st_size<=16384
 raw=helper.read_bytes();assert hashlib.sha256(raw).hexdigest()=='685f1d86232e98a2ad7222f13921eec2bd723990754ed4b610b091e1f0abdf40'
 assert not dest.exists()and not any(p.is_symlink()for p in dest.parents)
 dest.write_bytes(raw);dest.chmod(0o444)
