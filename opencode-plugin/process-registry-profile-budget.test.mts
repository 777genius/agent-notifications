import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import dc from 'node:diagnostics_channel';
import { performance } from 'node:perf_hooks';
import type { ChildProcess } from 'node:child_process';

type ProfileOptions = Readonly<{ isCurrent: () => boolean; deadline?: number }>;
type ChildResult = Readonly<{ status: 'invalid_request' | 'invalidated' | 'registry_unavailable'
  | 'capacity_suppressed' | 'deadline' | 'spawn_failed' | 'aborted' | 'stream_error'
  | 'output_limit' | 'ipc_termination_unproved' | 'ok' | 'exited'; output: Buffer }>;
type RegistryStatus = Readonly<{ accepting: boolean; disposed: boolean; occupied: number; unresolved: number }>;
type Registry = Readonly<{
  profile(options: ProfileOptions): Promise<ChildResult>;
  status(): RegistryStatus;
  dispose(): Promise<RegistryStatus & Readonly<{ reaped: boolean }>>;
}>;
type RegistryFactory = (configuration: Readonly<{ executable: string; privateCwd: string;
  controlRoot: string; origin: string }>) => Registry;
const registryModule: unknown = await import(new URL('./process-registry.mjs', import.meta.url).href);
const { createProcessRegistry } = registryModule as { createProcessRegistry: RegistryFactory };
const receipt = '{"protocol":1,"semantic":"unverified","generation":"none","resourceClosure":"reaped_or_not_started"}\n';

type ChildRecord = { child: ChildProcess; startedAt: number; closedAt?: number; code?: number | null };
async function bounded<T>(operation: Promise<T>, ms: number): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([operation, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error('TEST child wait expired')), ms);
    })]);
  } finally { clearTimeout(timer); }
}

// These ordinary TEST Node bytes exercise only the registry transport boundary.
// A valid unverified receipt makes no claim about official OpenCode qualification.
async function withFixture(run: (registry: Registry, home: string, children: ChildRecord[]) => Promise<void>): Promise<void> {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-profile-budget-'));
  const home = path.join(root, 'private-HOME-XDG');
  const executable = path.join(root, 'profile-budget-fixture.mts');
  const children: ChildRecord[] = [];
  const observe = (message: unknown): void => {
    const child = (message as { process: ChildProcess }).process;
    // Node publishes this channel from ChildProcess's constructor, before
    // spawnfile/pid are assigned. Subscribe to close immediately; verify the
    // launched executable and fixture-reported PID after actual closure.
    const record: ChildRecord = { child, startedAt: performance.now() };
    children.push(record);
    child.once('close', code => { record.closedAt = performance.now(); record.code = code; });
  };
  const fixture = `#!${process.execPath}
import fs from 'node:fs';
import path from 'node:path';
const root: string = process.cwd();
fs.writeFileSync(path.join(root, 'started.json'), JSON.stringify({ pid: process.pid, argv: process.argv.slice(2), home: process.env.HOME, cwd: root }));
let timer: ReturnType<typeof setTimeout>;
let ended: boolean = false;
const finish = (terminated: boolean): void => {
  if (ended) return;
  ended = true;
  clearTimeout(timer);
  fs.writeFileSync(path.join(root, 'finished.json'), JSON.stringify({ pid: process.pid, terminated }));
  process.stdout.end(${JSON.stringify(receipt)}, () => process.exit(0));
};
process.on('SIGTERM', () => finish(true));
process.stdin.resume();
process.stdin.on('end', () => { timer = setTimeout(() => finish(false), 11000); });
`;
  let registry: Registry | undefined;
  dc.subscribe('child_process', observe);
  try {
    await fs.mkdir(home, { mode: 0o700 });
    await fs.writeFile(executable, fixture, { mode: 0o700 });
    registry = createProcessRegistry({ executable, privateCwd: home, controlRoot: home, origin: 'a'.repeat(64) });
    await run(registry, home, children);
  } finally {
    try {
      if (registry) await bounded(registry.dispose(), 4000);
    } finally {
      for (const record of children) if (record.closedAt === undefined) record.child.kill('SIGKILL');
      try {
        await bounded(Promise.all(children.map(record => record.closedAt !== undefined ? Promise.resolve()
          : new Promise<void>(resolve => record.child.once('close', () => resolve())))), 4000);
      } finally {
        dc.unsubscribe('child_process', observe);
        if (children.every(record => record.closedAt !== undefined)) await fs.rm(root, { recursive: true, force: true });
      }
    }
  }
}

// Red under the old 10s profile budget; independent close observation proves
// reservation release follows a real child close, rather than receipt bytes alone.
test('profile permits 11s startup but an earlier caller deadline still closes and releases', { timeout: 25000 }, async () => {
  if (process.platform !== 'linux') return;
  for (const shortened of [false, true]) {
    await withFixture(async (registry, home, children) => {
      const startedAt = performance.now();
      const pending = registry.profile({ isCurrent: () => true,
        ...(shortened ? { deadline: startedAt + 2000 } : {}) });
      assert.equal(registry.status().occupied, 2);
      const result = await bounded(pending, shortened ? 6000 : 18000);
      const settledAt = performance.now();
      assert.equal(result.status, shortened ? 'deadline' : 'ok');
      assert.equal(result.output.toString(), shortened ? '' : receipt);
      assert.equal(children.length, 1);
      const child = children[0];
      assert.equal(child.child.spawnfile, path.join(home, '..', 'profile-budget-fixture.mts'));
      assert.ok(child.closedAt !== undefined && child.closedAt <= settledAt);
      assert.equal(child.code, 0);
      const startup: { pid: number; argv: string[]; home: string; cwd: string } =
        JSON.parse(await fs.readFile(path.join(home, 'started.json'), 'utf8'));
      const completion: { pid: number; terminated: boolean } =
        JSON.parse(await fs.readFile(path.join(home, 'finished.json'), 'utf8'));
      assert.equal(startup.pid, child.child.pid);
      assert.deepEqual(startup.argv, ['opencode-runtime-profile', '--protocol', '1']);
      assert.equal(startup.home, home);
      assert.equal(startup.cwd, home);
      assert.equal(completion.pid, startup.pid);
      assert.equal(completion.terminated, shortened);
      assert.ok(shortened ? settledAt - startedAt < 6000 : settledAt - child.startedAt >= 11000);
      assert.deepEqual(registry.status(), { accepting: true, disposed: false, occupied: 0, unresolved: 0 });
    });
  }
});
