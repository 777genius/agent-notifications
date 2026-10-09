"""Destructive root-only probe on the ORIGINAL live session/observer pair.

The root's integrated TEST executor supplies its real constructed session and
PassiveStrace. No authority object, method or record is replaced. Run only at
an actual held entry with the original independent credential worker waiting.
This exercises review R1364-3 at its records-lock publication boundary; it is
not whole-case/kernel proof or a complete integration executor.
"""
import os
import time
from pathlib import Path


def diagnostic_failure_at_held_ack(native, trace, session, scratch):
    assert isinstance(trace,native.PassiveStrace)
    assert isinstance(session,native.OwnedNotificationsSession) and trace.session is session
    scratch = Path(scratch).resolve(strict=True)
    assert scratch.is_relative_to(Path(trace.root).resolve()), 'own isolated TEST scratch'
    session.assert_continuous()
    with trace.parser.changed:
        assert trace.custody_keys and any(worker.is_alive() for worker in trace.held_workers), 'actual held credential worker prerequisite'
        waiting = [worker._args[0] for worker in trace.held_workers if worker.is_alive() and worker._args[0][-1] is not None]
        assert len(waiting) == 1, 'one actual original connection worker'
        packet,_,_,tid,held_birth,*rest = waiting[0]
        conn = rest[-1]
        assert not conn['acked'] and packet not in trace.pending_acks, 'probe precedes watched ACK'
        facts = native.trace_task_identity(tid)
        assert facts['birth'] == held_birth and facts['tracer'] == trace.tracer['pid']
    # A real exclusive-open failure in the original diagnostic reader.
    collision = scratch/'TEST-diagnostic-collision'; collision.mkdir()
    fd = writer = pipe = None
    failed = False; cleanup_errors = []
    try:
        fd,writer = os.pipe()
        pipe = os.fdopen(fd,'rb',buffering=0); fd = None
        with session.records_lock:
            assert session.error is None and trace.error is None
            session.diagnostic_pump(pipe,collision)
            pump = session.pumps[-1]
            pump.join(timeout=.1)
            assert session.error is None, 'diagnostic failure published across final ACK validation lock'
            assert pump.is_alive(), 'original failed diagnostic reader must await failure-publication lock'
        pump.join(timeout=1)
        assert not pump.is_alive() and session.error is not None, 'diagnostic failure must publish and join'
    except BaseException:
        failed = True
        raise
    finally:
        for resource in (writer,pipe,fd):
            if resource is not None:
                try:
                    if isinstance(resource,int): os.close(resource)
                    else: resource.close()
                except BaseException as error: cleanup_errors.append(type(error).__name__)
        if cleanup_errors and not failed: raise RuntimeError('TEST reader probe resource cleanup incomplete')
    # Original dispatcher observes the original session failure and refuses.
    end = time.monotonic()+trace.clock.wait(1)
    with trace.parser.changed:
        assert trace.parser.changed.wait_for(lambda:trace.error is not None,max(0,end-time.monotonic())), 'reader failure did not refuse held channel'
        assert not conn['acked'] and packet not in trace.pending_acks, 'watched ACK after diagnostic failure'
        fresh = native.trace_task_identity(tid)
        raw = (Path('/proc')/str(tid)/'stat').read_text()
        assert fresh['birth'] == held_birth and fresh['tracer'] == trace.tracer['pid'] and raw[raw.rindex(')')+2:].split()[0] == 't', 'watched stop resumed on reader refusal'
    return {'scope':'original reader/final ACK failure publication','RuntimeProof':'PENDING',
            'firstFailure':session.error,'readerJoined':not pump.is_alive(),'probeResourcesClosed':not cleanup_errors,
            'watchedConnectionAcknowledged':False,'actualStopRetained':True,'wholeContract':'INCOMPLETE'}
