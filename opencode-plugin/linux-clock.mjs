import { openSync, closeSync, readSync, fstatSync, statSync, statfsSync, readlinkSync, constants } from 'node:fs';
import { bootOK, ns } from './protocol.mjs';

const fail = () => { throw new TypeError('clock_unavailable'); };
const fixed = ['/proc/uptime', '/proc/sys/kernel/random/boot_id', '/proc/thread-self/ns/time'];
const PROCFS = 0x9fa0n, NSFS = 0x6e736673n, quantum = 10000000n;
const same = (a, b) => a.dev === b.dev && a.ino === b.ino && a.mode === b.mode;
function held(fd, magic) {
  if (statfsSync(`/proc/thread-self/fd/${fd}`, { bigint: true }).type !== magic) fail();
  const info = fstatSync(fd, { bigint: true });
  if (typeof info.dev !== 'bigint' || typeof info.ino !== 'bigint' || info.dev <= 0n || info.ino <= 0n) fail();
  return info;
}
function read(fd, size) {
  const buffer = Buffer.alloc(size + 1);
  const count = readSync(fd, buffer, 0, buffer.length, 0);
  if (count === 0 || count > size) fail();
  return buffer.subarray(0, count).toString('utf8');
}
export function parseUptime(text) {
  if (typeof text !== 'string' || !/^(0|[1-9][0-9]*)\.[0-9]{2} (0|[1-9][0-9]*)\.[0-9]{2}\n$/.test(text)) fail();
  const first = text.slice(0, text.indexOf(' ')).split('.');
  return ns((BigInt(first[0]) * 1000000000n + BigInt(first[1]) * quantum).toString());
}

// Public qualification seam reads the real module. It grants no clock policy.
// Trusted composition excludes adversarial proc mounts and patched builtins.
export function createLinuxClock() {
  if (process.platform !== 'linux' || !['x64', 'arm64'].includes(process.arch)) fail();
  const fds = [];
  let disposed = false, previous;
  try {
    for (const [index, file] of fixed.entries()) {
      // libuv opens cloexec; the Linux flag is also explicit. NS magic link follows.
      fds.push(openSync(file, constants.O_RDONLY | (constants.O_CLOEXEC ?? 0x80000) |
        (index === 2 ? 0 : constants.O_NOFOLLOW)));
    }
    const identities = fds.map((fd, index) => held(fd, index === 2 ? NSFS : PROCFS));
    const boot = read(fds[1], 37).trimEnd();
    if (!bootOK(boot) || read(fds[1], 37) !== `${boot}\n`) fail();
    const domain = `linux-time:${identities[2].dev}:${identities[2].ino}`;
    function verify() {
      if (disposed) fail();
      for (const [index, fd] of fds.entries()) if (!same(held(fd, index === 2 ? NSFS : PROCFS), identities[index])) fail();
      // A held namespace proves its own identity, not the CURRENT calling task's.
      const current = statSync(fixed[2], { bigint: true });
      if (!same(current, identities[2]) || statfsSync(fixed[2], { bigint: true }).type !== NSFS ||
          readlinkSync(fixed[2]) !== `time:[${current.ino}]` || read(fds[1], 37) !== `${boot}\n`) fail();
    }
    function sample() {
      try {
        verify();
        const lo = parseUptime(read(fds[0], 128));
        const wallMs = Date.now();
        const last = parseUptime(read(fds[0], 128));
        verify();
        if (!Number.isSafeInteger(wallMs) || wallMs <= 0 || last < lo || last - lo > 100000000n ||
            previous && lo < previous.loNS) fail();
        const wallNS = ns((BigInt(wallMs) * 1000000n).toString());
        const hi = ns((last + quantum).toString());
        const result = Object.freeze({ boot, domain, rawKind: 'linux-boottime', loNS: lo, hiNS: hi, wallNS,
          offsetLoNS: wallNS - hi - 2000000n, offsetHiNS: wallNS - lo + 2000000n });
        if (previous && (result.offsetLoNS > previous.offsetHiNS + 430000000n ||
            result.offsetHiNS < previous.offsetLoNS - 430000000n)) fail();
        previous = result;
        return result;
      } catch { dispose(); fail(); }
    }
    function dispose() {
      if (disposed) return;
      disposed = true;
      for (const fd of fds) { try { closeSync(fd); } catch {} }
    }
    return Object.freeze({ sample, dispose });
  } catch {
    for (const fd of fds) { try { closeSync(fd); } catch {} }
    fail();
  }
}
