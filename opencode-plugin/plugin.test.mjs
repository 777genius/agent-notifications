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
    assert.equal(options.env.AGENT_NOTIFICATIONS_CONFIG, '/test/selected-config.json');
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
  assert.equal(await forward(fact, spawnFake, '/test/owned-binary'), 'rejected');
  assert.deepEqual(JSON.parse(sent), fact);
  } finally {
    if (previousSelector === undefined) delete process.env.AGENT_NOTIFICATIONS_CONFIG;
    else process.env.AGENT_NOTIFICATIONS_CONFIG = previousSelector;
  }
});
