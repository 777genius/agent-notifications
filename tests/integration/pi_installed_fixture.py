"""Shared real-installed-Pi test observations; never a product installer/sender.

The product is installed by its normal setup wizard. This module supplies only
private TEST configuration, a loopback webhook destination and receipt readers.
It never invokes pi-event or writes product authority, locators or extensions.
"""
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import queue
import re
import socketserver
import subprocess
import tempfile
import threading
import time

COPY = {"task_complete": "Pi completed a task", "question": "Pi is waiting for input",
        "api_error": "Pi encountered an error"}


def require(ok, code):
    if not ok:
        raise RuntimeError(code)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def windows_native_temp_parent():
    """Ignore Python's TMPDIR preference; retain native Windows temp selection."""
    require(os.name == "nt", "windows_native_temp_parent_requires_windows")
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    try:
        get_temp = kernel.GetTempPath2W
    except AttributeError:  # Older Windows has only the original native API.
        get_temp = kernel.GetTempPathW
    get_temp.argtypes = (wintypes.DWORD, wintypes.LPWSTR)
    get_temp.restype = wintypes.DWORD
    buffer = ctypes.create_unicode_buffer(32768)
    count = get_temp(len(buffer), buffer)
    require(0 < count < len(buffer), "windows_native_temp_parent_length")
    value = buffer.value
    require(len(value.encode("utf-16-le")) // 2 == count, "windows_native_temp_parent_length")
    parent = Path(value)
    require(parent.is_absolute() and parent.is_dir(), "windows_native_temp_parent_invalid")
    return parent.resolve(strict=True)


def new_test_root(prefix):
    """Protect only a fresh TEST root before creating any product descendants.

    Windows chmod does not remove a temporary parent's inherited write grants.
    Reuse the existing native fixture's current-user/SYSTEM/Admin OICI DACL;
    production admission remains responsible for accepting every installed inode.
    """
    require(isinstance(prefix, str) and re.fullmatch(r"TEST-pi-[a-z-]+-", prefix), "private_TEST_root_prefix_required")
    # RUNNER_TEMP/Python TMPDIR may select D:\a\_temp, whose ancestors grant
    # foreign DeleteChild. A private child DACL cannot make those ancestors safe.
    parent = windows_native_temp_parent() if os.name == "nt" else None
    root = Path(tempfile.mkdtemp(prefix=prefix, dir=parent)).resolve(strict=True)
    require(root.is_dir() and not root.is_symlink() and not any(root.iterdir()), "fresh_empty_TEST_root_required")
    if os.name == "nt":
        source = Path(__file__).with_name("gemini_native_e2e.py")
        spec = importlib.util.spec_from_file_location("pi_windows_private_root", source)
        fixture = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(fixture)
        fixture.private_windows_lab(root)
    else:
        root.chmod(0o700)
    return root


def observe_unchanged_focus(observe, expected, stop_capture, *, drain_seconds, seconds=15):
    """Sample native focus for the full click window, with a separate audio stop.

    Platform adapters supply only their actual window/TTY/keyboard identity.
    An already focused decoy is the baseline; it cannot end observation early.
    """
    started, samples, capture_stopped = time.monotonic(), 0, False
    while True:
        observed = observe()
        require(observed == expected, "disabled_or_stale_click_changed_foreground_or_keyboard")
        samples += 1
        elapsed = time.monotonic() - started
        if elapsed >= drain_seconds and not capture_stopped:
            stop_capture()
            capture_stopped = True
        if elapsed >= seconds:
            return {"elapsed_seconds": round(elapsed, 3), "samples": samples, **observed}
        time.sleep(0.05)


def capture(payload):
    """The actual custom webhook contract, projected to non-private classes."""
    require(isinstance(payload, dict) and set(payload) == {
        "schema_version", "status", "notification_type", "agent_source", "message",
        "timestamp", "session_id", "source", "title"}, "pi_webhook_field_whitelist")
    status = payload["status"]
    require(isinstance(status, str) and status in COPY, "pi_webhook_status")
    require(payload["schema_version"] == "1.0" and payload["notification_type"] == status
            and payload["agent_source"] == "pi" and payload["source"] == "claude-notifications"
            and payload["title"] == "Pi" and payload["message"] == COPY[status]
            and payload["session_id"] == "", "pi_webhook_private_copy")
    require(isinstance(payload["timestamp"], str) and re.fullmatch(
        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})", payload["timestamp"]),
        "pi_webhook_timestamp")
    return status


class _LoopbackHTTPServer(http.server.ThreadingHTTPServer):
    def server_bind(self):
        # Preserve HTTPServer lifecycle/settings without resolving an unused name.
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = self.server_address[:2]


class Webhooks:
    def __init__(self, hold_response=False):
        self.events, self.failures, self.rows = queue.Queue(), [], []
        self.release = threading.Event()
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                try:
                    size = int(self.headers.get("content-length", "0"))
                    require(self.path == "/TEST-pi" and 0 < size <= 16384, "pi_webhook_request_shape")
                    status = capture(json.loads(self.rfile.read(size)))
                    request_id = self.headers.get("X-Request-ID", "")
                    if not re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", request_id):
                        request_id = ""  # Retain no arbitrary headers in evidence.
                    row = {"status": status, "received_monotonic": time.monotonic(), "request_id": request_id}
                    owner.rows.append(row)
                    owner.events.put(row)
                    if hold_response:
                        owner.release.wait(20)
                    self.send_response(204)
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                except (ValueError, RuntimeError) as error:
                    owner.failures.append(str(error))
                    self.send_error(400)
                except (BrokenPipeError, ConnectionResetError):
                    pass  # A bounded product timeout may close the held request.

        self.server = _LoopbackHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = False
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port) + "/TEST-pi"

    def wait(self, expected, timeout=20):
        try:
            row = self.events.get(timeout=timeout)
        except queue.Empty:
            raise RuntimeError("installed_pi_webhook_timeout")
        require(not self.failures and row["status"] == expected, "installed_pi_webhook_wrong_or_private")
        return row

    def quiet(self, seconds=0.5):
        try:
            self.events.get(timeout=seconds)
        except queue.Empty:
            require(not self.failures, "installed_pi_webhook_private_payload")
            return
        raise RuntimeError("installed_pi_unexpected_or_duplicate_webhook")

    def close(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()  # Collects the non-daemon request threads.
        self.thread.join(timeout=2)
        require(not self.thread.is_alive(), "installed_pi_webhook_server_not_collected")


def configure(path, defaults, sink, *, desktop=True, sound=True, click=True):
    cfg = json.loads(Path(defaults).read_text(encoding="utf-8"))
    cfg["schemaVersion"] = 2
    cfg["notifications"]["desktop"].update(enabled=desktop, sound=sound, clickToFocus=click,
                                              terminalBell=False, volume=1.0)
    cfg["notifications"]["webhook"].update(enabled=True, preset="custom", url=sink.url, format="json",
                                              headers={}, payloadFields={})
    # The producer/consumer must not leak configured templates through Pi's
    # fixed private webhook body. These are TEST strings, never user content.
    for status in COPY:
        cfg["statuses"][status]["title"] = "TEST private configured title"
    # Windows 0700 installs a protected OWNER RIGHTS DACL. Inherit the
    # explicit current-user/SYSTEM/Admin ACL already set on our TEST root.
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o777 if os.name == "nt" else 0o700)
    path.write_text(json.dumps(cfg) + "\n", encoding="utf-8")
    path.chmod(0o600)


def installed(control, profile):
    """Observe public installer receipts without supplying consumer authority."""
    ledger = json.loads((control / "ownership.json").read_text(encoding="utf-8"))
    bindings = [json.loads(c["Registration"]) for key, c in ledger["Consumers"].items()
                if key.startswith("portable:") and c.get("Registration")]
    bindings = [b for b in bindings if b["integration"] == "pi"]
    require(len(bindings) == 1, "installed_pi_binding_not_unique")
    binding = bindings[0]
    require(Path(binding["controlRoot"]) == control, "installed_pi_control_root_mismatch")
    state = json.loads((control.parent / "uap/state/state-v2.json").read_text(encoding="utf-8"))
    clients = [c for i in state["installations"] if i["installation_id"] == binding["installationID"]
               for c in i["clients"].values() if c["client_binding_id"] == binding["bindingID"]]
    require(len(clients) == 1, "installed_pi_uap_binding_not_unique")
    client = clients[0]
    require(Path(client["native_profile_root"]) == profile, "installed_pi_env_profile_not_selected")
    active = Path(client["target_locator"])
    require(active.parent == profile / "extensions" and not active.name.startswith(".")
            and re.fullmatch(r"agent-notify-[0-9a-f]{12}", active.name), "installed_pi_active_leaf_not_visible")
    require(client["activation"] == "active", "installed_pi_uap_not_active")
    data = Path(binding["dataRoot"])
    runtime = json.loads((data / "pi-runtime.json").read_text(encoding="utf-8"))
    require(set(runtime) == {"schema_version", "locator", "executable", "primary_sha256", "loaded_revision"}
            and runtime["schema_version"] == 1, "installed_pi_runtime_shape")
    require(Path(runtime["executable"]) == Path(binding["runtimeRoot"]) / binding["primary"]
            and sha(runtime["executable"]) == runtime["primary_sha256"], "installed_pi_runtime_executable_mismatch")
    require(runtime["loaded_revision"]["binding_id"] == binding["bindingID"], "installed_pi_runtime_binding_mismatch")
    return {"binding": binding, "active": active, "runtime": runtime, "generation": ledger["Generation"]}


def asset_receipts(control, runtime, canonical):
    ledger = json.loads((control / "ownership.json").read_text(encoding="utf-8"))
    result = {}
    for name in ("claude_icon.png", "sounds/task-complete.mp3", "sounds/question.mp3"):
        path = runtime / name
        expected = sha(canonical / name)
        owned = ledger["Files"].get(str(path))
        require(owned and owned["Exists"] is True and not owned.get("Link")
                and owned["SHA256"] == expected and sha(path) == expected, "installed_pi_asset_not_receipt_owned")
        result[name] = expected
    require(len(ledger["Consumers"]) == 1 and all(key.startswith("portable:") for key in ledger["Consumers"]),
            "pi_setup_retained_unrelated_or_preparation_consumer")
    return result


def setup_config_diagnostic(executable, root, env):
    """Inspect only this TEST config; expose typed native security metadata."""
    try:
        root = Path(root)
        require(root.is_absolute() and root.name.startswith("TEST-pi-")
                and root.resolve(strict=True) == root and root.is_dir()
                and (root / "project").is_dir(), "setup_config_diagnostic_TEST_root_required")
        selected = dict(env, PLUGIN_ROOT=str(root), AGENT_NOTIFICATIONS_CONFIG=str(root / "global/config.json"))
        with tempfile.TemporaryFile() as output:
            subprocess.run([str(executable), "config", "inspect", "--json"], stdin=subprocess.DEVNULL,
                           stdout=output, stderr=subprocess.DEVNULL, cwd=root / "project", env=selected, timeout=15)
            output.seek(0)
            raw = output.read(65537)
        require(len(raw) <= 65536, "setup_config_diagnostic_output_limit")
        value = json.loads(raw)
        require(isinstance(value, dict) and isinstance(value.get("errorCode"), str)
                and re.fullmatch(r"Config[A-Za-z]{1,48}", value["errorCode"]), "setup_config_diagnostic_error_code")
        result = {"errorCode": value["errorCode"]}
        detail = value.get("permissionFailure")
        if isinstance(detail, dict):
            safe = {}
            for key, pattern, limit in (("objectPath", r"[^\x00-\x1f]+", 32768), ("reason", r"[a-z_]+", 128),
                                        ("trusteeSid", r"S-\d+(?:-\d+)+", 256), ("ownerSid", r"S-\d+(?:-\d+)+", 256)):
                field = detail.get(key)
                if isinstance(field, str) and 0 < len(field) <= limit and re.fullmatch(pattern, field):
                    safe[key] = field
            mask = detail.get("accessMask")
            if type(mask) is int and 0 <= mask <= 0xFFFFFFFF:
                safe["accessMask"] = mask
            if safe:
                result["permissionFailure"] = safe
        return result
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
        return {}


def setup(executable, operation, root, package, pi, env, extra=()):
    """Use only the existing public common setup wizard; no explicit Pi root."""
    argv = [executable, "setup-notifications", "wizard", "--action", operation, "--agents", "pi",
            "--package", package, "--control-root", root / "control", "--runtime-root", root / "runtime",
            "--global-config", root / "global/config.json", "--yes", "--json", *extra]
    if pi is not None:
        argv += ["--pi-executable", pi]
    result = subprocess.run([str(x) for x in argv], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, cwd=root / "project", env=env, timeout=180)
    require(len(result.stdout) + len(result.stderr) <= 4 * 1024 * 1024, "installed_pi_setup_output_limit")
    try:
        value = json.loads(result.stdout)
    except ValueError:
        raise RuntimeError("installed_pi_setup_not_json:" + result.stderr.decode(errors="replace")[-2000:])
    if result.returncode != 0 or value.get("outcome") not in ("completed", "unchanged"):
        diagnostic = setup_config_diagnostic(executable, root, env) if value.get("reason") == "global_config_parent_invalid" else {}
        suffix = ":config_inspect=" + json.dumps(diagnostic, sort_keys=True) if diagnostic else ""
        raise RuntimeError("installed_pi_setup_failed:" + value.get("reason", "unknown") + suffix)
    return value
