#!/usr/bin/env python3
"""Credential-free Gemini 0.62.0 TEST fixture; native execution is orchestrator-only.

Red: missing CLI-emitted events/UI, nonneutral stdout, unsafe roots, wrong TEST
file effects, request/output overflow, or unconfirmed child shutdown. G0 uses
our recorder. G5 may reuse Fixture/Terminal/exercise with production-installed
hooks and its own delivery observer; a recorder run never claims G5 completion.
"""
import argparse
import hashlib
import http.server
import json
import math
import os
from pathlib import Path
import queue
import re
import shlex
import socket
import signal
import stat
import subprocess
import sys
import threading
import time
from urllib.parse import urlsplit

VERSION = "0.62.0"
SOURCE = "b460678f3db508407554afd604cc9d6635becb2a"
LIMIT = 1024 * 1024
MARKER = ".an-gemini-TEST"
CASES = ("plain", "equal", "approve", "deny", "cancel", "recovery")


class Red(Exception):
    pass


def require(ok, classification):
    if not ok:
        raise Red(classification)


def digest(data):
    return hashlib.sha256(data).hexdigest()


NODE_ERROR_CODES = frozenset(("ERR_DLOPEN_FAILED", "MODULE_NOT_FOUND", "ERR_MODULE_NOT_FOUND",
                             "ERR_PACKAGE_PATH_NOT_EXPORTED", "ERR_REQUIRE_ESM", "ERR_INVALID_ARG_TYPE",
                             "ERR_INVALID_ARG_VALUE", "ENOENT", "EACCES", "EPERM", "EINVAL", "ENOEXEC"))


def version_probe(code, out, err, redactions=()):
    """Only for the isolated public --version startup, never a session/PTY stream."""
    codes = sorted(c for c in NODE_ERROR_CODES if re.search(rb"\b" + c.encode() + rb"\b", err))
    facts = {"exit_code": code, "stdout_bytes": len(out), "stdout_sha256": digest(out),
             "stderr_bytes": len(err), "stderr_sha256": digest(err), "Node_error_codes": codes}
    if code == 0:
        return facts
    facts["startup_classification"] = "node_startup_error" if codes else "unclassified_public_startup_error"
    if "ENOENT" in codes:
        # Fixed public startup operations only; never emit arbitrary file names.
        operations = ("spawn", "spawnSync", "open", "mkdir", "stat", "lstat", "access", "scandir", "chdir", "realpath", "dlopen", "uv_cwd", "uv_os_get_passwd", "uv_os_homedir", "uv_exepath")
        facts["missing_operations"] = sorted(op for op in operations
            if re.search(rb"\b" + op.encode() + rb"\b", err))
        commands = ("git", "ioreg", "security", "uname", "whoami", "bash", "zsh", "node", "rg", "sysctl")
        facts["missing_known_commands"] = sorted(command for command in commands
            if re.search(rb"\bspawn(?:Sync)? (?:[^\r\n ]*/)?" + command.encode() + rb" ENOENT\b", err))
    # One bounded Error header only; stack, source excerpt and all other lines
    # are excluded. Do not expose session/provider/hook text even in this probe.
    text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", err.decode("utf-8", "replace"))
    lines = (line.removeprefix("An unexpected critical error occurred:").lstrip().removeprefix("[") for line in text.splitlines())
    header = r"(?:(?:[A-Za-z]*Error)(?: \[[A-Z_]+\])?|ENOENT|EACCES|EPERM|EINVAL|ENOEXEC):"
    line = next((match.group(0).strip() for candidate in lines
                 if (match := re.match(r"^\s*" + header + r"[^\r\n]*", candidate))), "")
    if re.fullmatch(r"ReferenceError: (?:File|Blob|ReadableStream|fetch|crypto|navigator) is not defined", line):
        facts["startup_classification"] = "public_runtime_global_missing"
    if not line or len(line) > 512:
        facts["startup_header"] = "absent" if not line else "overlong"
        return facts
    for root in sorted((str(p) for p in redactions), key=len, reverse=True):
        for spelling in {root, root.replace("\\", "/"), root.replace("/", "\\")}:
            line = re.sub(re.escape(spelling), "<TEST-path>", line, flags=re.I)
    line = re.sub(r"(?:https?|file)://[^\s\"'<>]+", "<URL>", line, flags=re.I)
    line = re.sub(r"\b[A-Za-z]:[\\/][^\r\n\"'<>]+", "<path>", line)
    line = re.sub(r"(?<![A-Za-z0-9])/(?:[^\s\"'<>:]+)", "<path>", line)
    line = re.sub(r"<TEST-path>[\\/][^\r\n\"'<>]*", "<TEST-path>", line)
    if re.search(r"prompt|session|transcript|provider|hook|authorization|api.?key|token|secret", line, re.I):
        facts["startup_header"] = "sensitive"
        return facts
    facts["startup_header"] = "accepted" if all(32 <= ord(c) <= 126 for c in line) else "nonascii"
    if facts["startup_header"] == "accepted":
        facts["startup_error_line"] = line[:240]
    return facts


BRIDGE_ERRORS = frozenset(("bridge_validation_error", "bridge_node_error", "bridge_syntax_error",
                          "bridge_unclassified_error", "bridge_protocol_error", "PTY_stop_failed",
                          "PTY_shutdown_unconfirmed", "native_watchdog_timeout", "terminal_output_limit"))
BRIDGE_STAGES = frozenset(("protocol", "installation_validation", "native_module_load",
                          "environment_validation", "native_spawn", "loaded_backend_validation"))
BRIDGE_VALIDATIONS = frozenset(("duplicate_start", "TEST_installation_required", "CLI_physical_path_required",
    "CLI_package_missing", "CLI_PTY_pin_mismatch", "PTY_outside_explicit_installation", "PTY_identity_mismatch",
    "environment_not_allowlisted", "TEST_cwd_required", "home_mismatch", "synthetic_key_required",
    "loopback_provider_required", "watchdog_bounds", "native_PTY_backend_unverified",
    "bridge_input_limit", "unknown_case", "write_bounds", "unknown_operation"))


def bridge_event_facts(item):
    """Project only fixed bridge facts; never trust arbitrary native error fields."""
    facts = {"error": item.get("error") if item.get("error") in BRIDGE_ERRORS else "bridge_protocol_error"}
    if item.get("stage") in BRIDGE_STAGES:
        facts["stage"] = item["stage"]
    if type(item.get("native_child_started")) is bool:
        facts["native_child_started"] = item["native_child_started"]
    if facts["error"] == "bridge_node_error" and item.get("node_error_code") in NODE_ERROR_CODES:
        facts["node_error_code"] = item["node_error_code"]
    if facts["error"] == "bridge_validation_error" and item.get("validation") in BRIDGE_VALIDATIONS:
        facts["validation"] = item["validation"]
    return facts


def physical(value, exists=True):
    p = Path(value)
    require(p.is_absolute(), "absolute_path_required")
    resolved = p.resolve(strict=exists)
    require(p == resolved, "physical_path_required")
    require(not any(x in str(p) for x in ("$", "`", "%", "\n", "\r", '"')), "unsupported_path")
    return resolved


def inside(p, root):
    return p == root or root in p.parents



def private_windows_lab(root):
    """Give this just-created TEST root a protected, inherited native DACL."""
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    api = ctypes.WinDLL("advapi32", use_last_error=True)
    pointer = ctypes.c_void_p
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    kernel.CloseHandle.argtypes = (wintypes.HANDLE,)
    kernel.LocalFree.argtypes = (pointer,)
    kernel.LocalFree.restype = pointer
    api.OpenProcessToken.argtypes = (wintypes.HANDLE, wintypes.DWORD, ctypes.POINTER(wintypes.HANDLE))
    api.GetTokenInformation.argtypes = (wintypes.HANDLE, ctypes.c_int, pointer, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD))
    api.ConvertSidToStringSidW.argtypes = (pointer, ctypes.POINTER(pointer))
    api.ConvertStringSecurityDescriptorToSecurityDescriptorW.argtypes = (wintypes.LPCWSTR, wintypes.DWORD,
                                                                       ctypes.POINTER(pointer), ctypes.POINTER(wintypes.DWORD))
    api.GetSecurityDescriptorDacl.argtypes = (pointer, ctypes.POINTER(wintypes.BOOL), ctypes.POINTER(pointer), ctypes.POINTER(wintypes.BOOL))
    api.SetNamedSecurityInfoW.argtypes = (wintypes.LPWSTR, ctypes.c_int, wintypes.DWORD, pointer, pointer, pointer, pointer)
    api.SetNamedSecurityInfoW.restype = wintypes.DWORD
    api.GetNamedSecurityInfoW.argtypes = (wintypes.LPWSTR, ctypes.c_int, wintypes.DWORD, pointer, pointer,
                                        pointer, pointer, ctypes.POINTER(pointer))
    api.GetNamedSecurityInfoW.restype = wintypes.DWORD
    api.GetSecurityDescriptorControl.argtypes = (pointer, ctypes.POINTER(wintypes.WORD), ctypes.POINTER(wintypes.DWORD))
    token, sid_text, descriptor, observed = wintypes.HANDLE(), pointer(), pointer(), pointer()
    try:
        require(api.OpenProcessToken(kernel.GetCurrentProcess(), 0x0008, ctypes.byref(token)), "TEST_ACL_token_failed")
        size = wintypes.DWORD()
        api.GetTokenInformation(token, 1, None, 0, ctypes.byref(size))
        require(0 < size.value < 65536, "TEST_ACL_token_size")
        data = ctypes.create_string_buffer(size.value)
        require(api.GetTokenInformation(token, 1, data, size.value, ctypes.byref(size)), "TEST_ACL_user_failed")
        sid = ctypes.cast(data, ctypes.POINTER(pointer))[0]
        require(api.ConvertSidToStringSidW(sid, ctypes.byref(sid_text)), "TEST_ACL_SID_failed")
        current = ctypes.wstring_at(sid_text)
        sddl = "O:" + current + "D:P(A;OICI;FA;;;" + current + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
        require(api.ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, ctypes.byref(descriptor), None),
                "TEST_ACL_descriptor_failed")
        present, defaulted, dacl = wintypes.BOOL(), wintypes.BOOL(), pointer()
        require(api.GetSecurityDescriptorDacl(descriptor, ctypes.byref(present), ctypes.byref(dacl), ctypes.byref(defaulted))
                and present.value and dacl, "TEST_ACL_DACL_missing")
        # The modern API explicitly supports protection; the legacy SetFileSecurity
        # call did not qualify descendant inheritance on the actual Windows runner.
        require(api.SetNamedSecurityInfoW(str(root), 1, 0x80000005, sid, None, dacl, None) == 0,
                "TEST_ACL_apply_failed")
        require(api.GetNamedSecurityInfoW(str(root), 1, 4, None, None, None, None, ctypes.byref(observed)) == 0,
                "TEST_ACL_readback_failed")
        control, revision = wintypes.WORD(), wintypes.DWORD()
        require(api.GetSecurityDescriptorControl(observed, ctypes.byref(control), ctypes.byref(revision))
                and control.value & 0x1000, "TEST_ACL_protection_missing")
    finally:
        if observed:
            kernel.LocalFree(observed)
        if descriptor:
            kernel.LocalFree(descriptor)
        if sid_text:
            kernel.LocalFree(sid_text)
        if token:
            kernel.CloseHandle(token)


def new_lab(value):
    p = physical(value, False)
    require(any(re.fullmatch(r"TEST(?:[-_].*)?", x) for x in p.parts), "TEST_root_required")
    require(not p.exists(), "lab_must_be_new")
    require(not inside(p, Path.home().resolve()), "inherited_home_forbidden")
    repo = Path(__file__).resolve().parents[2]
    require(not inside(p, repo) and not inside(p, Path.cwd().resolve()), "repository_cwd_forbidden")
    require(p.parent.is_dir(), "lab_parent_missing")
    require(not any((ancestor / ".git").exists() for ancestor in p.parents), "existing_repository_ancestor_forbidden")
    p.mkdir(mode=0o700)
    if os.name == "nt":
        private_windows_lab(p)
    (p / MARKER).write_text("owned disposable Gemini native TEST\n")
    for name in ("profile/.gemini", "tmp", "xdg/config", "xdg/cache", "xdg/data", "xdg/state", "an-control", "an-runtime"):
        directory = p / name
        directory.mkdir(parents=True, mode=0o700)
        if os.name == "nt":
            private_windows_lab(directory)
    for name in ("profile/.env", "profile/.gemini/.env", "profile/GEMINI.md"):
        (p / name).write_text("")
    # Actual empty git repository, without invoking git or changing history.
    gitdir = p / "profile/.git"
    (gitdir / "objects").mkdir(parents=True)
    (gitdir / "refs/heads").mkdir(parents=True)
    (gitdir / "HEAD").write_text("ref: refs/heads/TEST\n")
    (gitdir / "config").write_text("[core]\n\trepositoryformatversion = 0\n\tbare = false\n")
    for name in ("system.json", "defaults.json", "trusted.json"):
        (p / name).write_text("{}\n")
    (p / "events.jsonl").write_text("")
    return p


def minimal_env(lab, node, shell, system_root=None):
    profile = str(lab / "profile")
    bins = [str(shell.parent), str(node.parent), str(Path(sys.executable).resolve().parent)]
    env = {"HOME": profile, "USERPROFILE": profile, "GEMINI_CLI_HOME": profile,
           "XDG_CONFIG_HOME": str(lab / "xdg/config"), "XDG_CACHE_HOME": str(lab / "xdg/cache"),
           "XDG_DATA_HOME": str(lab / "xdg/data"), "XDG_STATE_HOME": str(lab / "xdg/state"),
           "TMPDIR": str(lab / "tmp"), "TMP": str(lab / "tmp"), "TEMP": str(lab / "tmp"),
           "GEMINI_CLI_SYSTEM_SETTINGS_PATH": str(lab / "system.json"),
           "GEMINI_CLI_SYSTEM_DEFAULTS_PATH": str(lab / "defaults.json"),
           "GEMINI_CLI_TRUSTED_FOLDERS_PATH": str(lab / "trusted.json"),
           "GEMINI_API_KEY": "an-gemini-test-not-a-secret", "GEMINI_FORCE_FILE_STORAGE": "true", "TERM": "xterm-256color"}
    if os.name == "nt":
        require(system_root is not None and shell.name.lower() == "pwsh.exe", "Windows_requires_explicit_pwsh_and_SystemRoot")
        env.update(SystemRoot=str(physical(system_root)), ComSpec=str(shell), PATHEXT=".COM;.EXE;.BAT;.CMD")
        bins.append(str(Path(system_root) / "System32"))
    else:
        require(shell.name == "bash", "Unix_requires_bash")
        bins += ["/usr/bin", "/bin", "/usr/sbin"]
        env.update(LANG="C.UTF-8", LC_ALL="C.UTF-8")
    env["PATH"] = os.pathsep.join(dict.fromkeys(bins))
    return env


def package_digest(directory):
    tree, size = hashlib.sha256(), 0
    files = sorted(p for p in directory.rglob("*") if p.is_file())
    require(len(files) <= 6000, "package_file_limit")
    for file in files:
        require(not file.is_symlink(), "package_symlink_unqualified")
        size += file.stat().st_size
        require(size <= 512 * LIMIT, "package_size_limit")
        tree.update(str(file.relative_to(directory)).replace(os.sep, "/").encode() + b"\0")
        with file.open("rb") as stream:
            content = hashlib.sha256()
            while block := stream.read(65536):
                content.update(block)
        tree.update(content.digest())
    return tree.hexdigest()


def cli_identity(executable, install_root):
    root = physical(install_root)
    require((root / MARKER).is_file(), "explicit_TEST_installation_marker_missing")
    require(any(re.fullmatch(r"TEST(?:[-_].*)?", x) for x in root.parts), "TEST_installation_required")
    require(not inside(root, Path.home().resolve()) and not inside(root, Path(__file__).resolve().parents[2]), "borrowed_installation_forbidden")
    exe = physical(executable)
    require(inside(exe, root) and exe.is_file(), "CLI_outside_TEST_installation")
    for parent in exe.parents:
        if not inside(parent, root):
            break
        package = parent / "package.json"
        if package.is_file():
            data = json.loads(package.read_text())
            if data.get("name") == "@google/gemini-cli":
                require(data.get("version") == VERSION, "wrong_CLI_version")
                require(data.get("optionalDependencies", {}).get("@lydell/node-pty") == "1.1.0", "wrong_PTY_pin")
                return {"version": VERSION, "source_commit": SOURCE,
                        "package_json_sha256": digest(package.read_bytes()), "installed_package_tree_sha256": package_digest(parent),
                        "bundle_sha256": digest(exe.read_bytes())}
    raise Red("CLI_package_identity_missing")


def response(parts):
    return {"candidates": [{"index": 0, "content": {"role": "model", "parts": parts}, "finishReason": "STOP"}],
            "usageMetadata": {"promptTokenCount": 8, "candidatesTokenCount": 8, "totalTokenCount": 16}}


class Fixture:
    """One bounded loopback provider/capture, reusable for built candidate G5.

    No outbound client, hook invocation, hook stdin injection, or transcript log.
    A caller may supply a fixed-payload capture_validator for actual G5 delivery.
    """
    def __init__(self, lab, capture_validator=None):
        self.lab, self.capture_validator = lab, capture_validator
        self.lock = threading.Lock()
        self.case, self.calls, self.requests = "plain", 0, 0
        self.counts, self.deliveries, self.error = {}, [], None
        self.server = self.thread = self.connection = None
        self.closed = threading.Event()
        self.cleanup_classification = None

    def arm(self, case):
        require(case in CASES, "unknown_case")
        (self.lab / "case.json").write_text(json.dumps({"case": case}))
        with self.lock:
            self.case, self.calls = case, 0

    def answer(self, path, body):
        with self.lock:
            self.requests += 1
            require(self.requests <= 128, "provider_request_limit")
            if path == "/capture":
                require(self.capture_validator is not None, "capture_contract_missing")
                # Validator returns ONLY a fixed allowlisted classification.
                result = self.capture_validator(body)
                require(result in ("task_complete", "permission_request"), "capture_contract_failed")
                self.deliveries.append(result)
                return {}, False
            match = re.fullmatch(r"/(?:v1beta|v1)/models/[A-Za-z0-9_.-]+:(streamGenerateContent|generateContent|countTokens)", path)
            require(match is not None, "unsupported_provider_endpoint")
            method = match[1]
            self.counts[method] = self.counts.get(method, 0) + 1
            if method == "countTokens":
                return {"totalTokens": 16}, False
            require(isinstance(body.get("contents"), list), "provider_contents_missing")
            # Nonstream ancillary calls do not consume the interactive stream script.
            if method == "generateContent":
                require(self.counts[method] <= 16, "ancillary_generation_limit")
                return response([{"text": "TEST turn finished."}]), False
            self.calls += 1
            require(self.calls <= 4, "unexpected_agent_loop")
            if self.case in ("approve", "deny", "cancel") and self.calls == 1:
                target = self.lab / "profile" / ("effect-" + self.case + ".txt")
                return response([{"functionCall": {"name": "write_file", "args": {
                    "file_path": str(target), "content": "owned TEST effect\n"}}}]), True
            return response([{"text": "TEST turn finished."}]), True

    def __enter__(self):
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def setup(self):
                super().setup()
                self.connection.settimeout(3)
                fixture.connection = self.connection

            def finish(self):
                try:
                    super().finish()
                finally:
                    fixture.connection = None

            def log_message(self, *_):
                pass

            def do_POST(self):
                self.connection.settimeout(3)
                try:
                    require(not self.headers.get("Transfer-Encoding"), "chunked_request_not_supported")
                    size = int(self.headers.get("Content-Length", "0"))
                    require(0 < size <= LIMIT, "provider_body_limit")
                    raw = self.rfile.read(size)
                    require(len(raw) == size, "incomplete_provider_body")
                    body = json.loads(raw)
                    require(isinstance(body, dict), "provider_object_required")
                    result, stream = fixture.answer(urlsplit(self.path).path, body)
                    data = json.dumps(result).encode()
                    if stream:
                        data = b"data: " + data + b"\n\n"
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream" if stream else "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    self.send_header("Connection", "close")
                    self.end_headers()
                    self.wfile.write(data)
                except Exception as exc:
                    fixture.error = str(exc) if isinstance(exc, Red) else "provider_protocol_error"
                    self.close_connection = True

        class Server(http.server.HTTPServer):
            def handle_error(self, *_):
                fixture.error = "server_request_error"  # Never emit a raw traceback.

        # Single server thread, socket deadline, capped request count; no thread per request.
        self.server = Server(("127.0.0.1", 0), Handler)
        self.server.timeout = 0.2
        def serve():
            while not self.closed.is_set():
                try:
                    self.server.handle_request()
                except OSError:
                    if not self.closed.is_set():
                        self.error = "server_socket_error"
        self.thread = threading.Thread(target=serve, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port)
        return self

    def __exit__(self, exc_type, exc, traceback):
        self.closed.set()
        connection = self.connection
        if connection is not None:
            try:
                connection.shutdown(socket.SHUT_RDWR)
                connection.close()
            except OSError:
                pass
        self.server.server_close()
        self.thread.join(4)
        if self.thread.is_alive():
            self.cleanup_classification = "server_shutdown_unconfirmed"
            if exc is None:
                raise Red(self.cleanup_classification)
            # Preserve the scenario/setup exception and attach fixed cleanup facts.
            exc.provider_cleanup_classification = self.cleanup_classification


def bounded_process(argv, data, cwd, env, timeout):
    """Bound both streams without ever writing raw child output to disk/stdout."""
    p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=os.name != "nt")
    chunks, overflow = [bytearray(), bytearray()], threading.Event()
    stopping = threading.Event()

    def reader(stream, index):
        fd = stream.fileno()
        try:
            while not stopping.is_set():
                part = os.read(fd, 4096)
                if not part:
                    return
                if len(chunks[index]) + len(part) > 65536:
                    overflow.set()
                    p.kill()
                    return
                chunks[index].extend(part)
        except OSError:
            if not stopping.is_set():
                overflow.set()

    readers = [threading.Thread(target=reader, args=(s, i), daemon=True) for i, s in enumerate((p.stdout, p.stderr))]
    for t in readers:
        t.start()
    def writer():
        try:
            view = memoryview(data)
            fd = p.stdin.fileno()
            while view and not stopping.is_set():
                written = os.write(fd, view[:4096])
                view = view[written:]
        except (BrokenPipeError, OSError):
            pass
        finally:
            p.stdin.close()

    input_thread = threading.Thread(target=writer, daemon=True)
    input_thread.start()
    try:
        code = p.wait(timeout=timeout)
        input_thread.join(1)
        require(not input_thread.is_alive(), "child_stdin_timeout")
        for t in readers:
            t.join(1)
        require(not overflow.is_set() and not any(t.is_alive() for t in readers), "child_output_limit")
        return code, bytes(chunks[0]), bytes(chunks[1])
    finally:
        if os.name != "nt":
            try:
                os.killpg(p.pid, signal.SIGKILL)  # Only our new session/process group.
            except ProcessLookupError:
                pass
        elif p.poll() is None:
            p.kill()
        p.wait(timeout=2)
        stopping.set()
        input_thread.join(1)
        for t in readers:
            t.join(1)
        for s in (p.stdin, p.stdout, p.stderr):
            s.close()


def record_hook(args):
    lab = physical(args.record_root)
    require((lab / MARKER).is_file() and Path.cwd().resolve() == lab / "profile", "hook_root_mismatch")
    result = {"event": args.event, "valid": False, "neutral": False}
    try:
        # Deadline also works on Windows pipes; this daemon has no external state.
        incoming = queue.Queue()
        def read_payload():
            data = bytearray()
            try:
                while len(data) <= LIMIT:
                    part = os.read(0, min(4096, LIMIT + 1 - len(data)))
                    if not part:
                        break
                    data.extend(part)
                incoming.put(bytes(data))
            except OSError:
                incoming.put(b"")
        threading.Thread(target=read_payload, daemon=True).start()
        raw = incoming.get(timeout=1)
        if getattr(args, "capture_frames", False):
            # Only the foreign G5 TEST recorder captures synthetic raw frames.
            # Additional I/O changes its duration; no managed command changes.
            started = time.monotonic()
            try:
                from gemini_frame_capture import capture
                result["frame_capture"] = capture(lab, args.event, raw)
            except Exception:
                result["frame_capture"] = "unavailable"
            result["frame_capture_ms"] = (time.monotonic() - started) * 1000
        require(len(raw) <= LIMIT, "hook_payload_limit")
        value = json.loads(raw)
        require(isinstance(value, dict) and value.get("hook_event_name") == args.event, "hook_event_mismatch")
        require(isinstance(value.get("session_id"), str) and value["session_id"], "hook_session_missing")
        require(value.get("cwd") == str(lab / "profile"), "hook_cwd_mismatch")
        require(isinstance(value.get("timestamp"), str) and value["timestamp"], "hook_timestamp_missing")
        if args.event == "AfterAgent":
            require(type(value.get("stop_hook_active")) is bool, "hook_stop_flag_missing")
            result["stop_hook_active"] = value["stop_hook_active"]
        else:
            require(value.get("notification_type") == "ToolPermission", "hook_subtype_mismatch")
            result["subtype"] = "ToolPermission"
        shell = re.fullmatch(r"(bash|powershell):([0-9][0-9A-Za-z.()_-]*)", args.native_shell or "")
        require(shell is not None, "actual_hook_shell_missing")
        result.update(valid=True, payload_sha256=digest(raw), session_sha256=digest(value["session_id"].encode()),
                      timestamp_sha256=digest(value["timestamp"].encode()), shell=shell[1], shell_version=shell[2])
        commands = json.loads((lab / "probe.json").read_text())
        if args.event in commands:
            require(os.name != "nt", "Windows_probe_process_tree_unqualified")
            child_env = minimal_env(lab, physical(args.node_executable), physical(args.hook_shell), args.system_root)
            port = int((lab / "provider-port").read_text())
            require(0 < port < 65536, "provider_port_invalid")
            child_env["GOOGLE_GEMINI_BASE_URL"] = "http://127.0.0.1:" + str(port)
            code, out, err = bounded_process(commands[args.event], raw, lab / "profile", child_env, 3)
            result.update(neutral=code == 0 and out.strip() == b"{}", exit_zero=code == 0,
                          stdout_sha256=digest(out), stderr_empty=not err)
        else:
            result["neutral"] = True
        result["case"] = json.loads((lab / "case.json").read_text())["case"]
    except Exception as exc:
        result["classification"] = str(exc) if isinstance(exc, Red) else "hook_protocol_error"
    finally:
        os.close(0)  # Our hook stdin only; raw reader has no buffered-I/O finalizer lock.
    fd = os.open(lab / "events.jsonl", os.O_WRONLY | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0))
    try:
        os.write(fd, (json.dumps(result, separators=(",", ":")) + "\n").encode())
    finally:
        os.close(fd)
    print("{}", flush=True)  # Advisory even when a red classification was recorded.


def capture_sdk_hook_outcomes(lab):
    """Pre-teardown prefix only: SDK flush is unknown; absence is not non-invocation."""
    facts = dict(capture="unavailable", identity_checked=False, snapshot_stable=False,
                 parse_complete=False, SDK_flush_complete=False, absence_means="unknown",
                 records=0, owned_calls=0, unjoined_owned_calls=0, outcomes=[])
    owned_inputs = []  # Private memory only; never part of the closed projection.
    try:
        path = lab / "sdk-private/telemetry.json"
        before = path.lstat()
        require(path.resolve() == path and stat.S_ISREG(before.st_mode) and before.st_nlink == 1
                and (os.name == "nt" or before.st_uid == os.getuid() and not before.st_mode & 0o077)
                and before.st_size <= 8 * LIMIT, "SDK_telemetry_identity_or_bound")
        stamp = lambda st: (st.st_dev, st.st_ino, st.st_mode, st.st_nlink, st.st_uid)
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            require(stamp(os.fstat(stream.fileno())) == stamp(before), "SDK_telemetry_open_changed")
            raw = stream.read(8 * LIMIT + 1); after = os.fstat(stream.fileno())
        require(len(raw) <= 8 * LIMIT and stamp(after) == stamp(before)
                and stamp(path.lstat()) == stamp(before), "SDK_telemetry_read_changed")
        facts.update(capture="bounded_prefix", identity_checked=True,
                     snapshot_stable=(before.st_size, before.st_mtime_ns) == (after.st_size, after.st_mtime_ns),
                     bytes=len(raw))
        # Retain the actual prefix privately even if later SDK writes/teardown append.
        snapshot = path.parent / ("pre-teardown-" + str(time.monotonic_ns()) + ".json")
        with snapshot.open("xb") as stream:
            os.chmod(snapshot, 0o600); stream.write(raw)
        rows = observations(lab)
        joined = {}
        bodies = {}
        for row in rows:
            key = (row.get("event"), row.get("session_sha256"), row.get("timestamp_sha256"))
            joined.setdefault(key, []).append(row.get("case"))
            bodies.setdefault(key, []).append(row.get("payload_sha256"))
        roles = {"agent-notifications-gemini-after-agent": "AfterAgent",
                 "agent-notifications-gemini-notification": "Notification"}
        decoder, text, offset = json.JSONDecoder(), raw.decode("utf-8"), 0
        while True:
            offset = re.compile(r"\s*").match(text, offset).end()
            if offset == len(text):
                facts["parse_complete"] = True
                break
            require(facts["records"] < 2048, "SDK_telemetry_record_bound")
            try:
                item, end = decoder.raw_decode(text, offset)
            except ValueError:
                facts["capture"] = "incomplete_or_invalid_JSON_prefix"
                break
            require(end - offset <= LIMIT and isinstance(item, dict), "SDK_telemetry_record_shape")
            facts["records"] += 1; offset = end
            attr = item.get("attributes", {})
            if not isinstance(attr, dict) or attr.get("event.name") != "gemini_cli.hook_call": continue
            role = roles.get(attr.get("hook_name"))
            if not role or attr.get("hook_type") != "command" or attr.get("hook_event_name") != role: continue
            facts["owned_calls"] += 1
            incoming = json.loads(attr.get("hook_input", "null"))
            require(isinstance(incoming, dict), "SDK_hook_input_unavailable")
            ids = [incoming.get(k) for k in ("session_id", "timestamp")]
            require(all(isinstance(v, str) and 0 < len(v) <= 4096 for v in ids), "SDK_hook_join_unavailable")
            cases = joined.get((role, *(digest(v.encode()) for v in ids)), [])
            if incoming.get("cwd") != str(lab / "profile") or incoming.get("hook_event_name") != role or len(cases) != 1 or cases[0] not in CASES:
                facts["unjoined_owned_calls"] += 1
                continue
            require(len(facts["outcomes"]) < 64, "SDK_hook_outcome_bound")
            outcome = dict(role=role, case=cases[0], claim_failures=[])
            body = attr["hook_input"].encode("utf-8")
            require(0 < len(body) <= LIMIT, "SDK_owned_input_bound")
            owned_inputs.append(dict(role=role, case=cases[0], raw=body,
                payload_sha256=digest(body), session_sha256=digest(ids[0].encode()),
                timestamp_sha256=digest(ids[1].encode()),
                foreign_payload_match=bodies.get((role, *(digest(v.encode()) for v in ids))) == [digest(body)]))
            code, duration = attr.get("exit_code"), attr.get("duration_ms")
            if type(code) is int and -256 <= code <= 65535: outcome["SDK_exit_code"] = code
            if type(duration) in (int, float) and math.isfinite(duration) and 0 <= duration <= 86400000:
                outcome["SDK_duration_ms"] = duration
            if type(attr.get("success")) is bool: outcome["SDK_success"] = attr["success"]
            stderr = attr.get("stderr", "")
            require(isinstance(stderr, str) and len(stderr) <= 65536, "SDK_hook_stderr_bound")
            pattern = (r"observation\.claim\.failure phase=(validate|path|root|prepare|lock|clock|read|decode|record|encode|publish|published) "
                       r"class=(invalid_request|invalid_path|validation|invalid_clock|none|invalid_document|deadline|canceled|os_error|sharing_violation|lock_violation|permission|not_found) "
                       r"code=(\d{1,10}) elapsed_ns=(\d{1,18}) stage_ns=(\d{1,18}) budget_ns=(\d{1,18}) "
                       r"budget=(not_started|active|deadline|canceled) publication_possible=(true|false)")
            for line in stderr.splitlines():
                match = re.fullmatch(pattern, line)
                if match and len(outcome["claim_failures"]) < 4:
                    phase, classification, oscode, elapsed, stage, budget, state, possible = match.groups()
                    outcome["claim_failures"].append(dict(phase=phase, classification=classification,
                        code=int(oscode), elapsed_ns=int(elapsed), stage_ns=int(stage), budget_ns=int(budget),
                        budget_state=state, publication_possible=possible == "true"))
            facts["outcomes"].append(outcome)
    except Exception:
        facts["capture"] = "bounded_capture_or_parse_failed"
    # Only this closed projection is copied to CI; the SDK outfile remains private.
    encoded = json.dumps(facts, sort_keys=True) + "\n"
    projection_bounded = len(encoded.encode()) <= 65536
    if not projection_bounded:
        encoded = json.dumps(dict(capture="projection_bound_exceeded", identity_checked=False,
            snapshot_stable=False, parse_complete=False, SDK_flush_complete=False,
            absence_means="unknown", records=0, owned_calls=0, unjoined_owned_calls=0,
            outcomes=[]), sort_keys=True) + "\n"
    (lab / "sdk-hook-outcomes.json").write_text(encoded)
    # A closed parsed prefix is qualified observation, never an SDK flush receipt.
    qualified = projection_bounded and facts["capture"] == "bounded_prefix" and facts["snapshot_stable"] and facts["parse_complete"]
    return {"qualified": qualified, "frames": owned_inputs if qualified else []}


def install_test_hooks(lab, commands, shell, node, system_root=None, capture_frames=False):
    (lab / "probe.json").write_text(json.dumps(commands))
    hooks = {}
    for event in ("AfterAgent", "Notification"):
        argv = [str(Path(sys.executable).resolve()), str(Path(__file__).resolve()), "--record-root", str(lab), "--event", event,
                "--node-executable", str(node), "--hook-shell", str(shell)]
        if capture_frames:
            argv += ["--capture-frames"]
        if system_root:
            argv += ["--system-root", str(physical(system_root))]
        if os.name == "nt":
            command = "& " + " ".join("'" + x.replace("'", "''") + "'" for x in argv)
            command += ' --native-shell "powershell:$($PSVersionTable.PSVersion.ToString())"'
        else:
            command = shlex.join(argv) + ' --native-shell "bash:${BASH_VERSION}"'
        group = {"hooks": [{"name": "an-TEST-" + event, "type": "command", "command": command, "timeout": 5000}]}
        if event == "Notification":
            group["matcher"] = "ToolPermission"
        hooks[event] = [group]
    private = lab / "sdk-private"
    private.mkdir(mode=0o700)
    outfile = private / "telemetry.json"
    fd = os.open(outfile, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600); os.close(fd)
    settings = {"hooks": hooks, "hooksConfig": {"enabled": True, "disabled": [], "notifications": False},
                "general": {"enableAutoUpdate": False, "enableAutoUpdateNotification": False, "enableNotifications": False},
                "privacy": {"usageStatisticsEnabled": False},
                "telemetry": {"enabled": True, "target": "local", "outfile": str(outfile), "logPrompts": True, "useCollector": False, "useCliAuth": False},
                "advanced": {"ignoreLocalEnv": True}, "security": {"auth": {"selectedType": "gateway", "useExternal": True}, "disableYoloMode": True, "disableAlwaysAllow": True},
                "context": {"includeDirectoryTree": False, "memoryBoundaryMarkers": [], "includeDirectories": []},
                "model": {"name": "gemini-2.5-flash"}, "tools": {"useRipgrep": False, "core": ["write_file"], "confirmationRequired": ["write_file"], "allowed": [], "disableLLMCorrection": True},
                "skills": {"enabled": False}, "mcpServers": {}}
    path = lab / "profile/.gemini/settings.json"
    path.write_text(json.dumps(settings, indent=2) + "\n")
    return path


class CaseObservation:
    """Controller observations, not native event order or key-receipt evidence.

    Provider deltas belong to the controller's case window, not authenticated
    per-turn requests. Snapshot while driving; failure projection performs no IO.
    """
    METHODS = ("streamGenerateContent", "generateContent", "countTokens")

    def __init__(self, fixture):
        self.fixture, self.started = fixture, time.monotonic()
        with fixture.lock:
            self.baseline = {key: fixture.counts.get(key, 0) for key in self.METHODS}
        self.flags = dict(permission_hook_seen=False, permission_UI_seen=False,
                          acted=False, AfterAgent_seen=False)
        self.counts = {key: 0 for key in self.METHODS}
        self.milestones = []
        self.mark("case_entered")

    def mark(self, event):
        if len(self.milestones) < 12 and not any(row["event"] == event for row in self.milestones):
            self.milestones.append(dict(event=event, observed_ms=round((time.monotonic() - self.started) * 1000)))

    def observe(self, permission, rendered, completion, acted):
        for key, value in zip(self.flags, (permission, rendered, acted, completion)):
            if value and not self.flags[key]:
                self.mark(key)
            self.flags[key] = value
        with self.fixture.lock:
            self.counts = {key: self.fixture.counts.get(key, 0) - self.baseline[key] for key in self.METHODS}

    def facts(self):
        return {"clock": "controller_monotonic", **self.flags,
                "provider_calls_since_case_entry": dict(self.counts),
                "milestones": [dict(row) for row in self.milestones]}


class Terminal:
    """Node bridge emits classifications only; native PTY text stays in bounded RAM."""
    def __init__(self, node, executable, install_root, lab, env, ui, timeout):
        env = dict(env, AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS="1")
        self.telemetry_lab, self.telemetry_captured = lab, False
        self.owned_input_capture = {"qualified": False, "frames": []}
        self.events, self.seen, self.exit = queue.Queue(), set(), None
        self.child_started = None
        self.stage, self.case, self.completed_cases = "starting", "plain", 0
        self.bridge_errors, self.cleanup_errors, self.diagnostics = [], [], set()
        self.graceful_requested = self.forced_requested = False
        self.case_observation, self.previous_case_observation = None, None
        bridge = Path(__file__).with_name("gemini_native_pty.cjs")
        self.p = subprocess.Popen([str(node), str(bridge)], cwd=lab / "profile", env=env,
                                  stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                  start_new_session=os.name != "nt")
        def reader():
            try:
                while line := self.p.stdout.readline(4097):
                    require(len(line) <= 4096 and line.endswith(b"\n"), "bridge_output_limit")
                    self.events.put(json.loads(line))
            except Exception:
                self.events.put({"error": "bridge_protocol_error"})
            self.events.put({"closed": True})
        threading.Thread(target=reader, daemon=True).start()
        try:
            self.send({"op": "start", "node": str(node), "executable": str(executable), "installRoot": str(install_root),
                   "cwd": str(lab / "profile"), "env": env, "permissionPattern": ui["permission_pattern"],
                   "timeoutMs": int(timeout * 1000)})
            self.identity = self.wait("ready", 10)
        except Exception as exc:
            try:
                self.close(graceful=False)
            except Exception:
                pass  # close records cleanup separately; keep startup primary.
            exc.bridge_diagnostic = self.failure_facts()
            raise

    def failure_facts(self):
        return {"stage": self.stage, "case": self.case, "completed_cases": self.completed_cases,
                "native_child_started": self.child_started, "own_child_exit": self.exit,
                "bridge_exit_code": self.p.poll(), "errors": self.bridge_errors,
                "graceful_requested": self.graceful_requested, "forced_requested": self.forced_requested,
                "cleanup_classifications": self.cleanup_errors[:4], "native_diagnostics": sorted(self.diagnostics),
                "case_observation": self.case_observation.facts() if self.case_observation else None,
                "previous_case_observation": self.previous_case_observation}

    def receive(self, item):
        if item.get("diagnostic") in ("auth_error", "startup_welcome", "startup_theme", "startup_trust",
                "startup_update", "startup_model", "startup_terms", "startup_continue", "startup_error",
                "prompt_seen", "turn_response_seen", "hook_timeout", "hook_command_missing", "hook_parse_error",
                "hook_python_error", "hook_root_error", "hook_input_error"):
            self.diagnostics.add(item["diagnostic"])
        if "error" in item:
            facts = bridge_event_facts(item)
            if len(self.bridge_errors) < 4:
                self.bridge_errors.append(facts)
            self.stage = facts.get("stage", self.stage)
            if "native_child_started" in facts:
                self.child_started = facts["native_child_started"]
            return facts["error"]
        if "ready" in item:
            self.child_started, self.stage = True, "protocol"
        if "exit" in item:
            self.exit = item["exit"]
        return "bridge_closed" if "closed" in item else None

    def send(self, message):
        self.p.stdin.write((json.dumps(message) + "\n").encode())
        self.p.stdin.flush()

    def write_line(self, text):
        # A text+Return burst is interpreted as a paste by the native input box.
        self.send({"op": "write", "data": text})
        time.sleep(0.15)
        self.send({"op": "write", "data": "\r"})

    def menu_choice(self, keys):
        for token in re.findall(r"\x1b\[[AB]|.", keys, re.DOTALL):
            self.send({"op": "write", "data": token})
            time.sleep(0.05)

    def drain(self):
        while not self.events.empty():
            item = self.events.get_nowait()
            error = self.receive(item)
            require(error is None, error)
            if "permission" in item:
                self.seen.add(item["permission"])
            if "exit" in item:
                self.exit = item["exit"]

    def wait(self, key, seconds, cleanup=False):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            try:
                item = self.events.get(timeout=0.1)
            except queue.Empty:
                continue
            error = self.receive(item)
            if cleanup and "error" in item:
                continue
            if cleanup and "closed" in item and self.child_started is False:
                return None
            require(error is None, error)
            if key in item:
                return item[key]
        raise Red("bridge_" + key + "_timeout")

    def close(self, graceful=True):
        if not self.telemetry_captured:
            self.telemetry_captured = True
            try:
                self.owned_input_capture = capture_sdk_hook_outcomes(self.telemetry_lab)
            except Exception:
                pass  # Diagnostic publication must never prevent SDK teardown.
        failure = None
        try:
            if graceful and self.exit is None:
                self.graceful_requested = True
                self.write_line("/quit")
                self.exit = self.wait("exit", 8)
            if graceful:
                require(self.exit is not None and self.exit["code"] == 0, "native_exit_nonzero")
        except Exception as exc:
            failure = exc
            self.cleanup_errors.append(str(exc) if isinstance(exc, Red) else "bridge_cleanup_error")
        finally:
            if self.exit is None and self.child_started is not False:
                try:
                    self.forced_requested = True
                    self.send({"op": "stop"})
                    self.exit = self.wait("exit", 4, cleanup=True)
                except Exception:
                    self.cleanup_errors.append("native_shutdown_unconfirmed")
                    failure = failure or Red("native_shutdown_unconfirmed")
            try:
                self.p.stdin.close()
                self.p.wait(timeout=5)
            except (BrokenPipeError, subprocess.TimeoutExpired):
                self.p.kill()
                self.p.wait(timeout=2)
                self.cleanup_errors.append("bridge_shutdown_timeout")
                failure = failure or Red("bridge_shutdown_timeout")
            finally:
                self.p.stdout.close()
        if failure is None:
            try:
                require(self.exit is not None or self.child_started is False, "native_shutdown_unconfirmed")
                if graceful:
                    require(not self.exit.get("forced"), "forced_native_shutdown")
                    require(self.p.returncode == 0, "bridge_exit_nonzero")
            except Exception as exc:
                failure = exc
                self.cleanup_errors.append(str(exc) if isinstance(exc, Red) else "bridge_cleanup_error")
        if failure:
            failure.bridge_diagnostic = self.failure_facts()
            raise failure


def observations(lab):
    raw = (lab / "events.jsonl").read_bytes()
    require(len(raw) <= 256 * 1024, "observation_limit")
    rows = [json.loads(x) for x in raw.splitlines()]
    require(len(rows) <= 128, "observation_count_limit")
    require(all(x.get("valid") and x.get("neutral") for x in rows), "invalid_or_nonneutral_hook")
    return rows


def exercise(lab, fixture, terminal, ui, observer=observations):
    """G5 reuses this agent/UI/tool driver; observer must inspect native evidence."""
    results = []
    for case in CASES:
        terminal.previous_case_observation = terminal.case_observation.facts() if terminal.case_observation else None
        state = terminal.case_observation = CaseObservation(fixture)
        terminal.case = case
        if case != "plain":
            fixture.arm(case)
            terminal.send({"op": "watch", "case": case})
            state.mark("watch_write_returned")
            terminal.write_line("AN_TEST_" + ("PLAIN" if case in ("equal", "recovery") else case.upper()))
            state.mark("prompt_write_returned")
        end, acted, completion, permission = time.monotonic() + 25, False, False, False
        cancelled_at = None
        while time.monotonic() < end:
            terminal.drain()
            require(terminal.exit is None, "native_exited_during_case")
            require(fixture.error is None, fixture.error or "provider_error")
            rows = [x for x in observer(lab) if x.get("case") == case]
            permission = any(x["event"] == "Notification" for x in rows)
            completion = any(x["event"] == "AfterAgent" for x in rows)
            state.observe(permission, case in terminal.seen, completion, acted)
            if case in ("approve", "deny", "cancel") and permission and case in terminal.seen and not acted:
                state.mark("menu_write_requested")
                terminal.menu_choice(ui[case])
                state.mark("menu_write_returned")
                acted = True
                state.observe(permission, case in terminal.seen, completion, acted)
                if case in ("deny", "cancel"):
                    cancelled_at = time.monotonic()
            if completion and (case not in ("approve", "deny", "cancel") or acted):
                break
            if cancelled_at and time.monotonic() - cancelled_at >= 4:
                break  # Absence is observed for this bounded window, never synthesized.
            time.sleep(0.05)
        state.mark("case_observation_finished")
        require(completion or case in ("deny", "cancel") and acted, "AfterAgent_missing_or_timeout")
        if case in ("approve", "deny", "cancel"):
            require(permission and case in terminal.seen and acted, "actual_permission_UI_missing")
            target = lab / "profile" / ("effect-" + case + ".txt")
            effect = target.is_file() and target.read_bytes() == b"owned TEST effect" + os.linesep.encode("ascii")
            require(effect if case == "approve" else not target.exists(), "wrong_TEST_tool_effect")
        results.append({"case": case, "AfterAgent_seen": completion, "permission_UI_seen": permission and case in terminal.seen,
                        "tool_effect_verified": True if case in ("approve", "deny", "cancel") else None})
        terminal.completed_cases = len(results)
        time.sleep(0.3)  # Hook fires before the UI's next input render.
    # Recheck rejection after recovery: a delayed write must not pass merely
    # because it appeared after the initial bounded cancellation observation.
    for rejected in ("deny", "cancel"):
        require(not os.path.lexists(lab / "profile" / ("effect-" + rejected + ".txt")), "wrong_TEST_tool_effect")
    rows = observer(lab)
    plain = [x for x in rows if x.get("case") in ("plain", "equal") and x["event"] == "AfterAgent"]
    require(len(plain) == 2 and all(x.get("stop_hook_active") is False for x in plain), "equal_turn_observations_missing")
    require(len({x["session_sha256"] for x in plain}) == 1, "equal_turn_session_changed")
    return results, rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("gemini-executable", "node-executable", "cli-install-root", "lab-root", "hook-shell", "system-root", "ui-contract", "probe-command", "record-root", "event", "native-shell"):
        parser.add_argument("--" + flag)
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--capture-frames", action="store_true")
    args = parser.parse_args()
    if args.record_root:
        require(args.event in ("AfterAgent", "Notification"), "unknown_hook_selector")
        record_hook(args)
        return
    require(all((args.gemini_executable, args.node_executable, args.cli_install_root, args.lab_root, args.hook_shell, args.ui_contract)), "explicit_native_inputs_required")
    require(60 <= args.timeout <= 240, "timeout_out_of_bounds")
    args.gemini_executable = str(physical(args.gemini_executable))
    args.cli_install_root = str(physical(args.cli_install_root))
    identity = cli_identity(args.gemini_executable, args.cli_install_root)
    node, shell = physical(args.node_executable), physical(args.hook_shell)
    ui_path = physical(args.ui_contract)
    require(inside(ui_path, physical(args.cli_install_root)) and ui_path.stat().st_size <= 16384, "UI_contract_must_be_in_TEST_installation")
    ui = json.loads(ui_path.read_text())
    require(set(ui) == {"permission_pattern", "approve", "deny", "cancel", "source_sha256"}, "UI_contract_fields")
    require(re.fullmatch(r"[0-9a-f]{64}", ui["source_sha256"]) is not None, "UI_source_hash_missing")
    require(all(isinstance(ui[k], str) and 0 < len(ui[k]) <= 32 for k in ("approve", "deny", "cancel")), "UI_keys_invalid")
    require(all(re.fullmatch(r"(?:[1-9]|\x1b\[[AB])*\r", ui[k]) for k in ("approve", "deny")), "UI_only_menu_navigation_allowed")
    require(ui["cancel"] in ("\x1b", "\x03"), "UI_cancel_key_invalid")
    require(isinstance(ui["permission_pattern"], str) and 0 < len(ui["permission_pattern"]) <= 512, "UI_pattern_invalid")
    re.compile(ui["permission_pattern"])
    require(os.name != "nt" or not args.probe_command, "Windows_probe_process_tree_unqualified")
    commands = json.loads(args.probe_command) if args.probe_command else {}
    require(isinstance(commands, dict) and set(commands) <= {"AfterAgent", "Notification"}, "probe_event_map_required")
    for argv in commands.values():
        require(isinstance(argv, list) and argv and all(isinstance(x, str) for x in argv), "probe_fixed_argv_required")
        target = physical(argv[0])
        require(any((p / MARKER).is_file() for p in target.parents), "probe_TEST_artifact_marker_missing")
        require(not inside(target, Path.home().resolve()) and not inside(target, Path(__file__).resolve().parents[2]), "borrowed_probe_forbidden")
    physical(str(Path(sys.executable).resolve()))
    physical(str(Path(__file__).resolve()))
    lab = new_lab(args.lab_root)
    manifest = {"evidence_level": "native_cli/provider_substitute", "native_execution": "failed",
                "CLI": identity, "fixture_sha256": digest(Path(__file__).read_bytes()),
                "PTY_bridge_sha256": digest(Path(__file__).with_name("gemini_native_pty.cjs").read_bytes()),
                "node_executable_sha256": digest(node.read_bytes()),
                "platform": sys.platform, "python_version": sys.version.split()[0],
                "probe_executable_sha256": {event: digest(Path(argv[0]).read_bytes()) for event, argv in commands.items()}, "settings_lifecycle": "TEST_direct_settings_only",
                "G5": "unverified_requires_production_install_and_delivery", "OS_API": "unverified", "visual": "unverified",
                "retry_resume": "unqualified", "continuation": "unqualified_no_blocking_hook_installed", "nested": "unqualified"}
    try:
        env = minimal_env(lab, node, shell, args.system_root)
        settings = install_test_hooks(lab, commands, shell, node, args.system_root)
        before = settings.read_bytes()
        fixture = Fixture(lab)
        with fixture:
            env["GOOGLE_GEMINI_BASE_URL"] = fixture.url
            (lab / "provider-port").write_text(str(fixture.server.server_port))
            manifest["native_version_probe"] = "running"
            code, out, version_err = bounded_process([str(node), args.gemini_executable, "--version"], b"", lab / "profile", env, 60 if os.name == "nt" else 12)
            manifest["native_version_probe"] = version_probe(code, out, version_err, (lab, args.cli_install_root, node, node.parent))
            require(code == 0 and out.strip() == VERSION.encode(), "native_version_mismatch")
            fixture.arm("plain")
            terminal = Terminal(node, args.gemini_executable, args.cli_install_root, lab, env, ui, args.timeout)
            try:
                results, rows = exercise(lab, fixture, terminal, ui)
            except Exception as exc:
                try:
                    terminal.close(graceful=False)
                except Exception:
                    pass  # Preserve scenario failure and attach cleanup facts.
                exc.bridge_diagnostic = terminal.failure_facts()
                raise
            else:
                terminal.close()
            require(settings.read_bytes() == before, "unexpected_settings_mutation")
            manifest.update(native_execution="passed_scenarios", PTY=terminal.identity,
                            provider_endpoints=fixture.counts, cases=results, observations=rows,
                            event_counts={event: sum(row["event"] == event for row in rows) for event in ("AfterAgent", "Notification")},
                            event_categories=["AfterAgent", "Notification:ToolPermission"],
                            settings_sha256=digest(before), UI_source_sha256=ui["source_sha256"],
                            own_child_exit=terminal.exit)
    except Exception as exc:
        manifest["native_execution"] = "failed"
        if hasattr(exc, "provider_cleanup_classification"):
            manifest["provider_cleanup_classification"] = exc.provider_cleanup_classification
        manifest["classification"] = str(exc) if isinstance(exc, Red) else "harness_execution_error"
        manifest["exception_type"] = type(exc).__name__
        if hasattr(exc, "bridge_diagnostic"):
            manifest["bridge_failure"] = exc.bridge_diagnostic
        if "fixture" in locals():
            manifest["provider_endpoints"] = {key: fixture.counts.get(key, 0) for key in
                ("streamGenerateContent", "generateContent", "countTokens")}
        raise
    finally:
        (lab / "evidence.json").write_text(json.dumps(manifest, indent=2) + "\n")
        print(json.dumps({"native_execution": manifest["native_execution"], "evidence": "<TEST>/evidence.json", "G5": manifest["G5"]}))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(json.dumps({"red": str(exc) if isinstance(exc, Red) else "harness_error"}), file=sys.stderr)
        sys.exit(1)
