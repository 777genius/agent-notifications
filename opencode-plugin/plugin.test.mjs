import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { forward, createBoundedForwarder } from './ipc.mjs';

test('loaded OpenCode bundle exposes one dual definition accepted by both host loaders', async () => {
  const exports = await import('../internal/opencodeplugin/dist/agent-notifications.js');
  assert.deepEqual(Object.keys(exports), ['default']);
  assert.equal(exports.default.id, 'agent-notifications');
  assert.equal(typeof exports.default.server, 'function');
  assert.equal(typeof exports.default.setup, 'function');
});

// Regression: a dead subscription permits an obsolete delayed completion to dispatch IPC.
test('subscription failure disposes pending verification before a late native snapshot', async () => {
  const { default: plugin } = await import('../internal/opencodeplugin/dist/agent-notifications.js');
  const location = { directory: '/TEST-stream-failure' };
  let resolveContext, reachedContext = false;
  const context = new Promise((resolve) => { resolveContext = resolve; });
  const errors = [];
  const previous = console.error;
  console.error = (...args) => errors.push(args.join(' '));
  const pause = () => new Promise(setImmediate);
  try {
    const stop = plugin.setup({ location, session: {
      get: async () => ({ id: 'session', location }),
      context: () => { reachedContext = true; return context; },
    }, event: { subscribe: () => (async function* () {
      for (const [type, data] of [
        ['session.inbox.enqueued', { inboxID: 'user', item: { type: 'user' } }],
        ['session.execution.started', {}], ['session.inbox.delivered', { inboxID: 'user' }],
        ['session.step.started', { assistantMessageID: 'assistant' }],
        ['session.step.ended', { assistantMessageID: 'assistant', finish: 'stop' }],
        ['session.execution.succeeded', {}],
      ]) yield { type, data: { sessionID: 'session', ...data } };
      while (!reachedContext) await pause();
      throw new Error('TEST-stream-failure');
    })() } });
    for (let i = 0; i < 12; i++) await pause();
    assert.equal(reachedContext, true);
    assert.ok(errors.some((line) => line.endsWith('subscription_failed')));
    resolveContext([{ id: 'user', type: 'user' }, { id: 'assistant', type: 'assistant', finish: 'stop', time: { completed: 1 } }]);
    for (let i = 0; i < 12; i++) await pause();
    // The unbound test bundle rejects IPC bindings and reports any attempted
    // delivery. A late dispatch would therefore make this assertion red.
    assert.equal(errors.some((line) => line.includes('OpenCode delivery:')), false);
    stop();
  } finally { console.error = previous; }
});

// Regression: a finished IPC Promise must not free a still-live child slot.
test('V2 delivery saturation holds capacity through child close and never replays', async () => {
  const children = [];
  const fake = () => {
    const child = new EventEmitter();
    child.stdin = new PassThrough(); child.stdout = new PassThrough(); child.stderr = new PassThrough();
    child.kill = () => {};
    children.push(child);
    return child;
  };
  const delivery = createBoundedForwarder({ limit: 2, spawnProcess: fake, binary: '/test/binary', root: '/test/root', platform: 'linux' });
  const first = delivery.forward({ version: 1 });
  const second = delivery.forward({ version: 1 });
  assert.equal(await delivery.forward({ version: 1 }), 'capacity');
  assert.equal(children.length, 2);
  children[0].stdout.write('x'.repeat(1025));
  assert.equal(await first, 'invalid_receipt');
  assert.equal(await delivery.forward({ version: 1 }), 'capacity');
  assert.equal(children.length, 2);
  children[0].emit('close', 1);
  const fresh = delivery.forward({ version: 1 });
  assert.equal(children.length, 3);
  delivery.dispose();
  assert.equal(await delivery.forward({ version: 1 }), 'suppressed');
  children[1].emit('close', 1); children[2].emit('close', 1);
  assert.equal(await second, 'rejected'); assert.equal(await fresh, 'rejected');
  assert.equal(children.length, 3);
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
