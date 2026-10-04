#!/usr/bin/env python3
"""SOURCE-only recipe; execute later only in a fresh Windows TEST source checkout."""
import argparse, hashlib, json, os, pathlib, subprocess, sys
P = pathlib.Path
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
p = argparse.ArgumentParser()
p.add_argument('--repo', type=P, required=True)
p.add_argument('--go', type=P, required=True)
p.add_argument('--go-sha256', required=True)
a = p.parse_args()
assert sys.platform == 'win32' and a.repo.is_absolute() and a.go.is_absolute()
r = a.repo.resolve(strict=True)
workspace = P(os.environ['GITHUB_WORKSPACE']).resolve(strict=True) if os.environ.get('GITHUB_ACTIONS') == 'true' else None
assert (r.name.startswith('TEST-') or workspace is not None and r == workspace / 'product') and not any(q.is_symlink() for q in (r, *r.parents, a.go))
env = dict(os.environ, GOPROXY='off', GOSUMDB='off', GOTOOLCHAIN='local', CGO_ENABLED='1')
head = subprocess.check_output(['git', '-C', str(r), 'rev-parse', 'HEAD'], env=env, text=True).strip()
assert head == '07e676131679eff7b9ca0fa64eda631b4969f45d'
assert not subprocess.check_output(['git', '-C', str(r), 'status', '--porcelain', '--untracked-files=no'], env=env)
assert sha(a.go) == a.go_sha256
assert subprocess.check_output([str(a.go), 'version'], env=env, text=True, timeout=10).strip() == 'go version go1.27.1 windows/amd64'
source = P(__file__).with_name('toast_phase_probe_windows_test.go').resolve(strict=True)
assert sha(source) == '03415e5af66b7ed5020cd9224114234586527cacc5e23d6796db9d670d882343'
out = r / '.task-tools/artifacts'
out.mkdir(parents=True, exist_ok=True)
held, virtual = out / 'held-toast-phase-probe_windows_test.go', r / 'internal/notifier/zz_TEST_toast_phase_probe_windows_test.go'
assert not virtual.exists()
with held.open('xb') as f: f.write(source.read_bytes())
overlay = out / 'toast-phase-overlay.json'
with overlay.open('x') as f: json.dump({'Replace': {str(virtual): str(held)}}, f)
binary = out / 'windows-toast-phase-probe.exe'
assert not binary.exists()
argv = [str(a.go), 'test', '-mod=readonly', '-overlay='+str(overlay), '-c', '-o', str(binary), './internal/notifier']
result = subprocess.run(argv, cwd=r, env=env, capture_output=True, timeout=180)
if result.returncode != 0:
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(result.stderr)
    sys.stderr.buffer.flush()
assert result.returncode == 0 and sha(a.go) == a.go_sha256 and sha(held) == sha(source)
assert not subprocess.check_output(['git', '-C', str(r), 'status', '--porcelain', '--untracked-files=no'], env=env)
record = {'schema': 1, 'productCommit': head, 'probeSourceSHA256': sha(source), 'binarySHA256': sha(binary), 'goToolSHA256': sha(a.go), 'overlaySHA256': sha(overlay), 'compileExitCode': 0, 'compileStdoutSHA256': hashlib.sha256(result.stdout).hexdigest(), 'compileStderrSHA256': hashlib.sha256(result.stderr).hexdigest()}
with (out/'windows-toast-phase-probe-build.json').open('x') as f: json.dump(record, f, sort_keys=True)
print(json.dumps({'status':'TEST_probe_compiled_only','binarySHA256':sha(binary),'qualificationGranted':False}))
