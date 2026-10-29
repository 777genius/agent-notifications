#!/usr/bin/env python3
"""Read-only natural notification readiness for an admitted fresh Windows host."""
import argparse
import ctypes
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import subprocess
import time

MAX_WAIT_SECONDS = 65 * 60
SAMPLE_SECONDS = 15


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def native_state():
    if os.name != "nt":
        raise RuntimeError("actual_windows_notification_readiness_required")
    shell = ctypes.WinDLL("shell32", use_last_error=True)
    shell.SHQueryUserNotificationState.argtypes = (ctypes.POINTER(ctypes.c_int),)
    shell.SHQueryUserNotificationState.restype = ctypes.c_long
    state = ctypes.c_int(-1)
    result = shell.SHQueryUserNotificationState(ctypes.byref(state))
    return {"hresult": result, "state": state.value}


class ExecutionRequest:
    """Keep only this bounded test wait awake; restore this thread's old state."""
    def __init__(self):
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        self.kernel.SetThreadExecutionState.argtypes = (ctypes.c_uint32,)
        self.kernel.SetThreadExecutionState.restype = ctypes.c_uint32
        self.kernel.GetCurrentThreadId.argtypes = ()
        self.kernel.GetCurrentThreadId.restype = ctypes.c_uint32
        self.previous, self.thread = None, None

    def acquire(self, proof):
        self.thread = self.kernel.GetCurrentThreadId()
        self.previous = self.kernel.SetThreadExecutionState(0x80000003)  # continuous, system, display
        error = ctypes.get_last_error() if not self.previous else 0
        proof["execution_request"] = {"flags": 0x80000003, "thread_id": self.thread,
                                      "previous_state": self.previous, "acquired": bool(self.previous),
                                      "released": False, "acquire_error": error}
        if not self.previous:
            raise RuntimeError("notification_readiness_execution_request_failed")

    def release(self, proof):
        if self.kernel.GetCurrentThreadId() != self.thread:
            raise RuntimeError("notification_readiness_execution_thread_changed")
        returned = self.kernel.SetThreadExecutionState(self.previous)
        error = ctypes.get_last_error() if not returned else 0
        proof["execution_request"].update(released=bool(returned), release_returned_state=returned, release_error=error)
        if not returned:
            raise RuntimeError("notification_readiness_execution_restore_failed")


def wait_with_execution_request(proof, measure, request, **kwargs):
    acquired, primary = False, None
    try:
        request.acquire(proof)
        acquired = True
        wait_for_readiness(proof, measure, **kwargs)
    except Exception as error:
        primary = error
    finally:
        if acquired:
            try:
                request.release(proof)
            except Exception as error:
                proof["execution_cleanup_failure"] = str(error)
                if primary is None:
                    primary = error
        if primary is not None:
            proof.update(status="failed", failure=str(primary))
    if primary is not None:
        raise primary


def wait_for_readiness(proof, measure, *, seconds=MAX_WAIT_SECONDS, clock=time.monotonic,
                       sleep=time.sleep, emit=print):
    if type(seconds) not in (int, float) or not math.isfinite(seconds) or not 0 <= seconds <= MAX_WAIT_SECONDS:
        raise ValueError("notification_readiness_budget_invalid")
    started = clock()
    proof.update(status="waiting", started_at=utc_now(), max_wait_seconds=seconds,
                 sample_interval_seconds=SAMPLE_SECONDS, samples=[], sample_count=0)
    next_progress = 0
    try:
        # A separate finite bound still refuses if the clock unexpectedly stalls.
        for index in range(int(seconds // SAMPLE_SECONDS) + 2):
            observed = measure()
            elapsed = clock() - started
            if not math.isfinite(elapsed) or elapsed < 0:
                raise RuntimeError("notification_readiness_clock_invalid")
            if not isinstance(observed, dict) or set(observed) != {"hresult", "state"} or any(type(value) is not int for value in observed.values()):
                raise RuntimeError("notification_readiness_observation_invalid")
            proof.update(last_hresult=observed["hresult"], last_state=observed["state"], sample_count=index + 1)
            proof["samples"].append({**observed, "elapsed_seconds": round(elapsed, 3)})
            if elapsed > seconds:
                raise RuntimeError("notification_readiness_timeout")
            if observed["hresult"] != 0:
                raise RuntimeError("notification_readiness_hresult_refused")
            if observed["state"] == 5:  # QUNS_ACCEPTS_NOTIFICATIONS; this is not banner proof.
                proof["status"] = "ready"
                return
            if observed["state"] != 6:  # Only documented first-logon quiet time is pending.
                raise RuntimeError("notification_readiness_state_refused")
            if elapsed >= seconds:
                raise RuntimeError("notification_readiness_timeout")
            if elapsed >= next_progress:
                emit(json.dumps({"status": "waiting_for_native_notification_readiness", **observed,
                                 "elapsed_seconds": round(elapsed, 3), "remaining_seconds": round(seconds - elapsed, 3)}), flush=True)
                next_progress = elapsed + 60
            sleep(min(SAMPLE_SECONDS, seconds - elapsed))
        raise RuntimeError("notification_readiness_sample_bound")
    except Exception as error:
        proof.update(status="failed", failure=str(error))
        raise
    finally:
        proof.update(finished_at=utc_now(), elapsed_seconds=round(clock() - started, 3))


def main(report):
    started = time.monotonic()
    proof = {"schema_version": 1, "scope": "fresh_host_native_notification_readiness_only",
             "native_api": "SHQueryUserNotificationState", "product_executed": False, "desktop_qualified": False,
             "started_at": utc_now(), "sample_count": 0, "last_hresult": None, "last_state": None,
             "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    try:
        source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=Path(__file__).resolve().parents[1], text=True).strip()
        if os.name != "nt" or source != os.environ.get("SOURCE_SHA"):
            raise RuntimeError("exact_native_windows_readiness_source_required")
        proof["source_sha"] = source
        wait_with_execution_request(proof, native_state, ExecutionRequest())
    except Exception as error:
        proof.update(status="failed", failure=str(error))
    proof.setdefault("finished_at", utc_now())
    proof.setdefault("elapsed_seconds", round(time.monotonic() - started, 3))
    report.parent.mkdir(parents=True, exist_ok=True)
    report.write_text(json.dumps(proof, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({key: proof.get(key) for key in ("status", "failure", "elapsed_seconds", "sample_count", "last_hresult", "last_state")}), flush=True)
    return 0 if proof["status"] == "ready" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, required=True)
    raise SystemExit(main(parser.parse_args().report))
