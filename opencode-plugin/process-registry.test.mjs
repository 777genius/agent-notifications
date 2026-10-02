import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import dc from 'node:diagnostics_channel';
import { performance } from 'node:perf_hooks';
import { absoluteNativePath, createProcessRegistry } from './process-registry.mjs';

const node = process.execPath;
const fixtureLimit = performance.now() + 53000; // Reserve cleanup even if the stop file fails.
const fixture = `#!${node}
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
const root = process.cwd(), mode = path.basename(process.argv[1], '.mjs');
const mark = (name, data = '') => fs.writeFileSync(path.join(root, process.pid + '-' + name), data);
const start = () => mark('start', JSON.stringify({ argv: process.argv.slice(2), cwd: root,
  keys: Object.keys(process.env).sort(), home: process.env.HOME, config: process.env.AGENT_NOTIFICATIONS_CONFIG,
  native: Object.fromEntries(['CONTROL_ROOT', 'ORIGIN', 'NATIVE_PID', 'HOST_EXECUTABLE', 'HOST_ENTRY', 'PUBLIC_EXEC_PATH']
    .map(key => ['AGENT_NOTIFICATIONS_' + key, process.env['AGENT_NOTIFICATIONS_' + key]])) }));
if (mode === 'tail') {
  const program = "const fs = require('node:fs'); const root = process.argv[1]; fs.writeFileSync(root + '/tail-start', ''); const end = () => { fs.writeFileSync(root + '/tail-done', ''); process.exit(0); }; setTimeout(end, 5000); setInterval(() => { if (fs.existsSync(root + '/stop')) end(); }, 20);";
  spawn(process.execPath, ['-e', program, root], { stdio: ['ignore', 'inherit', 'inherit'], env: { HOME: root } });
  start(); setInterval(() => {}, 100);
} else if (['hold', 'trickle', 'stdout', 'stderr'].includes(mode)) {
  process.on('SIGTERM', () => mark('term', String(performance.now())));
  start();
  let sent = false, ticks = 0;
  setInterval(() => {
    if (mode === 'trickle' && ++ticks % 10 === 0) process.stdout.write('t');
    if (!sent && ['stdout', 'stderr'].includes(mode) && fs.existsSync(path.join(root, 'go'))) {
      sent = true; process[mode].write(Buffer.alloc(2048, 88));
    }
  }, 10);
} else {
  start(); let bytes = 0, firstByte;
  process.stdin.on('data', chunk => { bytes += chunk.length; firstByte ??= chunk[0]; });
  process.stdin.on('end', () => {
    mark('input', JSON.stringify({ bytes, firstByte }));
    if (process.argv[2] === 'opencode-runtime-profile')
      process.stdout.write(JSON.stringify({ protocol: 1, semantic: 'unverified', generation: 'none', resourceClosure: 'reaped_or_not_started' }));
    else if (mode === 'boundary') { process.stderr.write(Buffer.alloc(1024, 83)); process.stdout.write(Buffer.alloc(1024, 79)); }
    else process.stdout.write('fixture-response');
  });
}
`;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function bounded(promise, ms = 6500) {
  ms = Math.max(0, Math.min(ms, fixtureLimit - performance.now()));
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('fixture_wait_expired')), ms);
  })]); } finally { clearTimeout(timer); }
}
async function until(predicate, ms = 2000, cleanup = false) {
  const end = cleanup ? performance.now() + ms : Math.min(performance.now() + ms, fixtureLimit);
  while (!await predicate()) {
    assert.ok(performance.now() < end, 'fixture phase did not arrive');
    await sleep(10);
  }
}
async function exists(file) { try { await fs.access(file); return true; } catch { return false; } }

// Real Node diagnostics observe returned handles and independent exit/close events;
// they neither replace spawn nor change the registry's production interface.
async function withFixture(mode, run) {
  assert.ok(performance.now() < fixtureLimit, '60s fixture budget exhausted');
  assert.equal(process.platform, 'linux', 'this fixture qualifies Linux only');
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-e2a-owned-'));
  const home = path.join(root, 'private-HOME-XDG');
  await fs.mkdir(home, { mode: 0o700 });
  const executable = path.join(root, `${mode}.mjs`), children = [];
  const observe = ({ process: child }) => {
    const record = { child, exited: false, closed: false };
    children.push(record);
    child.once('exit', () => { record.exited = true; record.exitAt = performance.now(); });
    child.once('close', () => { record.closed = true; record.closeAt = performance.now(); });
  };
  dc.subscribe('child_process', observe);
  const registries = [];
  try {
    await fs.writeFile(executable, fixture, { mode: 0o700 });
    const make = (overrides = {}) => {
      const registry = createProcessRegistry({ executable, privateCwd: home, controlRoot: home, ...overrides });
      registries.push(registry);
      return registry;
    };
    const phase = (record, name) => path.join(home, `${record.child.pid}-${name}`);
    await run({ make, root, home, executable, children, phase });
  } finally {
    // Descendant pipe-holder has its own bounded lifetime and a test-owned stop file.
    try { await fs.writeFile(path.join(home, 'stop'), ''); } catch { /* tail still self-expires */ }
    const disposal = registries.map((registry) => registry.dispose());
    for (const { child, closed } of children) if (!closed) {
      try { child.kill('SIGKILL'); } catch { /* wait for close, never infer reaping */ }
    }
    try {
      await Promise.all([Promise.all(disposal), until(() => children.every((record) => record.closed), 5500, true)]);
      if (await exists(path.join(home, 'tail-start'))) assert.ok(await exists(path.join(home, 'tail-done')));
    } finally {
      dc.unsubscribe('child_process', observe);
      if (children.every((record) => record.closed)) await fs.rm(root, { recursive: true, force: true });
    }
  }
}
const options = () => ({ isCurrent: () => true });
const eventOptions = () => ({ ...options(), frame: Buffer.from('private-original-frame') });
const focus = { timeout: 12000, concurrency: false };

// Red if a failing assertion or missing private directory skips actual child cleanup.
test('fixture cleanup reaps its owned child even when its stop-file write fails', focus, async () => {
  let record, registry;
  await assert.rejects(withFixture('hold', async ({ make, home, children, phase }) => {
    registry = make();
    registry.event(eventOptions());
    record = children[0];
    await until(() => exists(phase(record, 'start')));
    await fs.rm(home, { recursive: true });
    throw new Error('TEST-expected-failure');
  }), /TEST-expected-failure/);
  assert.ok(record.closed && record.exited);
  assert.equal(registry.status().occupied, 0);
});

// Red if a fifth combined helper/event starts, or abort frees a still-live handle.
test('four combined handles; immediate fifth suppression; abort retains until close', focus, async () => {
  await withFixture('hold', async ({ make, children, phase }) => {
    const registry = make(), controller = new AbortController();
    const pending = [registry.clock({ ...options(), signal: controller.signal }),
      ...Array.from({ length: 3 }, () => registry.event({ ...eventOptions(), signal: controller.signal }))];
    let settled = 0;
    pending.forEach((promise) => promise.then(() => { settled++; }));
    await until(async () => children.length === 4 && (await Promise.all(children.map((r) => exists(phase(r, 'start'))))).every(Boolean));
    assert.equal((await bounded(registry.event(eventOptions()), 100)).status, 'capacity_suppressed');
    assert.equal(children.length, 4);
    controller.abort();
    await until(async () => (await Promise.all(children.map((r) => exists(phase(r, 'term'))))).every(Boolean));
    assert.equal(settled, 0);
    assert.equal(registry.status().occupied, 4);
    assert.equal((await bounded(registry.clock(options()), 100)).status, 'capacity_suppressed');
    assert.ok(children.every((r) => !r.closed && !r.exited));
    const outcomes = await bounded(Promise.all(pending));
    assert.ok(outcomes.every((r) => r.status === 'invalidated' && r.output.length === 0));
    assert.ok(children.every((r) => r.closed && r.exited));
    assert.equal(registry.status().occupied, 0);
    await sleep(30);
    assert.equal(children.length, 4, 'suppressed work was retried or queued');
  });
});

// Red if registry capacity/disposal is global or kills another instance's handle.
test('capacity and disposal belong to one registry instance', focus, async () => {
  await withFixture('hold', async ({ make, children, phase }) => {
    const first = make(), second = make();
    const one = first.event(eventOptions()), two = second.event(eventOptions());
    await until(async () => (await Promise.all(children.map((r) => exists(phase(r, 'start'))))).every(Boolean));
    assert.equal(first.status().occupied, 1); assert.equal(second.status().occupied, 1);
    assert.equal((await bounded(first.dispose())).reaped, true);
    assert.equal((await bounded(one)).status, 'aborted');
    assert.ok(children[0].closed && !children[1].closed && !children[1].exited);
    assert.equal(second.status().occupied, 1); assert.equal(second.status().accepting, true);
    assert.equal((await bounded(second.dispose())).reaped, true);
    assert.equal((await bounded(two)).status, 'aborted');
  });
});

// Red if a real asynchronous spawn error settles/releases in its error callback.
test('asynchronous ENOENT is held through the actual close event', focus, async () => {
  await withFixture('normal', async ({ make, executable, children }) => {
    const registry = make({ executable: `${executable}-missing` });
    let settled = false, errorPhase = false, errorPhaseProbe;
    const pending = registry.event(eventOptions());
    pending.then(() => { settled = true; });
    assert.equal(children.length, 1);
    children[0].child.on('error', () => {
      errorPhase = true;
      assert.equal(registry.status().occupied, 1);
      assert.equal(settled, false);
      assert.equal(children[0].closed, false);
      errorPhaseProbe = Promise.race([pending, Promise.resolve('pending-at-error')]);
    });
    assert.equal((await bounded(pending)).status, 'spawn_failed');
    assert.ok(errorPhase && children[0].closed);
    assert.equal(await errorPhaseProbe, 'pending-at-error');
    assert.equal(registry.status().occupied, 0);
  });
});

// Red if overflowing either stream releases early or leaks diagnostic/output text.
test('stdout and stderr overflow terminate live children without early settlement', focus, async () => {
  for (const [mode, kind] of [['stdout', 'event'], ['stderr', 'event'], ['stdout', 'clock'], ['stderr', 'clock']])
    await withFixture(mode, async ({ make, home, children, phase }) => {
    const registry = make(); let settled = false;
    const pending = registry[kind](kind === 'clock' ? options() : eventOptions());
    pending.then(() => { settled = true; });
    await until(() => exists(phase(children[0], 'start')));
    await fs.writeFile(path.join(home, 'go'), '');
    await until(() => exists(phase(children[0], 'term')));
    assert.equal(registry.status().occupied, 1);
    assert.equal(settled, false);
    assert.equal(children[0].closed, false);
    const outcome = await bounded(pending);
    assert.deepEqual(Object.keys(outcome).sort(), ['output', 'status']);
    assert.equal(outcome.status, 'output_limit');
    assert.equal(outcome.output.length, 0);
    assert.ok(children[0].closed);
  });
});

// Red if a stream error forgets the live process (forced error on its real pipe).
test('real stdout pipe error retains its handle during escalation', focus, async () => {
  await withFixture('hold', async ({ make, children, phase }) => {
    const registry = make(); let settled = false;
    const pending = registry.event(eventOptions());
    pending.then(() => { settled = true; });
    await until(() => exists(phase(children[0], 'start')));
    children[0].child.stdout.destroy(new Error('TEST-private-stream-detail'));
    await until(() => exists(phase(children[0], 'term')));
    assert.equal(settled, false);
    assert.equal(registry.status().occupied, 1);
    assert.equal((await bounded(pending)).status, 'stream_error');
    assert.ok(children[0].closed);
  });
});

// Red if stale work acquires/spawns, or an expired deadline is reset on receipt.
test('validity is checked at acquisition and after close; deadlines only shorten', focus, async () => {
  await withFixture('normal', async ({ make, children }) => {
    const registry = make(); let checks = 0;
    assert.equal((await registry.event({ ...eventOptions(), isCurrent: () => ++checks === 1 })).status, 'invalidated');
    const controller = new AbortController(); controller.abort();
    assert.equal((await registry.clock({ ...options(), signal: controller.signal })).status, 'invalidated');
    assert.equal((await registry.event({ ...eventOptions(), deadline: performance.now() - 1 })).status, 'deadline');
    assert.equal(children.length, 0);
    let valid = true;
    const pending = registry.event({ ...eventOptions(), isCurrent: () => valid });
    valid = false;
    assert.equal((await bounded(pending)).status, 'invalidated');
    assert.equal(children.length, 1);
    assert.ok(children[0].closed);
  });
  await withFixture('hold', async ({ make, children, phase }) => {
    const registry = make(), start = performance.now();
    const pending = registry.event({ ...eventOptions(), deadline: start + 500 });
    await until(() => exists(phase(children[0], 'term')));
    const observedTerm = performance.now();
    assert.ok(performance.now() - start < 1000, 'earlier original deadline was extended');
    assert.equal(registry.status().occupied, 1);
    const outcome = await bounded(pending);
    assert.equal(outcome.status, 'deadline');
    assert.equal(children[0].child.signalCode, 'SIGKILL');
    assert.ok(children[0].exitAt - observedTerm < 1100, 'stubborn child did not escalate within one second plus observation tolerance');
    assert.ok(children[0].closed);
  });
});

// Red if a 2s helper bracket resets or pretends its timeout is actual reaping.
test('helper has a spawn-anchored two-second bracket and real escalation', focus, async () => {
  await withFixture('trickle', async ({ make, children, phase }) => {
    const registry = make(), start = performance.now(), pending = registry.clock(options());
    await until(() => exists(phase(children[0], 'term')), 2600);
    const elapsed = performance.now() - start;
    assert.ok(elapsed >= 1900 && elapsed < 2600);
    assert.equal(registry.status().occupied, 1);
    assert.equal((await bounded(pending)).status, 'deadline');
    assert.ok(children[0].closed && children[0].child.signalCode === 'SIGKILL');
  });
});

// Red if ongoing output resets the production event deadline or TERM is not escalated.
test('default event TERM by 22s, escalation within 1s, actual close before 25s', { ...focus, timeout: 27000 }, async () => {
  await withFixture('trickle', async ({ make, children, phase }) => {
    const registry = make(), start = performance.now(), pending = registry.event(eventOptions());
    await until(() => exists(phase(children[0], 'term')), 22700);
    const observedTerm = performance.now();
    const elapsed = performance.now() - start;
    assert.ok(elapsed >= 21500 && elapsed < 22700);
    assert.equal(registry.status().occupied, 1);
    assert.equal((await bounded(pending)).status, 'deadline');
    assert.ok(children[0].closed && children[0].child.signalCode === 'SIGKILL');
    assert.ok(children[0].exitAt - observedTerm < 1100);
    assert.ok(performance.now() - start < 25000);
  });
});

// Red if kill/exit is treated as close, dispose reports reaped, or replacements start.
test('inherited pipe delays actual close: bounded unproved dispose, pending handoff', focus, async () => {
  await withFixture('tail', async ({ make, home, children }) => {
    const registry = make(), controller = new AbortController(); let settled = false;
    const pending = registry.event({ ...eventOptions(), signal: controller.signal });
    pending.then(() => { settled = true; });
    await until(() => exists(path.join(home, 'tail-start')));
    const begin = performance.now(); controller.abort();
    const drainage = await bounded(registry.drain());
    assert.ok(performance.now() - begin < 3500);
    assert.equal(drainage.reaped, false);
    assert.equal(drainage.status, 'ipc_termination_unproved');
    assert.equal(drainage.occupied, 1);
    assert.equal(drainage.unresolved, 1);
    assert.equal(drainage.disposed, false);
    assert.equal(drainage.accepting, false);
    assert.equal((await bounded(registry.dispose())).reaped, false);
    assert.ok(children[0].exited && !children[0].closed);
    assert.equal(settled, false);
    assert.equal((await bounded(registry.event(eventOptions()), 100)).status, 'registry_unavailable');
    assert.equal(children.length, 1);
    assert.equal((await bounded(pending)).status, 'ipc_termination_unproved');
    assert.ok(children[0].closed && await exists(path.join(home, 'tail-done')));
    assert.equal((await registry.drain()).reaped, true);
    assert.equal(registry.status().accepting, false);
  });
});

// Red if byte limits, exact argv, private cwd or explicit environment separation break.
test('exact commands, boundary bytes, immutable frame and controlled environments', focus, async () => {
  await withFixture('boundary', async ({ make, home, children, phase }) => {
    const registry = make({ deliveryEnv: { AGENT_NOTIFICATIONS_CONFIG: path.join(home, 'config'), DBUS_SESSION_BUS_ADDRESS: 'TEST-only' } });
    const frame = Buffer.alloc(4096, 65), pending = registry.event({ ...eventOptions(), frame });
    frame.fill(66);
    const eventResult = await bounded(pending);
    assert.equal(eventResult.status, 'ok');
    assert.equal(eventResult.output.length, 1024);
    assert.deepEqual(JSON.parse(await fs.readFile(phase(children[0], 'input'))), { bytes: 4096, firstByte: 65 });
    const clockResult = await bounded(registry.clock(options()));
    assert.equal(clockResult.status, 'ok');
    assert.equal(clockResult.output.length, 1024);
    const event = JSON.parse(await fs.readFile(phase(children[0], 'start')));
    const clock = JSON.parse(await fs.readFile(phase(children[1], 'start')));
    assert.deepEqual(event.argv, ['opencode-event', '--protocol', '1']);
    assert.deepEqual(clock.argv, ['opencode-clock', '--protocol', '1']);
    assert.equal(clock.cwd, home); assert.equal(event.home, home);
    assert.deepEqual(clock.keys, ['HOME', 'XDG_CONFIG_HOME', 'XDG_RUNTIME_DIR']);
    assert.deepEqual(event.keys, ['AGENT_NOTIFICATIONS_CONFIG', 'AGENT_NOTIFICATIONS_CONTROL_ROOT',
      'AGENT_NOTIFICATIONS_HOST_ENTRY', 'AGENT_NOTIFICATIONS_HOST_EXECUTABLE', 'AGENT_NOTIFICATIONS_NATIVE_PID',
      'AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH', 'DBUS_SESSION_BUS_ADDRESS', 'HOME', 'XDG_CONFIG_HOME', 'XDG_RUNTIME_DIR']);
    assert.equal(clock.config, undefined); assert.equal(event.config, path.join(home, 'config'));
    assert.equal((await registry.event({ ...eventOptions(), frame: Buffer.alloc(4097) })).status, 'invalid_request');
    assert.equal(children.length, 2);
    for (const key of ['PATH', 'NODE_OPTIONS', 'PLUGINKITsupervisor', 'MCPGODEBUG', 'HTTP_PROXY'])
      assert.throws(() => make({ deliveryEnv: { [key]: 'TEST-poison' } }), /invalid_environment/);
  });
});

// Red if event loses the owned native descriptor or either child inherits ambient authority.
test('event and profile children receive the same owned native runtime descriptor', focus, async () => {
  await withFixture('normal', async ({ make, home, children, phase }) => {
    const descriptor = {
      AGENT_NOTIFICATIONS_CONTROL_ROOT: home, AGENT_NOTIFICATIONS_ORIGIN: 'a'.repeat(64),
      AGENT_NOTIFICATIONS_NATIVE_PID: String(process.pid), AGENT_NOTIFICATIONS_HOST_EXECUTABLE: process.execPath,
      AGENT_NOTIFICATIONS_HOST_ENTRY: 'serve', AGENT_NOTIFICATIONS_PUBLIC_EXEC_PATH: process.execPath,
    };
    const poison = [...Object.keys(descriptor), 'TEST_AMBIENT_UNTRUSTED', 'HTTP_PROXY'];
    const saved = poison.map(key => [key, process.env[key]]);
    try {
      for (const key of poison) process.env[key] = 'TEST-ambient-poison';
      const registry = make({ origin: descriptor.AGENT_NOTIFICATIONS_ORIGIN,
        deliveryEnv: { AGENT_NOTIFICATIONS_CONFIG: path.join(home, 'config') } });
      assert.equal((await bounded(registry.event(eventOptions()))).status, 'ok');
      assert.equal((await bounded(registry.profile(options()))).status, 'ok');
      assert.equal((await bounded(registry.clock(options()))).status, 'ok');
      const [event, profile, clock] = await Promise.all(children.map(child =>
        fs.readFile(phase(child, 'start')).then(bytes => JSON.parse(bytes))));
      assert.deepEqual(event.native, descriptor); assert.deepEqual(profile.native, descriptor);
      assert.deepEqual(clock.native, {}); assert.equal(clock.config, undefined);
      assert.equal(profile.config, undefined); assert.equal(event.config, path.join(home, 'config'));
      for (const child of [event, profile, clock]) {
        assert.ok(!child.keys.includes('TEST_AMBIENT_UNTRUSTED') && !child.keys.includes('HTTP_PROXY'));
      }
      for (const key of Object.keys(descriptor))
        assert.throws(() => make({ deliveryEnv: { [key]: 'TEST-unowned-descriptor' } }), /invalid_environment/);
      assert.equal(children.length, 3); assert.ok(children.every(child => child.closed && child.exited));
    } finally {
      for (const [key, value] of saved) { if (value === undefined) delete process.env[key]; else process.env[key] = value; }
    }
  });
});

// Red if native drive/UNC guards permit device, relative or noncanonical Windows paths.
test('owned path guards preserve native Windows and POSIX boundaries', () => {
  for (const value of ['C:\\owned\\app.exe', '\\\\server\\share\\app.exe']) assert.ok(absoluteNativePath(value, 'win32'));
  for (const value of ['C:app.exe', '\\app.exe', '/app.exe', '\\\\?\\C:\\app.exe', '\\\\.\\pipe\\app', '\\\\server\\share', 'C:\\owned\\..\\app.exe', 'C:\\bad\0.exe'])
    assert.equal(absoluteNativePath(value, 'win32'), false);
  assert.ok(absoluteNativePath('/TEST/owned binary'));
  assert.equal(absoluteNativePath('relative/binary'), false);
  assert.throws(() => createProcessRegistry({ executable: 'relative', privateCwd: '/TEST', controlRoot: '/TEST' }), /invalid_paths/);
});
