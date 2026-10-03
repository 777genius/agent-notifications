#!/usr/bin/env python3
"""Maintained, provider-free Claude SDK process E2E (Python standard library).

Run against an actual same-source Go artifact, for example:
  python -B scripts/claude_sdk_e2e.py --binary tmp/NEWTEST/base-binary \
    --go .test-tools/go/bin/go --report tmp/NEWTEST/claude-sdk-receipt.json

Every hook/installer subprocess runs in a freshly created TEST sandbox. The
only HTTP listener is an OS-assigned loopback webhook sink. No Claude client,
provider, terminal automation, registration mock, or desktop is involved.
Failures remain failures (including the base's open-stdin short-object bug);
independent checks continue so a RED receipt still records useful coverage.
Native Windows uses the installer's real generated exec hooks. On POSIX the
shipped ready-installed wrapper exercises the SDK, and an .exe-shaped native
artifact separately qualifies installer preservation, never Windows hosting.
"""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


BASE = "5a33c8f75e1f323dcd43bd83623db22032bad239"
EVENTS = ("PreToolUse", "Notification", "Stop", "SubagentStop", "TeammateIdle")
MODULE = "github.com/777genius/agent-notifications/cmd/claude-notifications"
PLACEHOLDER = "agent-notifications-sdk-placeholder"


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    path.chmod(0o600)


class Sink(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        self.received = []
        self.errors = []
        self.lock = threading.Lock()
        super().__init__(("127.0.0.1", 0), Receiver)

    def snapshot(self):
        with self.lock:
            return list(self.received)


class Receiver(BaseHTTPRequestHandler):
    def do_POST(self):
        self.connection.settimeout(5)
        try:
            size = int(self.headers.get("Content-Length", "0"))
            require(self.path == "/webhook" and 0 < size <= 4 * 1024 * 1024,
                    "unexpected webhook path/body size")
            data = self.rfile.read(size)
            require(len(data) == size, "incomplete webhook body")
            payload = json.loads(data)
            with self.server.lock:
                self.server.received.append({"payload": payload, "bytes": size,
                                             "sha256": hashlib.sha256(data).hexdigest(),
                                             "content_type": self.headers.get("Content-Type"),
                                             "user_agent": self.headers.get("User-Agent")})
            self.send_response(204)
            self.end_headers()
        except Exception as exc:
            with self.server.lock:
                self.server.errors.append(str(exc))
            self.send_error(400)

    def log_message(self, *_):
        pass


class Harness:
    def __init__(self, args):
        self.args = args
        self.source = args.source.resolve()
        self.binary = args.binary.resolve()
        self.lab = Path(tempfile.mkdtemp(prefix="NEWTEST-claude-sdk-", dir=args.temp_root)).resolve()
        if os.name == "nt":
            # Protect only this fresh lab before creating any children.
            acl_script = r"""
$ErrorActionPreference = 'Stop'
$owner = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = [System.Security.AccessControl.DirectorySecurity]::new()
$acl.SetOwner($owner)
$acl.SetAccessRuleProtection($true, $false)
foreach ($sid in @($owner, [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18'),
                  [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
    $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
        $sid, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
}
Set-Acl -LiteralPath $env:CLAUDE_SDK_TEST_LAB -AclObject $acl
"""
            acl_env = {key: os.environ[key] for key in
                       ("PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT") if key in os.environ}
            acl_env["CLAUDE_SDK_TEST_LAB"] = str(self.lab)
            subprocess.run(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", acl_script],
                           env=acl_env, check=True, timeout=30, capture_output=True)
        self.home = self.lab / "TEST home"
        self.project = self.lab / "TEST project"
        self.package = self.lab / "TEST plugin package with spaces"
        self.tmp = self.lab / "TEST tmp"
        self.stage = self.lab / "TEST installer stage"
        self.control = self.lab / "TEST control"
        self.commands = []
        self.checks = []
        self.receipts = []
        self.sink = None
        self.report = {"schema_version": 1, "base_sha": BASE, "sandbox": str(self.lab),
                       "host": {"system": platform.system(), "machine": platform.machine()},
                       "scope": {"provider": False, "browser": False, "desktop": False,
                                 "native_windows": os.name == "nt",
                                 "installer_preservation": "native Windows" if os.name == "nt"
                                 else "POSIX host with same-source .exe-shaped artifact"},
                       "limitations": []}
        self.control.mkdir(mode=0o700)
        for path in (self.home, self.project, self.tmp, self.stage,
                     self.package / "bin", self.package / "hooks", self.package / "config",
                     self.package / ".claude-plugin"):
            path.mkdir(parents=True, exist_ok=True)
        # Deliberately allowlist process environment: no inherited auth, Claude
        # settings, provider URLs, desktop socket, judge mode, or runtime state.
        self.env = {"PATH": os.environ.get("PATH", os.defpath), "HOME": str(self.home),
                    "USERPROFILE": str(self.home), "TMPDIR": str(self.tmp),
                    "TEMP": str(self.tmp), "TMP": str(self.tmp),
                    "CLAUDE_CONFIG_DIR": str(self.home / ".claude"),
                    "CLAUDE_HOME": str(self.home / ".claude"),
                    "CODEX_HOME": str(self.home / ".codex"),
                    "XDG_CONFIG_HOME": str(self.home / "xdg-config"),
                    "XDG_CACHE_HOME": str(self.home / "xdg-cache"),
                    "XDG_DATA_HOME": str(self.home / "xdg-data"),
                    "XDG_STATE_HOME": str(self.home / "xdg-state"),
                    "XDG_RUNTIME_DIR": str(self.home / "xdg-runtime"),
                    "APPDATA": str(self.home / "appdata"),
                    "LOCALAPPDATA": str(self.home / "localappdata"),
                    "PLUGIN_ROOT": str(self.package), "CLAUDE_PLUGIN_ROOT": str(self.package),
                    "AGENT_NOTIFICATIONS_CONTROL_ROOT": str(self.control),
                    "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
                    "GOTELEMETRY": "off", "GOCACHE": str(self.source / ".test-cache/gocache"),
                    "GOMODCACHE": str(self.source / ".test-cache/gomod"),
                    "LANG": "C.UTF-8"}
        if os.name == "nt":
            for key in ("SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"):
                if key in os.environ:
                    self.env[key] = os.environ[key]
        for key in ("CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
                    "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "APPDATA", "LOCALAPPDATA"):
            Path(self.env[key]).mkdir(parents=True, exist_ok=True)

    def run(self, argv, data=b"", *, timeout=15, open_stdin=False, cwd=None):
        argv = [str(item) for item in argv]
        number = len(self.commands) + 1
        stdout_path = self.lab / f"command-{number:03d}.stdout"
        stderr_path = self.lab / f"command-{number:03d}.stderr"
        record = {"argv": argv, "cwd": str(cwd or self.project),
                  "stdin_bytes": len(data), "stdin_sha256": hashlib.sha256(data).hexdigest(),
                  "writer_kept_open": open_stdin, "timeout_seconds": timeout}
        self.commands.append(record)
        started = time.monotonic()
        with stdout_path.open("wb") as out, stderr_path.open("wb") as err:
            process = subprocess.Popen(argv, cwd=cwd or self.project, env=self.env,
                                       stdin=subprocess.PIPE, stdout=out, stderr=err,
                                       start_new_session=os.name != "nt")
            record["pid"] = process.pid
            try:
                if open_stdin:
                    require(data == b"{}", "short-object probe must write exactly two bytes")
                    process.stdin.write(data)
                    process.stdin.flush()
                    # wait() does not close or drain stdin. In particular do
                    # not use communicate() here: it would conceal the bug.
                    process.wait(timeout=timeout)
                    record["exited_with_writer_open"] = not process.stdin.closed
                else:
                    process.communicate(data, timeout=timeout)
                record["timed_out"] = False
            except subprocess.TimeoutExpired:
                record["timed_out"] = True
                record["exited_with_writer_open"] = False
            finally:
                if process.poll() is None:
                    if os.name == "nt":
                        process.kill()  # Native exec hook has no shell child.
                    else:
                        os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
                if process.stdin and not process.stdin.closed:
                    process.stdin.close()
        record.update({"returncode": process.returncode,
                       "elapsed_seconds": round(time.monotonic() - started, 3),
                       "stdout_bytes": stdout_path.stat().st_size,
                       "stderr_bytes": stderr_path.stat().st_size,
                       "stdout_sha256": sha(stdout_path), "stderr_sha256": sha(stderr_path)})
        return record, stdout_path.read_bytes(), stderr_path.read_bytes()

    def success(self, result):
        record, out, err = result
        require(not record["timed_out"], f"command {len(self.commands)} timed out: {record['argv']}")
        require(record["returncode"] == 0, f"exit {record['returncode']}: {err[-1000:]!r}")
        require(out == b"", f"SDK decision stdout leaked: {out[:400]!r}")
        return record

    def check(self, name, fn):
        before = len(self.commands)
        start = time.monotonic()
        try:
            evidence = fn()
            self.checks.append({"name": name, "result": "PASS", "evidence": evidence})
        except Exception as exc:
            self.checks.append({"name": name, "result": "FAIL",
                                "error": f"{type(exc).__name__}: {exc}"})
        self.checks[-1].update({"command_numbers": list(range(before + 1, len(self.commands) + 1)),
                                "elapsed_seconds": round(time.monotonic() - start, 3)})

    def prepare(self):
        def inspect(argv):
            record, out, err = self.run(argv, cwd=self.source)
            require(not record["timed_out"] and record["returncode"] == 0, err.decode(errors="replace"))
            return out.decode().strip()

        root = inspect(["git", "rev-parse", "--show-toplevel"])
        require(Path(root).resolve() == self.source, "source cwd must be the verified repository root")
        revision = inspect(["git", "rev-parse", "HEAD"])
        inspect(["git", "merge-base", "--is-ancestor", BASE, revision])
        tracked_changes = inspect(["git", "diff", "--name-only", "HEAD"])
        require(not tracked_changes, "same-source verification requires a clean tracked source tree")
        info = inspect([self.args.go, "version", "-m", self.binary])
        require(f"path\t{MODULE}" in info, "artifact is not the actual product command")
        require(f"vcs.revision={revision}" in info, "artifact/source revision mismatch")
        require("vcs.modified=false" in info and "vcs.modified=true" not in info,
                "artifact must be built from clean source; ignore generated caches/output before building")
        require("github.com/777genius/plugin-kit-ai/sdk\t" in info, "Claude SDK missing from binary")
        expected_os = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}[platform.system()]
        require(f"GOOS={expected_os}" in info, "artifact is not native to this host")
        self.report.update({"source_cwd": str(self.source), "source_sha": revision,
                            "source_tracked_changes": tracked_changes,
                            "binary": {"path": str(self.binary), "sha256": sha(self.binary),
                                       "build_info": info, "vcs_modified": "vcs.modified=true" in info}})
        # Copy product resources, not runtime/auth/configuration directories.
        for relative in ("bin/hook-wrapper.sh", "hooks/hooks.json", "config/config.json",
                         ".claude-plugin/plugin.json"):
            shutil.copy2(self.source / relative, self.package / relative)
        self.template = json.loads((self.package / "hooks/hooks.json").read_text(encoding="utf-8"))
        require(set(self.template["hooks"]) == set(EVENTS), "unexpected product hook manifest")
        self.report["resource_hashes"] = {relative: sha(self.package / relative)
                                           for relative in ("hooks/hooks.json", "bin/hook-wrapper.sh",
                                                            ".claude-plugin/plugin.json")}
        for relative, digest in self.report["resource_hashes"].items():
            require(digest == sha(self.source / relative), "staged product resource changed")
        self.native_name = "claude-notifications" + (".exe" if os.name == "nt" else "")
        self.installed_binary = self.package / "bin" / self.native_name
        shutil.copy2(self.binary, self.installed_binary)
        self.installed_binary.chmod(0o700)
        require(sha(self.installed_binary) == sha(self.binary), "staged binary hash mismatch")
        self.report["binary"]["staged_sha256"] = sha(self.installed_binary)
        record, out, err = self.run([self.installed_binary, "version"])
        require(record["returncode"] == 0 and not record["timed_out"], repr(err))
        self.report["binary"]["version_stdout"] = out.decode(errors="replace").strip()
        manifest_version = json.loads((self.package / ".claude-plugin/plugin.json").read_text(encoding="utf-8"))["version"]
        # A supplied development binary may report "dev". The shipped wrapper
        # accepts unknown versions; known versions must actually agree.
        versions = re.findall(r"\d+\.\d+\.\d+", out.decode(errors="replace"))
        require(not versions or versions[0] == manifest_version, "wrapper would attempt an upgrade")
        self.sink = Sink()
        self.thread = threading.Thread(target=self.sink.serve_forever, daemon=True)
        self.thread.start()
        self.report["loopback"] = {"host": "127.0.0.1", "port": self.sink.server_port}
        self.cfg = json.loads((self.package / "config/config.json").read_text(encoding="utf-8"))
        self.cfg["notifications"].update({"suppressForSubagents": False,
                                         "notifyOnSubagentStop": True, "teamMode": "wait-all",
                                         "suppressQuestionAfterTaskCompleteSeconds": 0,
                                         "suppressQuestionAfterAnyNotificationSeconds": 0})
        self.cfg["notifications"]["desktop"].update({"enabled": False, "sound": False,
                                                     "clickToFocus": False})
        self.cfg["notifications"]["webhook"].update({"enabled": True, "preset": "custom",
                                                     "format": "json", "headers": {},
                                                     "payloadFields": {"raw_body": "${{raw_body}}",
                                                                       "session_name": "${{session_name}}",
                                                                       "cwd": "${{cwd}}",
                                                                       "folder": "${{folder}}"},
                                                     "url": f"http://127.0.0.1:{self.sink.server_port}/webhook"})
        self.config_path = self.home / ".claude/claude-notifications-go/config.json"
        write_json(self.config_path, self.cfg)
        self.report["desktop_controls"] = self.cfg["notifications"]["desktop"]
        # A real empty transcript prevents attempts to read any source project.
        self.transcript = self.project / "TEST transcript.jsonl"
        self.transcript.write_bytes(b"")
        self.transcript.chmod(0o600)
        self.hooks_path = self.package / "hooks/hooks.json"
        self.exe_name = "claude-notifications-windows-amd64.exe"
        if platform.machine().lower() in ("arm64", "aarch64"):
            self.exe_name = "claude-notifications-windows-arm64.exe"
        shutil.copy2(self.binary, self.stage / self.exe_name)
        require(sha(self.stage / self.exe_name) == sha(self.binary), "installer stage hash mismatch")
        self.fixture = copy.deepcopy(self.template)
        self.fixture["foreignTop"] = {"future": True, "nullable": None}
        self.fixture["hooks"]["ForeignEvent"] = None
        self.foreign = {"command": str(self.lab / "NEVER execute foreign command"), "future": None}
        self.near = copy.deepcopy(self.template["hooks"]["Stop"][0]["hooks"][0])
        self.near["timeout"] = 31
        self.fixture["hooks"]["Stop"] = [{"matcher": "", "annotation": {"keep": True},
                                             "hooks": [copy.deepcopy(self.template["hooks"]["Stop"][0]["hooks"][0]),
                                                       self.foreign, self.near]}]
        write_json(self.hooks_path, self.fixture)
        if os.name == "nt":
            self.report["limitations"].append("POSIX wrapper not executed on Windows; generated native exec hooks qualified")
        return {"verified_source_sha": revision, "staged_package": str(self.package)}

    def install(self, *, remove=False, refresh=False):
        argv = [self.installed_binary, "internal-install-runtime", "--target", self.package / "bin",
                "--entry", self.exe_name, "--control-root", self.control]
        if remove:
            argv += ["--remove"]
        else:
            argv += ["--stage", self.stage]
        if refresh:
            argv += ["--refresh"]
        record, _, err = self.run(argv)
        require(not record["timed_out"] and record["returncode"] == 0, repr(err[-2000:]))

    def managed(self, event):
        data = json.loads(self.hooks_path.read_text(encoding="utf-8"))
        expected = {"type": "command", "command": str(self.package / "bin" / self.exe_name),
                    "args": ["handle-hook", event], "timeout": 30}
        matches = [handler for group in data["hooks"][event] for handler in group.get("hooks", [])
                   if handler == expected]
        require(len(matches) == 1, f"expected one generated owned {event} exec hook")
        require(sha(Path(expected["command"])) == sha(self.binary), "managed artifact hash mismatch")
        return [matches[0]["command"], *matches[0]["args"]]

    def preserve(self):
        data = json.loads(self.hooks_path.read_text(encoding="utf-8"))
        require(data["foreignTop"] == self.fixture["foreignTop"], "foreign top fields changed")
        require("ForeignEvent" in data["hooks"] and data["hooks"]["ForeignEvent"] is None,
                "foreign null event lost")
        group = data["hooks"]["Stop"][0]
        require(group["matcher"] == "" and group["annotation"] == {"keep": True},
                "mixed Stop annotations/matcher changed")
        require(self.foreign in group["hooks"] and self.near in group["hooks"],
                "foreign command/customized near-match lost")
        for event in EVENTS:
            self.managed(event)
        require(not Path(self.foreign["command"]).exists(), "foreign command unexpectedly executed")
        return data

    def install_preservation(self):
        self.install()
        first = self.preserve()
        self.install()
        require(self.preserve() == first, "repeat install not semantically idempotent")
        edited = self.preserve()
        edited["laterForeignEdit"] = {"enabled": False}
        # Damage the actual owned hook and artifact; repair must restore them.
        for group in edited["hooks"]["Stop"]:
            group["hooks"] = [h for h in group["hooks"]
                              if h.get("command") != str(self.package / "bin" / self.exe_name)]
        write_json(self.hooks_path, edited)
        (self.package / "bin" / self.exe_name).unlink()
        self.install(refresh=True)
        repaired = self.preserve()
        require(repaired["laterForeignEdit"] == {"enabled": False}, "repair lost later edit")
        return {"install": True, "repeat": True, "repair": True,
                "managed_exec_argv": {e: self.managed(e) for e in EVENTS},
                "hooks_sha256": sha(self.hooks_path)}

    def command(self, event, route="installed", trace=False):
        if route == "direct":
            return [self.installed_binary, "handle-hook", event]
        if route == "managed" or os.name == "nt":
            return self.managed(event)
        # Use the exact shipped manifest argv, only expand its package root.
        handler = self.template["hooks"][event][0]["hooks"][0]
        require(handler["command"] == "sh", "unsupported shipped wrapper command")
        args = [arg.replace("${CLAUDE_PLUGIN_ROOT}", str(self.package)) for arg in handler["args"]]
        return ["sh", *(["-x"] if trace else []), *args]

    def payload(self, event, session, **fields):
        return {"session_id": session, "cwd": str(self.project),
                "transcript_path": str(self.transcript), "hook_event_name": event, **fields}

    def state(self, session):
        return json.loads((self.tmp / f"claude-session-state-{session}.json").read_text(encoding="utf-8"))

    def emit(self, event, session, status, message, *, fields=None, route="installed", wire=None,
             trace=False, persisted=True):
        before = len(self.sink.snapshot())
        payload = self.payload(event, session, **(fields or {}))
        data = wire if wire is not None else json.dumps(payload, ensure_ascii=False).encode()
        result = self.run(self.command(event, route, trace), data)
        self.success(result)
        received = self.sink.snapshot()[before:]
        require(len(received) == 1, f"{event}: expected one webhook, got {len(received)}")
        actual = received[0]["payload"]
        expected = {"schema_version": "1.0", "agent_source": "claude", "source": "claude-notifications",
                    "status": status, "notification_type": status, "session_id": session,
                    "raw_body": message, "cwd": payload["cwd"],
                    "folder": Path(payload["cwd"]).name}
        for key, value in expected.items():
            require(actual.get(key) == value, f"{event} {key}: expected {str(value)[:200]!r}, got {str(actual.get(key))[:200]!r}")
        # Supported runtime templates expose body and metadata independently.
        # Assert the complete joined text without copying session-name or summary algorithms.
        label = actual.get("session_name")
        require(isinstance(label, str) and re.fullmatch(r"[^\[\]|\r\n]+", label),
                "invalid webhook session label")
        if session == "unknown":
            require(label == "unknown", "missing session must retain the unknown label")
        prefix = re.escape(label) + r"(?:\|[^\[\]\r\n]+)? " + re.escape(expected["folder"])
        require(isinstance(actual.get("message"), str) and
                re.fullmatch(r"\[" + prefix + r"\] " + re.escape(message), actual["message"]),
                "webhook message must have anchored metadata and the exact original body suffix")
        require(PLACEHOLDER not in json.dumps(actual), "SDK placeholder leaked into webhook")
        require(received[0]["content_type"] == "application/json", "wrong webhook content type")
        require(received[0]["user_agent"] == "claude-notifications/1.0", "wrong real product sender")
        require(actual.get("title") and actual.get("timestamp"), "missing title/timestamp")
        if persisted:
            state = self.state(session)
            require(state["session_id"] == session and state["last_notification_status"] == status,
                    "notification state identity/status mismatch")
            require(state["last_notification_body"] == message, "original body missing from persisted state")
            require(state["last_notification_event"] == event, "wrong persisted hook event")
            require(state["last_notification_ts"] > 0, "missing persisted notification timestamp")
            require(PLACEHOLDER not in json.dumps(state), "SDK placeholder leaked into state")
        self.receipts.append({"event": event, "route": route, "status": status,
                              "session_id_bytes": len(session.encode()),
                              "session_id_sha256": hashlib.sha256(session.encode()).hexdigest(),
                              "message": message, "webhook_sha256": received[0]["sha256"],
                              "webhook_bytes": received[0]["bytes"]})
        if trace and os.name != "nt":
            trace_text = result[2].decode(errors="replace")
            expected_argv = [str(self.installed_binary), "handle-hook", event]
            proven = False
            for line in trace_text.splitlines():
                if not line.startswith("+ "):
                    continue
                invocation = line[2:]
                # Linux dash leaves spaced paths unquoted; macOS sh quotes them.
                if invocation == " ".join(expected_argv):
                    proven = True
                    break
                try:
                    if shlex.split(invocation) == expected_argv:
                        proven = True
                        break
                except ValueError:
                    continue
            require(proven, "sh trace did not prove actual staged binary argv")
        return actual

    def interactive(self, tool, status, message, session):
        self.emit("PreToolUse", session, status, message, fields={"tool_name": tool})
        state = self.state(session)
        require(state["last_interactive_tool"] == tool and state["last_ts"] > 0,
                "interactive tool state not persisted")
        require(state["cwd"] == str(self.project), "interactive cwd changed")
        return {"interactive_tool": tool, "status": status, "state": state}

    def short(self, route):
        before = len(self.sink.snapshot())
        result = self.run(self.command("PreToolUse", route), b"{}", open_stdin=True, timeout=3)
        self.success(result)
        require(result[0]["exited_with_writer_open"], "short object waited for stdin EOF")
        require(len(self.sink.snapshot()) == before, "empty PreToolUse emitted webhook")
        return {"exact_stdin": "{}", "writer_open_at_exit": True}

    def malformed(self):
        before = len(self.sink.snapshot())
        record, out, err = self.run(self.command("Stop", "direct"), b"{oops")
        require(not record["timed_out"] and record["returncode"] != 0 and out == b"",
                "malformed direct input must be loud nonzero with empty stdout")
        require(b"failed to parse hook data" in err, "missing malformed direct stderr diagnostic")
        if os.name != "nt":
            wrapped = self.run(self.command("Stop"), b"{oops")
            self.success(wrapped)
        else:
            self.report["limitations"].append("Claude shipped POSIX wrapper fail-open is POSIX-only; Windows generated exec keeps direct error contract")
        require(len(self.sink.snapshot()) == before, "malformed input emitted webhook")
        return {"direct_error": err[-1000:].decode(errors="replace"),
                "wrapper_failopen": os.name != "nt"}

    def team(self):
        team = "TEST-sdk-team"
        lead = "TEST-team-lead"
        write_json(self.home / ".claude/teams" / team / "config.json",
                   {"name": team, "leadSessionId": lead,
                    "members": [{"agentId": "lead", "name": "team-lead", "agentType": "team-lead"},
                                {"agentId": "alice", "name": "alice", "agentType": "general-purpose"},
                                {"agentId": "bob", "name": "bob", "agentType": "general-purpose"}]})
        path = self.tmp / f"claude-team-notify-{team}.json"
        before = len(self.sink.snapshot())
        self.success(self.run(self.command("Stop"), json.dumps(self.payload("Stop", lead,
                                           last_assistant_message="Lead work complete.")).encode()))
        state = json.loads(path.read_text(encoding="utf-8"))
        require(state["team_name"] == team and state["lead_stopped"] is True and state["lead_stop_at"] > 0,
                "lead Stop did not mark stopped")
        stopped = state
        require(not state.get("notified_at") and not state["idle_members"], "premature team state completion")
        require(len(self.sink.snapshot()) == before, "lead Stop prematurely notified")
        alice = self.payload("TeammateIdle", "TEST-idle-alice", team_name=team, teammate_name="alice")
        self.success(self.run(self.command("TeammateIdle"), json.dumps(alice).encode()))
        state = json.loads(path.read_text(encoding="utf-8"))
        require(state["team_name"] == team and state["lead_stopped"] is True and
                state["lead_stop_at"] == stopped["lead_stop_at"] and
                set(state["idle_members"]) == {"alice"} and state["idle_members"]["alice"] > 0 and
                not state.get("notified_at"), "first idle did not persist partial team state")
        partial = state
        require(len(self.sink.snapshot()) == before, "partial team prematurely notified")
        body = 'Team "TEST-sdk-team": all teammates finished work'
        self.emit("TeammateIdle", "TEST-idle-bob", "task_complete", body,
                  fields={"team_name": team, "teammate_name": "bob"}, persisted=False)
        state = json.loads(path.read_text(encoding="utf-8"))
        # Completion resets lead/idle flags durably for the next cycle.
        # The webhook belongs to the final idle event; persisted notification identity belongs to the lead.
        require(state["team_name"] == team and state["lead_stopped"] is False and
                state["idle_members"] == {} and state["lead_stop_at"] == stopped["lead_stop_at"] and
                state["notified_at"] >= state["lead_stop_at"], "team completion claim/reset not persisted")
        notification_state = self.state(lead)
        require(notification_state["session_id"] == lead and notification_state["last_notification_ts"] > 0 and
                notification_state["last_notification_body"] == body and
                notification_state["last_notification_event"] == "TeammateIdle" and
                notification_state["last_notification_status"] == "task_complete",
                "team notification not persisted under lead")
        require(PLACEHOLDER not in json.dumps(state) + json.dumps(notification_state),
                "SDK placeholder leaked into team/lead state")
        # Change event session to bypass the early dedup lock. The durable team
        # completion claim itself must suppress a replay in another process.
        replay = self.payload("TeammateIdle", "TEST-idle-replay", team_name=team, teammate_name="bob")
        self.success(self.run(self.command("TeammateIdle"), json.dumps(replay).encode()))
        require(len(self.sink.snapshot()) == before + 1, "team replay emitted duplicate")
        replay_state = json.loads(path.read_text(encoding="utf-8"))
        require(replay_state["team_name"] == team and replay_state["lead_stopped"] is False and
                replay_state["lead_stop_at"] == stopped["lead_stop_at"] and
                replay_state["notified_at"] == state["notified_at"] and
                set(replay_state["idle_members"]) == {"bob"} and replay_state["idle_members"]["bob"] > 0,
                "team replay changed claim or failed to persist idle without a new lead Stop")
        require(self.state(lead) == notification_state, "team replay changed lead notification state")
        return {"stopped_team_state": stopped, "partial_team_state": partial, "team_state": state,
                "replay_team_state": replay_state, "lead_notification_state": notification_state,
                "webhook_session_id": "TEST-idle-bob", "notification_state_session_id": lead,
                "webhook_count": 1, "replay_bypasses_session_lock": True}

    def bom_trailing(self):
        for route in ("direct", "installed"):
            session = f"TEST-bom-{route}"
            payload = self.payload("Stop", session, last_assistant_message="BOM fixture complete.")
            self.emit("Stop", session, "task_complete", "BOM fixture complete.", route=route,
                      wire=b"\xef\xbb\xbf" + json.dumps(payload).encode() + b"\n{not valid trailing JSON")
        return {"bom": "UTF-8", "trailing_bytes_ignored": True, "routes": ["direct", "installed"]}

    def oversized(self):
        # A useful first sentence remains observable after the real product's
        # normal summary truncation; no implementation is called for expected text.
        original = "Oversized final message restored correctly. " + "x" * (1024 * 1024 + 256)
        self.emit("Stop", "TEST-oversized", "task_complete", "Oversized final message restored correctly.",
                  fields={"last_assistant_message": original})
        return {"original_final_message_bytes": len(original), "original_summary_restored": True}

    def combined(self):
        # Each field is below 1 MiB; their *SDK projection* is above it. Keep
        # cwd lexically in the disposable project, with an impossible component
        # so no traversal can reach an existing project or resolve real paths.
        session = "TEST-combined"
        cwd = str(self.project) + os.sep + "c" * 600000
        message = "Combined projection preserved the original final message."
        original = message + " " + "x" * 600000
        require(all(len(value.encode()) < 1024 * 1024 for value in (session, cwd, original)),
                "individual field accidentally tests per-field fallback")
        require(len(cwd.encode()) + len(original.encode()) > 1024 * 1024,
                "SDK projection fields must exceed aggregate cap independently of envelope overhead")
        fields = {"cwd": cwd, "last_assistant_message": original}
        wire_bytes = len(json.dumps(self.payload("Stop", session, **fields)).encode())
        require(wire_bytes > 1024 * 1024, "combined payload must exceed SDK cap")
        actual = self.emit("Stop", session, "task_complete", message, fields=fields)
        require(actual["session_id"] == session, "combined projection lost original session")
        return {"session_bytes": len(session.encode()), "cwd_bytes": len(cwd.encode()),
                "original_final_message_bytes": len(original.encode()), "combined_bytes": wire_bytes,
                "original_session_restored": True, "original_final_message_restored": True,
                "original_cwd_delivered": True}

    def missing_session(self):
        log = self.package / "notification-debug.log"
        offset = log.stat().st_size if log.exists() else 0
        payload = self.payload("Stop", "", last_assistant_message="Missing session fixture complete.")
        self.emit("Stop", "unknown", "task_complete", "Missing session fixture complete.", route="direct",
                  wire=json.dumps(payload).encode())
        delta = log.read_bytes()[offset:].decode(errors="replace")
        warning = "Session ID is empty, using 'unknown'"
        lines = [line for line in delta.splitlines()
                 if re.fullmatch(r"\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] \[WARN\] PID:\d+: " +
                                 re.escape(warning), line)]
        require(len(lines) == 1, "missing exact WARN level/text with quoted 'unknown'")
        return {"warn_line": lines[0], "fallback_session_id": "unknown", "stdout_bytes": 0}

    def managed_execution(self):
        self.emit("Stop", "TEST-managed-owned", "task_complete", "Managed hook fixture complete.",
                  fields={"last_assistant_message": "Managed hook fixture complete."}, route="managed")
        return {"argv": self.managed("Stop"), "webhook_agent_source": "claude"}

    def remove(self):
        self.install(remove=True)
        data = json.loads(self.hooks_path.read_text(encoding="utf-8"))
        expected = copy.deepcopy(self.fixture)
        expected["laterForeignEdit"] = {"enabled": False}
        for event in EVENTS:
            if event == "Stop":
                expected["hooks"][event][0]["hooks"] = [self.foreign, self.near]
            else:
                expected["hooks"][event] = []
        require(data == expected, "remove changed foreign fields, handlers, groups, or near-match")
        for name in (self.exe_name, "claude-notifications.bat", "agent-notifications.bat"):
            require(not (self.package / "bin" / name).exists(), f"owned launcher survived removal: {name}")
        return {"foreign_document_preserved": True, "hooks_sha256": sha(self.hooks_path)}

    def execute(self):
        self.check("verified_same_source_sandbox_setup", self.prepare)
        if self.checks[-1]["result"] == "PASS":
            self.check("real_install_repeat_repair_foreign_preservation", self.install_preservation)
            self.check("short_object_direct_writer_open", lambda: self.short("direct"))
            self.check("short_object_installed_writer_open", lambda: self.short("installed"))
            self.check("PreToolUse_ExitPlanMode_plan_ready", lambda: self.interactive(
                "ExitPlanMode", "plan_ready", "Plan", "TEST-plan"))
            self.check("PreToolUse_AskUserQuestion_question", lambda: self.interactive(
                "AskUserQuestion", "question", "Question", "TEST-question"))
            self.check("Notification_permission_prompt_question", lambda: self.emit(
                "Notification", "TEST-permission", "question", "Question",
                fields={"notification_type": "permission_prompt", "message": "Fixture permission needed"}))
            self.check("Stop_message_installed_and_actual_wrapper_argv", lambda: self.emit(
                "Stop", "TEST-stop", "task_complete", "Stop fixture complete.", trace=True,
                fields={"last_assistant_message": "Stop fixture complete."}))
            self.check("Stop_message_direct_CLI", lambda: self.emit(
                "Stop", "TEST-cli-stop", "task_complete", "Direct CLI fixture complete.", route="direct",
                fields={"last_assistant_message": "Direct CLI fixture complete."}))
            self.check("SubagentStop_explicit_opt_in_message", lambda: self.emit(
                "SubagentStop", "TEST-subagent", "task_complete", "Subagent fixture complete.",
                fields={"last_assistant_message": "Subagent fixture complete."}))
            self.check("TeammateIdle_wait_all_persisted_claim_and_replay", self.team)
            self.check("malformed_direct_loud_wrapper_failopen", self.malformed)
            self.check("BOM_and_trailing_first_value", self.bom_trailing)
            self.check("oversized_original_final_message", self.oversized)
            self.check("combined_projection_over_1MiB_individually_smaller", self.combined)
            self.check("missing_session_exact_WARN_and_webhook_unknown", self.missing_session)
            self.check("only_generated_owned_hook_real_subprocess", self.managed_execution)
            self.check("real_remove_foreign_document_preserved", self.remove)
        if self.sink:
            self.sink.shutdown()
            self.sink.server_close()
            self.thread.join(timeout=5)
            self.check("loopback_receiver_clean", lambda: require(not self.sink.errors, str(self.sink.errors)))
        self.report.update({"checks": self.checks, "commands": self.commands,
                            "webhook_receipts": self.receipts,
                            "counts": {"checks": len(self.checks), "commands": len(self.commands),
                                       "passed": sum(c["result"] == "PASS" for c in self.checks),
                                       "failed": sum(c["result"] == "FAIL" for c in self.checks),
                                       "webhooks": len(self.sink.snapshot()) if self.sink else 0}})
        self.report["result"] = "FAIL" if self.report["counts"]["failed"] else "PASS"
        return self.report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path, help="actual same-source native Go binary")
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--go", default=shutil.which("go") or "go", help="Go tool for binary build-info verification")
    parser.add_argument("--temp-root", type=Path, default=Path(tempfile.gettempdir()))
    parser.add_argument("--report", required=True, type=Path, help="JSON evidence destination")
    args = parser.parse_args()
    harness = Harness(args)
    report = harness.execute()
    destination = args.report.resolve()
    write_json(destination, report)
    print(json.dumps({"result": report["result"], "counts": report["counts"], "report": str(destination),
                      "failed_checks": [c["name"] for c in report["checks"] if c["result"] == "FAIL"]}))
    return 1 if report["result"] == "FAIL" else 0


if __name__ == "__main__":
    sys.exit(main())
