import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createProcessRegistry } from './process-registry.mjs';

// Red if the real event child loses its URL, or profile/clock inherit the secret.
// Every process and file belongs to this disposable TEST directory.
test('webhook URL reaches only the real event child', { timeout: 12000 }, async () => {
  if (process.platform !== 'linux') return;
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-webhook-env-'));
  const executable = path.join(root, 'env-fixture.mts');
  const secret = 'https://example.invalid/TEST-webhook-secret';
  const fixture = `#!${process.execPath}
import fs from 'node:fs';
import path from 'node:path';
const command: string = process.argv[2];
fs.writeFileSync(path.join(process.cwd(), command + '.json'), JSON.stringify(process.env));
process.stdin.resume();
process.stdin.on('end', () => process.stdout.write(command === 'opencode-runtime-profile'
  ? JSON.stringify({ protocol: 1, semantic: 'unverified', generation: 'none', resourceClosure: 'reaped_or_not_started' })
  : 'TEST-response'));
`;
  let registry: ReturnType<typeof createProcessRegistry> | undefined;
  try {
    await fs.writeFile(executable, fixture, { mode: 0o700 });
    registry = createProcessRegistry({ executable, privateCwd: root, controlRoot: root,
      origin: 'a'.repeat(64), deliveryEnv: { AGENT_NOTIFICATIONS_WEBHOOK_URL: secret } });
    assert.equal((await registry.event({ isCurrent: () => true, frame: Buffer.from('{}') })).status, 'ok');
    await registry.profile({ isCurrent: () => true });
    assert.equal((await registry.clock({ isCurrent: () => true })).status, 'ok');
    const snapshots: Record<string, string>[] = await Promise.all(
      ['opencode-event', 'opencode-runtime-profile', 'opencode-clock'].map(async command =>
        JSON.parse(await fs.readFile(path.join(root, command + '.json'), 'utf8'))));
    assert.equal(snapshots[0].AGENT_NOTIFICATIONS_WEBHOOK_URL, secret);
    for (const child of snapshots.slice(1)) {
      assert.ok(!Object.hasOwn(child, 'AGENT_NOTIFICATIONS_WEBHOOK_URL'));
      assert.ok(!Object.values(child).includes(secret));
    }
    assert.throws(() => createProcessRegistry({ executable, privateCwd: root, controlRoot: root,
      deliveryEnv: { MY_WEBHOOK_URL: secret } }), /invalid_environment/);
  } finally {
    if (!registry || (await registry.dispose()).reaped) await fs.rm(root, { recursive: true, force: true });
  }
});
