"""Deterministic real-Pi process fixture, shared by contract/product tests.

Only the model service is substituted. Pi, its loader and native events remain
real. User credentials/environment are never inherited. No transcript is saved.
"""
import http.server
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import threading
import time


def require(value, code):
    if not value:
        raise RuntimeError(code)


class Provider:
    def __init__(self, scenario, hold_reply=False):
        self.scenario, self.requests = scenario, []
        self.release = threading.Event()
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                size = int(self.headers.get("content-length", "0"))
                if not 0 < size <= 2 * 1024 * 1024 or self.path != "/v1/chat/completions":
                    self.send_error(400)
                    return
                body = json.loads(self.rfile.read(size))
                if body.get("model") != "TEST-local" or body.get("stream") is not True:
                    self.send_error(400)
                    return
                owner.requests.append({"path": self.path, "stream": True, "model": "TEST-local"})
                if hold_reply and not owner.release.wait(30):
                    self.send_error(504)
                    return
                if owner.scenario == "error" or owner.scenario == "retry" and len(owner.requests) == 1:
                    error = b'{"error":{"message":"invalid_api_key"}}' if owner.scenario == "error" else b'{"error":{"message":"overloaded_error"}}'
                    self.send_response(400 if owner.scenario == "error" else 503)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(error)))
                    self.end_headers()
                    self.wfile.write(error)
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                common = {"id": "TEST-response", "object": "chat.completion.chunk", "created": 1, "model": "TEST-local"}
                first = {**common, "choices": [{"index": 0, "delta": {"role": "assistant", "content": "TEST_PI_DONE"}, "finish_reason": None}]}
                last = {**common, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}}
                try:
                    self.wfile.write(("data: " + json.dumps(first) + "\n\n").encode())
                    self.wfile.flush()
                    if owner.scenario == "abort":
                        owner.release.wait(3)
                    self.wfile.write(("data: " + json.dumps(last) + "\n\ndata: [DONE]\n\n").encode())
                except (BrokenPipeError, ConnectionResetError):
                    pass  # Expected when the real Pi client aborts its HTTP stream.

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = "http://127.0.0.1:" + str(self.server.server_port) + "/v1"

    def close(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        require(not self.thread.is_alive(), "provider_thread_not_collected")


def prepare(root, provider, scenario):
    root = Path(root)
    for name in ("home", "agent/extensions", "project", "tmp"):
        (root / name).mkdir(parents=True, exist_ok=True, mode=0o700)
    (root / "agent/models.json").write_text(json.dumps({"providers": {"TEST-local": {
        "api": "openai-completions", "baseUrl": provider.base_url, "apiKey": "TEST-only",
        "models": [{"id": "TEST-local", "name": "TEST local", "reasoning": False, "input": ["text"],
                    "contextWindow": 32000, "maxTokens": 1024,
                    "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}))
    (root / "agent/settings.json").write_text(json.dumps({"retry": {"enabled": scenario == "retry", "maxRetries": 1,
        "baseDelayMs": 1, "provider": {"maxRetries": 0}}, "compaction": {"enabled": False}}))
    # This is a foreign TEST witness, not the candidate product extension. The
    # product fixture must install its observer exclusively through common setup.
    (root / "agent/extensions/TEST-witness.ts").write_text('''
import { appendFileSync } from "node:fs";
export default function(pi) {
  const record = value => appendFileSync(process.env.TEST_PI_WITNESS, JSON.stringify(value) + "\\n");
  record({type: "factory", pid: process.pid});
  let queued = false;
  if (process.env.TEST_PI_CASE === "followup") pi.on("agent_end", () => {
    if (!queued) { queued = true; pi.sendUserMessage("TEST followup", {deliverAs: "followUp"}); }
  });
  pi.registerCommand("TEST-dialog", {description: "TEST UI witness", handler: async (_args, ctx) => {
    await ctx.ui.input("TEST question");
  }});
  for (const name of ["session_start", "agent_start", "agent_end", "agent_before_settle", "agent_settled", "ui_prompt_start", "ui_prompt_end", "session_shutdown"]) {
    pi.on(name, (event, ctx) => record({type: name, mode: ctx.mode, hasUI: ctx.hasUI, idle: ctx.isIdle(),
      aborted: event.aborted, outcome: event.outcome, reason: event.reason}));
  }
}
''')
    env = {key: os.environ[key] for key in ("PATH", "SystemRoot", "SYSTEMROOT", "WINDIR", "ComSpec", "PATHEXT") if key in os.environ}
    env.update(HOME=str(root / "home"), USERPROFILE=str(root / "home"), PI_CODING_AGENT_DIR=str(root / "agent"),
               XDG_CONFIG_HOME=str(root / "home/.config"), XDG_DATA_HOME=str(root / "home/.local/share"),
               XDG_CACHE_HOME=str(root / "home/.cache"), TMPDIR=str(root / "tmp"), TMP=str(root / "tmp"), TEMP=str(root / "tmp"),
               TEST_PI_WITNESS=str(root / "witness.jsonl"), TEST_PI_CASE=scenario, PI_OFFLINE="1")
    return env


class PiProcess:
    def __init__(self, executable, root, env, mode="rpc"):
        self.root, self.env, self.mode = Path(root), env, mode
        self.records, self.events, self.errors = [], queue.Queue(), []
        argv = [str(executable), "--offline", "--no-approve", "--no-session", "--no-mcp", "--no-tools",
                "--provider", "TEST-local", "--model", "TEST-local"]
        argv += ["--print"] if mode == "print" else ["--mode", mode]
        if mode != "rpc":
            argv.append("TEST say done")
        self.child = subprocess.Popen(argv, cwd=self.root / "project", env=env, stdin=subprocess.PIPE,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=os.name != "nt")
        if mode != "rpc":
            # Print/JSON startup consumes redirected stdin before prompting.
            # Holding this pipe open would block the actual CLI at startup.
            self.child.stdin.close()
        self.stderr_bytes = 0
        self.stdout_bytes = 0

        def stdout_reader():
            try:
                while line := self.child.stdout.readline(262145):
                    self.stdout_bytes += len(line)
                    require(len(line) <= 262144 and self.stdout_bytes <= 4 * 1024 * 1024, "native_stdout_limit")
                    if mode == "print":
                        require(line.strip() == b"TEST_PI_DONE", "unexpected_print_output")
                        continue
                    row = json.loads(line)
                    self.events.put(row)
                    # Retain only non-private event metadata. No prompts, model
                    # messages, provider errors or transcripts enter evidence.
                    metadata = {key: row[key] for key in ("type", "aborted", "command", "success") if key in row}
                    self.records.append(metadata)
            except (ValueError, RuntimeError) as error:
                self.errors.append(str(error))

        def stderr_reader():
            while block := self.child.stderr.read(4096):
                self.stderr_bytes += len(block)
                if self.stderr_bytes > 65536:
                    self.errors.append("native_stderr_limit")
                    break

        self.readers = [threading.Thread(target=stdout_reader, daemon=True), threading.Thread(target=stderr_reader, daemon=True)]
        for reader in self.readers:
            reader.start()

    def send(self, value):
        self.child.stdin.write((json.dumps(value) + "\n").encode())
        self.child.stdin.flush()

    def next(self, timeout=20):
        require(not self.errors, "native_reader_failed")
        try:
            return self.events.get(timeout=timeout)
        except queue.Empty:
            raise RuntimeError("native_event_timeout")

    def wait(self, timeout=20):
        code = self.child.wait(timeout=timeout)
        for reader in self.readers:
            reader.join(timeout=2)
        require(all(not reader.is_alive() for reader in self.readers), "native_reader_not_collected")
        require(not self.errors, "native_reader_failed")
        return code

    def close(self):
        if self.child.poll() is None:
            try:
                self.child.stdin.close()
                self.wait(timeout=3)
            except (BrokenPipeError, subprocess.TimeoutExpired, RuntimeError):
                if os.name == "nt":
                    subprocess.run(["taskkill.exe", "/PID", str(self.child.pid), "/T", "/F"],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
                else:
                    try:
                        os.killpg(self.child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                self.child.wait(timeout=3)
        for reader in self.readers:
            reader.join(timeout=2)


def witness(root):
    return [json.loads(line) for line in (Path(root) / "witness.jsonl").read_text().splitlines()]


def verify_case(case, rows, requests):
    counts = {kind: sum(row["type"] == kind for row in rows) for kind in ("factory", "agent_start", "agent_end", "agent_before_settle", "agent_settled", "ui_prompt_start", "ui_prompt_end")}
    require(counts["factory"] == 1, "witness_factory_not_exactly_once")
    if case == "ui":
        require(len(requests) == counts["agent_end"] == counts["agent_settled"] == 0, "idle_ui_unexpected_run")
        require(counts["ui_prompt_start"] == counts["ui_prompt_end"] == 1, "idle_ui_lifecycle_missing")
        require(all(row["idle"] and row["mode"] == "rpc" and row["hasUI"] for row in rows if row["type"].startswith("ui_prompt")), "idle_ui_context_wrong")
    else:
        expected = 2 if case in ("retry", "followup") else 1
        require(len(requests) == counts["agent_end"] == expected and counts["agent_settled"] == 1, "native_settlement_count_wrong")
        settled = [row for row in rows if row["type"] == "agent_settled"][0]
        require(settled["aborted"] is (case == "abort") and settled["idle"] is True, "native_settlement_outcome_wrong")
        outcomes = [row["outcome"] for row in rows if row["type"] == "agent_before_settle"]
        require(outcomes == ([] if case == "abort" else ["error" if case == "error" else "completed"]), "native_boundary_outcome_wrong")
    return counts
