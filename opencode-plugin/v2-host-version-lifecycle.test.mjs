import assert from 'node:assert/strict';
import { test } from 'node:test';
import { build } from 'esbuild';
import { fileURLToPath } from 'node:url';

// Isolated composition test: stubs the *qualified* Go/clock child ports. A
// fabricated host version must never be mistaken for installed native E2E.
test('V2 matches independently admitted host version and revokes on live context drift', async () => {
  const key = 'TEST-v2-version-boundary-' + process.pid;
  const port = `globalThis[${JSON.stringify(key)}]`;
  const stubs = {
    './owned-host.mjs': `export const prepareOwnedHost=async()=>${port}.owned;`,
    './clock-cells.mjs': `export const selectClockCell=(generation,version)=>{${port}.selected=[generation,version];return {generation:"v2"};};`,
    './platform-clock.mjs': 'export const createPlatformClock=async()=>{throw Error("not_native");};',
    './ipc.mjs': `export const createPreparedDelivery=()=>${port}.delivery;`,
    './display.ts': 'export const createDisplayContext=()=>({});',
    './native-v1.mjs': 'export const createNativeV1=()=>{};',
    './native-v2.mjs': 'export const createNativeV2=()=>({native:{},reset(){}});export const createRPCCheckpoint=()=>({});',
    './protocol.mjs': 'export const parseJSON=()=>({});',
    'universal-agent-plugins-opencode-events/v1': 'export const createObserver=()=>{};',
    'universal-agent-plugins-opencode-events/v2': `export const createV2Observer=({runtimeEligibility})=>{ ${port}.eligibility=runtimeEligibility; return {start(){${port}.starts++},dispose(){${port}.stops++}};};`,
  };
  const result = await build({ entryPoints: [fileURLToPath(new URL('./plugin.mjs', import.meta.url))],
    bundle: true, write: false, platform: 'node', format: 'esm',
    plugins: [{ name: 'v2-boundary-ports', setup(b) {
      b.onResolve({ filter: /.*/ }, args => Object.hasOwn(stubs, args.path) ? { path: args.path, namespace: 'boundary' } : undefined);
      b.onLoad({ filter: /.*/, namespace: 'boundary' }, args => ({ contents: stubs[args.path], loader: 'js' }));
    } }],
  });
  const { default: plugin } = await import('data:text/javascript;base64,' + Buffer.from(result.outputFiles[0].text).toString('base64'));
  const rows = [];
  const prior = console.error;
  console.error = (...args) => rows.push(args);
  try {
    let disposed = false;
    globalThis[key] = { starts: 0, stops: 0, owned: {
      registry: {}, origin: 'a'.repeat(64), hostVersion: '2.2.6',
      isOwned: () => !disposed,
      runtimeEligibility: () => disposed ? 'unverified' : 'supported',
      async dispose() { disposed = true; return { reaped: true }; },
    }, delivery: {
      activate: async () => true, clock: {}, invalidate() {}, nativeIngress() {}, dispose() {},
    } };
    const context = { app: { version: '2.2.6' },
      location: { directory: '/TEST', workspaceID: 'ws', project: { id: 'project' } },
      session: { get() {}, context() {} }, permission: { get() {}, list() {} },
      rpc: { register() {} }, event: { subscribe() {} },
    };
    const cleanup = await plugin.setup(context);
    assert.equal(typeof cleanup, 'function');
    assert.deepEqual(globalThis[key].selected, ['v2', '2.2.6']);
    assert.equal(globalThis[key].starts, 1);
    assert.equal(globalThis[key].eligibility(), 'supported');
    context.app.version = '2.99.0';
    assert.equal(globalThis[key].eligibility(), 'unverified', 'changed API version revokes the already loaded observer');
    context.app.version = '2.2.6';
    context.location.project.id = 'different';
    assert.equal(globalThis[key].eligibility(), 'unverified', 'project change revokes ownership');
    context.location.project.id = 'project';
    await cleanup();
    assert.equal(globalThis[key].stops, 1);
    assert.equal(globalThis[key].eligibility(), 'unverified', 'disposing the host revokes ownership');
    assert.deepEqual(rows, [], 'clean lifecycle must not warn');
    // Revocation while async activation is pending is not an activation failure.
    disposed = false;
    let entered, release;
    const entering = new Promise(resolve => { entered = resolve; });
    const pending = new Promise(resolve => { release = resolve; });
    globalThis[key].delivery = { ...globalThis[key].delivery,
      activate: async () => { entered(); await pending; return false; },
    };
    const changed = { ...context, app: { version: '2.2.6' },
      location: { directory: '/TEST', workspaceID: 'ws', project: { id: 'project' } },
    };
    const refused = plugin.setup(changed);
    await entering;
    changed.app.version = '2.2.7';
    release();
    assert.equal(await refused, undefined);
    assert.deepEqual(rows, [], 'host version drift must not produce a startup warning');

  } finally {
    console.error = prior;
    delete globalThis[key];
  }
});
