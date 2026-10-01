import { spawn } from 'node:child_process';
import path from 'node:path';
import { performance } from 'node:perf_hooks';
import { profileReceipt } from './protocol.mjs';

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
  if (!closedObject(configuration, ['executable', 'privateCwd', 'controlRoot', 'osEnv', 'deliveryEnv', 'origin']))
    throw new TypeError('invalid_configuration');
  const { executable, privateCwd, controlRoot, osEnv = {}, deliveryEnv = {}, origin } = configuration;
  if (![executable, privateCwd, controlRoot].every((value) => absoluteNativePath(value))
    || (process.platform === 'win32' && !executable.toLowerCase().endsWith('.exe')))
    throw new TypeError('invalid_paths');
  if (origin !== undefined && !/^[a-f0-9]{64}$/.test(origin)) throw new TypeError('invalid_origin');
  const nativePID = process.pid, publicExecPath = process.execPath;
  const profileInput = Buffer.from(JSON.stringify({ protocol: 1, hostExecutable: publicExecPath, origin, controlRoot,
    nativePID, entry: 'serve', publicExecPath }));
  const base = process.platform === 'win32'
    ? { USERPROFILE: privateCwd, APPDATA: privateCwd, LOCALAPPDATA: privateCwd, TEMP: privateCwd, TMP: privateCwd }
    : { HOME: privateCwd, XDG_CONFIG_HOME: privateCwd, XDG_RUNTIME_DIR: privateCwd };
  const clockEnv = { ...base, ...environment(osEnv, osKeys) };
  const eventEnv = { ...clockEnv, ...environment(deliveryEnv, deliveryKeys), AGENT_NOTIFICATIONS_CONTROL_ROOT: controlRoot };
  const profileEnv = { ...clockEnv, AGENT_NOTIFICATIONS_CONTROL_ROOT: controlRoot,
    AGENT_NOTIFICATIONS_ORIGIN: origin, AGENT_NOTIFICATIONS_NATIVE_PID: String(nativePID),
    AGENT_NOTIFICATIONS_HOST_EXECUTABLE: publicExecPath, AGENT_NOTIFICATIONS_HOST_ENTRY: 'serve',
    AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH: publicExecPath };
  const entries = new Set(), watchers = new Set();
  let disposed = false, disabled = false;
  const status = () => Object.freeze({ accepting: !disposed && !disabled, disposed,
    occupied: [...entries].reduce((sum, entry) => sum + entry.weight, 0), unresolved: [...entries].filter((entry) => entry.unresolved).length });
  const changed = () => { for (const watcher of watchers) watcher(); };
  const current = (signal, isCurrent) => {
    try { return !signal?.aborted && isCurrent() === true && !signal?.aborted; }
    catch { return false; }
  };

  function handoff(kind, options) {
    if (!closedObject(options, kind !== 'event' ? ['signal', 'isCurrent', 'deadline'] : ['frame', 'prepare', 'signal', 'isCurrent', 'deadline']))
      return Promise.resolve(result('invalid_request'));
    const { frame, prepare, signal, isCurrent, deadline } = options;
    const weight = kind === 'profile' ? 2 : 1;
    if (typeof isCurrent !== 'function' || (signal !== undefined && !(signal instanceof AbortSignal))
      || (deadline !== undefined && !Number.isFinite(deadline))
      || (kind === 'event' && !(prepare === undefined && Buffer.isBuffer(frame) && frame.length <= 4096 ||
        frame === undefined && typeof prepare === 'function'))
      || (kind === 'profile' && (origin === undefined || !absoluteNativePath(publicExecPath) || profileInput.length > 4096)))
      return Promise.resolve(result('invalid_request'));
    if (!current(signal, isCurrent)) return Promise.resolve(result('invalidated'));
    if (disposed || disabled) return Promise.resolve(result('registry_unavailable'));
    if (status().occupied + weight > 4) return Promise.resolve(result('capacity_suppressed'));
    if (deadline !== undefined && deadline <= performance.now()) return Promise.resolve(result('deadline'));
    let body = kind === 'profile' ? Buffer.from(profileInput) : kind === 'event' && frame ? Buffer.from(frame) : undefined;
    // Recheck at acquisition/spawn, after all synchronous input preparation.
    if (!current(signal, isCurrent)) return Promise.resolve(result('invalidated'));
    if (disposed || disabled) return Promise.resolve(result('registry_unavailable'));
    if (status().occupied + weight > 4) return Promise.resolve(result('capacity_suppressed'));
    if (deadline !== undefined && deadline <= performance.now()) return Promise.resolve(result('deadline'));
    const entry = { controller: new AbortController(), unresolved: false, weight };
    entries.add(entry);
    return new Promise((resolve) => {
      let child, afterSpawn, forcedKill = false, proofOutput = true, closed = false, failure, output = Buffer.alloc(0), stderrBytes = 0;
      let deadlineTimer, escalationTimer, proofTimer;
      const spawnedAt = performance.now();
      const stopAt = Math.min(spawnedAt + (kind === 'clock' ? 2000 : kind === 'profile' ? 10000 : 22000), deadline ?? Infinity);
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
        if (kind !== 'profile' || !['aborted', 'invalidated', 'deadline'].includes(reason)) {
          proofOutput = false; output = Buffer.alloc(0);
        }
        // Invalidate our authorization before either signal is sent.
        entry.controller.abort();
        clearTimeout(deadlineTimer);
        kill('SIGTERM');
        const now = performance.now();
        escalationTimer = setTimeout(() => { if (!closed) { forcedKill = true; kill('SIGKILL'); } }, delay(Math.min(now + 1000, closeBy)));
        entry.proofBy = Math.min(now + 3000, closeBy);
        proofTimer = setTimeout(unproved, delay(entry.proofBy));
      }
      const abort = () => terminate('aborted');
      entry.stop = abort;
      try {
        afterSpawn = prepare?.();
        if (prepare && typeof afterSpawn !== 'function' || entry.controller.signal.aborted || signal?.aborted || disposed || disabled)
          throw new TypeError('invalid_preparation');
        child = spawn(executable, [kind === 'clock' ? 'opencode-clock' : kind === 'profile' ? 'opencode-runtime-profile' : 'opencode-event', '--protocol', '1'], {
          shell: false, windowsHide: true, cwd: privateCwd,
          stdio: ['pipe', 'pipe', 'pipe'], env: kind === 'clock' ? clockEnv : kind === 'profile' ? profileEnv : eventEnv,
        });
      } catch {
        // A synchronous throw returned no handle. Only this path releases without close.
        entries.delete(entry);
        clearTimeout(deadlineTimer); clearTimeout(escalationTimer); clearTimeout(proofTimer);
        changed();
        resolve(result('spawn_failed'));
        return;
      }
      let postFailure = false;
      if (afterSpawn) {
        try { body = afterSpawn(); if (!Buffer.isBuffer(body) || body.length > 4096) postFailure = true; }
        catch { postFailure = true; }
      }
      child.on('error', () => { proofOutput = false; terminate('spawn_failed'); });
      for (const stream of [child.stdin, child.stdout, child.stderr])
        stream.on('error', () => { proofOutput = false; terminate('stream_error'); });
      child.stdout.on('data', (chunk) => {
        if (closed || failure && !(kind === 'profile' && proofOutput)) return;
        if (output.length + chunk.length > 1024) { proofOutput = false; output = Buffer.alloc(0); terminate('output_limit'); return; }
        output = Buffer.concat([output, chunk]);
      });
      child.stderr.on('data', (chunk) => {
        if (closed || failure && !(kind === 'profile' && proofOutput)) return;
        stderrBytes = Math.min(1025, stderrBytes + chunk.length);
        if (stderrBytes > 1024) { proofOutput = false; output = Buffer.alloc(0); terminate('output_limit'); }
      });
      child.once('close', (code) => {
        // A late close remains a missed qualification even if timer dispatch was delayed.
        if (performance.now() > (entry.proofBy ?? closeBy)) unproved();
        closed = true;
        clearTimeout(deadlineTimer);
        clearTimeout(escalationTimer);
        clearTimeout(proofTimer);
        signal?.removeEventListener('abort', abort);
        let innerProof = kind !== 'profile';
        if (kind === 'profile' && code === 0 && !forcedKill && proofOutput && !entry.unresolved) {
          try { profileReceipt(output); innerProof = true; } catch {}
        }
        if (!innerProof) { entry.unresolved = true; disabled = true; }
        // Invalid inner closure permanently retains BOTH reservations, even after outer close.
        if (innerProof) entries.delete(entry);
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
      if (postFailure) terminate('invalidated');
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
  function cancel() {
    // Tokens invalidate as a group before any owned signal.
    for (const entry of entries) entry.controller.abort();
    for (const entry of entries) entry.stop();
    return drain();
  }
  function dispose() {
    disposed = true;
    // All internal tokens become invalid before termination of any child.
    for (const entry of entries) entry.controller.abort();
    for (const entry of entries) entry.stop();
    return drain();
  }
  return Object.freeze({ profile: (options) => handoff('profile', options), clock: (options) => handoff('clock', options), event: (options) => handoff('event', options),
    status, drain, cancel, dispose });
}
