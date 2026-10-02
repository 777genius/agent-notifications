import { bootOK, int64Max } from './protocol.mjs';

export const unavailable = () => { throw new TypeError('clock_unavailable'); };
const U64 = 18446744073709551615n;
export function uint64(value) {
  if (typeof value !== 'bigint' && !Number.isSafeInteger(value)) unavailable();
  const n = BigInt(value);
  if (n < 0n || n > U64) unavailable();
  return n;
}
export function positiveNS(value) {
  if (typeof value !== 'bigint' || value <= 0n || value > int64Max) unavailable();
  return value;
}
export function machNS(ticks, numer, denom) {
  ticks = uint64(ticks);
  if (!Number.isInteger(numer) || !Number.isInteger(denom) || numer <= 0 || denom <= 0 ||
      numer > 0xffffffff || denom > 0xffffffff || ticks > U64 / BigInt(numer)) unavailable();
  // Same unsigned multiply/divide as Apple's CLOCK_MONOTONIC_RAW, no wrap.
  return positiveNS(ticks * BigInt(numer) / BigInt(denom));
}
export function interruptNS(ticks) { return positiveNS(uint64(ticks) * 100n); }
export function filetimeNS(ticks) {
  return positiveNS((uint64(ticks) - 116444736000000000n) * 100n);
}
export function darwinBoot(bytes, length, status) {
  if (status !== 0 || length !== 37n || !(bytes instanceof Uint8Array) || bytes.length < 37 ||
      bytes[36] !== 0 || !bytes.subarray(0, 36).every(b => b > 0 && b < 128)) unavailable();
  const boot = Buffer.from(bytes.subarray(0, 36)).toString('ascii').toLowerCase();
  if (!bootOK(boot)) unavailable();
  return boot;
}
export function windowsBoot(bytes, length, status) {
  if (status !== 0 || length !== 32 || !(bytes instanceof Uint8Array) || bytes.length !== 32) unavailable();
  const b = Buffer.from(bytes), hex = (n, w) => n.toString(16).padStart(w, '0');
  // SDK/Go GUID memory layout: first three fields little endian, tail bytes.
  const boot = `${hex(b.readUInt32LE(0), 8)}-${hex(b.readUInt16LE(4), 4)}-${hex(b.readUInt16LE(6), 4)}-${b.subarray(8, 10).toString('hex')}-${b.subarray(10, 16).toString('hex')}`;
  if (!bootOK(boot)) unavailable();
  return boot;
}
export function address(value, alignment = 1) {
  if (!Number.isSafeInteger(value) || value <= 0 || value % alignment !== 0) unavailable();
  return value;
}
export function closeAll(releases) {
  let failed = false;
  for (const release of releases.splice(0).reverse()) { try { release(); } catch { failed = true; } }
  if (failed) unavailable();
}
function abi(definitions) {
  return Object.freeze(Object.fromEntries(Object.entries(definitions).map(([key, [returns, ...args]]) =>
    [key, Object.freeze({ returns, args: Object.freeze(args) })])));
}
export const darwinABI = abi({
  mach_continuous_time: ['u64'], mach_timebase_info: ['i32', 'ptr'],
  sysctlbyname: ['i32', 'ptr', 'ptr', 'ptr', 'ptr', 'u64'],
});
export const windowsKernelABI = abi({
  GetSystemDirectoryW: ['u32', 'ptr', 'u32'], LoadLibraryExW: ['u64', 'ptr', 'u64', 'u32'],
  GetProcAddress: ['ptr', 'u64', 'ptr'], FreeLibrary: ['i32', 'u64'],
  GetSystemTimePreciseAsFileTime: ['void', 'ptr'],
});
export const windowsNtABI = abi({ NtQuerySystemInformation: ['i32', 'u32', 'ptr', 'u32', 'ptr'] });
export const interruptABI = Object.freeze({ returns: 'void', args: Object.freeze(['ptr']) });

// A library port, not a qualification seam: fixed coordinates, no Q/T policy,
// no offsets inferred from sampling and no source Date synthesis on Windows.
export function createNativeClock(port, domain) {
  let disposed = false, previous, boot, rawKind, quantum;
  function dispose() { if (!disposed) { disposed = true; port.close(); } }
  try {
    rawKind = domain === 'darwin-kernel' ? 'darwin-monotonic-raw' :
      domain === 'windows-kernel' ? 'windows-interrupt-precise' : unavailable();
    quantum = domain === 'darwin-kernel' ? 1n : 100n;
    port.verify(); boot = port.readBoot();
    if (!bootOK(boot)) unavailable();
  } catch { try { dispose(); } catch {} unavailable(); }
  function sample() {
    try {
      if (disposed) unavailable();
      port.verify();
      if (port.readBoot() !== boot) unavailable();
      const loNS = positiveNS(port.readCounter());
      const wallNS = positiveNS(port.readWall());
      const last = positiveNS(port.readCounter()), hiNS = positiveNS(last + quantum);
      if (last < loNS || hiNS - loNS > 100000000n ||
          previous && (loNS < previous.last || wallNS < previous.wallNS)) unavailable();
      if (port.readBoot() !== boot) unavailable();
      port.verify();
      previous = { last, wallNS };
      return Object.freeze({ boot, domain, rawKind, loNS, hiNS, wallNS });
    } catch { try { dispose(); } catch {} unavailable(); }
  }
  return Object.freeze({ sample, dispose });
}
