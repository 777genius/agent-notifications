import { pinNativeImage } from './native-clock-image.mjs';
import { address, closeAll, createNativeClock, darwinABI, darwinBoot, machNS, positiveNS, unavailable } from './native-clock-contract.mjs';

// Async ABI preparation only; the returned sample/dispose methods are sync.
export async function createDarwinClock() {
  if (process.platform !== 'darwin' || !['x64', 'arm64'].includes(process.arch)) unavailable();
  const releases = [];
  try {
    const image = pinNativeImage(); releases.push(image.close);
    const { dlopen, ptr } = await import('bun:ffi');
    if (typeof dlopen !== 'function' || typeof ptr !== 'function') unavailable();
    const library = dlopen('/usr/lib/libSystem.B.dylib', darwinABI);
    releases.push(() => library.close());
    const lib = library.symbols, aligned = view => address(ptr(view), view.BYTES_PER_ELEMENT);
    const base = new Uint32Array(2);
    if (lib.mach_timebase_info(aligned(base)) !== 0 || base[0] === 0 || base[1] === 0) unavailable();
    const numer = base[0], denom = base[1];
    const name = Buffer.from('kern.bootsessionuuid\0');
    const buffer = new Uint32Array(16), size = new BigUint64Array(1);
    function readBoot() {
      buffer.fill(0); size[0] = BigInt(buffer.byteLength);
      const status = lib.sysctlbyname(address(ptr(name)), aligned(buffer), aligned(size), null, 0n);
      return darwinBoot(new Uint8Array(buffer.buffer), size[0], status);
    }
    return createNativeClock({
      readBoot, readCounter: () => machNS(lib.mach_continuous_time(), numer, denom),
      readWall() {
        const ms = Date.now();
        if (!Number.isSafeInteger(ms)) unavailable();
        return positiveNS(BigInt(ms) * 1000000n);
      },
      verify() {
        image.verify(); base.fill(0);
        if (lib.mach_timebase_info(aligned(base)) !== 0 || base[0] !== numer || base[1] !== denom) unavailable();
      },
      close: () => closeAll(releases),
    }, 'darwin-kernel');
  } catch { try { closeAll(releases); } catch {} unavailable(); }
}
