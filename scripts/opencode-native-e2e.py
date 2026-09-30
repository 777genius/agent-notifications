#!/usr/bin/env python3
"""Bounded OpenCode 1.18.33 webhook qualification in a disposable project."""

import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import secrets
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


VERSION = "1.18.33"
ARCHIVES = {
    ("darwin", "arm64"): ("opencode-darwin-arm64.zip", "24b12873e605b3db3387cb355f43ba7451cd6065c180d8c188663337d2eeb553"),
    ("darwin", "amd64"): ("opencode-darwin-x64.zip", "90c7e7d9ffa0d8691ca0f15b42a7b89b72e17a4d26074b9ef06559ff87b221ec"),
    ("linux", "arm64"): ("opencode-linux-arm64.tar.gz", "c63486624621924bf43be5c01abd252885661a734814224f6d70188a33aea858"),
    ("linux", "amd64"): ("opencode-linux-x64.tar.gz", "e546123213ae47909a4268692aa4b94950d011afe9cac9938753a2194f1c16d5"),
    ("windows", "amd64"): ("opencode-windows-x64.zip", "cc827fda2e32502373c5de25a4b78471766b99881d12a441ae5b9fcff39c5780"),
}
ANSWER = "Sandbox notification check complete."


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def run(args, *, cwd, env, timeout=30):
    result = subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True,
                            encoding="utf-8", errors="replace", timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{pathlib.Path(args[0]).name} {args[1]} exited {result.returncode}: "
                           f"{redact((result.stderr or result.stdout)[-1200:])}")
    return result.stdout + result.stderr


def redact(value):
    return re.sub(r"PRIVATE_PROMPT_[0-9a-f]{24}", "PRIVATE_PROMPT_REDACTED", value)


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Sink(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, kind):
        self.kind = kind
        self.requests = []
        self.lock = threading.Lock()
        super().__init__(("127.0.0.1", 0), Handler)

    def append(self, body):
        with self.lock:
            self.requests.append(body)

    def count(self):
        with self.lock:
            return len(self.requests)


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size > 1024 * 1024:
            self.send_error(413)
            return
        body = self.rfile.read(size)
        if self.server.kind == "webhook":
            if self.path != "/webhook":
                self.send_error(404)
                return
            self.server.append(body)
            self.send_response(204)
            self.end_headers()
            return
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        request = json.loads(body)
        self.server.append(body)
        now = int(time.time())
        if request.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for delta, finish in (({"role": "assistant"}, None),
                                  ({"content": ANSWER}, None), ({}, "stop")):
                chunk = {"id": "chatcmpl-sandbox", "object": "chat.completion.chunk",
                         "created": now, "model": "mock-notification",
                         "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
                self.wfile.flush()
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        else:
            response = {"id": "chatcmpl-sandbox", "object": "chat.completion",
                        "created": now, "model": "mock-notification",
                        "choices": [{"index": 0, "message": {"role": "assistant", "content": ANSWER},
                                     "finish_reason": "stop"}],
                        "usage": {"prompt_tokens": 10, "completion_tokens": 6, "total_tokens": 16}}
            data = json.dumps(response).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    def log_message(self, *_):
        pass


def extract_opencode(archive, target, os_name):
    expected = "opencode.exe" if os_name == "windows" else "opencode"
    with (zipfile.ZipFile(archive) if archive.suffix == ".zip" else tarfile.open(archive, "r:gz")) as bundle:
        members = bundle.infolist() if isinstance(bundle, zipfile.ZipFile) else bundle.getmembers()
        matches = [m for m in members if pathlib.PurePosixPath(m.filename if isinstance(bundle, zipfile.ZipFile)
                   else m.name).name == expected]
        if len(matches) != 1:
            raise RuntimeError(f"archive must contain exactly one {expected}")
        member = matches[0]
        with (bundle.open(member) if isinstance(bundle, zipfile.ZipFile) else bundle.extractfile(member)) as source:
            if source is None:
                raise RuntimeError("OpenCode archive member is not a file")
            with target.open("wb") as output:
                while chunk := source.read(1024 * 1024):
                    output.write(chunk)
    if os_name != "windows":
        target.chmod(0o755)


def wait_for(predicate, seconds, label):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        if predicate():
            return
        time.sleep(0.2)
    raise RuntimeError(f"timed out waiting for {label}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--archive", type=pathlib.Path)
    source.add_argument("--opencode", type=pathlib.Path, help="already extracted CLI for local self-check")
    parser.add_argument("--os", choices=("darwin", "linux", "windows"), required=True)
    parser.add_argument("--arch", choices=("arm64", "amd64"), required=True)
    parser.add_argument("--report", type=pathlib.Path, required=True)
    args = parser.parse_args()
    expected_name, expected_digest = ARCHIVES[(args.os, args.arch)]
    if args.archive and (args.archive.name != expected_name or digest(args.archive) != expected_digest):
        raise RuntimeError("OpenCode release archive name or SHA-256 differs from v1.18.33 pin")
    actual_os = {"darwin": "darwin", "linux": "linux", "win32": "windows"}.get(sys.platform)
    actual_arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "AMD64": "amd64"}.get(platform.machine())
    if (args.os, args.arch) != (actual_os, actual_arch):
        raise RuntimeError("requested native matrix target differs from the actual host")
    candidate = args.binary.resolve(strict=True)
    source_sha = subprocess.check_output(["git", "rev-parse", "HEAD"],
        cwd=pathlib.Path(__file__).resolve().parents[1], text=True).strip()
    build_info = subprocess.check_output(["go", "version", "-m", str(candidate)], text=True)
    revision = re.search(r"(?m)^\s*build\s+vcs\.revision=([0-9a-f]{40})\s*$", build_info)
    modified = re.search(r"(?m)^\s*build\s+vcs\.modified=(true|false)\s*$", build_info)
    verified_source = bool(revision and modified and revision.group(1) == source_sha
                           and modified.group(1) == "false")
    if args.archive and not verified_source:
        raise RuntimeError("candidate binary lacks clean VCS metadata for exact checkout HEAD")
    report = {"schema_version": 1, "status": "fail",
              "candidate_sha": source_sha if verified_source else "local_binary_unattributed",
              "source_checkout_sha": source_sha,
              "host": platform.platform(), "os": args.os, "arch": args.arch,
              "product_binary_sha256": digest(candidate), "opencode_version": VERSION,
              "opencode_archive_sha256": expected_digest if args.archive else "local_self_check",
              "desktop_visual_outcome": "not_observed"}
    repo = pathlib.Path(__file__).resolve().parents[1]
    report["bundled_js_source_sha256"] = digest(repo / "internal" / "opencodeplugin" /
                                                 "dist" / "agent-notifications.js")
    report["uap_go_pins"] = sorted(line.strip() for line in (repo / "go.mod").read_text().splitlines()
        if line.strip().startswith("github.com/777genius/plugin-kit-ai/"))
    observer = json.loads((repo / "opencode-plugin" / "package-lock.json").read_text())[
        "packages"]["node_modules/universal-agent-plugins-opencode-events"]
    report["uap_observer_pin"] = {"version": observer["version"], "integrity": observer["integrity"]}
    servers = [Sink("provider"), Sink("webhook")]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    provider, webhook = servers
    try:
        with tempfile.TemporaryDirectory(prefix="TEST-opencode-native-") as name:
            root = pathlib.Path(name).resolve()
            project = root / "project"
            project.mkdir()
            (project / "README.md").write_text("Disposable native qualification project.\n")
            config_dir = root / "opencode-config"
            config_dir.mkdir()
            config = root / "notifications.json"
            config.write_text(json.dumps({"notifications": {"desktop": {"enabled": False},
                "webhook": {"enabled": True, "preset": "custom", "format": "json",
                            "url": f"http://127.0.0.1:{webhook.server_port}/webhook"}}}))
            (project / "opencode.json").write_text(json.dumps({"model": "mock-notification/mock-notification",
                "provider": {"mock-notification": {"npm": "@ai-sdk/openai-compatible",
                    "name": "Sandbox provider", "options": {"baseURL": f"http://127.0.0.1:{provider.server_port}/v1",
                    "apiKey": "sandbox-only"}, "models": {"mock-notification": {
                    "name": "Sandbox scripted model", "limit": {"context": 128000, "output": 8192}}}}}}))
            env = {key: os.environ[key] for key in ("PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT",
                   "LD_LIBRARY_PATH", "SSL_CERT_FILE") if key in os.environ}
            home = root / "home"
            for key, path in {"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": root / "xdg-config",
                "XDG_DATA_HOME": root / "xdg-data", "XDG_CACHE_HOME": root / "xdg-cache",
                "XDG_STATE_HOME": root / "xdg-state", "XDG_RUNTIME_DIR": root / "xdg-run",
                "APPDATA": root / "appdata", "LOCALAPPDATA": root / "localappdata",
                "TEMP": root / "tmp", "TMP": root / "tmp", "TMPDIR": root / "tmp",
                "BUN_INSTALL_CACHE_DIR": root / "bun-cache"}.items():
                path.mkdir(exist_ok=True)
                env[key] = str(path)
            env.update({"OPENCODE_CONFIG_DIR": str(config_dir), "AGENT_NOTIFICATIONS_CONFIG": str(config),
                        "AGENT_NOTIFICATIONS_CONTROL_ROOT": str(root / "control"), "CI": "true",
                        "NO_COLOR": "1", "OPENCODE_DISABLE_AUTOUPDATE": "1"})
            opencode = root / ("opencode.exe" if args.os == "windows" else "opencode")
            if args.archive:
                extract_opencode(args.archive, opencode, args.os)
            else:
                opencode = args.opencode.resolve(strict=True)
            if VERSION not in run([str(opencode), "--version"], cwd=project, env=env):
                raise RuntimeError("OpenCode version mismatch")
            common = ["--control-root", str(root / "control"), "--runtime-root", str(root / "runtime"),
                      "--opencode-config-dir", str(config_dir), "--home", str(home),
                      "--xdg-config-home", env["XDG_CONFIG_HOME"]]
            run([str(candidate), "setup-opencode", "install", *common, "--binary", str(candidate),
                 "--webhook"], cwd=project, env=env)
            plugin = config_dir / "plugins" / "agent-notifications.js"
            if not plugin.is_file():
                raise RuntimeError("setup-opencode did not install its JS plugin")
            report["installed_plugin_sha256"] = digest(plugin)
            server_port = port()
            with (root / "opencode-serve.log").open("w", encoding="utf-8") as log:
                def start_server():
                    return subprocess.Popen([str(opencode), "serve", "--port", str(server_port),
                        "--hostname", "127.0.0.1", "--print-logs"], cwd=project, env=env,
                        stdout=log, stderr=subprocess.STDOUT)

                def stop_server(process):
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)

                server = start_server()
                try:
                    def ready():
                        if server.poll() is not None:
                            raise RuntimeError("OpenCode server exited before readiness")
                        try:
                            with socket.create_connection(("127.0.0.1", server_port), timeout=0.3):
                                return True
                        except OSError:
                            return False
                    wait_for(ready, 90, "OpenCode server")
                    def turn(label):
                        marker = "PRIVATE_PROMPT_" + secrets.token_hex(12)
                        before = provider.count()
                        run([str(opencode), "run", "--attach", f"http://127.0.0.1:{server_port}",
                            "--model", "mock-notification/mock-notification", "--format", "json",
                            f"{label} in this disposable project. {marker}"], cwd=project, env=env, timeout=120)
                        if not any(marker.encode() in body for body in provider.requests[before:]):
                            raise RuntimeError("OpenCode did not send the turn to the scripted model")
                        return marker
                    first_marker = turn("Complete first sandbox turn")
                    wait_for(lambda: webhook.count() >= 1, 25, "first completion webhook")
                    if webhook.count() != 1:
                        raise RuntimeError("first turn did not produce exactly one webhook")
                    payload = webhook.requests[0]
                    event = json.loads(payload)
                    if event.get("notification_type") != "task_complete" or event.get("agent_source") != "opencode":
                        raise RuntimeError("first webhook is not an OpenCode completion")
                    if first_marker.encode() in payload or b"PRIVATE_PROMPT_" in payload:
                        raise RuntimeError("private prompt marker leaked into webhook")
                    run([str(candidate), "setup-opencode", "update", *common, "--binary", str(candidate),
                         "--webhook"], cwd=project, env=env)
                    stop_server(server)
                    server = start_server()
                    wait_for(ready, 90, "updated OpenCode server")
                    second_marker = turn("Complete sandbox turn after update")
                    wait_for(lambda: webhook.count() >= 2, 25, "post-update completion webhook")
                    if webhook.count() != 2:
                        raise RuntimeError("updated setup did not produce exactly one more webhook")
                    updated_payload = webhook.requests[1]
                    updated_event = json.loads(updated_payload)
                    if updated_event.get("notification_type") != "task_complete" or updated_event.get("agent_source") != "opencode":
                        raise RuntimeError("post-update webhook is not an OpenCode completion")
                    if second_marker.encode() in updated_payload or b"PRIVATE_PROMPT_" in updated_payload:
                        raise RuntimeError("post-update private prompt marker leaked into webhook")
                    run([str(candidate), "setup-opencode", "remove", *common], cwd=project, env=env)
                    if plugin.exists():
                        raise RuntimeError("remove left the installed plugin file")
                    managed_binary = root / "runtime" / args.binary.name
                    if managed_binary.exists():
                        raise RuntimeError("remove left the managed executable")
                    # Keep the old executable at its former sandbox path to exercise
                    # the registration gate, rather than only observing spawn_failed.
                    shutil.copyfile(candidate, managed_binary)
                    if args.os != "windows":
                        managed_binary.chmod(0o755)
                    probe = {"version": 1, "kind": "turn_idle_verified", "sessionID": "sandbox",
                             "turnID": "revocation", "messageID": "revocation", "rootSession": True}
                    direct = subprocess.run([str(managed_binary), "opencode-event", "--protocol", "1"],
                        cwd=project, env=env, input=json.dumps(probe), capture_output=True, text=True,
                        encoding="utf-8", errors="replace", timeout=15)
                    if direct.returncode or json.loads(direct.stdout).get("reason") != "not_registered":
                        raise RuntimeError("restored sandbox executable did not deny after removal")
                    before = provider.count()
                    turn("Complete sandbox turn after removal")
                    if provider.count() <= before:
                        raise RuntimeError("post-removal turn did not reach scripted provider")
                    # The bundled JS bounds its spawned event process at 25 seconds.
                    time.sleep(27)
                    if webhook.count() != 2:
                        raise RuntimeError("still-loaded plugin sent a webhook after removal")
                    if server.poll() is not None:
                        raise RuntimeError("OpenCode server exited before post-removal check")
                    report.update({"status": "pass", "native_turns": 3, "provider_requests": provider.count(),
                                   "completion_webhooks_before_remove": 2,
                                   "webhooks_after_remove": webhook.count() - 2,
                                   "post_remove_observation_seconds": 27,
                                   "restored_executable_gate": "not_registered",
                                   "loaded_old_plugin_post_remove": "no_webhook"})
                finally:
                    stop_server(server)
    except Exception as error:
        report["status"] = "fail"
        report["failure"] = redact(str(error))[-1200:]
        raise
    finally:
        for item in servers:
            item.shutdown()
            item.server_close()
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
        print(json.dumps(report, sort_keys=True))


if __name__ == "__main__":
    main()
