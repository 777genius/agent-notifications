#!/bin/bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
python3 -I - "$ROOT" "$TMPDIR" <<'PY'
import copy, json, ntpath, os, pathlib, subprocess, sys, types
root, temp = map(pathlib.Path, sys.argv[1:])
# Execute the production containment predicate with a lexical Windows adapter.
source=(root/'bin/bootstrap.sh').read_text()
predicate=source.split('def within(base, root):',1)[1].split('\nif any(within',1)[0]
scope={'os':types.SimpleNamespace(path=ntpath)}
exec('def within(base, root):'+predicate,scope)
within=scope['within']
for base, parent, expected in [
    (r'C:\Temp',r'D:\bundle',False),
    (r'\\server\one\Temp',r'\\server\two\bundle',False),
    (r'\\one\share\Temp',r'\\two\share\bundle',False),
    (r'C:\BUNDLE\temp',r'c:\bundle',True),
    (r'C:\bundle',r'c:\BUNDLE',True),
    (r'C:\bundle-other',r'C:\bundle',False),
    (r'C:\bundle\..\safe',r'C:\bundle',False),
    (r'\\server\share\BUNDLE\temp',r'\\SERVER\SHARE\bundle',True),
]:
    assert within(base,parent)==expected,(base,parent)
scope={'os':os}; exec('def within(base, root):'+predicate,scope)
bundle=temp/'bundle'; bundle.mkdir()
alias=temp/'alias'; alias.symlink_to(bundle,target_is_directory=True)
assert scope['within'](str(alias/'new'),str(bundle))
assert not scope['within'](str(temp/'safe'),str(bundle))
print('stage containment: 8 Windows lexical cases + 2 POSIX canonical cases passed')

# Full debug script with a closed command PATH: all host/desktop probes are fake.
cwd=temp/'hostile'; cwd.mkdir()
canary=temp/'executed'
(cwd/'json.py').write_text('open('+repr(str(canary))+',"w").write("executed")\n')
fake=temp/'fake'; fake.mkdir()
(fake/'python3').symlink_to(sys.executable)
for name in ['uname','date','hostname','ps','claude','xdotool','wmctrl','xprop',
             'gdbus','busctl','wlrctl','kdotool','remotinator','notify-send','cat']:
    p=fake/name
    p.write_text('#!/bin/bash\nprintf "%s\\n" '+('Linux' if name=='uname' else 'fake-probe')+'\n')
    p.chmod(0o755)
helper=fake/'helper'
helper.write_text('#!/bin/bash\nprintf "%s" "$FIXTURE_JSON"\nprintf "%s" "SECRET_CANARY" >&2\nexit 1\n')
helper.chmod(0o755)
env=dict(os.environ,PATH=str(fake),PYTHONOPTIMIZE='1',PYTHONPATH=str(cwd),
         CLAUDE_NOTIFICATIONS_BIN=str(helper))
valid=dict(selection=dict(path='/fixture/config.json',source='universal',exists=False,
                          diagnostics=[dict(code='ConfigMissing',path='/fixture/config.json')]),
           valid=True,revision='a'*64,schemaVersion=1,
           settings=dict(desktopEnabled=True,desktopSound=False,volume=0.5,
                         statuses=dict(task_complete=dict(enabled=None,desktopEnabled=True,webhookEnabled=False))))
cases=[(valid,True)]
for mutate in [
    lambda x:x.update(secret='SECRET_CANARY'),
    lambda x:x['selection'].update(secret='SECRET_CANARY'),
    lambda x:x['selection'].update(exists='SECRET_CANARY'),
    lambda x:x['selection'].update(diagnostics=[dict(code='SECRET_CANARY')]),
    lambda x:x.update(revision='SECRET_CANARY'),
    lambda x:x.update(valid='SECRET_CANARY'),
    lambda x:x['settings'].update(volume=True),
    lambda x:x['settings'].update(desktopSound='SECRET_CANARY'),
    lambda x:x['settings']['statuses'].update(SECRET_CANARY={}),
    lambda x:x['settings']['statuses']['task_complete'].update(enabled='SECRET_CANARY'),
]:
    value=copy.deepcopy(valid); mutate(value); cases.append((value,False))
cases.extend([('{"SECRET_CANARY":',False),('null',False),
              ('{"valid":true,"valid":false,"secret":"SECRET_CANARY"}',False)])
for value, safe in cases:
    env['FIXTURE_JSON']=value if isinstance(value,str) else json.dumps(value)
    run=subprocess.run(['/bin/bash',str(root/'scripts/linux-focus-debug.sh'),'--stdout'],
                       cwd=cwd,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    assert run.returncode==0,run.stderr
    assert 'SECRET_CANARY' not in run.stdout+run.stderr
    assert not canary.exists()
    assert ('"volume": 0.5' in run.stdout)==safe
    assert ('Safe config inspect unavailable.' in run.stdout)!=safe
print('debug report: 14 hostile cwd/optimized Python projection cases passed; no execution or secret leakage')
PY
