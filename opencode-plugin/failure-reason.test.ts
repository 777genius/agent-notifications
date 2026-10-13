import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import dc from 'node:diagnostics_channel';
import type { ChildProcess } from 'node:child_process';

type Registry = Readonly<{
  event(options: { frame: Buffer; isCurrent: () => boolean }): Promise<{ status: string; output: Buffer }>;
  dispose(): Promise<{ reaped: boolean }>;
}>;
type RegistryFactory = (configuration: { executable: string; privateCwd: string; controlRoot: string;
  diagnostics: boolean }) => Registry;
const module: unknown = await import(new URL('./process-registry.mjs', import.meta.url).href);
const { createProcessRegistry } = module as { createProcessRegistry: RegistryFactory };

// Red on the old real registry because the finite reason disappears. Unknown
// native details must remain absent, with original output/close/claim unchanged.
test('actual closed event child retains only a fixed public failure reason', { timeout: 12000, concurrency: false }, async t => {
  if (process.platform === 'win32') { t.skip('existing POSIX executable fixture boundary'); return; }
  const marker = 'PRIVATE_UNKNOWN_REASON_SENTINEL';
  for (const reason of ['handoff_unconfirmed', 'native_submission_deadline', 'native_submission_cancelled', 'webhook_deadline', 'webhook_cancelled', marker]) {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-public-failure-reason-'));
    const executable = path.join(root, 'receipt.mts');
    const raw = JSON.stringify({ status: 'unknown', reason, desktop: 'unknown', webhook: 'submitted' });
    const children: { child: ChildProcess; closed: boolean }[] = [], lines: string[] = [];
    const logger = console.error;
    const observed = (message: unknown) => {
      const { process: child } = message as { process: ChildProcess };
      const record = { child, closed: false }; children.push(record);
      child.once('close', () => { record.closed = true; });
    };
    let registry: Registry | undefined;
    dc.subscribe('child_process', observed);
    console.error = (line: unknown) => {
      assert.equal(children.length, 1);
      assert.equal(children[0].closed, true, 'diagnostic preceded actual child close');
      assert.ok(typeof line === 'string'); lines.push(line);
    };
    try {
      await fs.writeFile(executable, `#!${process.execPath}
import fs from 'node:fs';
let bytes: number = 0;
process.stdin.on('data', (chunk: Buffer) => { bytes += chunk.length; });
process.stdin.on('end', () => {
  fs.writeFileSync('input-bytes', String(bytes));
  process.stdout.write(${JSON.stringify(raw)});
});
`, { mode: 0o700 });
      registry = createProcessRegistry({ executable, privateCwd: root, controlRoot: root, diagnostics: true });
      const frame = Buffer.from('TEST-private-original-frame');
      const result = await registry.event({ frame, isCurrent: () => true });
      assert.equal(result.status, 'ok');
      assert.equal(result.output.toString(), raw, 'diagnostics changed the receipt delivered to caller');
      assert.equal(await fs.readFile(path.join(root, 'input-bytes'), 'utf8'), String(frame.length));
      assert.equal(children.length, 1); assert.equal(children[0].closed, true); assert.equal(lines.length, 1);
      assert.ok(lines[0].startsWith('[agent-notifications] '));
      const decoded: unknown = JSON.parse(lines[0].slice('[agent-notifications] '.length));
      assert.ok(decoded !== null && typeof decoded === 'object');
      const record = decoded as Record<string, unknown>;
      assert.deepEqual(record, { protocol: 1, kind: 'event', ipc: 'ok', childClosure: 'closed', exitCode: 0, forcedKill: false,
        receipt: { status: 'unknown', desktop: 'unknown', webhook: 'submitted',
          ...(reason !== marker ? { reason } : {}) } });
      assert.equal(lines.join('').includes(marker), false);
    } finally {
      console.error = logger;
      const reaped = registry ? (await registry.dispose()).reaped : children.length === 0;
      dc.unsubscribe('child_process', observed);
      assert.equal(reaped, true, 'owned event child did not reap');
      assert.ok(children.every(record => record.closed));
      await fs.rm(root, { recursive: true, force: true });
    }
  }
});
