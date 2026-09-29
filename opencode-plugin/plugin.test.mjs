import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { test } from 'node:test';
import { acquireObserver, uapSourceSHA256 } from './build.mjs';
import { forward } from '../internal/opencodeplugin/dist/agent-notifications.js';

test('build accepts only the pinned UAP source digest', async () => {
  const source = 'export function createObserver() {}';
  await assert.rejects(acquireObserver(async () => ({ ok: true, arrayBuffer: async () => Buffer.from(source) })), /digest mismatch/);
  assert.equal(createHash('sha256').update(source).digest('hex') === uapSourceSHA256, false);
});

test('IPC sends only neutral wire and treats an ambiguous child failure as non-success', async () => {
  let sent;
  const spawnFake = (_binary, args, options) => {
    assert.deepEqual(args, ['opencode-event', '--protocol', '1']);
    assert.equal(options.shell, false);
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
});
