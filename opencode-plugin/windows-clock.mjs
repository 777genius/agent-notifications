import { lstatSync, realpathSync } from 'node:fs';
import { holdFile, pinNativeImage } from './native-clock-image.mjs';
import { address, closeAll, createNativeClock, filetimeNS, interruptABI, interruptNS,
  uint64, unavailable, windowsBoot, windowsKernelABI, windowsNtABI } from './native-clock-contract.mjs';

// Bounded bootstrap: only the protected default OS directory. Other layouts
// are denied; no environment/WINDIR/search-path or api-set disk-file fallback.
const directory = 'C:\\Windows\\System32';
export async function createWindowsClock({ signal } = {}) {
  if (signal?.aborted || process.platform !== 'win32' || process.arch !== 'x64') unavailable();
  const releases = [];
  const abort = () => { try { closeAll(releases); } catch {} };
  signal?.addEventListener('abort', abort, { once: true });
  try {
    const image = pinNativeImage(); releases.push(image.close);
    const files = ['kernel32.dll', 'ntdll.dll'].map(name => `${directory}\\${name}`);
    function verifyPaths() {
      for (const path of ['C:\\', 'C:\\Windows', directory, ...files]) {
        if (lstatSync(path).isSymbolicLink() || realpathSync(path).toLowerCase() !== path.toLowerCase()) unavailable();
      }
    }
    verifyPaths();
    const held = [];
    for (const path of files) { const file = holdFile(path); held.push(file); releases.push(file.close); }
    const { dlopen, ptr, linkSymbols } = await import('bun:ffi');
    if (signal?.aborted) unavailable();
    if (typeof dlopen !== 'function' || typeof ptr !== 'function' || typeof linkSymbols !== 'function') unavailable();
    function library(path, abi) {
      const lib = dlopen(path, abi); releases.push(() => lib.close()); return lib.symbols;
    }
    const aligned = view => address(ptr(view), view.BYTES_PER_ELEMENT);
    const kernel = library(files[0], windowsKernelABI);
    const system = new Uint16Array(32768);
    const length = kernel.GetSystemDirectoryW(aligned(system), system.length);
    if (!Number.isInteger(length) || length <= 0 || length >= system.length || system[length] !== 0 ||
        String.fromCharCode(...system.subarray(0, length)).toLowerCase() !== directory.toLowerCase()) unavailable();
    const contract = Buffer.from('api-ms-win-core-realtime-l1-1-1.dll\0', 'utf16le');
    // PR2956602: resolve the API-set TOKEN with LOAD_LIBRARY_SEARCH_SYSTEM32.
    // HMODULE is unsigned pointer width; FARPROC is Bun's checked numeric ptr.
    const handle = uint64(kernel.LoadLibraryExW(address(ptr(contract), 2), 0n, 0x800));
    if (handle === 0n) unavailable();
    releases.push(() => { if (kernel.FreeLibrary(handle) === 0) unavailable(); });
    const name = Buffer.from('QueryInterruptTimePrecise\0');
    const target = address(kernel.GetProcAddress(handle, address(ptr(name))));
    // linkSymbols owns an explicit closeable FFI handle on both pinned Buns.
    // V2 CFunction.close is a no-op; do not pretend it releases that handle.
    const wrapper = linkSymbols({ QueryInterruptTimePrecise: { ...interruptABI, ptr: target } });
    releases.push(() => wrapper.close());
    const nt = library(files[1], windowsNtABI);
    const info = new BigUint64Array(4), returned = new Uint32Array(1);
    const counter = new BigUint64Array(1), wall = new BigUint64Array(1);
    return createNativeClock({
      imageSHA256: image.imageSHA256,
      readBoot() {
        info.fill(0n); returned[0] = 0;
        const status = nt.NtQuerySystemInformation(90, aligned(info), 32, aligned(returned));
        return windowsBoot(new Uint8Array(info.buffer), returned[0], status);
      },
      readCounter() {
        counter[0] = 0n; wrapper.symbols.QueryInterruptTimePrecise(aligned(counter));
        return interruptNS(counter[0]); // VOID: never interpret return/last-error.
      },
      readWall() {
        wall[0] = 0n; kernel.GetSystemTimePreciseAsFileTime(aligned(wall));
        return filetimeNS(wall[0]);
      },
      verify() { image.verify(); verifyPaths(); for (const file of held) file.verify(); },
      close: () => closeAll(releases),
    }, 'windows-kernel');
  } catch { try { closeAll(releases); } catch {} unavailable(); }
  finally { signal?.removeEventListener('abort', abort); }
}
