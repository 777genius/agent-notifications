"""Root-only observable setup regression; does not execute ptrace or kcmp.

Run against the carrier's linux_contract.py for OLD RED and the reviewed file
for NEW. The outer watchdog bounds the *old* pre-try readline hang. It reports
and contains surviving TEST births after observing the failure. It cannot
qualify any observer/kernel or DBus obligation.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


def load(path):
    spec = importlib.util.spec_from_file_location('TEST_linux_contract', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def birth(pid):
    raw = (Path('/proc')/str(pid)/'stat').read_text()
    tail = raw[raw.rindex(')')+2:].split()
    return int(tail[19]), int(tail[1]), int(tail[2]), int(tail[3]), tail[0]


def descriptors():
    # listdir's own already-closed directory fd is not a leaked descriptor.
    live = set()
    for value in os.listdir('/proc/self/fd'):
        fd = int(value)
        try: os.fstat(fd)
        except OSError as error:
            if error.errno == 9: continue
            raise
        live.add(fd)
    return live


def setup_boundary(case, first, observation, origin):
    """Classify observed failures, never arbitrary exceptions plus good cleanup."""
    if not observation or not observation.get('qualifiedActor') or origin != observation.get('phase'):
        return False
    phase = observation.get('phase')
    if case == 'stall-ready':
        return phase == 'actor-readiness' and first['type'] == 'TimeoutError' and first['message'] == 'fixture readiness deadline'
    if case == 'bad-ready':
        return phase == 'actor-readiness' and first['type'] == 'AssertionError' and first['message'] in ('fixture readiness bytes','') and observation.get('actorMode') == case
    if case == 'ready-eof':
        return phase == 'actor-readiness' and first['type'] == 'AssertionError' and first['message'] in ('fixture readiness EOF','') and observation.get('actorMode') == case
    if case == 'stderr-open':
        return observation.get('ready') and first['type'] == 'IsADirectoryError' and phase == 'stderr-open' and first.get('filename') == observation.get('stderrPath')
    return case == 'spawn-failure' and observation.get('ready') and phase == 'tracer-spawn' and first['type'] == 'FileNotFoundError' and first.get('filename') == observation.get('absentTracer')


def worker(args):
    scratch = args.case_directory
    before = descriptors()
    observation = {'qualifiedActor':False,'phase':'prerequisite','actorMode':None,'ready':False,
                   'absentTracer':str(scratch/'TEST-no-such-executable'),
                   'stderrPath':str(scratch/'fork-None.stderr')}
    allocation = None
    harness = None
    def publish():
        temporary = scratch/'observations-before-outer.publishing'
        temporary.write_text(json.dumps(observation)+'\n')
        os.replace(temporary,scratch/'observations-before-outer.json')
    def profile(frame,event,value):
        nonlocal allocation
        if harness is None: return
        if event == 'return' and frame.f_code is subprocess.Popen.__init__.__code__:
            child = frame.f_locals['self']
            if frame.f_locals.get('args') == [str(args.fixture),observation['actorMode']] and getattr(child,'pid',0) > 0:
                allocation = child
                observation['actualAllocatedPID'] = child.pid
                publish()
        if event == 'c_call' and frame.f_code is harness.run.__code__ and getattr(value,'__name__',None) == 'readline':
            root = frame.f_locals.get('root')
            assert root is allocation and getattr(value,'__self__',None) is root.stdout
            observation['phase'] = 'actor-readiness'
            # Exact OLD executor's real readiness read, observed before blocking.
            end = time.monotonic()+2
            actor_path = scratch/'fixture-allocation.json'
            while not actor_path.exists():
                assert time.monotonic() < end, 'TEST actor allocation receipt deadline'
                time.sleep(.005)
            actor = json.loads(actor_path.read_text())
            assert actor['pid'] == root.pid and actor['mode'] == observation['actorMode'] and actor['prctlReady']
            observation['actor'] = actor
            observation['qualifiedActor'] = True
            publish()
        if event == 'c_return' and frame.f_code is getattr(getattr(harness,'ready_line',None),'__code__',None):
            publish()
    previous_profile = sys.getprofile()
    try:
        harness = load(args.harness)
        binary = scratch/'TEST-no-such-executable'
        assert not binary.exists(), 'intentional tracer absence prerequisite'
        if args.case == 'stderr-open': (scratch/'fork-None.stderr').mkdir()
        mode = args.case if args.case in ('stall-ready','bad-ready','ready-eof') else 'fork'
        observation['actorMode'] = mode
        publish(); sys.setprofile(profile)
        harness.run(binary,args.fixture,scratch,mode=mode)
    except BaseException as error:
        first = getattr(error,'first_failure',error)
        cleanup = getattr(error,'cleanup',None)
        observed = getattr(error,'observation',None)
        if observed is not None:
            observation.update(observed)
            actor_path = scratch/'fixture-allocation.json'
            if actor_path.exists():
                actor = json.loads(actor_path.read_text())
                observation['qualifiedActor'] = (allocation is not None and actor['pid'] == allocation.pid and actor['mode'] == observation['actorMode'] and actor['prctlReady'] and
                    (observation.get('actor') is None or observation['actor']['birth'] == actor['birth']))
                observation['actualActorAllocation'] = actor
        origin = []; origin_boundary = None
        source_lines = args.harness.read_text().splitlines() if harness is not None else []
        tb = first.__traceback__
        while tb is not None:
            frame = tb.tb_frame
            if harness is not None and frame.f_code in (harness.run.__code__,getattr(getattr(harness,'ready_line',None),'__code__',None)):
                origin.append({'function':frame.f_code.co_name,'line':tb.tb_lineno})
                source_line = source_lines[tb.tb_lineno-1].strip()
                if frame.f_code.co_name == 'ready_line' or source_line.startswith('assert root.stdout.readline()'):
                    origin_boundary = 'actor-readiness'
                elif source_line.startswith('err = ') and '.stderr' in source_line and '.open(' in source_line:
                    origin_boundary = 'stderr-open'
                elif source_line.startswith(('tracer = subprocess.Popen(', 'tracer, tracer_record = owner.spawn(')):
                    origin_boundary = 'tracer-spawn'
                if observed is None and frame.f_code is harness.run.__code__:
                    context = frame.f_locals
                    if args.case in ('stderr-open','spawn-failure'):
                        observation['ready'] = True
                        observation['phase'] = 'stderr-open' if args.case == 'stderr-open' else 'tracer-spawn'
            tb = tb.tb_next
        first_record = {'type':type(first).__name__,'message':str(first),'filename':getattr(first,'filename',None),'origin':origin}
        qualified = setup_boundary(args.case,first_record,observation,origin_boundary)
        after = descriptors()
        result = {'expectedFailure':qualified,'firstFailure':first_record,'observation':observation,
                  'cleanupBeforeOuter':cleanup,'cleanup':cleanup,'descriptorDelta':sorted(after-before),
                  'descriptorLoss':sorted(before-after),'boundaryQualified':qualified,
                  'classification':'EXPECTED_SETUP_REFUSAL' if qualified else 'NOT_RUN_PREREQUISITE' if not observation['qualifiedActor'] else 'FAIL_WRONG_BOUNDARY',
                  'pass':qualified and cleanup is not None and cleanup['complete'] and after == before}
        publish()
    else: result = {'pass':False,'classification':'FAIL_MISSING_REFUSAL','observation':observation}
    finally: sys.setprofile(previous_profile)
    print(json.dumps(result),flush=True)
    return 0 if result['pass'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--harness',type=Path,required=True)
    parser.add_argument('--fixture',type=Path,required=True)
    parser.add_argument('--scratch-parent',type=Path,required=True)
    parser.add_argument('--worker',action='store_true')
    parser.add_argument('--case',choices=('stall-ready','bad-ready','ready-eof','stderr-open','spawn-failure'))
    parser.add_argument('--case-directory',type=Path)
    parser.add_argument('--harness-sha256',required=True)
    parser.add_argument('--native-sha256',required=True)
    parser.add_argument('--fixture-sha256',required=True)
    parser.add_argument('--expected',required=True,choices=('OLD_RED','NEW_GREEN'))
    parser.add_argument('--workspace',type=Path,required=True)
    args = parser.parse_args()
    sha = lambda path:hashlib.sha256(path.read_bytes()).hexdigest()
    # A missing original import was SETUP_INPUT_INCOMPLETE in the first root
    # attempt. It must never count as OLD RED. Bind both modules before launch.
    assert sha(args.harness) == args.harness_sha256, 'exact OLD/NEW harness binding'
    assert sha(args.harness.parent/'native_case.py') == args.native_sha256, 'exact original imported module binding'
    assert sha(args.fixture) == args.fixture_sha256, 'exact built TEST fixture binding'
    expected_native = 'acf854926851a96407aafa893ac1b3f0c0a2101495baa34c66ab098e825e6ca0' if args.expected == 'NEW_GREEN' else '07047821776a91e3095eb5d4baed8467eb019ad31a8649fd58d67ca5ce46b6cc'
    assert args.native_sha256 == expected_native, 'accepted exact NEW/OLD original module'
    if args.expected == 'NEW_GREEN':
        assert args.harness_sha256 == sha(Path(__file__).resolve().parents[1]/'linux_contract.py'), 'accepted current NEW executor'
    if args.expected == 'OLD_RED':
        assert args.harness_sha256 == 'ef77db92d6adc58b6dbf18c52ab3085118981ae15a2824fa46add7e7ef467752', 'accepted original OLD source'
    if args.worker: return worker(args)
    workspace = args.workspace.resolve(strict=True)
    parent = args.scratch_parent.resolve(strict=True)
    assert workspace.is_relative_to('/srv/workers') and parent.is_relative_to(workspace), 'workspace scratch only'
    assert parent.stat().st_uid == os.geteuid() and not parent.stat().st_mode & 0o022
    scratch = Path(tempfile.mkdtemp(prefix='TEST-lifecycle-r1364-',dir=parent))
    # The test supervisor adopts and joins OLD leaked setup children. This is
    # TEST containment only, never observer or native qualification.
    assert ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0) == 0, 'TEST subreaper prerequisite'
    cases = []
    for case in ('stall-ready','bad-ready','ready-eof','stderr-open','spawn-failure'):
        directory = scratch/case; directory.mkdir(); (directory/'tmp').mkdir()
        env = {'PATH':'/usr/bin:/bin','TMPDIR':str(directory/'tmp'),'PYTHONDONTWRITEBYTECODE':'1'}
        child = None
        watchdog = False; survivors = []; containment = []; out = err = b''
        outer_error = None; communicate_failure = None; stream_errors = []; stream_receipts = {}
        def persist_streams(disposition):
            for suffix,raw in (('stdout',out),('stderr',err)):
                path = directory/('worker.'+suffix); kept = raw[:16<<20]
                try:
                    path.write_bytes(kept)
                    stream_receipts[suffix] = {'path':str(path),'bytes':len(kept),'sha256':sha(path),
                        'disposition':'PARTIAL_BYTE_CAP' if len(kept)!=len(raw) else disposition}
                    if len(kept)!=len(raw): stream_errors.append(suffix+' byte cap')
                except BaseException as error: stream_errors.append(type(error).__name__)
        try:
            child = subprocess.Popen([sys.executable,'-B',str(Path(__file__).resolve()),
                '--worker','--harness',str(args.harness.resolve()),'--fixture',str(args.fixture.resolve()),
                '--scratch-parent',str(parent),'--case',case,'--case-directory',str(directory),
                '--workspace',str(workspace),'--expected',args.expected,
                '--harness-sha256',args.harness_sha256,'--native-sha256',args.native_sha256,
                '--fixture-sha256',args.fixture_sha256],
                stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=env,cwd=directory,start_new_session=True)
            try:
                out,err = child.communicate(timeout=16)
            except subprocess.TimeoutExpired as error:
                watchdog = True; out,err = error.output or b'',error.stderr or b''
                communicate_failure = type(error).__name__+':'+str(error)[:240]
                persist_streams('TIMEOUT_PARTIAL')
                # Keep the NEW cleanup owner alive past its 12+4 work/join
                # clocks. OLD's intentional pre-try hang needs outer containment.
                if args.expected == 'OLD_RED':
                    child.kill()
                    # Adopted OLD actors can hold the worker pipes open.
                    # Join the worker here; retain TIMEOUT_PARTIAL bytes until
                    # the existing finally contains and joins those actors.
                    child.wait(timeout=2)
                else:
                    out,err = child.communicate()
        except BaseException as error:
            outer_error = type(error).__name__
            if communicate_failure is None: communicate_failure = type(error).__name__+':'+str(error)[:240]
            if isinstance(error,subprocess.TimeoutExpired): out,err = error.output or out,error.stderr or err
        finally:
            # Independent ownership cleanup even after a broken receipt/read.
            if child is not None:
                try:
                    if args.expected == 'OLD_RED':
                        if child.poll() is None: child.kill()
                        child.wait(timeout=2)
                    else:
                        # A returned/erroring communicate is not descendant joins.
                        # Preserve NEW's existing driver finally on this route.
                        if child.returncode is None: out,err = child.communicate()
                        child.wait()
                except BaseException as error: containment.append(type(error).__name__)
                for stream in (child.stdout,child.stderr):
                    try: stream.close()
                    except BaseException as error: containment.append(type(error).__name__)
            # Persist before JSON parsing; communicate retries replace prefixes.
            persist_streams('TIMEOUT_PARTIAL' if watchdog else 'FAILURE_PARTIAL' if outer_error else 'COMMUNICATE_RETURNED')
            pidfile = directory/'fixture.pid'
            try:
                if pidfile.exists():
                    pid = int(pidfile.read_text())
                    try: fresh = birth(pid)
                    except FileNotFoundError: fresh = None
                    if fresh is not None:
                        # Adopted process is freshly our own child; pin all
                        # fixture facts before any signal, never trust PID file alone.
                        assert fresh[1] == os.getpid() and fresh[2] == fresh[3] == pid, 'own adopted TEST child'
                        proc = Path('/proc')/str(pid)
                        mode = case if case in ('stall-ready','bad-ready','ready-eof') else 'fork'
                        survivors.append({'pid':pid,'birth':fresh[0],'state':fresh[4]})
                        if fresh[4] != 'Z':
                            assert os.readlink(proc/'exe') == str(args.fixture.resolve())
                            assert os.readlink(proc/'cwd') == str(directory)
                            assert (proc/'cmdline').read_bytes() == (str(args.fixture.resolve())+'\0'+mode+'\0').encode()
                            assert birth(pid) == fresh, 'fresh TEST birth bookend'
                            os.killpg(pid,signal.SIGKILL)
                        end = time.monotonic()+2
                        while True:
                            joined,status = os.waitpid(pid,os.WNOHANG)
                            if joined == pid:
                                containment.append({'pid':pid,'joined':True,'pidAbsent':not proc.exists()}); break
                            assert time.monotonic() < end, 'adopted TEST child join deadline'
                            time.sleep(.01)
            except BaseException as error: containment.append({'complete':False,'error':type(error).__name__})
        try:
            result = json.loads(out)
            if not isinstance(result,dict): raise ValueError('worker receipt object required')
        except (ValueError,UnicodeDecodeError): result = {'pass':False,'reason':'no valid failure/cleanup receipt'}
        passed = child is not None and child.returncode == 0 and result.get('pass') and not watchdog and not survivors and not outer_error and not containment and not stream_errors
        observed_path = directory/'observations-before-outer.json'
        try:
            observed = json.loads(observed_path.read_text()) if observed_path.exists() else {}
            if not isinstance(observed,dict): raise ValueError('worker observation object required')
        except (OSError,ValueError) as error:
            observed = {}; stream_errors.append('observation:'+type(error).__name__)
        qualified_watchdog = case == 'stall-ready' and watchdog and observed.get('qualifiedActor') and observed.get('phase') == 'actor-readiness' and any(row['pid'] == observed.get('actualAllocatedPID') and row['birth'] == observed.get('actor',{}).get('birth') for row in survivors)
        actual_old_defect = qualified_watchdog or result.get('boundaryQualified') and (bool(result.get('descriptorDelta')) or bool(survivors))
        # Wrong inputs, arbitrary exceptions and absence of a receipt are not
        # the old observable hang/leak. Emergency containment must also join
        # all adopted survivors, independently of the regression verdict.
        outer_resolved = not outer_error and not stream_errors and all(isinstance(row,dict) and row.get('joined') and row.get('pidAbsent') for row in containment)
        passed = passed and not stream_errors
        witnessed = passed if args.expected == 'NEW_GREEN' else not passed and actual_old_defect and outer_resolved
        cases.append({'case':case,'result':'GREEN_SETUP_ONLY' if passed else 'RED_SETUP_ONLY' if actual_old_defect and outer_resolved else result.get('classification','NOT_RUN_OR_FAIL'),
                      'observationsBeforeOuterCleanup':observed,
                      'expected':args.expected,'expectedOutcomeWitnessed':bool(witnessed),
                      'watchdog':watchdog,'watchdogDisposition':'OLD_CONTAINMENT' if watchdog and args.expected=='OLD_RED' else 'NEW_OWNER_PRESERVED' if watchdog else 'NOT_FIRED','survivorsBeforeContainment':survivors,'externalContainment':containment,
                      'receipt':result,'stderrBytes':len(err),'outerError':outer_error,
                      'workerStreams':stream_receipts,'streamErrors':stream_errors,'communicateFirstFailure':communicate_failure,
                      'workerJoined':child is not None and child.returncode is not None,
                      'workerStreamsClosed':child is not None and child.stdout.closed and child.stderr.closed})
    receipt = {'scope':'setup lifecycle only','RuntimeProof':'PENDING','wholeContract':'INCOMPLETE',
               'sourceBindings':{'harnessSHA256':args.harness_sha256,'nativeSHA256':args.native_sha256,
                                 'fixtureSHA256':args.fixture_sha256},
               'cases':cases,'pass':all(case['expectedOutcomeWitnessed'] for case in cases)}
    (scratch/'lifecycle-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
    print(json.dumps(receipt))
    return 0 if receipt['pass'] else 1


if __name__ == '__main__': sys.exit(main())
