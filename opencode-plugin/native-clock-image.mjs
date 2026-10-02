import { openSync, closeSync, readSync, fstatSync, statSync, realpathSync, constants } from 'node:fs';
import { createHash } from 'node:crypto';
import { unavailable } from './native-clock-contract.mjs';

// Closed source candidate identities, NOT clock-policy or semantic grants.
const images = Object.freeze([
  ['darwin', 'x64', '1.3.14', 'f53aae8eb68d832ab1bcd27bed88c02de910be61f4b5f90068ae8e93d5e794c9'],
  ['darwin', 'arm64', '1.3.14', '139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524'],
  ['win32', 'x64', '1.3.14', '52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c'],
  ['darwin', 'x64', '1.4.2', '4642b7da61279c8aa5d389d9f29454936e449fea6bc510689e9cc976fff6579f'],
  ['darwin', 'arm64', '1.4.2', '0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442'],
  ['win32', 'x64', '1.4.2', 'ec7a3909bad41ef88e4650f737ab6f0b0c402a7f49a588812d0a79820c2dfc1f'],
].map(Object.freeze));
const same = (a, b) => ['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs'].every(k => a[k] === b[k]);
export function holdFile(path) {
  const fd = openSync(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const initial = fstatSync(fd, { bigint: true });
    if (!initial.isFile() || !['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs'].every(k => typeof initial[k] === 'bigint') ||
        initial.ino <= 0n || initial.size <= 0n || initial.size > 536870912n) unavailable();
    const hash = createHash('sha256'), block = Buffer.alloc(65536);
    let offset = 0;
    while (offset < Number(initial.size)) {
      const count = readSync(fd, block, 0, Math.min(block.length, Number(initial.size) - offset), offset);
      if (count <= 0) unavailable();
      hash.update(block.subarray(0, count)); offset += count;
    }
    const digest = hash.digest('hex');
    let disposed = false;
    function verify() {
      if (disposed || !same(initial, fstatSync(fd, { bigint: true })) ||
          !same(initial, statSync(path, { bigint: true }))) unavailable();
    }
    verify();
    return Object.freeze({ digest, verify, close() { if (!disposed) { disposed = true; closeSync(fd); } } });
  } catch { try { closeSync(fd); } catch {} unavailable(); }
}
export function pinNativeImage() {
  const bun = globalThis.Bun?.version;
  if (!images.some(row => row[0] === process.platform && row[1] === process.arch && row[2] === bun)) unavailable();
  const path = realpathSync(process.execPath), held = holdFile(path);
  try {
    if (!images.some(row => row[0] === process.platform && row[1] === process.arch && row[2] === bun && row[3] === held.digest)) unavailable();
    return Object.freeze({ imageSHA256: held.digest, close: held.close, verify() {
      if (globalThis.Bun?.version !== bun || realpathSync(process.execPath) !== path) unavailable();
      held.verify();
    } });
  } catch { held.close(); unavailable(); }
}
