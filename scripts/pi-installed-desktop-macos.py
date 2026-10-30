#!/usr/bin/env python3
"""Actual common-install Pi TUI -> macOS banner/audio/click -> exact Terminal.

Only the loopback model and webhook destination are fixtures. Product source,
authority, native helper, sound and click callback use the normal owned runtime.
Missing GUI/permission/audio observations fail; this lane never grants the final
three-platform desktop qualification itself.
"""
import argparse
import array
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import plistlib
import re
import select
import shlex
import shutil
import signal
import stat
import subprocess
import tempfile
import threading
import time
import wave
import zipfile

REPO = Path(__file__).resolve().parents[1]
CASES = ("completion", "question", "sound-off", "click-off", "desktop-off", "stale-origin")


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


F = module("pi_native_fixture", REPO / "tests/integration/pi_native_fixture.py")
P = module("pi_installed_fixture", REPO / "tests/integration/pi_installed_fixture.py")
D = module("pi_installed_process", REPO / "scripts/pi-installed-process-e2e.py")
H = module("pi_macos_helper", REPO / "scripts/pi-macos-helper.py")


def wait_for(predicate, reason, seconds=12):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError(reason)


def partial_case_failure(case_root, provider, sink, sink_start, expected, observe, app, origin_pid, *, observations=None):
    """Project failed-case observations to fixed counts before owned cleanup."""
    rows = sink.rows[sink_start[0]:sink_start[0] + 128]
    result = {"desktop_qualified": False, "provider_request_count": len(provider.requests),
              "sink_row_count": len(sink.rows) - sink_start[0], "sink_rows_sampled": len(rows),
              "sink_failure_count": len(sink.failures) - sink_start[1],
              "sink_expected_status_count": sum(row.get("status") == expected for row in rows),
              "sink_request_id_present_count": sum(bool(row.get("request_id")) for row in rows)}
    try:
        path = case_root / "witness.jsonl"
        if observations is not None:
            witness = observations.get("witness")
            P.require(isinstance(witness, list) and len(witness) <= 64
                      and all(isinstance(row, dict) for row in witness), "partial_witness_observation_unavailable")
            witness_present = True
        elif path.exists():
            with path.open("rb") as source:
                body = source.read(65537)
            P.require(len(body) <= 65536, "partial_witness_byte_limit")
            lines = body.splitlines()
            P.require(len(lines) <= 128, "partial_witness_row_limit")
            witness = [json.loads(line) for line in lines]
            P.require(all(isinstance(row, dict) for row in witness), "partial_witness_shape")
            witness_present = True
        else:
            witness = []
            witness_present = False
        kinds = ("factory", "session_start", "agent_start", "agent_end", "agent_before_settle",
                 "agent_settled", "ui_prompt_start", "ui_prompt_end", "session_shutdown")
        result["witness"] = {
            "file_present": witness_present, "row_count": len(witness),
            "event_counts": {kind: sum(row.get("type") == kind for row in witness) for kind in kinds},
            "unknown_event_count": sum(row.get("type") not in kinds for row in witness),
            "factory_matches_origin": origin_pid is not None and any(
                row.get("type") == "factory" and row.get("pid") == origin_pid for row in witness),
            "tui_count": sum(row.get("mode") == "tui" for row in witness),
            "true_counts": {key: sum(row.get(key) is True for row in witness)
                            for key in ("hasUI", "idle", "aborted")},
            "boundary_outcome_counts": {outcome: sum(row.get("type") == "agent_before_settle"
                and row.get("outcome") == outcome for row in witness) for outcome in ("completed", "aborted", "error")}}
    except Exception as error:
        result["witness_error_class"] = type(error).__name__
    try:
        # The actual failure path reuses the original shared six-second
        # observation. It never adds another file read or native helper probe.
        helper_snapshot = observations.get("installed_helper_processes") if observations is not None \
            else observe("helper-processes", str(app), timeout=3)
        P.require(isinstance(helper_snapshot, dict) and isinstance(helper_snapshot.get("installed_helpers"), list),
                  "partial_native_helper_observation_unavailable")
        helpers = helper_snapshot["installed_helpers"]
        result["native_helpers"] = {"count": len(helpers),
            "active_count": sum(row.get("active") is True for row in helpers),
            "finished_launching_count": sum(row.get("finished_launching") is True for row in helpers),
            "kernel_birth_observed_count": sum(bool(row.get("process_start_identity")) for row in helpers)}
    except Exception as error:
        result["native_helpers_error_class"] = type(error).__name__
    return result


def wait_for_consent(request, observe, seconds=130):
    """Observe the exact candidate dialog or the actual request's early exit."""
    # Observe the one normal request through its helper's 120 s authorization
    # budget (Go allows 125 s inside a 130 s setup lease). This is an observation
    # cap, not a guarantee about the CLI's total lifetime before native setup.
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        dialogs = observe()
        if dialogs or request.poll() is not None:
            return dialogs
        time.sleep(0.05)
    raise RuntimeError("actual_candidate_notification_consent_dialog_unavailable")


def permission_receipt(stdout, stderr, exit_code):
    """Retain bounded setup diagnostics; exit zero alone never proves a grant.

    These are the installed product's fixed setup-notifications JSON envelope
    and diagnostics, not Pi/provider output or notification contents.
    """
    bounded = len(stdout) + len(stderr) <= 65536
    receipt = {"exit_code": exit_code, "stdout_bytes": len(stdout), "stderr_bytes": len(stderr),
               "output_bounded": bounded, "stdout": stdout[:32768].decode("utf-8", errors="replace"),
               "stderr": stderr[:32768].decode("utf-8", errors="replace")}
    if bounded:
        try:
            value = json.loads(stdout)
        except (ValueError, UnicodeError):
            value = None
        if isinstance(value, dict):
            permission = value.get("permission")
            if permission in ("allowed", "denied", "undetermined", "unavailable"):
                receipt["permission"] = permission
                receipt["matching_reason"] = value.get("reason") == "permission_" + permission
    return receipt


def observed_permission(receipt):
    P.require(receipt["exit_code"] == 0 and receipt["stderr_bytes"] == 0 and receipt["output_bounded"]
              and receipt.get("matching_reason") is True
              and receipt.get("permission") in ("allowed", "denied", "undetermined"),
              "normal_notification_consent_request_failed")
    return receipt["permission"]


def verify_permission_grant(request_receipt, fresh_receipt):
    requested, fresh = observed_permission(request_receipt), observed_permission(fresh_receipt)
    P.require(requested == "allowed" and fresh == "allowed",
              "candidate_signed_helper_notification_permission_" + (requested if requested != "allowed" else fresh))
    return fresh


def permission_ui_identity(archive, app, pin):
    """Bind OS display aliases to the pinned archive and exact installed plist."""
    P.require(H.sha(archive) == pin["archive_sha256"], "permission_ui_archive_identity_mismatch")
    member = "ClaudeNotifier.app/Contents/Info.plist"
    with zipfile.ZipFile(archive) as packaged:
        P.require(0 < packaged.getinfo(member).file_size <= 65536, "permission_ui_metadata_limit")
        body = packaged.read(member)
    installed = app / "Contents/Info.plist"
    P.require(installed.is_file() and not installed.is_symlink() and installed.stat().st_size <= 65536
              and installed.read_bytes() == body, "permission_ui_installed_metadata_mismatch")
    metadata = plistlib.loads(body)
    P.require(metadata.get("CFBundleIdentifier") == H.BUNDLE
              and metadata.get("CFBundleExecutable") == "terminal-notifier-modern", "permission_ui_bundle_identity_mismatch")
    names = list(dict.fromkeys([metadata[key] for key in ("CFBundleDisplayName", "CFBundleName") if key in metadata]
                              + [Path(member.split("/", 1)[0]).stem]))
    P.require(all(isinstance(name, str) and 0 < len(name) <= 80 and not any(ord(c) < 32 for c in name)
                  for name in names), "permission_ui_display_identity_invalid")
    return {"bundle_id": metadata["CFBundleIdentifier"], "display_names": names,
            "archive_member": member, "installed_info_plist_sha256": P.sha(installed)}


def consent_request_identity(request, observe, candidate):
    """Capture this one public CLI child; an early exit never needs a click."""
    if request.poll() is not None:
        return {"status": "original_request_completed_before_identity_observation"}
    try:
        identity = observe("terminal-process-identity", str(request.pid), timeout=3)
        P.require(identity.get("status") == "observed" and identity.get("pid") == request.pid
                  and identity.get("parent_pid") == os.getpid()
                  and identity.get("executable_path") == str(candidate.resolve())
                  and identity.get("process_start_identity"), "original_consent_cli_identity_unverified")
        return identity
    except Exception as error:
        return {"status": "inconclusive_original_request_identity", "error_class": type(error).__name__}


def consent_menu_context(receipt):
    """Carry only the popup newly created by the original Options click."""
    step = receipt.get("permission_card_options", {})
    menu = step.get("opened_menu_window", {})
    card = receipt.get("permission_card_identity", {})
    bounds, options = menu.get("bounds", []), menu.get("options_bounds", [])
    P.require(step.get("physical_options_click") is True and step.get("original_request_verified") is True
              and step.get("popup_windows_before_options") == []
              and menu.get("new_after_original_options_click") is True
              and menu.get("positive_alpha_verified") is True
              and type(menu.get("window_id")) is int and menu["window_id"] > 0
              and menu.get("owner_pid") == card.get("os_dialog_pid")
              and isinstance(bounds, list) and isinstance(options, list) and len(bounds) == len(options) == 4
              and all(type(x) in (int, float) and math.isfinite(x) for x in bounds + options)
              and 0 < bounds[2] <= 400 and 0 < bounds[3] <= 400 and options[2] > 0 and options[3] > 0
              and step.get("action_target_bounds") == options,
              "original_options_menu_window_unverified")
    after = step.get("popup_windows_after_options", [])
    P.require(len(after) == 1 and all(after[0].get(key) == menu.get(key) for key in ("window_id", "owner_pid", "bounds", "layer")),
              "original_options_menu_transition_unverified")
    return {"menuWindowID": menu["window_id"], "menuBounds": bounds, "optionsBounds": options}


def allow_consent(request, observe, app, candidate, receipt, deadline, now=time.monotonic,
                  operation="allow-notification-consent"):
    """One physical phase, bound to the same original actors/card/deadline."""
    remaining = deadline - now()
    P.require(request.poll() is None and remaining > 0, "original_consent_request_no_longer_pending")
    identity = receipt.get("request_process_identity", {})
    helper = receipt.get("request_helper_identity", {})
    P.require(identity.get("status") == "observed" and identity.get("pid") == request.pid
              and identity.get("parent_pid") == os.getpid()
              and identity.get("executable_path") == str(candidate.resolve())
              and identity.get("process_start_identity")
              and type(helper.get("pid")) is int and helper["pid"] > 0
              and helper.get("finished_launching") is True and helper.get("process_start_identity"),
              "original_consent_actors_unverified")
    card = receipt.get("permission_card_identity", {})
    bounds = card.get("bounds", [])
    P.require(type(card.get("os_dialog_pid")) is int and card["os_dialog_pid"] > 0
              and card.get("os_owner_start_identity") and card.get("candidate_name")
              and isinstance(bounds, list) and len(bounds) == 4
              and all(type(value) in (int, float) and math.isfinite(value) for value in bounds)
              and bounds[2] > 0 and bounds[3] > 0, "original_consent_card_unverified")
    remaining = deadline - now()
    P.require(request.poll() is None and remaining > 0, "original_consent_request_no_longer_pending")
    context = {"app": str(app), "helperPID": helper["pid"], "helperStart": helper["process_start_identity"],
               "requestPID": request.pid, "requestStart": identity["process_start_identity"],
               "requestExecutable": str(candidate.resolve()), "deadlineMonotonic": deadline, "clock": "mach_absolute_time",
               "ownerPID": card["os_dialog_pid"], "ownerStart": card["os_owner_start_identity"],
               "candidateName": card["candidate_name"], "cardBounds": bounds}
    if operation == "observe-notification-consent-menu" or (operation == "allow-notification-consent" and "permission_card_options" in receipt):
        context.update(consent_menu_context(receipt))
    click = observe(operation, json.dumps(context, sort_keys=True), timeout=min(5, remaining))
    flag = {"hover-notification-consent": "physical_pointer_hover",
            "open-notification-consent-options": "physical_options_click",
            "allow-notification-consent": "physical_allow_click", "observe-notification-consent-menu": None}[operation]
    receipt["physical_consent_flow" if operation == "allow-notification-consent" else operation] = click
    P.require((flag is None or (click.get(flag) is True and click.get("pointer_at_target") is True))
              and click.get("original_request_verified") is True
              and click.get("installed_helper_pid") == helper["pid"]
              and click.get("installed_helper_start_identity") == helper["process_start_identity"]
              and click.get("original_request_pid") == request.pid
              and click.get("original_request_start_identity") == identity["process_start_identity"]
              and (operation != "allow-notification-consent" or click.get("denial_button_observed") is True),
              "original_candidate_consent_physical_click_unverified")
    if "menuWindowID" in context and (operation == "allow-notification-consent" or click.get("dialogs")):
        observed = click.get("observed_menu", {})
        P.require(observed.get("window_id") == context["menuWindowID"] and observed.get("bounds") == context["menuBounds"]
                  and observed.get("options_bounds") == context["optionsBounds"] and observed.get("denial_button_observed") is True
                  and (operation != "allow-notification-consent" or click.get("topmost_allow_item_verified") is True),
                  "original_options_menu_identity_or_topmost_item_unverified")
    return click


def consent_observer(request, observe, app, receipt, pending_capture=None, now=time.monotonic, ui_capture=None,
                     *, candidate=None, deadline=None):
    """One activation/hover/Options phase on the original public request only."""
    attempted, captured, hovered, opened_options = False, False, False, False
    started = now()

    def observed():
        nonlocal attempted, captured, hovered, opened_options
        if opened_options:
            current = allow_consent(request, observe, app, candidate, receipt, deadline, now,
                                    operation="observe-notification-consent-menu")
        else:
            current = observe("notification-consent")
        dialogs, cards = current["dialogs"], current.get("permission_cards", [])
        if "request_helper_identity" not in receipt and request.poll() is None:
            state = observe("helper-processes", str(app))
            receipt["helper_observation"] = state
            helpers = state.get("installed_helpers", [])
            if len(helpers) == 1 and helpers[0].get("finished_launching") is True \
                    and helpers[0].get("process_start_identity") and request.poll() is None:
                receipt["request_helper_identity"] = dict(helpers[0])
        if len(cards) == 1 and "permission_card_identity" not in receipt:
            receipt["permission_card_identity"] = dict(cards[0])
        if not dialogs and not hovered and len(cards) == 1 and request.poll() is None:
            P.require(candidate is not None and deadline is not None, "original_consent_actors_unverified")
            hovered = True
            receipt["permission_card_hover"] = allow_consent(request, observe, app, candidate, receipt, deadline, now,
                                                            operation="hover-notification-consent")
            if ui_capture is not None:
                receipt["permission_card_hover_capture"] = ui_capture("hover")
        elif not dialogs and hovered and not opened_options and len(cards) == 1 \
                and cards[0].get("options_bounds") and request.poll() is None:
            P.require(candidate is not None and deadline is not None, "original_consent_actors_unverified")
            opened_options = True
            receipt["permission_card_options"] = allow_consent(request, observe, app, candidate, receipt, deadline, now,
                                                              operation="open-notification-consent-options")
            if ui_capture is not None:
                receipt["permission_card_options_capture"] = ui_capture("options")
        helper = receipt.get("request_helper_identity")
        if not dialogs and not cards and not attempted and helper is not None and request.poll() is None:
            attempted = True
            receipt["helper_activation"] = observe("activate-permission-helper", str(app), str(helper["pid"]),
                                                   helper["process_start_identity"])
        if not dialogs and not captured and pending_capture is not None and now() - started >= 5 \
                and request.poll() is None:
            captured = True
            try:
                receipt["pending_capture"] = pending_capture()
            except Exception as error:
                receipt["pending_capture"] = {"status": "inconclusive_pending_capture_failed",
                                               "error_class": type(error).__name__, "desktop_qualified": False}
        return dialogs

    return observed


class PermissionLog:
    """One finite, read-only permission-only OS log stream with an owned child."""
    def __init__(self, evidence_root, *, argv=None, seconds=20, limit=1024 * 1024):
        self.path = evidence_root / "permission-services.ndjson"
        P.require(not self.path.exists() and not self.path.is_symlink(), "permission_log_path_exists")
        self.stop = threading.Event()
        self.receipt = {"scope": "permission_only_service_logs", "desktop_qualified": False,
                        "maximum_seconds": seconds, "output_limit_bytes": limit}
        predicate = '(process == "terminal-notifier-modern") OR (process == "usernoted") OR (process == "NotificationCenter")'
        self.child = subprocess.Popen(argv or ["/usr/bin/log", "stream", "--style", "ndjson", "--level", "debug",
                                               "--predicate", predicate], stdin=subprocess.DEVNULL,
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)

        def collect():
            started, count = time.monotonic(), 0
            try:
                with self.path.open("xb") as output:
                    while not self.stop.is_set() and time.monotonic() - started < seconds and count < limit:
                        ready, _, _ = select.select([self.child.stdout], [], [], 0.05)
                        if not ready:
                            if self.child.poll() is not None:
                                break
                            continue
                        data = os.read(self.child.stdout.fileno(), min(4096, limit - count))
                        if not data:
                            break
                        output.write(data)
                        count += len(data)
                self.receipt.update(bytes=count, output_limit_reached=count == limit)
            except Exception as error:
                self.receipt["inconclusive_error_class"] = type(error).__name__
            finally:
                if self.child.poll() is None:
                    try:
                        os.killpg(self.child.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                self.child.stdout.close()
                try:
                    self.child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(self.child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    self.child.wait(timeout=2)
                self.receipt.update(exit_code=self.child.returncode,
                                    elapsed_seconds=round(time.monotonic() - started, 3))

        self.thread = threading.Thread(target=collect)
        self.thread.start()

    def close(self):
        self.stop.set()
        self.thread.join(timeout=6)
        if self.thread.is_alive():
            try:
                os.killpg(self.child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            self.thread.join(timeout=2)
        self.receipt["owned_child_collected"] = not self.thread.is_alive() and self.child.poll() is not None
        if self.path.is_file() and not self.path.is_symlink():
            self.receipt["sha256"] = P.sha(self.path)
        return self.receipt


def permission_services():
    """Fixed process/service fields only; never change launchd or OS defaults."""
    result = {"scope": "readonly_notification_services", "desktop_qualified": False}
    names = {"terminal-notifier-modern", "usernoted", "usernotificationsd", "NotificationCenter"}
    try:
        rows = command(["/bin/ps", "-axo", "pid=,ppid=,uid=,comm="], timeout=3)
        result["processes"] = []
        for line in rows.splitlines():
            values = line.split(None, 3)
            if len(values) == 4 and Path(values[3]).name in names and all(value.isdecimal() for value in values[:3]):
                result["processes"].append(dict(zip(("pid", "ppid", "uid", "comm"), values)))
        domain = "gui/" + str(os.getuid())
        for label in ("com.apple.notificationcenterui.agent", "com.apple.usernoted", "com.apple.usernotificationsd"):
            state = subprocess.run(["/bin/launchctl", "print", domain + "/" + label], stdin=subprocess.DEVNULL,
                                   capture_output=True, timeout=3)
            text = state.stdout[:65536].decode(errors="replace")
            fields = [line.strip() for line in text.splitlines()
                      if line.strip().startswith(("state = ", "pid = ", "runs = ", "last exit code = "))]
            result[label] = {"exit_code": state.returncode, "fields": fields[:8],
                             "output_bounded": len(state.stdout) + len(state.stderr) <= 65536}
        disabled = subprocess.run(["/bin/launchctl", "print-disabled", domain], stdin=subprocess.DEVNULL,
                                  capture_output=True, timeout=3)
        result["disabled_entries"] = [line.strip() for line in disabled.stdout[:65536].decode(errors="replace").splitlines()
                                      if any('"' + label + '"' in line for label in
                                             ("com.apple.notificationcenterui.agent", "com.apple.usernoted", "com.apple.usernotificationsd"))]
        result["disabled_query_exit"] = disabled.returncode
    except Exception as error:
        result["inconclusive_error_class"] = type(error).__name__
    return result


def capture_pending_consent(request, observe, app, evidence_root, consent=None):
    """Read the original live request's UI once; observations never grant consent."""
    result = {"scope": "pending_original_notification_consent", "desktop_qualified": False,
              "original_request_running_before": request.poll() is None,
              "maximum_command_seconds": 21}
    if not result["original_request_running_before"]:
        return {**result, "original_request_running_after": False, "status": "inconclusive_original_request_completed"}
    try:
        result["installed_helper_processes"] = observe("helper-processes", str(app), timeout=3)
        if request.poll() is not None:
            result["status"] = "inconclusive_original_request_completed"
            return result
        screenshot = evidence_root.resolve(strict=True) / "pending-notification-consent.png"
        P.require(not screenshot.exists() and not screenshot.is_symlink(), "pending_consent_capture_path_exists")
        command(["/usr/sbin/screencapture", "-x", screenshot], timeout=3)
        P.require(screenshot.is_file() and not screenshot.is_symlink()
                  and 0 < screenshot.stat().st_size <= 16 * 1024 * 1024, "pending_consent_capture_unavailable")
        result["screenshot_sha256"] = P.sha(screenshot)
        result["native_diagnostics"] = observe("permission-diagnostics", str(app), timeout=4)
        if consent is not None and request.poll() is None:
            result["helper_sample"] = sample_permission_helper(observe, app, consent, evidence_root)
        result["status"] = "captured"
    except Exception as error:
        result.update(status="inconclusive_pending_capture_failed", error_class=type(error).__name__)
    finally:
        result["original_request_running_after"] = request.poll() is None
        if not result["original_request_running_after"]:
            result["status"] = "inconclusive_original_request_completed"
    return result


def sample_permission_helper(observe, app, receipt, evidence_root):
    """Sample only the original activated helper; diagnostics never prove consent."""
    original = receipt.get("helper_activation", {}).get("before", {})
    pid, start = original.get("pid"), original.get("process_start_identity")
    result = {"status": "inconclusive_original_identity_unavailable", "desktop_qualified": False,
              "duration_seconds": 1, "sampling_interval_ms": 10, "timeout_seconds": 5,
              "output_limit_bytes": 262144}
    if not isinstance(pid, int) or isinstance(pid, bool) or pid <= 0 or not isinstance(start, str) or not start:
        return result

    def same_helper():
        helpers = observe("helper-processes", str(app), timeout=3).get("installed_helpers", [])
        return len(helpers) == 1 and helpers[0].get("pid") == pid \
            and helpers[0].get("process_start_identity") == start

    if not same_helper():
        result["status"] = "inconclusive_original_identity_changed"
        return result
    # A fixed basename in the owned evidence directory is the only sample path.
    sample = evidence_root.resolve(strict=True) / "permission-helper.sample.txt"
    P.require(not sample.exists() and not sample.is_symlink(), "permission_sample_path_already_exists")
    result.update(pid=pid, process_start_identity=start, identity_verified_before=True)
    try:
        try:
            child = subprocess.run(["/usr/bin/sample", str(pid), "1", "10", "-file", str(sample)],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                   timeout=5)
            result.update(exit_code=child.returncode, status="inconclusive_sample_output_unavailable"
                          if child.returncode == 0 else "inconclusive_sample_command_failed")
        except subprocess.TimeoutExpired:
            result["status"] = "inconclusive_sample_timeout"
        except OSError:
            result["status"] = "inconclusive_sample_unavailable"
        result["identity_verified_after"] = same_helper()
        if not result["identity_verified_after"]:
            result["status"] = "inconclusive_original_identity_changed"
        elif result.get("exit_code") == 0 and sample.is_file() and not sample.is_symlink():
            result["output_bytes"] = sample.stat().st_size
            if 0 < result["output_bytes"] <= result["output_limit_bytes"]:
                result.update(status="sample_collected", artifact=sample.name, sha256=P.sha(sample))
            else:
                result["status"] = "inconclusive_sample_output_limit"
    finally:
        # Keep no output if either ownership check or the output bound failed.
        if result.get("status") != "sample_collected":
            sample.unlink(missing_ok=True)
    return result


def observe_unchanged_focus(terminal, stop_capture, seconds=15):
    def observed():
        current = terminal.front()
        return {"window": current["window"], "tty": current["tty"]}
    return P.observe_unchanged_focus(observed, {"window": terminal.window, "tty": terminal.tty},
                                     stop_capture, drain_seconds=3, seconds=seconds)


def command(argv, *, timeout=20):
    result = subprocess.run(list(map(str, argv)), stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    P.require(len(result.stdout) + len(result.stderr) < 1024 * 1024, "native_observer_output_limit")
    P.require(result.returncode == 0, "native_command_failed:" + Path(argv[0]).name + ":"
              + result.stderr.decode(errors="replace")[-1000:] + result.stdout.decode(errors="replace")[-1000:])
    return result.stdout.decode()


def applescript(source):
    return command(["/usr/bin/osascript", "-e", source]).strip()


def terminal_automation_admission(evidence_root, source):
    """Continue only the already admitted disposable owner host's UI request."""
    path = evidence_root / "host-preparation.json"
    P.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 131072,
              "official_terminal_automation_host_receipt_required")
    host = json.loads(path.read_text())
    admission = host.get("admission", {})
    clock = time.get_clock_info("monotonic")
    P.require(os.environ.get("GITHUB_REPOSITORY") == "777genius/agent-notifications"
              and os.environ.get("GITHUB_ACTOR") == os.environ.get("GITHUB_TRIGGERING_ACTOR") == "777genius"
              and os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and os.environ.get("GITHUB_RUN_ATTEMPT") == "1"
              and os.environ.get("SOURCE_SHA") == source
              and host.get("scope") == "explicit_disposable_official_macos_host_preparation"
              and host.get("status") == "notification_center_ready"
              and admission.get("source_sha") == source
              and admission.get("image_os") == os.environ.get("ImageOS") == "macos15"
              and admission.get("image_version") == os.environ.get("ImageVersion")
              and admission.get("host", {}).get("os") == platform.system() == "Darwin"
              and admission.get("host", {}).get("arch") == platform.machine() == "arm64"
              and admission.get("host", {}).get("version") == platform.mac_ver()[0]
              and platform.mac_ver()[0].startswith("15."), "official_owner_terminal_automation_host_required")
    P.require(clock.implementation == "mach_absolute_time()" and clock.monotonic and not clock.adjustable,
              "shared_terminal_automation_clock_unavailable")
    return {"scope": "original_public_terminal_automation_only", "source_sha": source,
            "host_preparation_sha256": P.sha(path), "requester": "hosted-compute-agent", "target": "com.apple.Terminal"}


def terminal_applescript(source, observe, receipt, admit, *, now=time.monotonic, spawn=subprocess.Popen):
    """Collect one original do-script through its normal Automation consent."""
    started = now()
    deadline = started + 20
    child = spawn(["/usr/bin/osascript", "-e", source], stdin=subprocess.DEVNULL,
                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    receipt.update(original_pid=child.pid, original_deadline_monotonic=deadline, physical_allow_click=False)
    actors, clicked, primary = None, False, None
    try:
        while child.poll() is None:
            remaining = deadline - now()
            P.require(remaining > 0, "original_terminal_applescript_deadline_expired")
            if actors is None:
                try:
                    actors = observe("terminal-automation-actors", str(child.pid), str(os.getpid()), timeout=min(3, remaining))
                    receipt["actors"] = actors
                except Exception:
                    if child.poll() is None:
                        raise
                    break
            if actors.get("admitted") is True and not clicked and child.poll() is None:
                remaining = deadline - now()
                P.require(remaining > 0, "original_terminal_applescript_deadline_expired")
                context = {key: value for key, value in actors.items() if key != "chain"}
                context.update(deadlineMonotonic=deadline, clock="mach_absolute_time")
                try:
                    observed = observe("terminal-automation-consent", json.dumps(context, sort_keys=True), timeout=min(3, remaining))
                except Exception as error:
                    if child.poll() is None:
                        raise
                    # A fast, already-authorized do-script can exit while the
                    # read-only observer revalidates its kernel identity. Only
                    # its own stdout can establish the returned window/TTY.
                    receipt["completed_request_observation_error"] = type(error).__name__ + ":" + str(error)[:1024]
                    break
                if child.poll() is not None:
                    receipt["completed_during_consent_observation"] = True
                    break
                dialogs = observed.get("dialogs", [])
                if dialogs:
                    P.require(len(dialogs) == 1 and child.poll() is None and now() < deadline,
                              "unique_original_terminal_automation_dialog_required")
                    receipt["host_admission"] = admit()
                    context.update(officialHostAdmitted=True, dialog=dialogs[0])
                    P.require(child.poll() is None and now() < deadline, "original_terminal_request_ended_before_allow")
                    clicked = True
                    result = observe("allow-terminal-automation", json.dumps(context, sort_keys=True), timeout=min(3, deadline - now()))
                    receipt["allow_result"] = result
                    P.require(result.get("physical_allow_click") is True and result.get("topmost_allow_item_verified") is True
                              and result.get("original_request_verified") is True and result.get("denial_button_observed") is True
                              and result.get("requestPID") == child.pid and result.get("requestStart") == actors.get("requestStart")
                              and result.get("requesterPID") == actors.get("requesterPID")
                              and result.get("requesterStart") == actors.get("requesterStart"),
                              "original_terminal_automation_allow_unverified")
                    receipt["physical_allow_click"] = True
            time.sleep(0.02)
        remaining = deadline - now()
        P.require(remaining > 0, "original_terminal_applescript_deadline_expired")
        out, err = child.communicate(timeout=remaining)
        receipt["stdout_collected"] = True
        P.require(now() < deadline, "original_terminal_applescript_deadline_expired")
        receipt.update(exit_code=child.returncode, stdout_bytes=len(out), stderr_bytes=len(err),
                       elapsed_seconds=round(now() - started, 3))
        P.require(len(out) + len(err) < 1024 * 1024, "native_terminal_launch_output_limit")
        P.require(child.returncode == 0 and not err, "original_terminal_applescript_failed")
        return out.decode().strip()
    except BaseException as error:
        primary = error
        receipt.update(failure_class=type(error).__name__, failure=str(error)[:4096])
        raise
    finally:
        cleanup = []

        def signal_original(sig):
            try:
                os.killpg(child.pid, sig)
            except ProcessLookupError:
                pass
            except Exception as error:
                cleanup.append(type(error).__name__ + ":" + str(error)[:256])

        try:
            if child.poll() is None:
                signal_original(signal.SIGTERM)
            if not receipt.get("stdout_collected"):
                try:
                    child.communicate(timeout=1)
                except subprocess.TimeoutExpired:
                    signal_original(signal.SIGKILL)
                    child.communicate(timeout=1)
            receipt["owned_child_collected"] = child.poll() is not None
        except Exception as error:
            cleanup.append(type(error).__name__ + ":" + str(error)[:256])
            try:
                receipt["owned_child_collected"] = child.poll() is not None
            except Exception:
                receipt["owned_child_collected"] = False
        if cleanup:
            receipt["cleanup_failures"] = cleanup
            if primary is None:
                raise RuntimeError("owned_terminal_launch_cleanup_failed")


def quoted(value):
    # AppleScript string syntax. Shell arguments inside it are independently
    # escaped by shlex.quote before this language boundary.
    return '"' + str(value).replace("\\", "\\\\").replace('"', '\\"') + '"'


def terminal_launcher(root, argv, env, cwd):
    """Write the exact Terminal child script, preserving the native input TTY.

    Noninteractive Bash otherwise redirects an asynchronous command's stdin to
    /dev/null. Preserve the actual inherited terminal before the backgrounded
    child so PID/wait ownership does not silently turn real Pi into non-TUI.
    """
    script = root / "launch.command"
    entries = [shlex.quote(key + "=" + value) for key, value in env.items()]
    launch = " ".join(["/usr/bin/env", "-i", *entries,
                       'TERM_PROGRAM=Apple_Terminal', 'TERM=xterm-256color',
                       'TERM_SESSION_ID="${TERM_SESSION_ID:-}"', *map(shlex.quote, map(str, argv))])
    script.write_text("#!/bin/bash\nset -eu\nprintf '%s\\n' \"$$\" > " + shlex.quote(str(root / "shell.pid"))
                      + "\ncd " + shlex.quote(str(cwd)) + "\nexec 9<&0\nset +e\n" + launch
                      + " <&9 &\nchild=$!\nexec 9<&-\nprintf '%s\\n' \"$child\" > " + shlex.quote(str(root / "process.pid"))
                      + "\nwait \"$child\"\ncode=$?\nprintf '%s\\n' \"$code\" > "
                      + shlex.quote(str(root / "process.exit")) + "\nexit 0\n")
    script.chmod(0o700)
    return script


class Terminal:
    """An owned real Terminal window and its observed native TTY/Pi PID."""
    def __init__(self, root, argv, env, observer, cwd=None, *, owned, launch=None):
        self.root, self.observer, self.closed = root, observer, False
        self.window = self.tty = self.pid = self.shell_pid = self.process_start_identity = None
        self.window_closed = False
        self.executable = str(Path(argv[0]).resolve(strict=True))
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.pid_path, self.exit_path = root / "process.pid", root / "process.exit"
        self.shell_path = root / "shell.pid"
        cwd = cwd or root / "project"
        script = terminal_launcher(root, argv, env, cwd)
        # Every exception after the first launch side effect must leave a
        # reachable cleanup owner, even before window/child discovery finishes.
        owned.append(self)
        result = (launch or applescript)('tell application id "com.apple.Terminal"\nset t to do script '
                             + quoted("/bin/bash --noprofile --norc " + shlex.quote(str(script)))
                             + '\nset custom title of t to ' + quoted(cwd.name)
                             + '\nset w to front window\nreturn (id of w as text) & "|" & (tty of t as text)\nend tell')
        window, self.tty = result.split("|", 1)
        P.require(window.isdecimal() and self.tty.startswith("/dev/ttys"), "native_terminal_identity_unavailable")
        self.window = int(window)
        wait_for(self.pid_path.exists, "native_pi_pid_missing")
        self.pid = int(self.pid_path.read_text())
        self.shell_pid = int(self.shell_path.read_text())
        P.require(self.pid > 0 and self.shell_pid > 0, "owned_terminal_pid_invalid")
        identity = observer("terminal-process-identity", str(self.pid))
        P.require(identity.get("status") == "observed" and identity.get("pid") == self.pid
                  and identity.get("parent_pid") == self.shell_pid
                  and identity.get("executable_path") == self.executable
                  and identity.get("process_start_identity"), "owned_terminal_process_identity_unverified")
        self.process_start_identity = identity["process_start_identity"]

    def focus(self):
        applescript('tell application id "com.apple.Terminal"\nactivate\nset index of window id '
                    + str(self.window) + ' to 1\nend tell')
        wait_for(lambda: self.is_front(), "owned_terminal_could_not_be_foregrounded")

    def front(self):
        native = self.observer("foreground")
        if native.get("bundle_id") != "com.apple.Terminal":
            return {"native": native, "window": None, "tty": None}
        value = applescript('tell application id "com.apple.Terminal"\nreturn (id of front window as text) & "|" & '
                            '(tty of selected tab of front window as text)\nend tell')
        window, tty = value.split("|", 1)
        return {"native": native, "window": int(window), "tty": tty}

    def is_front(self):
        value = self.front()
        return value["window"] == self.window and value["tty"] == self.tty

    def type(self, text):
        P.require(self.is_front(), "physical_input_target_is_not_owned_terminal")
        self.observer("type", text)

    def collected(self):
        if self.closed:
            return True
        if not self.process_start_identity or not self.exit_path.exists():
            return False
        state = self.observer("terminal-process-identity", str(self.pid), self.process_start_identity)
        P.require(state.get("status") in ("observed", "absent", "replaced"), "owned_terminal_collection_unverified")
        return state["status"] in ("absent", "replaced")

    def close(self):
        if self.closed:
            return
        try:
            P.require(self.process_start_identity, "partial_terminal_cleanup_unverified")
            if not self.collected():
                self.observer("signal-owned-terminal-process", str(self.pid), self.process_start_identity,
                              self.executable, "TERM")
                try:
                    wait_for(self.collected, "owned_terminal_process_not_collected", seconds=3)
                except RuntimeError as error:
                    if str(error) != "owned_terminal_process_not_collected":
                        raise
                    self.observer("signal-owned-terminal-process", str(self.pid), self.process_start_identity,
                                  self.executable, "KILL")
                    wait_for(self.collected, "owned_terminal_process_not_reaped", seconds=3)
        finally:
            # Never discover ownership from a different foreground window on
            # failure. Close only the exact window/TTY actually returned at launch.
            P.require(self.window is not None and self.tty is not None, "partial_terminal_cleanup_unverified")
            if not self.window_closed:
                result = applescript('tell application id "com.apple.Terminal"\nif exists window id ' + str(self.window)
                            + ' then\nif (tty of selected tab of window id ' + str(self.window) + ') is not '
                            + quoted(self.tty) + ' then error "owned_terminal_window_identity_changed"\n'
                            + 'close window id ' + str(self.window) + ' saving no\nreturn "closed"\n'
                            + 'else\nreturn "absent"\nend if\nend tell')
                P.require(result in ("closed", "absent"), "owned_terminal_window_cleanup_unverified")
                self.window_closed = True
        self.closed = True


def samples(path):
    raw = path.read_bytes()
    values = array.array("h", raw[:len(raw) // 2 * 2])
    if __import__("sys").byteorder != "little":
        values.byteswap()
    return values


def envelope_match(recording, reference):
    """Compare the observed cue's 10ms energy contour with its actual asset.

    This checks configured cue identity in addition to recorded energy. The
    reference is decoded only, never played. Centered cosine tolerates the
    capture's native gain and OS resampling while rejecting a flat ambient tone.
    """
    with wave.open(str(reference)) as source:
        P.require(source.getframerate() == 48000 and source.getnchannels() == 1 and source.getsampwidth() == 2,
                  "configured_cue_reference_format")
        ref = array.array("h", source.readframes(source.getnframes()))
    data = samples(recording)
    def bins(values):
        return [math.sqrt(sum(x * x for x in values[i:i + 480]) / 480)
                for i in range(0, len(values) - 479, 480)]
    target, actual = bins(ref), bins(data)
    while target and target[-1] < 8:
        target.pop()
    while target and target[0] < 8:
        target.pop(0)
    P.require(len(target) >= 3 and len(actual) >= len(target), "configured_cue_recording_too_short")
    mean = sum(target) / len(target)
    target = [x - mean for x in target]
    target_norm = math.sqrt(sum(x * x for x in target))
    P.require(target_norm > 0, "configured_cue_reference_has_no_contour")
    best = 0
    for offset in range(len(actual) - len(target) + 1):
        segment = actual[offset:offset + len(target)]
        mean = sum(segment) / len(segment)
        segment = [x - mean for x in segment]
        norm = math.sqrt(sum(x * x for x in segment))
        if norm > 0:
            best = max(best, sum(a * b for a, b in zip(segment, target)) / (norm * target_norm))
    return round(best, 6)


def global_settings_projection(value):
    required = {"scope", "diagnostic_only", "qualification_input", "desktop_qualified", "maximum_seconds", "owner_pid",
                "owner_start_identity", "owner_uid", "owner_bundle", "owner_path", "apple_signature_verified", "settings_left_open",
                "settings_present_before", "opened_new_settings_instance", "initial_tree", "notifications_navigation_performed",
                "observed_notifications_navigation_rows", "sidebar_scroll_performed", "observed_notifications_sidebar_count", "elapsed_seconds"}
    P.require(isinstance(value, dict) and required <= set(value) <= required | {"final_tree", "sidebar_reveal_tree"}
              and value["scope"] == "readonly_global_notification_settings" and value["diagnostic_only"] is True
              and value["qualification_input"] is False and value["desktop_qualified"] is False and value["maximum_seconds"] == 6
              and value["owner_bundle"] == "com.apple.systempreferences"
              and value["owner_path"] == "/System/Applications/System Settings.app"
              and value["apple_signature_verified"] is True and value["settings_left_open"] is True
              and type(value["owner_pid"]) is int and value["owner_pid"] > 0 and type(value["owner_uid"]) is int and value["owner_uid"] == os.getuid()
              and isinstance(value["owner_start_identity"], str) and re.fullmatch(r"[1-9][0-9]{0,19}:[0-9]{1,6}", value["owner_start_identity"])
              and isinstance(value["settings_present_before"], list) and len(value["settings_present_before"]) <= 1
              and all(type(pid) is int and pid > 0 for pid in value["settings_present_before"])
              and type(value["opened_new_settings_instance"]) is bool
              and value["opened_new_settings_instance"] == (value["owner_pid"] not in value["settings_present_before"])
              and type(value["notifications_navigation_performed"]) is bool
              and type(value["sidebar_scroll_performed"]) is bool
              and type(value["observed_notifications_sidebar_count"]) is int and 0 <= value["observed_notifications_sidebar_count"] <= 256
              and (not value["sidebar_scroll_performed"] or value["observed_notifications_sidebar_count"] == 1)
              and type(value["observed_notifications_navigation_rows"]) is int and 0 <= value["observed_notifications_navigation_rows"] <= 256
              and (not value["notifications_navigation_performed"] or value["observed_notifications_navigation_rows"] == 1)
              and type(value["elapsed_seconds"]) in (int, float) and 0 <= value["elapsed_seconds"] <= 6,
              "closed_global_settings_owner_receipt_required")
    labels = {"notifications", "notification_center", "application_notifications", "allow_notifications", "notifications_off",
              "banners", "alerts", "none", "sound", "focus", "do_not_disturb", "always", "when_unlocked", "never",
              "mirroring_or_sharing", "display_sleeping", "screen_locked", "play_sound", "alert_style", "notifications_silenced"}
    roles = {"AXApplication", "AXWindow", "AXGroup", "AXSplitGroup", "AXScrollArea", "AXOutline", "AXList", "AXRow", "AXStaticText",
             "AXButton", "AXCheckBox", "AXPopUpButton", "AXRadioButton", "AXRadioGroup", "AXToolbar", "AXScrollBar", "AXSlider",
             "AXTextField", "AXTable", "AXColumn", "AXCell", "AXImage", "AXHeading", "other"}
    for name in ("initial_tree", "sidebar_reveal_tree", "final_tree"):
        if name not in value:
            continue
        tree = value[name]
        reasons = {"messaging_timeout", "deadline", "node_or_depth_limit", "children_unavailable", "children_limit", "window_limit", "windows_unavailable"}
        P.require(isinstance(tree, dict) and set(tree) == {"nodes", "traversal_capped", "maximum_nodes", "maximum_depth", "maximum_children", "cap_reasons", "ax_timeout_status", "tree_scope"}
                  and tree["maximum_nodes"] == 256 and tree["maximum_depth"] == 18 and tree["maximum_children"] == 64
                  and tree["tree_scope"] in ("app_windows", "retained_sidebar")
                  and type(tree["traversal_capped"]) is bool and type(tree["ax_timeout_status"]) is int
                  and isinstance(tree["cap_reasons"], list) and len(tree["cap_reasons"]) <= len(reasons)
                  and all(reason in reasons for reason in tree["cap_reasons"])
                  and tree["traversal_capped"] == bool(tree["cap_reasons"])
                  and isinstance(tree["nodes"], list) and len(tree["nodes"]) <= 256, "closed_global_settings_tree_required")
        for index, row in enumerate(tree["nodes"]):
            fixed = {"index", "depth", "role", "fixed_labels", "navigation_press_action", "inert_navigation_descendant",
                     "children_query_status", "children_array_verified", "action_query_status", "action_array_verified", "subtree_complete",
                     "public_action_bits", "unclassified_action_count"}
            P.require(isinstance(row, dict) and fixed <= set(row)
                      <= fixed | {"parent", "bounds", "enabled", "selected", "value_boolean", "value_enum", "value_settable_query_status", "value_settable"}
                      and type(row["index"]) is int and row["index"] == index and type(row["depth"]) is int and 0 <= row["depth"] < 18
                      and row["role"] in roles and isinstance(row["fixed_labels"], list) and len(row["fixed_labels"]) <= len(labels)
                      and all(label in labels for label in row["fixed_labels"]) and type(row["navigation_press_action"]) is bool
                      and type(row["inert_navigation_descendant"]) is bool and type(row["subtree_complete"]) is bool,
                      "closed_global_settings_node_required")
            action_keys = {"press", "increment", "decrement", "confirm", "pick", "show_menu"}
            P.require(isinstance(row["public_action_bits"], dict) and set(row["public_action_bits"]) == action_keys
                      and all(type(bit) is bool for bit in row["public_action_bits"].values())
                      and row["public_action_bits"]["press"] == row["navigation_press_action"]
                      and type(row["unclassified_action_count"]) is int and 0 <= row["unclassified_action_count"] <= 65536,
                      "global_settings_action_bits_invalid")
            if "value_settable_query_status" in row:
                P.require(type(row["value_settable_query_status"]) is int and -(2 ** 31) <= row["value_settable_query_status"] < 2 ** 31
                          and ((row["value_settable_query_status"] == 0 and type(row.get("value_settable")) is bool)
                               or (row["value_settable_query_status"] != 0 and "value_settable" not in row)),
                          "global_settings_settable_status_invalid")
            else:
                P.require("value_settable" not in row, "global_settings_settable_status_required")
            for prefix in ("children", "action"):
                P.require(type(row[prefix + "_query_status"]) is int and -(2 ** 31) <= row[prefix + "_query_status"] < 2 ** 31
                          and type(row[prefix + "_array_verified"]) is bool
                          and (not row[prefix + "_array_verified"] or row[prefix + "_query_status"] == 0),
                          "global_settings_query_status_invalid")
            if "parent" in row:
                P.require(type(row["parent"]) is int and 0 <= row["parent"] < index, "global_settings_parent_invalid")
            if "bounds" in row:
                P.require(isinstance(row["bounds"], list) and len(row["bounds"]) == 4
                          and all(type(number) in (int, float) and math.isfinite(number) for number in row["bounds"]), "global_settings_bounds_invalid")
            for key in ("enabled", "selected", "value_boolean"):
                P.require(key not in row or type(row[key]) is bool, "global_settings_boolean_invalid")
            P.require("value_enum" not in row or row["value_enum"] in labels | {"on", "off"}, "global_settings_enum_invalid")
    return value


def global_settings_capture_measurements(value):
    P.require(isinstance(value, dict) and set(value) == {"audio_samples", "audio_peak", "screen_frames", "audio_nonzero_samples",
              "audio_rms", "sample_rate", "channels", "format"} and value["sample_rate"] == 48000
              and value["channels"] == 1 and value["format"] == "s16le"
              and all(type(value[key]) is int and 0 <= value[key] <= 10_000_000 for key in ("audio_samples", "screen_frames", "audio_nonzero_samples"))
              and value["audio_nonzero_samples"] <= value["audio_samples"]
              and all(type(value[key]) in (int, float) and math.isfinite(value[key]) and 0 <= value[key] <= 100 for key in ("audio_peak", "audio_rms")),
              "global_settings_capture_measurements_invalid")
    return value


def inspect_global_notification_settings(observer, evidence_root, *, execute=command, spawn=subprocess.Popen):
    """Isolated observer lane: no product, helper acquisition or notification."""
    evidence_root.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="TEST-macos-notification-settings-")).resolve()
    root.chmod(0o700)
    started, capture = time.monotonic(), None
    result = {"scope": "observer_only_global_notifications", "diagnostic_only": True, "qualification_input": False,
              "desktop_qualified": False, "status": "unavailable", "capture_original_maximum_seconds": 30,
              "capture_started": False, "owned_capture_collected": True}
    try:
        capture = spawn([str(observer), "capture", str(root)], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                        stderr=subprocess.PIPE, start_new_session=True)
        result.update(capture_started=True, owned_capture_collected=False)
        wait_for(lambda: (root / "capture-ready.json").is_file(), "global_settings_capture_not_ready", seconds=5)
        ready = root / "capture-ready.json"
        P.require(not ready.is_symlink() and ready.stat().st_size <= 4096, "global_settings_capture_ready_invalid")
        baseline = global_settings_capture_measurements(json.loads(ready.read_text()))
        P.require(baseline["screen_frames"] > 0 and baseline["audio_samples"] > 0, "global_settings_capture_not_observing")
        result["capture_baseline"] = baseline
        P.require(capture.poll() is None, "global_settings_capture_not_running")
        result["capture_active_before_inspection"] = True
        clock = time.get_clock_info("monotonic")
        P.require(clock.implementation == "mach_absolute_time()" and clock.monotonic and not clock.adjustable,
                  "global_settings_shared_clock_required")
        deadline = time.monotonic() + 6
        raw = execute([observer, "inspect-global-notification-settings", str(deadline)], timeout=6)
        P.require(len(raw) <= 256 * 1024, "global_settings_output_bound")
        result["settings"] = global_settings_projection(json.loads(raw))
        result["capture_active_after_inspection"] = capture.poll() is None
        P.require(result["capture_active_after_inspection"], "global_settings_capture_ended_during_inspection")
        screenshot = evidence_root / "global-notifications-settings.png"
        execute(["/usr/sbin/screencapture", "-x", screenshot], timeout=2)
        P.require(screenshot.is_file() and not screenshot.is_symlink() and screenshot.stat().st_size <= 16 * 1024 * 1024,
                  "global_settings_screenshot_unavailable")
        result["capture_active_after_screenshot"] = capture.poll() is None
        P.require(result["capture_active_after_screenshot"], "global_settings_capture_ended_before_screenshot")
        result.update(status="inspected", screenshot_sha256=P.sha(screenshot))
    except Exception as error:
        result["failure_class"] = type(error).__name__
    finally:
        if capture is not None:
            try:
                (root / "capture-stop").touch()
                try:
                    out, err = capture.communicate(timeout=max(0.001, min(3, started + 30 - time.monotonic())))
                except subprocess.TimeoutExpired:
                    capture.kill()
                    out, err = capture.communicate(timeout=2)
                result.update(capture_exit_code=capture.returncode, capture_stdout_bytes=len(out), capture_stderr_bytes=len(err),
                              owned_capture_collected=capture.poll() is not None)
                if capture.returncode != 0 or err or not result["owned_capture_collected"]:
                    result["status"] = "unavailable"
                elif len(out) <= 4096:
                    captured = json.loads(out)
                    P.require(isinstance(captured, dict) and set(captured) == {"baseline", "measurements"}, "global_settings_capture_result_invalid")
                    result["capture_final_measurements"] = global_settings_capture_measurements(captured["measurements"])
                    result["capture_frames_advanced"] = captured["measurements"]["screen_frames"] > result.get("capture_baseline", {}).get("screen_frames", 0)
                    if not result["capture_frames_advanced"]:
                        result["status"] = "unavailable"
                else:
                    result["status"] = "unavailable"
            except Exception as error:
                result.update(status="unavailable", cleanup_failure_class=type(error).__name__)
                try:
                    if capture.poll() is None:
                        capture.kill()
                    capture.communicate(timeout=2)
                except Exception:
                    pass
                result["owned_capture_collected"] = capture.poll() is not None
        result["elapsed_seconds"] = round(time.monotonic() - started, 3)
        (evidence_root / "global-notifications-settings.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def case_failure_observation(case_root, terminal, provider, sink, data_root, helper_app, expected, observe, screenshot,
                             *, now=time.monotonic):
    """Failure-only fixed metadata, before owned UI/process cleanup begins."""
    started = now()
    deadline = started + 6
    result = {"scope": "failed_owned_TEST_case_only", "desktop_qualified": False,
              "maximum_seconds": 6, "provider_requests": len(provider.requests),
              "provider_released": provider.release.is_set(), "witness": [], "webhooks": []}

    def read(path, limit):
        P.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= limit,
                  "case_diagnostic_file_unavailable_or_oversized")
        with path.open("rb") as source:
            raw = source.read(limit + 1)
        P.require(len(raw) <= limit, "case_diagnostic_file_oversized")
        return raw

    def attempt(name, action):
        try:
            result[name] = action()
        except Exception as error:
            result[name] = {"status": "unavailable", "error_class": type(error).__name__}

    def witness():
        raw = read(case_root / "witness.jsonl", 65536)
        lines = raw.splitlines()
        P.require(len(lines) <= 64, "case_diagnostic_witness_rows_exceeded")
        kinds = {"factory", "session_start", "agent_start", "agent_end", "agent_before_settle",
                 "agent_settled", "ui_prompt_start", "ui_prompt_end", "session_shutdown"}
        rows = []
        for line in lines:
            value = json.loads(line)
            P.require(isinstance(value, dict) and value.get("type") in kinds, "case_diagnostic_witness_kind")
            row = {"type": value["type"]}
            for key in ("hasUI", "idle", "aborted"):
                if type(value.get(key)) is bool:
                    row[key] = value[key]
            if value.get("mode") in ("tui", "rpc", "json", "print"):
                row["mode"] = value["mode"]
            if value.get("outcome") in ("completed", "aborted", "error"):
                row["outcome"] = value["outcome"]
            if type(value.get("pid")) is int and value["pid"] > 0:
                row["pid"] = value["pid"]
            rows.append(row)
        return rows

    def webhooks():
        rows = list(sink.rows)
        P.require(len(rows) <= 32, "case_diagnostic_webhook_rows_exceeded")
        return [{"status": row["status"], "received_monotonic": row["received_monotonic"]}
                for row in rows if row.get("status") in P.COPY
                and type(row.get("received_monotonic")) in (int, float)
                and math.isfinite(row["received_monotonic"])]

    def cache():
        # Attempt bits are not evidence of submission or visible delivery.
        P.require(data_root.is_absolute() and data_root.resolve(strict=True) == data_root and data_root.is_dir()
                  and data_root.is_relative_to(case_root.parent.resolve(strict=True)),
                  "case_diagnostic_cache_outside_owned_TEST_root")
        path = data_root / "observations.json"
        if not path.exists():
            return {"status": "absent"}
        value = json.loads(read(path, 48 * 1024))
        entries = value.get("entries") if isinstance(value, dict) else None
        P.require(isinstance(entries, list) and len(entries) <= 256, "case_diagnostic_cache_shape")
        bits = [row.get("bits") for row in entries if isinstance(row, dict)]
        P.require(len(bits) == len(entries) and all(type(bit) is int and bit in (1, 2, 3) for bit in bits),
                  "case_diagnostic_cache_bits")
        return {"status": "observed_attempt_bits_only", "entries": len(bits),
                "desktop_attempt_entries": sum(bool(bit & 1) for bit in bits),
                "webhook_attempt_entries": sum(bool(bit & 2) for bit in bits)}

    attempt("witness", witness)
    attempt("webhooks", webhooks)
    attempt("observation_cache", cache)
    result["webhook_failure_count"] = len(sink.failures)
    if terminal is not None:
        result["origin"] = {"window": terminal.window, "tty": terminal.tty, "pid": terminal.pid,
                            "shell_pid": terminal.shell_pid, "process_start_identity": terminal.process_start_identity,
                            "window_closed": terminal.window_closed}

    def bounded(action):
        remaining = deadline - now()
        P.require(remaining > 0, "case_diagnostic_deadline_expired")
        return action(min(2, remaining))

    attempt("screenshot", lambda: bounded(screenshot))
    attempt("installed_helper_processes", lambda: bounded(lambda seconds: observe("helper-processes", str(helper_app), timeout=seconds)))
    attempt("foreground", lambda: bounded(lambda seconds: observe("foreground", timeout=seconds)))
    attempt("expected_banner", lambda: bounded(lambda seconds: observe("banner", P.COPY[expected], timeout=seconds)))
    if terminal is not None and terminal.pid and terminal.process_start_identity:
        attempt("origin_process", lambda: bounded(lambda seconds: observe("terminal-process-identity", str(terminal.pid),
                    terminal.process_start_identity, timeout=seconds)))
    result["elapsed_seconds"] = round(now() - started, 3)
    return result


def cleanup_case(actions, primary_failure, report, name):
    """Attempt every owned cleanup and keep the actual observation failure."""
    failures = []
    for role, action in actions:
        try:
            action()
        except Exception as error:
            failures.append({"case": name, "role": role, "error_class": type(error).__name__})
    if failures:
        report.setdefault("case_cleanup_failures", []).extend(failures)
        if primary_failure is None:
            raise RuntimeError("owned_native_case_cleanup_failed")


class NativeContextObservation:
    """Read only original native transport metadata; never delivery evidence."""
    unsealed = object()

    def __init__(self, temporary, *, now=time.monotonic):
        self.now, self.started = now, now()
        self.stop_event = threading.Event()
        self.seen, self.attempts, self.started_attempts = set(), set(), set()
        self.bytes_read, self.entries_visited = 0, 0
        self.receipt = {"scope": "read_only_original_native_context_metadata", "desktop_qualified": False,
                        "diagnostic_only": True, "qualification_input": False, "absence_is_unknown": True, "maximum_seconds": 15,
                        "maximum_bytes": 256 * 1024, "maximum_attempts": 8, "maximum_rows": 16,
                        "maximum_directory_entries": 65536,
                        "poll_interval_seconds": 0.005, "capped": False, "rows": []}
        self.thread = None
        fd = None
        try:
            P.require(temporary.is_absolute() and temporary.resolve(strict=True) == temporary
                      and temporary.name == "tmp" and temporary.parent.name.startswith("case-")
                      and temporary.parent.parent.name.startswith("TEST-pi-installed-macos-"),
                      "owned_native_context_TEST_tmp_required")
            fd = self.open_root(temporary)
            try:
                self.private_directory(fd)
            except Exception:
                os.close(fd)
                fd = None
                raise
            self.thread = threading.Thread(target=self.run, args=(fd,), daemon=True)
            self.thread.start()
            fd = None  # The one owned thread closes its directory descriptor.
        except Exception as error:
            if fd is not None:
                os.close(fd)
            self.thread = None
            self.receipt["unavailable_class"] = type(error).__name__

    @staticmethod
    def open_root(path):
        fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        try:
            for component in path.parts[1:]:
                child = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
                os.close(fd)
                fd = child
            return fd
        except Exception:
            os.close(fd)
            raise

    @staticmethod
    def private_directory(fd):
        metadata = os.fstat(fd)
        P.require(stat.S_ISDIR(metadata.st_mode) and metadata.st_uid == os.getuid()
                  and stat.S_IMODE(metadata.st_mode) == 0o700, "native_context_private_directory_required")

    def entries(self, fd, limit):
        names = []
        with os.scandir(fd) as entries:
            for entry in entries:
                if len(names) == limit or self.entries_visited >= self.receipt["maximum_directory_entries"]:
                    self.receipt["capped"] = True
                    raise RuntimeError("native_context_inventory_capped")
                self.entries_visited += 1
                names.append(entry.name)
        return names

    def directory(self, parent, name):
        fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
        try:
            self.private_directory(fd)
        except Exception:
            os.close(fd)
            raise
        return fd

    def read(self, directory, name, limit):
        fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=directory)
        try:
            before = os.fstat(fd)
            P.require(stat.S_ISREG(before.st_mode) and before.st_uid == os.getuid()
                      and stat.S_IMODE(before.st_mode) == 0o600
                      and 0 < before.st_size <= limit, "native_context_private_file_required")
            # OwnedRequest publishes by hardlink before unlinking its private
            # temporary name. Observe only the subsequently sealed single link.
            if before.st_nlink == 2:
                self.receipt["unsealed_publications"] = self.receipt.get("unsealed_publications", 0) + 1
                return self.unsealed
            P.require(before.st_nlink == 1, "native_context_private_file_required")
            if self.bytes_read + before.st_size > self.receipt["maximum_bytes"]:
                self.receipt["capped"] = True
                raise RuntimeError("native_context_bytes_capped")
            raw = bytearray()
            while len(raw) <= limit:
                remaining = self.receipt["maximum_bytes"] - self.bytes_read
                if remaining <= 0:
                    break
                data = os.read(fd, min(4096, limit + 1 - len(raw), remaining))
                if not data:
                    break
                raw.extend(data)
                self.bytes_read += len(data)
            after = os.fstat(fd)
            P.require(len(raw) == before.st_size and after.st_dev == before.st_dev and after.st_ino == before.st_ino
                      and after.st_size == before.st_size and after.st_nlink == 1
                      and after.st_uid == before.st_uid and after.st_mode == before.st_mode,
                      "native_context_file_changed")
            def fields(pairs):
                value = {}
                for key, item in pairs:
                    P.require(key not in value, "duplicate_native_context_metadata_field")
                    value[key] = item
                return value
            return json.loads(raw, object_pairs_hook=fields)
        finally:
            os.close(fd)

    def scan(self, root):
        uuid = r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}"
        roots = [name for name in self.entries(root, 64)
                 if re.fullmatch(r"agent-notifications-context-[A-Za-z0-9-]{1,64}", name)]
        if len(roots) > 8:
            self.receipt["capped"] = True
            raise RuntimeError("native_context_roots_capped")
        for name in roots:
            try:
                parent = self.directory(root, name)
            except FileNotFoundError:
                continue
            try:
                children = self.entries(parent, 8)
                for identity in children:
                    if re.fullmatch(uuid, identity) is None:
                        continue
                    try:
                        directory = self.directory(parent, identity)
                    except FileNotFoundError:
                        continue
                    try:
                        for filename in sorted(self.entries(directory, 16), key=lambda value: not value.endswith(".started")):
                            match = re.fullmatch("(" + uuid + r")\.(started|receipt)", filename)
                            if match is None:
                                continue
                            nonce, phase = match.groups()
                            key = (name, identity, nonce)
                            if (key, phase) in self.seen:
                                continue
                            if key not in self.attempts and len(self.attempts) >= 8:
                                self.receipt["capped"] = True
                                raise RuntimeError("native_context_attempts_capped")
                            self.attempts.add(key)
                            try:
                                value = self.read(directory, filename, 1024 if phase == "started" else 16384)
                            except FileNotFoundError:
                                continue
                            if value is self.unsealed:
                                continue
                            P.require(isinstance(value, dict) and type(value.get("schemaVersion")) is int and value["schemaVersion"] == 1
                                      and value.get("correlationID") == identity and value.get("nonce") == nonce,
                                      "native_context_metadata_correlation_unverified")
                            row = {"phase": phase, "elapsed_seconds": round(self.now() - self.started, 3)}
                            if phase == "started":
                                P.require(set(value) == {"schemaVersion", "correlationID", "nonce", "process"},
                                          "native_context_started_shape")
                                process = value.get("process")
                                P.require(isinstance(process, dict) and set(process) == {"pid", "startSeconds", "startMicroseconds"}
                                          and all(type(process[key]) is int for key in process)
                                          and 0 < process["pid"] <= 0x7fffffff and 0 < process["startSeconds"] <= 0xffffffffffffffff
                                          and 0 <= process["startMicroseconds"] < 1_000_000,
                                          "native_context_started_process_shape")
                                row["reported_process"] = process
                                row["process_identity_verified"] = False
                                self.started_attempts.add(key)
                            else:
                                P.require(set(value) == {"schemaVersion", "correlationID", "nonce", "notificationID", "status", "reason", "retrySafe"}
                                          and value.get("notificationID") == identity and value.get("retrySafe") is False
                                          and value.get("status") in ("rejected", "suppressed", "submitted", "unknown")
                                          and value.get("reason") in ("malformed_request", "unsupported_version", "unsupported_action",
                                              "invalid_file", "expired", "activation_required", "permission_denied",
                                              "unsupported_notifier", "os_rejected", "os_accepted", "timeout"),
                                          "native_context_receipt_shape")
                                row.update(status=value["status"], reason=value["reason"],
                                           matching_started_observed=key in self.started_attempts)
                            if len(self.receipt["rows"]) >= 16:
                                self.receipt["capped"] = True
                                raise RuntimeError("native_context_rows_capped")
                            self.receipt["rows"].append(row)
                            self.seen.add((key, phase))
                    finally:
                        os.close(directory)
            finally:
                os.close(parent)

    def run(self, fd):
        try:
            while not self.stop_event.is_set() and self.now() - self.started < 15:
                self.scan(fd)
                self.stop_event.wait(0.005)
        except Exception as error:
            self.receipt["unavailable_class"] = type(error).__name__
        finally:
            os.close(fd)

    def close(self):
        self.stop_event.set()
        if self.thread is not None:
            self.thread.join(timeout=1)
        self.receipt.update(owned_thread_collected=self.thread is None or not self.thread.is_alive(),
                            elapsed_seconds=round(self.now() - self.started, 3),
                            bytes_read=self.bytes_read, entries_visited=self.entries_visited,
                            attempts_observed=len(self.attempts))
        P.require(self.receipt["owned_thread_collected"], "native_context_observer_not_collected")
        return self.receipt


def run(args):
    P.require(platform.system() == "Darwin", "actual_macos_host_required")
    root = Path(tempfile.mkdtemp(prefix="TEST-pi-installed-macos-")).resolve()
    root.chmod(0o700)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    sink, initial_provider = P.Webhooks(), F.Provider("success")
    env = F.prepare(root, initial_provider, "success")
    env["AGENT_NOTIFICATIONS_CONFIG"] = str(root / "global/config.json")
    profile, terminals, helpers, app = root / "agent", [], [], None
    permission_request = permission_log = None
    consent_names = []
    report = {"schema_version": 1, "scope": "actual_installed_pi_macos_desktop", "status": "failed",
              "test_root": str(root), "source_sha": command(["git", "-C", REPO, "rev-parse", "HEAD"]).strip(),
              "working_tree_dirty": bool(command(["git", "-C", REPO, "status", "--porcelain"])),
              "script_sha256": P.sha(__file__), "observer_source_sha256": P.sha(REPO / "tests/integration/pi_installed_desktop_darwin.swift"),
              "observer_binary_sha256": P.sha(args.observer),
              "fixture_sha256": {name: P.sha(REPO / "tests/integration" / name)
                                  for name in ("pi_native_fixture.py", "pi_installed_fixture.py")},
              "host_os": platform.system(), "host_architecture": platform.machine(),
              "candidate_binary_sha256": P.sha(args.candidate), "package_sha256": P.sha(args.package),
              "pi_binary_sha256": P.sha(args.pi_executable), "pi_acquisition": json.loads(args.pi_acquisition.read_text()),
              "helper": {"input": json.loads(args.helper_input.read_text()),
                         "archive_sha256": P.sha(args.helper_archive)},
              "desktop_qualified": False, "native_product_observations_passed": False,
              "native_helper_verified": False, "cases": []}

    def observer(*argv, timeout=20):
        if argv and argv[0] in ("notification-consent", "observe-notification-consent-menu", "hover-notification-consent", "open-notification-consent-options", "allow-notification-consent"):
            argv = (*argv, *consent_names)
        return json.loads(command([args.observer, *argv], timeout=timeout))

    def capture_permission_ui(phase):
        screenshot = args.report.parent / ("notification-consent-after-" + phase + ".png")
        P.require(not screenshot.exists() and not screenshot.is_symlink(), "permission_ui_capture_path_exists")
        command(["/usr/sbin/screencapture", "-x", screenshot], timeout=3)
        P.require(screenshot.is_file() and 0 < screenshot.stat().st_size <= 16 * 1024 * 1024,
                  "permission_ui_capture_unavailable")
        return {"artifact": screenshot.name, "sha256": P.sha(screenshot), "desktop_qualified": False}

    report["terminal_automation"] = []

    def launch_terminal(source):
        receipt = {"scope": "original_owned_terminal_applescript", "desktop_qualified": False}
        report["terminal_automation"].append(receipt)
        return terminal_applescript(source, observer, receipt,
            lambda: terminal_automation_admission(args.report.parent, report["source_sha"]))

    def case(name, decoy):
        case_root = root / ("case-" + name)
        case_root.mkdir(mode=0o700)
        provider = F.Provider("success", hold_reply=name != "question")
        terminal, recorder, native_context = None, None, None
        primary_failure = None
        started = time.monotonic()
        timing = {}
        desktop, sound, click = name != "desktop-off", name != "sound-off", name != "click-off"
        expected = "question" if name == "question" else "task_complete"
        sink_start = (len(sink.rows), len(sink.failures))
        try:
            native_env = F.prepare(case_root, provider, "success", agent_dir=profile)
            native_env.update(AGENT_NOTIFICATIONS_CONFIG=str(root / "global/config.json"), TEST_PI_DEFER_DIALOG="1",
                              CLAUDE_PLUGIN_ROOT=str(root / "foreign-TEST-root"))
            P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink,
                        desktop=desktop, sound=sound, click=click)
            P.require(not observer("banner", P.COPY[expected])["matches"], "candidate_banner_baseline_not_empty")
            argv = [args.pi_executable, "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                    "--provider", "TEST-local", "--model", "TEST-local", "--tui-mode", "regular"]
            if name != "question":
                argv.append("TEST say done")
            cwd = case_root / "project" / ("TEST-Pi-" + name)
            cwd.mkdir()
            terminal = Terminal(case_root, argv, native_env, observer, cwd, owned=terminals, launch=launch_terminal)
            wait_for(lambda: (case_root / "witness.jsonl").exists()
                     and any(x["type"] == "session_start" for x in F.witness(case_root)), "actual_pi_tui_start_missing")
            rows = F.witness(case_root)
            P.require(any(x["type"] == "factory" and x["pid"] == terminal.pid for x in rows)
                      and all(x.get("mode") == "tui" for x in rows if x["type"] != "factory"), "actual_native_pi_tui_identity_wrong")
            recorder = subprocess.Popen([str(args.observer), "capture", str(case_root)], stdin=subprocess.DEVNULL,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            wait_for((case_root / "capture-ready.json").exists, "native_product_capture_not_ready")
            baseline = json.loads((case_root / "capture-ready.json").read_text())
            P.require(baseline["audio_peak"] < 0.0001, "native_audio_baseline_not_quiet")
            timing["capture_ready_seconds"] = round(time.monotonic() - started, 3)
            # Question events begin in the real input command, before provider
            # release. Observe only this original case's native transport.
            native_context = NativeContextObservation(case_root / "tmp")
            timing["native_context_armed_seconds"] = round(time.monotonic() - started, 3)
            report.setdefault("native_context_observations", []).append({"case": name, "observation": native_context.receipt})
            if name == "question":
                terminal.focus()
                terminal.type("/TEST-dialog")
            else:
                wait_for(lambda: bool(provider.requests), "actual_pi_model_request_missing")
            decoy.focus()
            P.require(decoy.is_front() and not terminal.is_front(), "native_pi_origin_not_backgrounded")
            timing["provider_release_seconds"] = round(time.monotonic() - started, 3)
            provider.release.set()
            screenshot, webhook_received, click_observation = None, False, None
            if desktop:
                timing["banner_wait_started_seconds"] = round(time.monotonic() - started, 3)
                banner = wait_for(lambda: observer("banner", P.COPY[expected])["matches"], "actual_product_banner_not_observed")
                timing["banner_observed_seconds"] = round(time.monotonic() - started, 3)
                P.require(decoy.is_front(), "product_focused_before_physical_notification_click")
                if name == "question":
                    rows = F.witness(case_root)
                    P.require(any(x["type"] == "ui_prompt_start" for x in rows)
                              and not any(x["type"] == "ui_prompt_end" for x in rows), "native_question_not_pending_at_click")
                screenshot = case_root / "actual-product-banner.png"
                command(["/usr/sbin/screencapture", "-x", screenshot])
                if name == "stale-origin":
                    sink.wait(expected)
                    webhook_received = True
                    terminal.close()
                    decoy.focus()
                clicked = observer("click", P.COPY[expected])
                P.require(clicked["physical_mouse_click"] and clicked["matches"], "physical_product_banner_click_missing")
                if click and name != "stale-origin":
                    wait_for(terminal.is_front, "product_click_did_not_focus_exact_origin")
                else:
                    click_observation = observe_unchanged_focus(decoy, lambda: (case_root / "capture-stop").touch())
                after = terminal.front()
            else:
                time.sleep(1.5)
                P.require(not observer("banner", P.COPY[expected])["matches"] and decoy.is_front(), "desktop_off_had_native_effect")
                banner, after = [], terminal.front()
            # The real helper may remain alive until the actual notification
            # click. Its webhook is observed after that normal helper lifecycle.
            if not webhook_received:
                sink.wait(expected)
            # The configured task-complete cue is 2.544s. Capture its complete
            # native playback, including the tail after a prompt physical click.
            if click_observation is None:
                time.sleep(3)
                (case_root / "capture-stop").touch()
            out, err = recorder.communicate(timeout=5)
            P.require(recorder.returncode == 0 and not err, "native_capture_not_collected")
            captured = json.loads(out)
            audio = captured["measurements"]
            P.require(audio["audio_samples"] >= 4800 and audio["screen_frames"] > 0, "native_audio_observer_incomplete")
            expected_sound = desktop and sound
            recording = case_root / "native-output.s16le"
            if expected_sound:
                P.require(audio["audio_peak"] > 0.03 and audio["audio_nonzero_samples"] >= 480, "actual_configured_product_cue_not_recorded")
                reference = case_root / "configured-cue-reference.wav"
                cue = root / "runtime/sounds" / ("question.mp3" if name == "question" else "task-complete.mp3")
                command(["/usr/bin/afconvert", "-f", "WAVE", "-d", "LEI16@48000", "-c", "1", cue, reference])
                audio["configured_cue_envelope_cosine"] = envelope_match(recording, reference)
                P.require(audio["configured_cue_envelope_cosine"] >= 0.9, "recorded_output_did_not_match_configured_cue")
            else:
                P.require(audio["audio_peak"] < 0.0001, "sound_off_had_recorded_output")
            if not terminal.closed:
                terminal.focus()
                if name == "question":
                    terminal.type("TEST accepted")
                    wait_for(lambda: any(x["type"] == "ui_prompt_end" for x in F.witness(case_root)), "native_question_not_resolved")
                terminal.type("/quit")
                wait_for(terminal.collected, "actual_pi_tui_not_collected")
                P.require(terminal.exit_path.read_text().strip() == "0", "actual_pi_tui_exit_failed")
                terminal.close()
            rows = F.witness(case_root)
            if name == "question":
                P.require(not provider.requests and sum(x["type"] == "ui_prompt_start" for x in rows) == 1
                          and sum(x["type"] == "ui_prompt_end" for x in rows) == 1
                          and not any(x["type"] == "agent_settled" for x in rows), "native_question_lifecycle_wrong")
            else:
                F.verify_case("success", rows, provider.requests)
            sink.quiet()
            report["cases"].append({"case": name, "native_mode": "tui", "origin_window": terminal.window,
                "origin_tty": terminal.tty, "origin_pi_pid": terminal.pid, "decoy_window": decoy.window,
                "decoy_tty": decoy.tty, "foreground_after_click": after, "banner_matches": banner,
                "unchanged_click_observation": click_observation,
                "real_mouse_click": desktop, "banner_observed": desktop, "sound_expected": expected_sound,
                "audio": audio, "audio_sha256": P.sha(recording),
                "banner_screenshot_sha256": P.sha(screenshot) if screenshot else None,
                "pending_question_at_click": name == "question", "terminal_collected": terminal.collected(),
                "checks": {key: True for key in ("native_tui", "configured_banner_policy", "configured_audio_policy",
                    "configured_click_policy", "native_event_counts", "ambient_root_isolated", "owned_terminal_collected")}})
        except Exception as error:
            primary_failure = error
            timing["failure_seconds"] = round(time.monotonic() - started, 3)
            failure = {"case": name, "failure": str(error), "timing": timing}
            report["case_failure"] = failure
            try:
                def capture_failure_ui(seconds):
                    path = case_root / "failure-before-cleanup.png"
                    command(["/usr/sbin/screencapture", "-x", path], timeout=seconds)
                    P.require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 16 * 1024 * 1024,
                              "case_diagnostic_screenshot_unavailable")
                    return {"artifact": "case-" + name + "/" + path.name, "sha256": P.sha(path)}
                failure["observations"] = case_failure_observation(case_root, terminal, provider, sink,
                    Path(view["binding"]["dataRoot"]), app, expected, observer, capture_failure_ui)
            except Exception as diagnostic_error:
                failure["diagnostic_error_class"] = type(diagnostic_error).__name__
            report["partial_case_failure"] = {"case": name, "desktop_qualified": False}
            try:
                report["partial_case_failure"].update(partial_case_failure(case_root, provider, sink, sink_start,
                    expected, observer, app, terminal.pid if terminal else None,
                    observations=failure.get("observations", {})))
            except Exception as diagnostic_error:
                report["partial_case_failure"]["evidence_error_class"] = type(diagnostic_error).__name__
            raise
        finally:
            def stop_recorder():
                if recorder and recorder.poll() is None:
                    (case_root / "capture-stop").touch()
                    try:
                        recorder.communicate(timeout=5)
                    except subprocess.TimeoutExpired:
                        recorder.kill()
                        recorder.communicate(timeout=3)

            def retain():
                retained = args.report.parent / ("case-" + name)
                retained.mkdir(parents=True, exist_ok=True)
                for filename in ("actual-product-banner.png", "native-output.s16le", "capture-ready.json", "failure-before-cleanup.png"):
                    source = case_root / filename
                    if source.is_file() and not source.is_symlink() and source.stat().st_size <= 16 * 1024 * 1024:
                        shutil.copyfile(source, retained / filename)

            actions = []
            if native_context is not None:
                actions.append(("native_context_observation", native_context.close))
            actions.append(("capture", stop_recorder))
            if terminal:
                actions.append(("origin", terminal.close))
            actions.extend((("provider", provider.close), ("evidence", retain)))
            cleanup_case(actions, primary_failure, report, name)

    try:
        report["native_preflight"] = observer("preflight")
        P.require(report["pi_acquisition"].get("version") == "1.4.0"
                  and report["pi_acquisition"].get("binary_sha256") == report["pi_binary_sha256"]
                  and command([args.pi_executable, "--version"]).strip() == "1.4.0",
                  "official_pinned_pi_identity_wrong")
        helper_pin = H.load_pin()
        H.candidate_tree(helper_pin)
        H.validate_receipt(report["helper"]["input"], helper_pin)
        P.require(report["helper"]["archive_sha256"] == helper_pin["archive_sha256"],
                  "signed_helper_archive_mismatch")
        (root / "foreign-TEST-root").mkdir()
        P.configure(root / "global/config.json", args.assets_root / "config/config.json", sink)
        D.command(args.candidate, ["internal-install-runtime", "--stage", args.runtime_stage, "--target", root / "runtime/bin",
                                  "--entry", args.candidate.name, "--control-root", root / "control",
                                  "--consumer", "pi-runtime-preparation"], root, env)
        P.setup(args.candidate, "install", root, args.package, args.pi_executable, env,
                ("--hooks", "false", "--agent-notify", "true", "--pi-desktop", "true", "--pi-webhook", "true"))
        ledger = json.loads((root / "control/ownership.json").read_text())
        native = ledger.get("Native")
        P.require(native and native["Path"] and native["InstalledTreeSHA256"], "installed_signed_native_receipt_missing")
        app = Path(native["Path"])
        helper = app / "Contents/MacOS/terminal-notifier-modern"
        P.require(P.sha(helper) == P.sha(args.runtime_stage / "ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern"),
                  "installed_signed_helper_changed")
        P.require(P.sha(helper) == helper_pin["executable_sha256"]
                  and P.sha(args.runtime_stage / "ClaudeNotifier.app.managed-runtime.json") == helper_pin["attestation_sha256"],
                  "installed_signed_helper_pin_mismatch")
        report["helper"].update(installed_path=str(app), installed_tree_sha256=native["InstalledTreeSHA256"],
                                executable_sha256=P.sha(helper), source_stage_attestation_sha256=P.sha(args.runtime_stage / "ClaudeNotifier.app.managed-runtime.json"))
        report["native_helper_verified"] = True
        report["helper"]["permission_ui_identity"] = permission_ui_identity(args.helper_archive, app, helper_pin)
        consent_names = report["helper"]["permission_ui_identity"]["display_names"]
        permission = json.loads(D.command(args.candidate, ["setup-notifications", "permission-status", "--control-root", root / "control", "--json"], root, env))
        report["notification_consent"] = {"initial_permission": permission.get("permission"), "requested": False}
        if permission.get("permission") == "undetermined":
            report["notification_consent"]["services_before_request"] = permission_services()
            permission_log = PermissionLog(args.report.parent)
            started = time.monotonic()
            permission_request = subprocess.Popen([str(args.candidate), "setup-notifications", "request-permission",
                                        "--control-root", str(root / "control"), "--expected-generation", str(ledger["Generation"]), "--json"],
                                       cwd=root / "project", env=env, stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            helpers.append(permission_request)
            report["notification_consent"]["requested"] = True
            clock_info = time.get_clock_info("monotonic")
            P.require(clock_info.implementation == "mach_absolute_time()" and clock_info.monotonic
                      and not clock_info.adjustable, "shared_consent_monotonic_clock_unavailable")
            report["notification_consent"]["clock_contract"] = {"implementation": clock_info.implementation,
                "monotonic": clock_info.monotonic, "adjustable": clock_info.adjustable}
            consent_deadline = started + 130
            report["notification_consent"]["request_process_identity"] = consent_request_identity(permission_request, observer, args.candidate)
            dialogs = wait_for_consent(permission_request,
                consent_observer(permission_request, observer, app, report["notification_consent"],
                    lambda: capture_pending_consent(permission_request, observer, app, args.report.parent,
                                                     report["notification_consent"]), ui_capture=capture_permission_ui,
                    candidate=args.candidate, deadline=consent_deadline),
                seconds=max(0, consent_deadline - time.monotonic()))
            report["notification_consent"].update(dialogs=dialogs, completed_without_dialog=not dialogs)
            if dialogs:
                P.require(permission_request.poll() is None, "original_permission_request_ended_before_allow")
                command(["/usr/sbin/screencapture", "-x", args.report.parent / "actual-notification-consent.png"])
                click = allow_consent(permission_request, observer, app, args.candidate,
                                      report["notification_consent"], consent_deadline)
                report["notification_consent"].update(physical_allow_click=click["physical_allow_click"],
                    screenshot_sha256=P.sha(args.report.parent / "actual-notification-consent.png"))
            out, err = permission_request.communicate(timeout=10)
            report["notification_consent"]["service_log"] = permission_log.close()
            receipt = permission_receipt(out, err, permission_request.returncode)
            report["notification_consent"].update(request_result=receipt,
                elapsed_seconds=round(time.monotonic() - started, 3), request_collected=True)
            fresh_out = D.command(args.candidate, ["setup-notifications", "permission-status", "--control-root", root / "control", "--json"], root, env)
            fresh_receipt = permission_receipt(fresh_out, b"", 0)
            report["notification_consent"]["fresh_permission_result"] = fresh_receipt
            permission = {"permission": verify_permission_grant(receipt, fresh_receipt)}
        report["candidate_notification_permission"] = permission.get("permission")
        P.require(permission.get("permission") == "allowed", "candidate_signed_helper_notification_permission_unavailable")
        policy = json.loads((root / "control/agent-notifications.json").read_text())
        P.require(policy.get("enabled") is False, "pi_setup_enabled_unrelated_mcp_policy")
        report["unrelated_mcp_policy_enabled"] = False
        view = P.installed(root / "control", profile)
        P.require(P.sha(view["runtime"]["executable"]) == P.sha(args.candidate), "installed_candidate_bytes_differ")
        report["installed_asset_sha256"] = P.asset_receipts(root / "control", root / "runtime", args.assets_root)
        report["active_leaf"] = view["active"].name
        decoy_root = root / "decoy"
        (decoy_root / "project").mkdir(parents=True)
        decoy_cwd = decoy_root / "project/TEST-Pi-decoy"
        decoy_cwd.mkdir()
        decoy = Terminal(decoy_root, ["/bin/sleep", "240"], {"PATH": "/usr/bin:/bin", "HOME": str(root / "home")},
                         observer, decoy_cwd, owned=terminals, launch=launch_terminal)
        for name in CASES:
            case(name, decoy)
        report.update(status="passed_native_product_observations", native_product_observations_passed=True)
    except Exception as error:
        report["failure"] = str(error)
        if permission_request is not None:
            report["notification_consent"].setdefault("elapsed_seconds", round(time.monotonic() - started, 3))
            report["notification_consent"]["request_running_at_failure"] = permission_request.poll() is None
            try:
                report["notification_consent"]["installed_helper_processes_at_failure"] = observer("helper-processes", str(app))
            except Exception as observation_error:
                report["notification_consent"]["helper_process_observation_failed"] = str(observation_error)
        try:
            screenshot = args.report.parent / "native-failure.png"
            command(["/usr/sbin/screencapture", "-x", screenshot])
            report["native_failure_screenshot_sha256"] = P.sha(screenshot)
        except Exception as capture_error:
            report["native_failure_capture"] = str(capture_error)
        if permission_request is not None and report.get("failure") == "actual_candidate_notification_consent_dialog_unavailable":
            consent = report["notification_consent"]
            try:
                consent["failure_diagnostics"] = observer("permission-diagnostics", str(app), timeout=4)
            except Exception:
                consent["failure_diagnostics"] = {"status": "inconclusive_observer_unavailable"}
            try:
                consent["helper_sample"] = sample_permission_helper(observer, app, consent, args.report.parent)
            except Exception:
                consent["helper_sample"] = {"status": "inconclusive_sample_collection_failed"}
    finally:
        failures = ["case_cleanup_failed:" + row["case"] + ":" + row["role"] + ":" + row["error_class"]
                    for row in report.get("case_cleanup_failures", [])]
        if permission_log is not None:
            report["notification_consent"]["service_log"] = permission_log.close()
            if not report["notification_consent"]["service_log"]["owned_child_collected"]:
                failures.append("permission_diagnostic_log_not_collected")
        for terminal in reversed(terminals):
            try:
                terminal.close()
            except Exception as error:
                failures.append(str(error))
        for child in helpers:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.communicate(timeout=3)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.communicate(timeout=3)
            if child is permission_request:
                out, err = child.communicate(timeout=3)
                report["notification_consent"]["request_result"] = permission_receipt(out, err, child.returncode)
                report["notification_consent"]["request_collected"] = child.poll() is not None
        if app is not None:
            try:
                wait_for(lambda: not observer("helper-processes", str(app))["installed_helper_pids"],
                         "owned_native_notification_helper_not_collected", seconds=15)
                report["native_helper_processes_collected"] = True
            except Exception as error:
                failures.append(str(error))
                report["native_helper_processes_collected"] = False
        initial_provider.close()
        sink.close()
        report["owned_children_collected"] = not failures and all(t.collected() for t in terminals) and all(p.poll() is not None for p in helpers) \
            and all(row.get("owned_child_collected") is True for row in report["terminal_automation"])
        if failures or not report["owned_children_collected"]:
            report.update(status="failed", native_product_observations_passed=False, cleanup_failures=failures)
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps({"status": report["status"], "failure": report.get("failure"), "desktop_qualified": False}))
    return 0 if report["native_product_observations_passed"] and report["owned_children_collected"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("candidate", "runtime-stage", "package", "assets-root", "pi-executable", "observer", "pi-acquisition", "helper-archive", "helper-input", "report"):
        parser.add_argument("--" + name, type=Path, required=True)
    options = parser.parse_args()
    for name in ("candidate", "runtime_stage", "package", "assets_root", "pi_executable", "observer", "pi_acquisition", "helper_archive", "helper_input"):
        setattr(options, name, getattr(options, name).resolve(strict=True))
    raise SystemExit(run(options))
