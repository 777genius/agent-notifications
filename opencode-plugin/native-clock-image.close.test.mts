import assert from 'node:assert/strict';
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
import test from 'node:test';

test('holdFile actually closes its real descriptor, independently of disposed flags', async () => {
  const here = dirname(fileURLToPath(import.meta.url));
  assert.match(fs.realpathSync(here), /(?:^|[\\/])TEST-[^\\/]+(?:[\\/]|$)/);
  const root = fs.mkdtempSync(join(tmpdir(), 'TEST-image-close-')), path = join(root, 'image');
  fs.writeFileSync(path, 'actual owned TEST image');
  const realOpen = fs.openSync, realClose = fs.closeSync;
  let observed = -1, closes = 0;
  // Forward the original OS calls. No returned fd, file data or error is fabricated.
  fs.openSync = (input, flags, mode) => {
    const fd = realOpen(input, flags, mode); if (input === path) observed = fd; return fd;
  };
  fs.closeSync = fd => { realClose(fd); if (fd === observed) closes++; };
  syncBuiltinESMExports(); // Node only; actual Bun qualification uses read-only fstat scanning.
  try {
    const api: { holdFile(path: string): { verify(): void; close(): void } } =
      await import(pathToFileURL(join(here, 'native-clock-image.mjs')).href);
    const held = api.holdFile(path); held.verify();
    assert.ok(Number.isInteger(observed) && observed >= 0);
    assert.ok(fs.fstatSync(observed).isFile());
    held.close(); held.close();
    assert.throws(() => fs.fstatSync(observed),
      (error: unknown) => (error as NodeJS.ErrnoException).code === 'EBADF');
    assert.equal(closes, 1);
  } finally {
    fs.openSync = realOpen; fs.closeSync = realClose; syncBuiltinESMExports();
    if (observed >= 0) { try { realClose(observed); } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EBADF') throw error;
    } }
    fs.rmSync(root, { recursive: true });
  }
});
