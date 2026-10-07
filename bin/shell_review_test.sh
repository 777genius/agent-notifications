#!/bin/bash
TEST_ENV_HANDOFF_GOMODCACHE=1
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/test-env.sh"
test_env_enter "$0" "$@"
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
(cd "$ROOT" && GOWORK=off go build -o "$TMPDIR/config-helper" ./cmd/claude-notifications)
python3 -I - "$ROOT" "$TMPDIR" <<'PY'
import copy, json, os, pathlib, subprocess, sys
root, temp = map(pathlib.Path, sys.argv[1:])
# Containment now lives in the native installer protocol, not bootstrap Python.
# Exercise that production boundary with real canonical paths in this sandbox.
bundle=temp/'bundle'; bundle.mkdir()
stage=bundle/'stage'; stage.mkdir()
safe=temp/'bundle-other'; safe.mkdir()
alias=temp/'alias'; alias.symlink_to(bundle,target_is_directory=True)
registry=temp/'registry.json'; registry.write_text('{"plugins":{}}')
for candidate, refresh, allowed in [
    (safe, bundle, True),
    (bundle, bundle, False),
    (stage, bundle, False),
    (alias/'stage', bundle, False),
    (stage, alias, False),
    (safe, alias/'missing', True),
    (bundle/'..'/'bundle-other', bundle, True),
    (safe/'..'/'bundle'/'stage', bundle, False),
]:
    args=[registry,'test',temp/'claude',refresh,temp/'market',temp/'codex',
          'both',candidate,registry,'']
    run=subprocess.run([str(temp/'config-helper'),'config','installer','bootstrap',
                        *map(str,args)],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    assert (run.returncode==0)==allowed,(candidate,refresh,run.stdout,run.stderr)
    if not allowed:
        assert run.stderr.strip()=='ConfigInvalid',(run.stdout,run.stderr)
print('stage containment: 8 native protocol canonical/alias cases passed')

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
