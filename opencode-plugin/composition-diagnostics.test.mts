import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import test from 'node:test';

type Row = Record<string, unknown>;
type Prepared = { beforeEmit(event: Row, handoff: Row): Promise<boolean>; ready(): boolean; dispose(): void };
type Registry = { dispose(): Promise<{ reaped: boolean }> };
const directory = new URL('.', import.meta.url);
const load = (name: string): Promise<unknown> => import(new URL(name, directory).href);
const unused = (): never => { throw new Error('UNEXPECTED_PUBLIC_QUERY'); };

// Red if omitted/false/nonboolean diagnostics logs, or opt-in startup refusal is silent.
// Calls the genuine unrendered source factories, whose missing origin denies before OS work.
test('composition diagnostics require exact opt-in in actual V1 and V2 startup', async () => {
  const api = await load('plugin.mjs') as { default: {
    server(input: Row, options?: Row): Promise<unknown>; setup(context: Row): Promise<unknown>;
  } };
  const prior = console.error, rows: unknown[][] = [];
  console.error = (...args: unknown[]) => { rows.push(args); };
  try {
    for (const diagnostics of [undefined, false, 'PRIVATE_SENTINEL', true]) {
      rows.length = 0;
      const client = { session: { get: unused, messages: unused } };
      await api.default.server({ directory: '/TEST-unrendered', client }, diagnostics === undefined ? undefined : { diagnostics });
      await api.default.setup({ app: { version: '2.0.21' }, location: { directory: '/TEST-unrendered' },
        event: { subscribe: unused }, session: { get: unused, context: unused },
        permission: { get: unused, list: unused }, rpc: { register: unused },
        ...(diagnostics === undefined ? {} : { options: { diagnostics } }) });
      assert.deepEqual(rows, diagnostics === true ? [
        ['Agent Notifications OpenCode composition: v1.owned.denied'],
        ['Agent Notifications OpenCode composition: v2.owned.denied'],
      ] : []);
    }
  } finally { console.error = prior; }
});

// Red if an unknown exception is serialized, diagnostics omission logs, or a
// throwing diagnostic callback changes the actual denial/control result.
test('preparation diagnostics disclose fixed cause only and callback failure cannot change denial', async () => {
  const api = await load('prepared-delivery.mjs') as { createPreparedDelivery(config: {
    registry: Registry; origin: string; policy: Row; isOwned(): boolean;
    onDiagnostic?: (reason: string) => void;
  }): Prepared };
  const registryApi = await load('process-registry.mjs') as { createProcessRegistry(config: Row): Registry };
  const root = mkdtempSync(join(tmpdir(), 'TEST-composition-diagnostics-'));
  const prior = console.error, consoleRows: unknown[][] = [];
  console.error = (...args: unknown[]) => { consoleRows.push(args); };
  try {
    for (const mode of ['omitted', 'observed', 'throwing'] as const) {
      const registry = registryApi.createProcessRegistry({ executable: process.execPath, privateCwd: root, controlRoot: root });
      const reasons: string[] = [];
      const delivery = api.createPreparedDelivery({ registry, origin: 'a'.repeat(64), policy: {}, isOwned: () => true,
        ...(mode === 'omitted' ? {} : { onDiagnostic: (reason: string) => {
          reasons.push(reason); if (mode === 'throwing') throw new Error('PRIVATE_CALLBACK_SENTINEL');
        } }) });
      try {
        const handoff = { get ingressMonotonicMs(): number { throw new Error('PRIVATE_UNKNOWN_SENTINEL'); } };
        assert.equal(await delivery.beforeEmit({ rootSession: true }, handoff), false);
        assert.equal(delivery.ready(), false);
        assert.deepEqual(reasons, mode === 'omitted' ? [] : ['prep.catch.original']);
        assert.equal(JSON.stringify(reasons).includes('PRIVATE'), false);
        assert.deepEqual(consoleRows, []);
      } finally { delivery.dispose(); assert.equal((await registry.dispose()).reaped, true); }
    }
  } finally { console.error = prior; rmSync(root, { recursive: true }); }
});
