import { spawn } from 'node:child_process';
import path from 'node:path';
import { performance } from 'node:perf_hooks';

const deliveryKeys = process.platform === 'win32'
  ? ['AGENT_NOTIFICATIONS_CONFIG', 'USERPROFILE', 'HOMEDRIVE', 'HOMEPATH', 'APPDATA', 'LOCALAPPDATA', 'TEMP', 'TMP']
  : ['AGENT_NOTIFICATIONS_CONFIG', 'HOME', 'XDG_CONFIG_HOME', 'DBUS_SESSION_BUS_ADDRESS', 'XDG_RUNTIME_DIR'];
const osKeys = process.platform === 'win32' ? ['SystemRoot', 'WINDIR'] : [];
const result = (status, output = Buffer.alloc(0)) => Object.freeze({ status, output });
const closedObject = (value, keys) => value && Object.getPrototypeOf(value) === Object.prototype
  && Reflect.ownKeys(value).every((key) => keys.includes(key));

// Shared native-path guard; platform is explicit so installers can validate targets.
export function absoluteNativePath(value, platform = process.platform) {
  if (typeof value !== 'string' || value.includes('\0')) return false;
  if (platform !== 'win32') return path.posix.isAbsolute(value);
  if (value.startsWith('\\\\?\\') || value.startsWith('\\\\.\\')) return false;
  return (/^[A-Za-z]:\\/.test(value) || /^\\\\[^\\]+\\[^\\]+\\/.test(value))
    && path.win32.normalize(value) === value;
}

function environment(values, allowed) {
  if (!closedObject(values, allowed)) throw new TypeError('invalid_environment');
  for (const [key, value] of Object.entries(values)) {
    if (!allowed.includes(key) || typeof value !== 'string' || value.includes('\0') || value.length > 8192)
      throw new TypeError('invalid_environment');
  }
  return { ...values };
}

// Caller supplies installation-owned paths and an already provisioned private cwd.
// There is deliberately no spawn injection, arbitrary command, queue or retry port.
export function createProcessRegistry(configuration) {
  if (!closedObject(configuration, ['executable', 'privateCwd', 'controlRoot', 'osEnv', 'deliveryEnv']))
    throw new TypeError('invalid_configuration');
  const { executable, privateCwd, controlRoot, osEnv = {}, deliveryEnv = {} } = configuration;
  if (![executable, privateCwd, controlRoot].every((value) => absoluteNativePath(value))
    || (process.platform === 'win32' && !executable.toLowerCase().endsWith('.exe')))
    throw new TypeError('invalid_paths');
  const base = process.platform === 'win32'
    ? { USERPROFILE: privateCwd, APPDATA: privateCwd, LOCALAPPDATA: privateCwd, TEMP: privateCwd, TMP: privateCwd }
    : { HOME: privateCwd, XDG_CONFIG_HOME: privateCwd, XDG_RUNTIME_DIR: privateCwd };
  const clockEnv = { ...base, ...environment(osEnv, osKeys) };
  const eventEnv = { ...clockEnv, ...environment(deliveryEnv, deliveryKeys), AGENT_NOTIFICATIONS_CONTROL_ROOT: controlRoot };
  const entries = new Set(), watchers = new Set();
  let disposed = false, disabled = false;
  const status = () => Object.freeze({ accepting: !disposed && !disabled, disposed,
    occupied: entries.size, unresolved: [...entries].filter((entry) => entry.unresolved).length });
  const changed = () => { for (const watcher of watchers) watcher(); };
  const current = (signal, isCurrent) => {
    try { return !signal?.aborted && isCurrent() === true && !signal?.aborted; }
    catch { return false; }
  };

  function handoff(kind, options) {
    if (!closedObject(options, kind === 'clock' ? ['signal', 'isCurrent', 'deadline'] : ['frame', 'signal', 'isCurrent', 'deadline']))
      return Promise.resolve(result('invalid_request'));
    const { frame, signal, isCurrent, deadline } = options;
    if (typeof isCurrent !== 'function' || (signal !== undefined && !(signal instanceof AbortSignal))
      || (deadline !== undefined && !Number.isFinite(deadline))
      || (kind === 'event' && (!Buffer.isBuffer(frame) || frame.length > 4096)))
      return Promise.resolve(result('invalid_request'));
    if (!current(signal, isCurrent)) return Promise.resolve(result('invalidated'));
    if (disposed || disabled) return Promise.resolve(result('registry_unavailable'));
    if (entries.size >= 4) return Promise.resolve(result('capacity_suppressed'));
    if (deadline !== undefined && deadline <= performance.now()) return Promise.resolve(result('deadline'));
    const body = kind === 'event' ? Buffer.from(frame) : undefined;
    // Recheck at acquisition/spawn, after all synchronous input preparation.
    if (!current(signal, isCurrent)) return Promise.resolve(result('invalidated'));
    if (disposed || disabled) return Promise.resolve(result('registry_unavailable'));
    if (entries.size >= 4) return Promise.resolve(result('capacity_suppressed'));
    if (deadline !== undefined && deadline <= performance.now()) return Promise.resolve(result('deadline'));
    const entry = { controller: new AbortController(), unresolved: false };
    entries.add(entry);
    return new Promise((resolve) => {
      let child, closed = false, failure, output = Buffer.alloc(0), stderrBytes = 0;
      let deadlineTimer, escalationTimer, proofTimer;
      const spawnedAt = performance.now();
      const stopAt = Math.min(spawnedAt + (kind === 'clock' ? 2000 : 22000), deadline ?? Infinity);
      const closeBy = stopAt + 3000;
      const delay = (at) => Math.max(0, at - performance.now());
      const kill = (signalName) => { try { child.kill(signalName); } catch { /* close alone is proof */ } };
      const unproved = () => {
        if (closed) return;
        entry.unresolved = true;
        disabled = true;
        output = Buffer.alloc(0);
        changed();
      };
      function terminate(reason) {
        if (closed || failure) return;
        failure = reason;
        output = Buffer.alloc(0);
        // Invalidate our authorization before either signal is sent.
        entry.controller.abort();
        clearTimeout(deadlineTimer);
        kill('SIGTERM');
        const now = performance.now();
        escalationTimer = setTimeout(() => { if (!closed) kill('SIGKILL'); }, delay(Math.min(now + 1000, closeBy)));
        entry.proofBy = Math.min(now + 3000, closeBy);
        proofTimer = setTimeout(unproved, delay(entry.proofBy));
      }
      const abort = () => terminate('aborted');
      entry.stop = abort;
      try {
        child = spawn(executable, [kind === 'clock' ? 'opencode-clock' : 'opencode-event', '--protocol', '1'], {
          shell: false, windowsHide: true, cwd: privateCwd,
          stdio: ['pipe', 'pipe', 'pipe'], env: kind === 'clock' ? clockEnv : eventEnv,
        });
      } catch {
        // A synchronous throw returned no handle. Only this path releases without close.
        entries.delete(entry);
        changed();
        resolve(result('spawn_failed'));
        return;
      }
      child.on('error', () => terminate('spawn_failed'));
      for (const stream of [child.stdin, child.stdout, child.stderr])
        stream.on('error', () => terminate('stream_error'));
      child.stdout.on('data', (chunk) => {
        if (closed || failure) return;
        if (output.length + chunk.length > 1024) { terminate('output_limit'); return; }
        output = Buffer.concat([output, chunk]);
      });
      child.stderr.on('data', (chunk) => {
        if (closed || failure) return;
        stderrBytes = Math.min(1025, stderrBytes + chunk.length);
        if (stderrBytes > 1024) terminate('output_limit');
      });
      child.once('close', (code) => {
        // A late close remains a missed qualification even if timer dispatch was delayed.
        if (performance.now() > (entry.proofBy ?? closeBy)) unproved();
        closed = true;
        clearTimeout(deadlineTimer);
        clearTimeout(escalationTimer);
        clearTimeout(proofTimer);
        signal?.removeEventListener('abort', abort);
        entries.delete(entry);
        // This is the handoff's only asynchronous settlement/release point.
        const valid = current(signal, isCurrent);
        const outcome = entry.unresolved ? 'ipc_termination_unproved'
          : !valid ? 'invalidated'
            : failure ?? (performance.now() > stopAt ? 'deadline' : code === 0 ? 'ok' : 'exited');
        resolve(result(outcome, outcome === 'ok' ? output : undefined));
        changed();
      });
      deadlineTimer = setTimeout(() => terminate('deadline'), delay(stopAt));
      signal?.addEventListener('abort', abort, { once: true });
      if (!current(signal, isCurrent)) terminate('invalidated');
      if (!failure) {
        try { child.stdin.end(body); } catch { terminate('stream_error'); }
      }
    });
  }

  function drain(options = {}) {
    if (!closedObject(options, ['timeoutMs'])) throw new TypeError('invalid_timeout');
    const { timeoutMs = 3000 } = options;
    if (!Number.isFinite(timeoutMs) || timeoutMs < 0 || timeoutMs > 3000) throw new TypeError('invalid_timeout');
    return new Promise((resolve) => {
      let timer;
      const finish = () => {
        clearTimeout(timer);
        watchers.delete(check);
        const snapshot = status();
        resolve(Object.freeze({ ...snapshot, reaped: snapshot.occupied === 0,
          status: snapshot.occupied === 0 ? 'drained' : snapshot.unresolved ? 'ipc_termination_unproved' : 'pending' }));
      };
      const check = () => { if (entries.size === 0 || status().unresolved) finish(); };
      watchers.add(check);
      timer = setTimeout(finish, timeoutMs);
      check();
    });
  }
  function dispose() {
    disposed = true;
    // All internal tokens become invalid before termination of any child.
    for (const entry of entries) entry.controller.abort();
    for (const entry of entries) entry.stop();
    return drain();
  }
  return Object.freeze({ clock: (options) => handoff('clock', options), event: (options) => handoff('event', options),
    status, drain, dispose });
}
