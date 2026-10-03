import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { forward } from './ipc.mjs';

test('loaded OpenCode bundle exports exactly one plugin function', async () => {
  const exports = await import('../internal/opencodeplugin/dist/agent-notifications.js');
  assert.deepEqual(Object.keys(exports), ['AgentNotifications']);
  assert.equal(typeof exports.AgentNotifications, 'function');
});

test('IPC sends only neutral wire and treats an ambiguous child failure as non-success', async () => {
  let sent;
  const previousSelector = process.env.AGENT_NOTIFICATIONS_CONFIG;
  process.env.AGENT_NOTIFICATIONS_CONFIG = '/test/selected-config.json';
  try {
  const spawnFake = (_binary, args, options) => {
    assert.deepEqual(args, ['opencode-event', '--protocol', '1']);
    assert.equal(options.shell, false);
    assert.equal(options.windowsHide, true);
    assert.equal(options.env.AGENT_NOTIFICATIONS_CONFIG, '/test/selected-config.json');
    assert.equal(options.env.AGENT_NOTIFICATIONS_CONTROL_ROOT, '/test/managed-control');
    const child = new EventEmitter();
    child.stdin = new PassThrough();
    child.stdout = new PassThrough();
    child.stderr = new PassThrough();
    child.kill = () => {};
    child.stdin.on('data', (chunk) => { sent = chunk.toString(); });
    queueMicrotask(() => child.emit('close', 1));
    return child;
  };
  const fact = { version: 1, kind: 'terminal_error', sessionID: 's', turnID: 't', rootSession: true };
  assert.equal(await forward(fact, spawnFake, '/test/owned-binary', '/test/managed-control', 'linux'), 'rejected');
  assert.deepEqual(JSON.parse(sent), fact);
  } finally {
    if (previousSelector === undefined) delete process.env.AGENT_NOTIFICATIONS_CONFIG;
    else process.env.AGENT_NOTIFICATIONS_CONFIG = previousSelector;
  }
});

test('Windows IPC accepts owned absolute exe paths and sends a narrow startup environment', async () => {
  const previous = { ...process.env };
  process.env.SystemRoot = 'C:\\Windows';
  process.env.USERPROFILE = 'C:\\Users\\test';
  process.env.SECRET_TEST_TOKEN = 'must-not-leak';
  try {
    let calls = 0;
    const fake = (binary, args, options) => {
      calls++;
      assert.equal(binary, 'C:\\Program Files\\Agent Notifications\\agent.exe');
      assert.deepEqual(args, ['opencode-event', '--protocol', '1']);
      assert.equal(options.shell, false);
      assert.equal(options.windowsHide, true);
      assert.equal(options.env.SystemRoot, 'C:\\Windows');
      assert.equal(options.env.USERPROFILE, 'C:\\Users\\test');
      assert.equal(options.env.AGENT_NOTIFICATIONS_CONTROL_ROOT, 'C:\\Users\\test\\control');
      assert.equal(options.env.SECRET_TEST_TOKEN, undefined);
      assert.equal(options.env.PATH, undefined);
      const child = new EventEmitter();
      child.stdin = new PassThrough(); child.stdout = new PassThrough(); child.stderr = new PassThrough();
      child.kill = () => {};
      queueMicrotask(() => { child.stdout.write('{"status":"submitted"}'); child.emit('close', 0); });
      return child;
    };
    assert.equal(await forward({ version: 1 }, fake, 'C:\\Program Files\\Agent Notifications\\agent.exe', 'C:\\Users\\test\\control', 'win32'), 'submitted');
    assert.equal(calls, 1);
    for (const [binary, root] of [
      ['agent.exe', 'C:\\control'], ['C:agent.exe', 'C:\\control'],
      ['C:\\agent', 'C:\\control'], ['C:\\agent.exe', '\\control'],
      ['\\\\?\\C:\\agent.exe', 'C:\\control'],
    ]) {
      assert.equal(await forward({}, fake, binary, root, 'win32'), 'invalid_plugin');
    }
    assert.equal(calls, 1);
  } finally {
    for (const key of ['SystemRoot', 'USERPROFILE', 'SECRET_TEST_TOKEN']) {
      if (previous[key] === undefined) delete process.env[key]; else process.env[key] = previous[key];
    }
  }
});

// Optional desktop data must not drop an otherwise valid notification when its
// JSON escaping/combined fields would exceed the total 4096-byte wire budget.
test('oversized optional display falls back to exactly the neutral wire', async () => {
  let sent;
  const fake = () => {
    const child = new EventEmitter();
    child.stdin = new PassThrough(); child.stdout = new PassThrough(); child.stderr = new PassThrough();
    child.kill = () => {};
    child.stdin.on('data', (chunk) => { sent = JSON.parse(chunk.toString()); });
    queueMicrotask(() => { child.stdout.write('{"status":"submitted"}'); child.emit('close', 0); });
    return child;
  };
  const neutral = { version: 1, kind: 'question_asked', sessionID: 's', turnID: 't', requestID: 'r', rootSession: true };
  assert.equal(await forward({ ...neutral, display: { question: '"'.repeat(4096) } }, fake, '/test/owned-binary', '/test/managed-control', 'linux'), 'submitted');
  assert.deepEqual(sent, neutral);
});
