#!/usr/bin/env python3
"""BUILD_ONLY fixture prerequisites; no installation, test execution or tracing.

Supply an existing exact Go1.25.8 executable. A missing PATH entry does not
identify the prerequisite: the isolated helper module needs Go1.25.8, godbus
v5.2.2 and its x/sys v0.27.0 dependency, rather than the product module graph.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time

HERE = Path(__file__).resolve().parent
PINS = {'native_case.py':'6711958e74161cc2bbc0c71404694dedcddb5cdf8dbe486cfb70ef1a919af240',
        'src/strace.c':'656ccbcd614055b777a11d69bc55e35a0256426f421ea4f7c58026e95fa029e5',
        'src/defs.h':'9914057470fbec3428b051c4a307fe48e7c19207192a936b88c5b14b8a624eae',
        'src/syscall.c':'390ff1bc410226b4c843ee046ba8e576879e0edc2b3b3087c79afb4ceb506803'}
OLD_PINS = dict(PINS,**{'native_case.py':'07047821776a91e3095eb5d4baed8467eb019ad31a8649fd58d67ca5ce46b6cc',
                      'src/strace.c':'7953842ccb83b6419172217943b0f1c40ce4b10f3514a093ea9feb333f1da452'})

def digest(path): return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ('go','compiler','derived','scratch-parent','workspace'): p.add_argument('--'+name,type=Path,required=True)
    p.add_argument('--derived-sha256',required=True)
    p.add_argument('--comparison',choices=('NEW','OLD'),default='NEW')
    p.add_argument('--original-source-root',type=Path,default=HERE.parent)
    args = p.parse_args()
    parent,workspace = args.scratch_parent.resolve(strict=True),args.workspace.resolve(strict=True)
    assert workspace.is_relative_to('/srv/workers') and parent.is_relative_to(workspace)
    assert parent.stat().st_uid == os.geteuid() and not parent.stat().st_mode & 0o022
    scratch = Path(tempfile.mkdtemp(prefix='TEST-r1366-build-',dir=parent))
    for name in ('tmp','cache','module-cache','gopath','module'): (scratch/name).mkdir()
    env = {'PATH':'/usr/bin:/bin','LANG':'C','TMPDIR':str(scratch/'tmp'),'GOTMPDIR':str(scratch/'tmp'),
           'GOCACHE':str(scratch/'cache'),'GOMODCACHE':str(scratch/'module-cache'),'GOPATH':str(scratch/'gopath'),
           'GOTOOLCHAIN':'local','GOENV':'off','GOPROXY':'https://proxy.golang.org','GOSUMDB':'sum.golang.org',
           'CGO_ENABLED':'0','GOOS':'linux','GOARCH':'amd64'}
    children = []; commands = []; first = None; outputs = {}
    stream_receipts = []
    def run(argv, label, cwd=scratch):
        commands.append(argv)
        child = None; out = err = b''; failure = None
        row = {'label':label,'disposition':'START_FAILED','cleanupErrors':[],'groupCancellation':'NOT_NEEDED',
               'descendantJoins':'UNKNOWN'}
        try:
            if 'CURSOR_PROOF_PREP_END' in os.environ:
                assert float(os.environ['CURSOR_PROOF_PREP_END'])-time.monotonic() >= 124, 'build work and cancellation/join reserve before launch'
            child = subprocess.Popen(argv,cwd=cwd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
            children.append(child)  # custody precedes communicate/qualification.
            out,err = child.communicate(timeout=120)
            row['disposition'] = 'RETURNED'
            assert child.returncode == 0, label+' build prerequisite failed'
        except BaseException as error:
            failure = error; row['disposition'] = type(error).__name__
            if isinstance(error,subprocess.TimeoutExpired): out,err = error.output or b'',error.stderr or b''
            if child is not None:
                try:
                    if child.returncode is None:
                        raw = (Path('/proc')/str(child.pid)/'stat').read_text(); tail = raw[raw.rindex(')')+2:].split()
                        assert int(tail[1]) == os.getpid() and int(tail[2]) == int(tail[3]) == child.pid, 'owned build group'
                        os.killpg(child.pid,signal.SIGKILL); row['groupCancellation'] = 'SIGKILL_SENT'
                    # communicate retry returns the complete accumulated streams,
                    # including the earlier prefix: replace, never concatenate.
                    out,err = child.communicate(timeout=2)
                except BaseException as cleanup_error:
                    if isinstance(cleanup_error,subprocess.TimeoutExpired):
                        out,err = cleanup_error.output or out,cleanup_error.stderr or err
                    row['cleanupErrors'].append(type(cleanup_error).__name__)
        finally:
            if child is not None:
                try: child.wait(timeout=2)
                except BaseException as error: row['cleanupErrors'].append(type(error).__name__)
                for stream in (child.stdout,child.stderr):
                    try: stream.close()
                    except BaseException as error: row['cleanupErrors'].append(type(error).__name__)
            row['joined'] = child is not None and child.returncode is not None
            row['streamsClosed'] = child is not None and child.stdout.closed and child.stderr.closed
            for suffix,raw in (('.stdout',out),('.stderr',err)):
                kept = raw[:16<<20]; path = scratch/(label+suffix)
                try:
                    path.write_bytes(kept)
                    row[suffix[1:]] = {'path':str(path),'bytes':len(kept),'sha256':hashlib.sha256(kept).hexdigest(),
                                      'disposition':'PARTIAL_BYTE_CAP' if len(kept)!=len(raw) else 'ACTUAL_COMMUNICATE_BYTES'}
                    if len(kept)!=len(raw) and failure is None: failure = AssertionError(label+' stream retention cap')
                except BaseException as error:
                    row['cleanupErrors'].append(type(error).__name__)
                    if failure is None: failure = error
            row['firstFailure'] = None if failure is None else type(failure).__name__+':'+str(failure)[:240]
            stream_receipts.append(row)
        if failure is not None: raise failure
        assert row['joined'] and row['streamsClosed'] and not row['cleanupErrors'], label+' cleanup incomplete'
        outputs[label] = out.decode('utf-8','strict')
        return outputs[label]
    receipt = {'qualification':'BUILD_ONLY','RuntimeProof':'PENDING','kernelOutcome':'NOT_RUN',
               'nativeEligibility':'HOLD','scratch':str(scratch),'pass':False}
    try:
        pins = OLD_PINS if args.comparison == 'OLD' else PINS
        for name,pin in pins.items(): assert digest(args.original_source_root/name) == pin
        go = args.go.resolve(strict=True); compiler = args.compiler.resolve(strict=True)
        derived = args.derived.resolve(strict=True)
        assert digest(derived) == args.derived_sha256
        version = run([str(go),'version'],'go-version').strip()
        assert version == 'go version go1.25.8 linux/amd64', 'exact Go1.25.8 linux/amd64 prerequisite'
        run([str(compiler),'--version'],'compiler-version')
        source = scratch/'module'
        for name,target in (('matrix_helper.go','main.go'),('matrix-go.mod','go.mod'),('matrix-go.sum','go.sum')):
            shutil.copyfile(HERE/name,source/target)
        actor,helper = scratch/'TEST-matrix-actor',scratch/'TEST-matrix-helper'
        held = scratch/'TEST-held-child'
        run([str(compiler),'-O2','-g0','-Wall','-Wextra','-Werror','-pthread','-o',str(actor),str(HERE/'matrix_actor.c')],'actor-build')
        run([str(compiler),'-O2','-g0','-Wall','-Wextra','-Werror','-pthread','-o',str(held),str(HERE/'held_child.c')],'held-child-build')
        run([str(go),'build','-trimpath','-buildvcs=false','-mod=readonly','-o',str(helper),'.'],'helper-build',source)
        buildinfo = run([str(go),'version','-m',str(helper)],'helper-build-info')
        assert 'go1.25.8' in buildinfo and 'github.com/godbus/dbus/v5\tv5.2.2' in buildinfo
        receipt.update({'pass':True,'sources':pins,'comparison':args.comparison,'derivedSHA256':args.derived_sha256,
            'helperSHA256':digest(helper),'actorSHA256':digest(actor),'actor':str(actor),'helper':str(helper),
            'heldChild':str(held),'heldChildSHA256':digest(held),
            'fixtureSources':{path.name:digest(path) for path in sorted(HERE.iterdir()) if path.is_file()},
            'linuxContractSHA256':digest(HERE.parent/'linux_contract.py'),
            'goVersion':'go1.25.8','godbusVersion':'v5.2.2','goExecutableSHA256':digest(go),
            'compilerSHA256':digest(compiler),'helperBuildInfo':buildinfo,
            'helperBuildInfoContains':['go1.25.8','github.com/godbus/dbus/v5 v5.2.2'],'commands':commands})
    except BaseException as error:
        first = error; receipt['firstFailure'] = type(error).__name__+':'+str(error)[:240]
    finally:
        receipt['commands'] = commands
        receipt['buildStreams'] = stream_receipts
        receipt['allBuildChildrenJoined'] = all(c.returncode is not None for c in children)
        receipt['allBuildStreamsClosed'] = all(c.stdout.closed and c.stderr.closed for c in children)
        receipt['pass'] = receipt['pass'] and receipt['allBuildChildrenJoined'] and receipt['allBuildStreamsClosed']
        try: (scratch/'fixture-build-bindings.json').write_text(json.dumps(receipt,indent=2)+'\n')
        except BaseException as error:
            receipt['retentionError'] = type(error).__name__
            receipt['pass'] = False
            if first is None: first = error
    print(json.dumps(receipt))
    return 0 if first is None and receipt['pass'] else 1

if __name__ == '__main__': raise SystemExit(main())
